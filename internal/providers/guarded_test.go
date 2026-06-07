package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/metrics"
)

func app() domain.Application { return domain.Application{ApplicantID: "x", Product: "p"} }

// TestGuard_BreakerOpensAndFailsFast is the core Stage-2 assertion: after a run
// of failures the breaker opens, and subsequent calls return ErrOpenState
// IMMEDIATELY without invoking the (slow/failing) vendor.
func TestGuard_BreakerOpensAndFailsFast(t *testing.T) {
	rec := metrics.NewCollector()
	inner := NewSimulated(SimConfig{
		ProviderName: "flaky",
		Latency:      0,
		FailErr:      errors.New("vendor boom"),
	})
	g := Guard(inner, GuardConfig{
		Budget:                    100 * time.Millisecond,
		ConsecutiveFailuresToTrip: 3,
		OpenTimeout:               time.Hour, // stay open for the duration of the test
	}, rec)

	// First 3 calls execute and fail, tripping the breaker.
	for i := 0; i < 3; i++ {
		if _, err := g.Fetch(context.Background(), app()); err == nil {
			t.Fatalf("call %d: expected error", i)
		}
	}

	// 4th call must fail FAST with ErrOpenState (breaker open, vendor not called).
	start := time.Now()
	_, err := g.Fetch(context.Background(), app())
	elapsed := time.Since(start)

	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("expected ErrOpenState, got %v", err)
	}
	if elapsed > 5*time.Millisecond {
		t.Errorf("breaker-open call took %v, expected near-instant fail-fast", elapsed)
	}
	if rec.Count("flaky", metrics.OutcomeError) < 3 {
		t.Errorf("expected >=3 error outcomes, got %d", rec.Count("flaky", metrics.OutcomeError))
	}
	if rec.Count("flaky", metrics.OutcomeBreakerOpen) < 1 {
		t.Errorf("expected >=1 breaker_open outcome, got %d", rec.Count("flaky", metrics.OutcomeBreakerOpen))
	}
}

// TestGuard_PerSourceTimeout verifies a vendor is bounded by its OWN budget,
// independent of any shared deadline, and the outcome is recorded as a timeout.
func TestGuard_PerSourceTimeout(t *testing.T) {
	rec := metrics.NewCollector()
	inner := NewSimulated(SimConfig{ProviderName: "slow", Latency: 800 * time.Millisecond, BaseScore: 50})
	g := Guard(inner, GuardConfig{Budget: 50 * time.Millisecond, ConsecutiveFailuresToTrip: 99}, rec)

	start := time.Now()
	_, err := g.Fetch(context.Background(), app())
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if elapsed > 120*time.Millisecond {
		t.Errorf("per-source timeout not honoured: took %v, budget 50ms", elapsed)
	}
	if rec.Count("slow", metrics.OutcomeTimeout) != 1 {
		t.Errorf("expected 1 timeout outcome, got %d", rec.Count("slow", metrics.OutcomeTimeout))
	}
}

// TestGuard_OuterDeadlineCapsBudget verifies the per-source budget is capped by
// the outer deadline — whichever is sooner wins.
func TestGuard_OuterDeadlineCapsBudget(t *testing.T) {
	inner := NewSimulated(SimConfig{ProviderName: "slow", Latency: 800 * time.Millisecond})
	g := Guard(inner, GuardConfig{Budget: 500 * time.Millisecond, ConsecutiveFailuresToTrip: 99}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := g.Fetch(ctx, app())
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("outer deadline did not cap budget: took %v", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

// TestGuard_SuccessPassesThrough sanity-checks the happy path + success metric.
func TestGuard_SuccessPassesThrough(t *testing.T) {
	rec := metrics.NewCollector()
	inner := NewSimulated(SimConfig{ProviderName: "ok", Latency: 5 * time.Millisecond, BaseScore: 42})
	g := Guard(inner, GuardConfig{Budget: 100 * time.Millisecond}, rec)

	data, err := g.Fetch(context.Background(), app())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.Source != "ok" {
		t.Errorf("source = %q, want ok", data.Source)
	}
	if rec.Count("ok", metrics.OutcomeSuccess) != 1 {
		t.Errorf("expected 1 success outcome, got %d", rec.Count("ok", metrics.OutcomeSuccess))
	}
}
