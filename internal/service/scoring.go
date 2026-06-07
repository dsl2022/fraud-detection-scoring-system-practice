// Package service is the application service that orchestrates a scoring request
// end to end: hash inputs, score (collecting per-vendor responses), then PUBLISH
// a ScoringEvent for the durable follow-up work. Transports (HTTP, gRPC) call
// this; it knows nothing about either.
//
// STAGE 4 CHANGE: the immutable audit write moved OFF the request path. The
// service now publishes an event (a fast, durable handoff to SQS); the consumer
// writes the audit record, enqueues MANUAL_REVIEW cases, and feeds retraining.
// The caller no longer waits on DynamoDB.
package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/audit"
	"github.com/blocklocmedia/fraud-signals/internal/auth"
	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/events"
	"github.com/blocklocmedia/fraud-signals/internal/reqid"
)

// Scorer is the slice of the core the service needs.
type Scorer interface {
	ScoreObserved(ctx context.Context, app domain.Application, obs func(domain.RiskData)) domain.ScoreResult
}

// ScoringService implements the same Score(ctx, app) signature the transports
// expect, so it slots in wherever a raw scorer would.
type ScoringService struct {
	scorer    Scorer
	publisher events.Publisher
	mode      audit.PersistMode
	log       *slog.Logger
	now       func() time.Time // injectable for tests
}

func New(scorer Scorer, publisher events.Publisher, mode audit.PersistMode, log *slog.Logger) *ScoringService {
	if log == nil {
		log = slog.Default()
	}
	if mode == "" {
		mode = audit.PersistCombined
	}
	return &ScoringService{scorer: scorer, publisher: publisher, mode: mode, log: log, now: time.Now}
}

// Score scores the application and publishes a ScoringEvent for the durable,
// off-request-path follow-up work.
func (s *ScoringService) Score(ctx context.Context, app domain.Application) domain.ScoreResult {
	reqID := reqid.FromContext(ctx)
	if reqID == "" {
		reqID = reqid.Generate() // ensure an idempotency key even without middleware
	}
	inputHash := audit.HashInputs(app)
	subject := auth.SubjectFromContext(ctx)
	now := s.now().UTC()

	// In as-you-go mode, capture each vendor response so the event can carry it.
	// The observer runs concurrently in the fan-out, hence the mutex.
	var (
		mu        sync.Mutex
		providers []audit.ProviderRecord
		obs       func(domain.RiskData)
	)
	if s.mode == audit.PersistAsYouGo {
		obs = func(d domain.RiskData) {
			mu.Lock()
			providers = append(providers, audit.ProviderRecord{
				RequestID: reqID, InputHash: inputHash, Source: d.Source,
				Score: d.Score, Confidence: d.Confidence, Timestamp: now,
			})
			mu.Unlock()
		}
	}

	result := s.scorer.ScoreObserved(ctx, app, obs)

	ev := events.ScoringEvent{
		EventID:   reqID, // one decision per request id => natural idempotency key
		RequestID: reqID,
		Decision: audit.Record{
			RequestID:    reqID,
			InputHash:    inputHash,
			Score:        result.Score,
			Decision:     string(result.Decision),
			SignalsUsed:  result.SignalsUsed,
			LogicVersion: result.LogicVersion,
			Subject:      subject,
			Timestamp:    now,
		},
		ProviderResponses: providers,
		PersistMode:       s.mode,
	}

	// Publishing is the durable handoff. A failure here is serious (we'd lose the
	// audit trail), but we must still answer the caller — log loudly so an alarm
	// on this can fire. (Publishing to SQS is the fast, decoupled step; the heavy
	// DynamoDB write happens in the consumer.)
	if err := s.publisher.Publish(ctx, ev); err != nil {
		s.log.ErrorContext(ctx, "scoring event publish failed",
			"request_id", reqID, "error", err.Error())
	}

	return result
}
