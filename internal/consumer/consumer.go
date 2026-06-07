// Package consumer holds the SQS consumer's CORE logic, deliberately free of any
// Lambda/SQS transport types so it can be unit-tested directly and reused by both
// the Lambda entrypoint (cmd/consumer) and the local poller (cmd/worker).
//
// IDEMPOTENCY under at-least-once delivery is the central concern. We get it
// from two facts:
//  1. The audit write is a conditional (write-once) put keyed on request id — a
//     duplicate returns audit.ErrAlreadyExists, which we treat as success.
//  2. The downstream side effects (review enqueue, retraining emit) are
//     themselves idempotent on request id, so we can run every step on every
//     delivery without gating them behind the audit write. That avoids the
//     classic trap where a side effect fails AFTER the dedup row is committed and
//     a retry then skips it forever.
package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/blocklocmedia/fraud-signals/internal/audit"
	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/events"
)

// ReviewSink enqueues MANUAL_REVIEW cases for a human queue. Must be idempotent
// on request id (production: SQS FIFO dedup / conditional write).
type ReviewSink interface {
	EnqueueReview(ctx context.Context, ev events.ScoringEvent) error
}

// RetrainingSink feeds the model-retraining data lake. Must be idempotent on
// request id (production: keyed S3 object / partitioned write).
type RetrainingSink interface {
	Emit(ctx context.Context, ev events.ScoringEvent) error
}

// Handler wires the consumer's dependencies.
type Handler struct {
	audit   audit.Sink
	review  ReviewSink
	retrain RetrainingSink
	log     *slog.Logger
}

func New(a audit.Sink, r ReviewSink, t RetrainingSink, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{audit: a, review: r, retrain: t, log: log}
}

// Process handles a single event idempotently. A returned error means "retry me"
// (and, after maxReceiveCount, DLQ me). nil means done (including duplicates).
func (h *Handler) Process(ctx context.Context, ev events.ScoringEvent) error {
	// 1. Immutable audit record (the dedup fence).
	if err := h.writeAudit(ctx, ev); err != nil {
		return err
	}
	// 2. MANUAL_REVIEW cases go to the human queue.
	if ev.Decision.Decision == string(domain.DecisionManualReview) {
		if err := h.review.EnqueueReview(ctx, ev); err != nil {
			return fmt.Errorf("enqueue review: %w", err)
		}
	}
	// 3. Every decision feeds the retraining sink.
	if err := h.retrain.Emit(ctx, ev); err != nil {
		return fmt.Errorf("emit retraining: %w", err)
	}
	return nil
}

func (h *Handler) writeAudit(ctx context.Context, ev events.ScoringEvent) error {
	err := h.audit.PutDecision(ctx, ev.Decision)
	switch {
	case errors.Is(err, audit.ErrAlreadyExists):
		// Already written by a prior delivery — idempotent no-op.
		h.log.InfoContext(ctx, "duplicate scoring event ignored",
			"request_id", ev.RequestID)
		return nil
	case err != nil:
		return fmt.Errorf("audit decision write: %w", err)
	}
	// First time we've seen it: persist any per-vendor responses too.
	for _, pr := range ev.ProviderResponses {
		if err := h.audit.PutProviderResponse(ctx, pr); err != nil && !errors.Is(err, audit.ErrAlreadyExists) {
			return fmt.Errorf("audit provider write (%s): %w", pr.Source, err)
		}
	}
	return nil
}

// Message is a transport-agnostic queue message: an id (to report failures back)
// and a raw JSON body.
type Message struct {
	ID   string
	Body string
}

// ProcessBatch processes a batch and returns the IDs that FAILED. Callers map
// these to SQS partial-batch-failure reporting (ReportBatchItemFailures) so only
// failed messages are retried and eventually redriven to the DLQ — successful
// ones in the same batch are not re-delivered.
func (h *Handler) ProcessBatch(ctx context.Context, msgs []Message) []string {
	var failed []string
	for _, m := range msgs {
		ev, err := events.Unmarshal([]byte(m.Body))
		if err != nil {
			// Unparseable: retrying won't help, but reporting it as failed sends
			// it down the DLQ path for inspection rather than silently dropping.
			h.log.Error("unparseable message", "message_id", m.ID, "error", err.Error())
			failed = append(failed, m.ID)
			continue
		}
		if err := h.Process(ctx, ev); err != nil {
			h.log.Error("event processing failed", "message_id", m.ID,
				"request_id", ev.RequestID, "error", err.Error())
			failed = append(failed, m.ID)
		}
	}
	return failed
}
