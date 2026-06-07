package consumer

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/blocklocmedia/fraud-signals/internal/audit"
	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/events"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newHandler() (*Handler, *audit.MemorySink) {
	sink := audit.NewMemorySink()
	h := New(sink, NewLoggingReviewSink(quiet()), NewLoggingRetrainingSink(quiet()), quiet())
	return h, sink
}

func eventFor(reqID, decision string) events.ScoringEvent {
	return events.ScoringEvent{
		EventID:   reqID,
		RequestID: reqID,
		Decision: audit.Record{
			RequestID: reqID, InputHash: "hash", Score: 50,
			Decision: decision, LogicVersion: "v1",
		},
		PersistMode: audit.PersistCombined,
	}
}

func TestProcess_WritesAuditOnce(t *testing.T) {
	h, sink := newHandler()
	ev := eventFor("req-1", string(domain.DecisionApprove))

	if err := h.Process(context.Background(), ev); err != nil {
		t.Fatalf("first process: %v", err)
	}
	decisions, _ := sink.Snapshot()
	if len(decisions) != 1 {
		t.Fatalf("after first process: %d decisions, want 1", len(decisions))
	}
}

// TestProcess_IdempotentOnDuplicate is the core at-least-once guarantee: the same
// event delivered twice writes the audit record exactly once and does not error.
func TestProcess_IdempotentOnDuplicate(t *testing.T) {
	h, sink := newHandler()
	ev := eventFor("req-dup", string(domain.DecisionManualReview))

	for i := 0; i < 3; i++ {
		if err := h.Process(context.Background(), ev); err != nil {
			t.Fatalf("process %d returned error: %v", i, err)
		}
	}
	decisions, _ := sink.Snapshot()
	if len(decisions) != 1 {
		t.Errorf("duplicate deliveries wrote %d decisions, want exactly 1", len(decisions))
	}
}

func TestProcess_AsYouGoWritesProviderRecords(t *testing.T) {
	h, sink := newHandler()
	ev := eventFor("req-2", string(domain.DecisionApprove))
	ev.PersistMode = audit.PersistAsYouGo
	ev.ProviderResponses = []audit.ProviderRecord{
		{RequestID: "req-2", Source: "a", Score: 40},
		{RequestID: "req-2", Source: "b", Score: 60},
	}
	if err := h.Process(context.Background(), ev); err != nil {
		t.Fatalf("process: %v", err)
	}
	_, providers := sink.Snapshot()
	if len(providers) != 2 {
		t.Errorf("wrote %d provider records, want 2", len(providers))
	}
}

func TestProcessBatch_ReportsFailures(t *testing.T) {
	h, _ := newHandler()
	good, _ := events.Marshal(eventFor("ok-1", string(domain.DecisionApprove)))

	msgs := []Message{
		{ID: "m1", Body: string(good)},
		{ID: "m2", Body: "{not valid json"}, // unparseable -> reported failed
	}
	failed := h.ProcessBatch(context.Background(), msgs)

	if len(failed) != 1 || failed[0] != "m2" {
		t.Errorf("failed = %v, want [m2]", failed)
	}
}
