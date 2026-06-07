// Package events defines the message published after a scoring decision and the
// Publisher port that puts it on the wire. Publishing is the DECOUPLING point:
// the caller gets its decision immediately; the slow/durable follow-up work
// (writing the immutable audit record, enqueuing MANUAL_REVIEW cases, feeding the
// retraining sink) happens off the request path in the SQS consumer.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/blocklocmedia/fraud-signals/internal/audit"
)

// ScoringEvent is the immutable fact "a decision was made". It carries enough to
// reconstruct the audit record and to drive downstream consumers.
//
// RequestID doubles as the IDEMPOTENCY KEY: with at-least-once SQS delivery the
// consumer may see the same event twice, and "one decision per request id" lets
// it dedupe (via a conditional write keyed on request id).
type ScoringEvent struct {
	EventID           string                 `json:"event_id"`
	RequestID         string                 `json:"request_id"`
	Decision          audit.Record           `json:"decision"`
	ProviderResponses []audit.ProviderRecord `json:"provider_responses,omitempty"`
	PersistMode       audit.PersistMode      `json:"persist_mode"`
}

// Marshal/Unmarshal centralise the wire format (plain JSON) so producer and
// consumer can't drift.
func Marshal(ev ScoringEvent) ([]byte, error) { return json.Marshal(ev) }

func Unmarshal(b []byte) (ScoringEvent, error) {
	var ev ScoringEvent
	err := json.Unmarshal(b, &ev)
	return ev, err
}

// Publisher is the port the scoring service depends on. The SQS implementation
// is one adapter; LogPublisher/MemoryPublisher are others for local/dev/test.
type Publisher interface {
	Publish(ctx context.Context, ev ScoringEvent) error
}

// LogPublisher writes events to the structured log instead of a queue. Handy for
// local runs without any AWS/LocalStack dependency.
type LogPublisher struct{ log *slog.Logger }

func NewLogPublisher(log *slog.Logger) *LogPublisher {
	if log == nil {
		log = slog.Default()
	}
	return &LogPublisher{log: log}
}

func (p *LogPublisher) Publish(ctx context.Context, ev ScoringEvent) error {
	p.log.InfoContext(ctx, "event.published",
		"event_id", ev.EventID, "request_id", ev.RequestID,
		"decision", ev.Decision.Decision)
	return nil
}

// MemoryPublisher records events for assertions in tests.
type MemoryPublisher struct {
	mu     sync.Mutex
	Events []ScoringEvent
}

func NewMemoryPublisher() *MemoryPublisher { return &MemoryPublisher{} }

func (p *MemoryPublisher) Publish(_ context.Context, ev ScoringEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Events = append(p.Events, ev)
	return nil
}

func (p *MemoryPublisher) Snapshot() []ScoringEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ScoringEvent(nil), p.Events...)
}
