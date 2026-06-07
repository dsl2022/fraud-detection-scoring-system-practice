package service

import (
	"context"
	"testing"

	"github.com/blocklocmedia/fraud-signals/internal/audit"
	"github.com/blocklocmedia/fraud-signals/internal/auth"
	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/events"
	"github.com/blocklocmedia/fraud-signals/internal/reqid"
)

// stubScorer emits a fixed set of provider results to the observer (if any),
// then returns a fixed decision.
type stubScorer struct {
	result domain.ScoreResult
	emit   []domain.RiskData
}

func (s stubScorer) ScoreObserved(_ context.Context, _ domain.Application, obs func(domain.RiskData)) domain.ScoreResult {
	if obs != nil {
		for _, d := range s.emit {
			obs(d)
		}
	}
	return s.result
}

func fixture() (stubScorer, domain.Application) {
	stub := stubScorer{
		result: domain.ScoreResult{Score: 42, Decision: domain.DecisionManualReview, SignalsUsed: []string{"a", "b"}, LogicVersion: "v1"},
		emit:   []domain.RiskData{{Source: "a", Score: 40, Confidence: 0.9}, {Source: "b", Score: 44, Confidence: 0.8}},
	}
	return stub, domain.Application{ApplicantID: "acct-1", Product: "checking"}
}

func TestScore_PublishesEvent_Combined(t *testing.T) {
	stub, app := fixture()
	pub := events.NewMemoryPublisher()
	svc := New(stub, pub, audit.PersistCombined, nil)

	ctx := reqid.NewContext(context.Background(), "req-123")
	res := svc.Score(ctx, app)
	if res.Decision != domain.DecisionManualReview {
		t.Errorf("decision = %q", res.Decision)
	}

	evs := pub.Snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.RequestID != "req-123" || ev.EventID != "req-123" {
		t.Errorf("ids = (%q,%q), want req-123", ev.EventID, ev.RequestID)
	}
	if ev.Decision.InputHash != audit.HashInputs(app) {
		t.Errorf("input hash not stamped correctly")
	}
	if ev.Decision.Decision != string(domain.DecisionManualReview) {
		t.Errorf("decision field = %q", ev.Decision.Decision)
	}
	// Combined mode: no per-provider records ride along.
	if len(ev.ProviderResponses) != 0 {
		t.Errorf("combined mode carried %d provider responses, want 0", len(ev.ProviderResponses))
	}
}

func TestScore_PublishesEvent_AsYouGoCarriesProviders(t *testing.T) {
	stub, app := fixture()
	pub := events.NewMemoryPublisher()
	svc := New(stub, pub, audit.PersistAsYouGo, nil)

	_ = svc.Score(reqid.NewContext(context.Background(), "req-9"), app)

	evs := pub.Snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	if len(evs[0].ProviderResponses) != 2 {
		t.Errorf("as-you-go carried %d provider responses, want 2", len(evs[0].ProviderResponses))
	}
	for _, p := range evs[0].ProviderResponses {
		if p.RequestID != "req-9" || p.InputHash != audit.HashInputs(app) {
			t.Errorf("provider record missing correlation fields: %+v", p)
		}
	}
}

func TestScore_GeneratesRequestIDWhenMissing(t *testing.T) {
	stub, app := fixture()
	pub := events.NewMemoryPublisher()
	svc := New(stub, pub, audit.PersistCombined, nil)

	_ = svc.Score(context.Background(), app) // no reqid in context
	evs := pub.Snapshot()
	if evs[0].RequestID == "" {
		t.Error("expected a generated request id as idempotency key")
	}
}

func TestScore_StampsSubjectFromAuthContext(t *testing.T) {
	stub, app := fixture()
	pub := events.NewMemoryPublisher()
	svc := New(stub, pub, audit.PersistCombined, nil)

	ctx := auth.NewContext(reqid.NewContext(context.Background(), "r1"), auth.Claims{Subject: "analyst-7"})
	_ = svc.Score(ctx, app)

	if pub.Snapshot()[0].Decision.Subject != "analyst-7" {
		t.Errorf("subject not stamped from auth context")
	}
}
