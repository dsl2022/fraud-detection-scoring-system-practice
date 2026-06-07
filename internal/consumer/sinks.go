package consumer

import (
	"context"
	"log/slog"
	"sync"

	"github.com/blocklocmedia/fraud-signals/internal/events"
)

// LoggingReviewSink models the MANUAL_REVIEW queue: it logs the case and dedupes
// in-memory on request id so re-delivery is a no-op. In production this is an SQS
// send to a FIFO review queue (MessageDeduplicationId = request id).
type LoggingReviewSink struct {
	log  *slog.Logger
	mu   sync.Mutex
	seen map[string]bool
}

func NewLoggingReviewSink(log *slog.Logger) *LoggingReviewSink {
	if log == nil {
		log = slog.Default()
	}
	return &LoggingReviewSink{log: log, seen: map[string]bool{}}
}

func (s *LoggingReviewSink) EnqueueReview(ctx context.Context, ev events.ScoringEvent) error {
	s.mu.Lock()
	dup := s.seen[ev.RequestID]
	s.seen[ev.RequestID] = true
	s.mu.Unlock()
	if dup {
		return nil // idempotent
	}
	s.log.InfoContext(ctx, "manual_review.enqueued",
		"request_id", ev.RequestID, "score", ev.Decision.Score)
	return nil
}

// LoggingRetrainingSink models the retraining data sink with the same idempotency
// shape. In production this is a keyed write to S3 / a feature store.
type LoggingRetrainingSink struct {
	log  *slog.Logger
	mu   sync.Mutex
	seen map[string]bool
}

func NewLoggingRetrainingSink(log *slog.Logger) *LoggingRetrainingSink {
	if log == nil {
		log = slog.Default()
	}
	return &LoggingRetrainingSink{log: log, seen: map[string]bool{}}
}

func (s *LoggingRetrainingSink) Emit(ctx context.Context, ev events.ScoringEvent) error {
	s.mu.Lock()
	dup := s.seen[ev.RequestID]
	s.seen[ev.RequestID] = true
	s.mu.Unlock()
	if dup {
		return nil // idempotent
	}
	s.log.InfoContext(ctx, "retraining.emitted",
		"request_id", ev.RequestID, "decision", ev.Decision.Decision)
	return nil
}
