package scorer

import (
	"context"
	"testing"
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/metrics"
	"github.com/blocklocmedia/fraud-signals/internal/ports"
	"github.com/blocklocmedia/fraud-signals/internal/providers"
)

// This file is the runnable "before/after" of the incident story.
//
// Setup: one HEALTHY independent vendor (10ms) and one SICK independent vendor
// (800ms — far beyond any budget), plus a fast bureau. We fire a burst of
// sequential requests and measure per-request latency under each design.
//
//	BEFORE (NaiveScorer + raw providers): a single shared timeout. The sick
//	vendor pins the Stage-A fan-in to the shared deadline on EVERY request, so
//	every request pays ~the full Stage-A budget. The healthy vendor is starved of
//	a fast response.
//
//	AFTER (Scorer + guarded providers): each vendor has its own budget and a
//	breaker. After a few failures the breaker opens and the sick vendor fails fast
//	(~0ms), so the request completes as soon as the healthy vendor answers. The
//	sick vendor SELF-LIMITS.
//
// Run it:  go test ./internal/scorer -run TestIncident -v

const (
	healthyLatency = 10 * time.Millisecond
	sickLatency    = 800 * time.Millisecond
	bureauLatency  = 10 * time.Millisecond
	burst          = 12
)

func incidentCfg() Config {
	return Config{
		Budget:       300 * time.Millisecond,
		StageABudget: 150 * time.Millisecond,
		// Decision thresholds are irrelevant here; we only measure latency.
		ApproveBelow: 100, DeclineAbove: 101,
		MinSignals: 1, MinConfidence: 0,
		LogicVersion: "incident",
	}
}

func rawSet() ([]ports.Provider, ports.Provider) {
	independent := []ports.Provider{
		providers.NewSimulated(providers.SimConfig{ProviderName: "healthy", Latency: healthyLatency, BaseScore: 20}),
		providers.NewSimulated(providers.SimConfig{ProviderName: "sick", Latency: sickLatency, BaseScore: 20}),
	}
	bureau := providers.NewSimulated(providers.SimConfig{ProviderName: "bureau", Latency: bureauLatency, BaseScore: 30})
	return independent, bureau
}

// guardedSet wraps the raw set with per-source budgets + breakers (trip after 3).
func guardedSet(rec metrics.Recorder) ([]ports.Provider, ports.Provider) {
	indep, bureau := rawSet()
	gc := providers.GuardConfig{
		Budget:                    120 * time.Millisecond,
		ConsecutiveFailuresToTrip: 3,
		OpenTimeout:               5 * time.Second, // stays open across the burst
	}
	out := providers.GuardSet(providers.Set{Independent: indep, Dependent: bureau}, gc, rec)
	return out.Independent, out.Dependent
}

type latencyStats struct {
	all      []time.Duration
	total    time.Duration
	lastFive time.Duration // avg of the final 5 requests (steady state)
	worst    time.Duration
}

func runBurst(score func(context.Context, domain.Application) domain.ScoreResult) latencyStats {
	var st latencyStats
	app := domain.Application{ApplicantID: "acct-1", Product: "checking"}
	for i := 0; i < burst; i++ {
		start := time.Now()
		_ = score(context.Background(), app)
		d := time.Since(start)
		st.all = append(st.all, d)
		st.total += d
		if d > st.worst {
			st.worst = d
		}
	}
	var lastSum time.Duration
	for _, d := range st.all[burst-5:] {
		lastSum += d
	}
	st.lastFive = lastSum / 5
	return st
}

func TestIncident_PerSourceSelfLimits(t *testing.T) {
	// BEFORE.
	indepRaw, bureauRaw := rawSet()
	naive := NewNaive(indepRaw, bureauRaw, incidentCfg(), quietLogger())
	before := runBurst(naive.Score)

	// AFTER.
	rec := metrics.NewCollector()
	indepG, bureauG := guardedSet(rec)
	prod := New(indepG, bureauG, incidentCfg(), quietLogger())
	after := runBurst(prod.Score)

	t.Logf("BEFORE (shared timeout):   total=%v  steady(avg last 5)=%v  worst=%v",
		before.total.Round(time.Millisecond), before.lastFive.Round(time.Millisecond), before.worst.Round(time.Millisecond))
	t.Logf("AFTER  (per-source+breaker): total=%v  steady(avg last 5)=%v  worst=%v",
		after.total.Round(time.Millisecond), after.lastFive.Round(time.Millisecond), after.worst.Round(time.Millisecond))
	t.Logf("sick vendor breaker_open fast-fails recorded: %d", rec.Count("sick", metrics.OutcomeBreakerOpen))

	// 1. Naive: EVERY request pays ~the shared Stage-A deadline (sick vendor pins
	//    the fan-in). Steady-state stays high.
	if before.lastFive < 120*time.Millisecond {
		t.Errorf("BEFORE steady latency %v unexpectedly low; the shared-timeout starvation should keep it near the stage budget", before.lastFive)
	}

	// 2. After the breaker opens, the sick vendor fails fast: steady-state latency
	//    collapses to roughly the healthy vendor + bureau.
	if after.lastFive > 60*time.Millisecond {
		t.Errorf("AFTER steady latency %v too high; the breaker should make the sick vendor cheap", after.lastFive)
	}

	// 3. Overall the per-source design is dramatically faster across the burst.
	if after.total >= before.total/2 {
		t.Errorf("expected per-source total (%v) to be < half of shared total (%v)", after.total, before.total)
	}

	// 4. The breaker actually opened for the sick vendor.
	if rec.Count("sick", metrics.OutcomeBreakerOpen) == 0 {
		t.Error("expected the sick vendor's breaker to open and fast-fail at least once")
	}
}
