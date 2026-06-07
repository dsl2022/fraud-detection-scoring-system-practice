// Package audit produces the immutable, reviewable record that every scoring
// decision must leave behind (an ITGC requirement, built in — not bolted on).
//
// STORAGE CHOICE — DynamoDB (justified):
//   - Single-digit-millisecond writes keep us inside the latency budget even when
//     we (temporarily, pre-Stage-4) write on the request path.
//   - Write-once immutability is enforced with a conditional put
//     (attribute_not_exists(request_id)) — the store itself rejects overwrites,
//     so immutability doesn't depend on application discipline.
//   - Point lookups by request_id (the natural audit query: "show me decision X")
//     are a primary-key GetItem — no scan.
//   - Encryption at rest via a customer-managed KMS key; per-item, no bucket
//     lifecycle to reason about.
//
// The alternative, S3 with Object Lock (true WORM), is better when a regulator
// demands tamper-proof retention; we note it in the ADR. For an
// operational-audit-by-id workload, DynamoDB is the simpler, faster fit.
//
// The concrete DynamoDB sink is wired in Stage 5; here we define the record,
// the Sink port, the input hashing, and local sinks so the toggle and the
// pipeline are testable now.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
)

// ErrAlreadyExists signals that a record for this request id was already written.
// It is the backbone of consumer idempotency under at-least-once delivery: a Sink
// backed by a write-once store (DynamoDB conditional put) returns this on a
// duplicate, and the consumer treats it as a successful no-op.
var ErrAlreadyExists = errors.New("audit record already exists")

// PersistMode selects how provider responses are persisted. This mirrors the
// real flow's two options.
type PersistMode string

const (
	// PersistCombined writes a single decision record AFTER the bureau call.
	// Fewer writes, one cohesive record; you lose per-vendor responses if the
	// request dies mid-flight.
	PersistCombined PersistMode = "combined"
	// PersistAsYouGo additionally writes each provider response the moment it
	// returns. More writes, but you retain partial evidence even if the request
	// later fails — useful for debugging vendor behaviour and for replay.
	PersistAsYouGo PersistMode = "as_you_go"
)

// Record is the immutable decision record. It captures WHAT was decided, on WHAT
// inputs (by hash, so we never store raw PII in the audit trail), by WHICH logic,
// for WHOM, and WHEN — everything a reviewer needs to reconstruct a decision.
type Record struct {
	RequestID    string    `json:"request_id"`
	InputHash    string    `json:"input_hash"`
	Score        float64   `json:"score"`
	Decision     string    `json:"decision"`
	SignalsUsed  []string  `json:"signals_used"`
	LogicVersion string    `json:"logic_version"`
	Subject      string    `json:"subject,omitempty"` // who (auth claim)
	Timestamp    time.Time `json:"timestamp"`
}

// ProviderRecord is a single vendor's response, persisted in as-you-go mode.
type ProviderRecord struct {
	RequestID  string    `json:"request_id"`
	InputHash  string    `json:"input_hash"`
	Source     string    `json:"source"`
	Score      float64   `json:"score"`
	Confidence float64   `json:"confidence"`
	Timestamp  time.Time `json:"timestamp"`
}

// Sink persists audit records. Implementations MUST be safe for concurrent use:
// in as-you-go mode PutProviderResponse is called from multiple provider
// goroutines at once.
type Sink interface {
	PutDecision(ctx context.Context, r Record) error
	PutProviderResponse(ctx context.Context, r ProviderRecord) error
}

// HashInputs returns a SHA-256 hex digest of the application inputs. We store the
// HASH, not the raw fields: the audit trail proves "these exact inputs produced
// this decision" without becoming a second copy of customer PII. PriorSignals is
// json:"-" so internal enrichment never affects the input hash.
func HashInputs(app domain.Application) string {
	b, _ := json.Marshal(app)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// LogSink writes records as structured log lines. Append-only logs are a
// reasonable local/dev audit surface and a fine fallback; it is also handy as a
// tee alongside the real store.
type LogSink struct{ log *slog.Logger }

func NewLogSink(log *slog.Logger) *LogSink {
	if log == nil {
		log = slog.Default()
	}
	return &LogSink{log: log}
}

func (s *LogSink) PutDecision(ctx context.Context, r Record) error {
	s.log.InfoContext(ctx, "audit.decision",
		"request_id", r.RequestID, "input_hash", r.InputHash, "score", r.Score,
		"decision", r.Decision, "signals_used", r.SignalsUsed,
		"logic_version", r.LogicVersion, "subject", r.Subject, "ts", r.Timestamp)
	return nil
}

func (s *LogSink) PutProviderResponse(ctx context.Context, r ProviderRecord) error {
	s.log.InfoContext(ctx, "audit.provider_response",
		"request_id", r.RequestID, "source", r.Source, "score", r.Score,
		"confidence", r.Confidence, "ts", r.Timestamp)
	return nil
}

// MemorySink is a concurrency-safe, WRITE-ONCE in-memory Sink for tests. It
// models a DynamoDB conditional put: a second PutDecision for the same request
// id returns ErrAlreadyExists, so it exercises the consumer's idempotency path.
type MemorySink struct {
	mu        sync.Mutex
	Decisions []Record
	Providers []ProviderRecord
	seen      map[string]bool // request ids already written
}

func NewMemorySink() *MemorySink { return &MemorySink{seen: map[string]bool{}} }

func (s *MemorySink) PutDecision(_ context.Context, r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[r.RequestID] {
		return ErrAlreadyExists
	}
	s.seen[r.RequestID] = true
	s.Decisions = append(s.Decisions, r)
	return nil
}

func (s *MemorySink) PutProviderResponse(_ context.Context, r ProviderRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Providers = append(s.Providers, r)
	return nil
}

// Snapshot returns copies of the recorded slices for assertions.
func (s *MemorySink) Snapshot() ([]Record, []ProviderRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := append([]Record(nil), s.Decisions...)
	p := append([]ProviderRecord(nil), s.Providers...)
	return d, p
}
