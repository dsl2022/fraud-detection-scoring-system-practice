package scorer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/ports"
)

// fakeProvider is a fully-controllable Provider for deterministic tests:
// configurable latency, an optional injected error, and a fixed score. It also
// records the last Application it was Fetched with, so tests can assert Stage B
// enrichment.
type fakeProvider struct {
	name    string
	latency time.Duration
	err     error
	score   float64
	conf    float64
	weight  float64

	mu      sync.Mutex
	gotApp  domain.Application
	gotCall bool
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Fetch(ctx context.Context, app domain.Application) (domain.RiskData, error) {
	t := time.NewTimer(f.latency)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return domain.RiskData{}, ctx.Err()
	case <-t.C:
	}
	f.mu.Lock()
	f.gotApp = app
	f.gotCall = true
	f.mu.Unlock()
	if f.err != nil {
		return domain.RiskData{}, f.err
	}
	conf := f.conf
	if conf == 0 {
		conf = 0.9
	}
	weight := f.weight
	if weight == 0 {
		weight = 1
	}
	return domain.RiskData{Source: f.name, Score: f.score, Confidence: conf, Weight: weight}, nil
}

func (f *fakeProvider) captured() (domain.Application, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotApp, f.gotCall
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func baseCfg() Config {
	return Config{
		Budget:        150 * time.Millisecond,
		StageABudget:  80 * time.Millisecond,
		ApproveBelow:  35,
		DeclineAbove:  70,
		MinSignals:    2,
		MinConfidence: 0.6,
		LogicVersion:  "test-v1",
	}
}

func TestScore_Decisions(t *testing.T) {
	tests := []struct {
		name         string
		independent  []ports.Provider
		dependent    ports.Provider
		wantDecision domain.Decision
		wantSignals  []string
		// maxLatency asserts the call returns within ~budget+slack, proving the
		// pipeline is deadline-bounded.
		maxLatency time.Duration
	}{
		{
			name: "all fast, low score => APPROVE",
			independent: []ports.Provider{
				&fakeProvider{name: "ident", latency: 10 * time.Millisecond, score: 20},
				&fakeProvider{name: "txn", latency: 10 * time.Millisecond, score: 25},
			},
			dependent:    &fakeProvider{name: "bureau", latency: 10 * time.Millisecond, score: 15},
			wantDecision: domain.DecisionApprove,
			wantSignals:  []string{"bureau", "ident", "txn"},
		},
		{
			name: "all fast, high score => DECLINE",
			independent: []ports.Provider{
				&fakeProvider{name: "ident", latency: 10 * time.Millisecond, score: 85},
				&fakeProvider{name: "txn", latency: 10 * time.Millisecond, score: 90},
			},
			dependent:    &fakeProvider{name: "bureau", latency: 10 * time.Millisecond, score: 80},
			wantDecision: domain.DecisionDecline,
			wantSignals:  []string{"bureau", "ident", "txn"},
		},
		{
			name: "mid-band score => MANUAL_REVIEW",
			independent: []ports.Provider{
				&fakeProvider{name: "ident", latency: 10 * time.Millisecond, score: 50},
				&fakeProvider{name: "txn", latency: 10 * time.Millisecond, score: 55},
			},
			dependent:    &fakeProvider{name: "bureau", latency: 10 * time.Millisecond, score: 52},
			wantDecision: domain.DecisionManualReview,
			wantSignals:  []string{"bureau", "ident", "txn"},
		},
		{
			name: "one independent slow beyond Stage A => excluded, bureau still runs",
			independent: []ports.Provider{
				&fakeProvider{name: "ident", latency: 10 * time.Millisecond, score: 85},
				&fakeProvider{name: "slow", latency: 500 * time.Millisecond, score: 0},
			},
			dependent: &fakeProvider{name: "bureau", latency: 10 * time.Millisecond, score: 90},
			// 2 signals (ident + bureau) >= MinSignals, high score => DECLINE.
			wantDecision: domain.DecisionDecline,
			wantSignals:  []string{"bureau", "ident"},
			maxLatency:   150 * time.Millisecond,
		},
		{
			name: "dependent (bureau) fails => score on independent signals only",
			independent: []ports.Provider{
				&fakeProvider{name: "ident", latency: 10 * time.Millisecond, score: 20},
				&fakeProvider{name: "txn", latency: 10 * time.Millisecond, score: 25},
			},
			dependent:    &fakeProvider{name: "bureau", latency: 5 * time.Millisecond, err: errors.New("bureau down")},
			wantDecision: domain.DecisionApprove, // 2 low signals
			wantSignals:  []string{"ident", "txn"},
		},
		{
			name: "only one independent survives, bureau down => MANUAL_REVIEW (never auto-decline)",
			independent: []ports.Provider{
				&fakeProvider{name: "ident", latency: 10 * time.Millisecond, score: 95},
				&fakeProvider{name: "slow", latency: 500 * time.Millisecond, score: 95},
			},
			dependent:    &fakeProvider{name: "bureau", latency: 5 * time.Millisecond, err: errors.New("bureau down")},
			wantDecision: domain.DecisionManualReview, // only 1 signal < MinSignals
			wantSignals:  []string{"ident"},
			maxLatency:   150 * time.Millisecond,
		},
		{
			name: "everything fails => MANUAL_REVIEW, no signals",
			independent: []ports.Provider{
				&fakeProvider{name: "ident", latency: 5 * time.Millisecond, err: errors.New("boom")},
				&fakeProvider{name: "txn", latency: 5 * time.Millisecond, err: errors.New("boom")},
			},
			dependent:    &fakeProvider{name: "bureau", latency: 5 * time.Millisecond, err: errors.New("boom")},
			wantDecision: domain.DecisionManualReview,
			wantSignals:  []string{},
		},
		{
			name: "no dependent configured => pure fan-out still works",
			independent: []ports.Provider{
				&fakeProvider{name: "ident", latency: 10 * time.Millisecond, score: 20},
				&fakeProvider{name: "txn", latency: 10 * time.Millisecond, score: 25},
			},
			dependent:    nil,
			wantDecision: domain.DecisionApprove,
			wantSignals:  []string{"ident", "txn"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(tt.independent, tt.dependent, baseCfg(), quietLogger())
			start := time.Now()
			res := s.Score(context.Background(), domain.Application{ApplicantID: "x", Product: "p"})
			elapsed := time.Since(start)

			if res.Decision != tt.wantDecision {
				t.Errorf("decision = %q, want %q (score=%.1f)", res.Decision, tt.wantDecision, res.Score)
			}
			if !equalStrings(res.SignalsUsed, tt.wantSignals) {
				t.Errorf("signals = %v, want %v", res.SignalsUsed, tt.wantSignals)
			}
			if tt.maxLatency > 0 && elapsed > tt.maxLatency {
				t.Errorf("Score took %v, exceeds bound %v", elapsed, tt.maxLatency)
			}
		})
	}
}

// TestScore_StageBReceivesEnrichment verifies the two-phase contract: the
// dependent provider is called AFTER the independents and with their results in
// PriorSignals. This is what distinguishes a pipeline from a flat fan-out.
func TestScore_StageBReceivesEnrichment(t *testing.T) {
	bureau := &fakeProvider{name: "bureau", latency: 5 * time.Millisecond, score: 40}
	independent := []ports.Provider{
		&fakeProvider{name: "ident", latency: 5 * time.Millisecond, score: 30},
		&fakeProvider{name: "txn", latency: 5 * time.Millisecond, score: 50},
	}
	s := New(independent, bureau, baseCfg(), quietLogger())
	_ = s.Score(context.Background(), domain.Application{ApplicantID: "x", Product: "p"})

	gotApp, called := bureau.captured()
	if !called {
		t.Fatal("dependent provider was never called")
	}
	if len(gotApp.PriorSignals) != 2 {
		t.Fatalf("bureau received %d prior signals, want 2 (enrichment from Stage A)", len(gotApp.PriorSignals))
	}
	names := map[string]bool{}
	for _, sig := range gotApp.PriorSignals {
		names[sig.Source] = true
	}
	if !names["ident"] || !names["txn"] {
		t.Errorf("bureau enrichment missing expected sources: got %v", names)
	}
}

// TestScore_RespectsCallerCancellation verifies that cancelling the parent ctx
// (e.g. client disconnect) short-circuits the pipeline well before the budget.
func TestScore_RespectsCallerCancellation(t *testing.T) {
	independent := []ports.Provider{
		&fakeProvider{name: "a", latency: 500 * time.Millisecond, score: 50},
		&fakeProvider{name: "b", latency: 500 * time.Millisecond, score: 50},
	}
	s := New(independent, &fakeProvider{name: "bureau", latency: 500 * time.Millisecond}, baseCfg(), quietLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res := s.Score(ctx, domain.Application{ApplicantID: "x", Product: "p"})
	if elapsed := time.Since(start); elapsed > 120*time.Millisecond {
		t.Errorf("Score did not honour cancellation; took %v", elapsed)
	}
	if res.Decision != domain.DecisionManualReview {
		t.Errorf("decision = %q, want MANUAL_REVIEW on cancellation", res.Decision)
	}
}

// TestScore_NoGoroutineLeak runs many pipelines that include a vendor far slower
// than the budget and asserts the goroutine count returns to baseline.
//
// We avoid an external dependency (go.uber.org/goleak) for Stage 1 and use a
// runtime poll instead; goleak can be added in CI later if desired.
func TestScore_NoGoroutineLeak(t *testing.T) {
	independent := []ports.Provider{
		&fakeProvider{name: "a", latency: 5 * time.Millisecond, score: 20},
		&fakeProvider{name: "slow", latency: 800 * time.Millisecond, score: 20},
	}
	s := New(independent, &fakeProvider{name: "bureau", latency: 5 * time.Millisecond, score: 20}, baseCfg(), quietLogger())

	time.Sleep(20 * time.Millisecond)
	runtime.GC()
	before := runtime.NumGoroutine()

	for i := 0; i < 50; i++ {
		_ = s.Score(context.Background(), domain.Application{ApplicantID: "x", Product: "p"})
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.GC()
		after := runtime.NumGoroutine()
		if after <= before+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: before=%d after=%d", before, after)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
