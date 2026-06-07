package providers

import (
	"context"
	"errors"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/metrics"
	"github.com/blocklocmedia/fraud-signals/internal/ports"
)

// GuardConfig configures the per-source protection wrapped around a vendor.
type GuardConfig struct {
	// Budget is this vendor's OWN timeout. The whole point of the incident fix:
	// every source is bounded independently, not by one shared fan-out timeout.
	// It is still capped by the outer request deadline (see Fetch).
	Budget time.Duration

	// Breaker tuning. ConsecutiveFailuresToTrip is the headline knob; the rest
	// have sane defaults if left zero.
	ConsecutiveFailuresToTrip uint32
	// HalfOpenMaxRequests is how many trial requests are allowed while half-open.
	HalfOpenMaxRequests uint32
	// OpenTimeout is how long the breaker stays open before probing (half-open).
	OpenTimeout time.Duration
}

// guardedProvider decorates a Provider with three per-source protections:
//
//  1. its OWN timeout budget (a child context), capped by the outer deadline so
//     whichever is sooner wins — one slow vendor can never consume the whole
//     request's budget;
//  2. a circuit breaker (gobreaker) that, once the vendor looks unhealthy, fails
//     fast with ErrOpenState instead of paying the timeout on every call;
//  3. per-vendor latency + outcome metrics — the data behind the per-vendor
//     latency alarm we added after the incident.
//
// It implements ports.Provider, so the Scorer wraps it transparently — the
// decorator pattern is why none of the scoring logic had to change.
type guardedProvider struct {
	inner  ports.Provider
	budget time.Duration
	cb     *gobreaker.CircuitBreaker[domain.RiskData]
	rec    metrics.Recorder
}

var _ ports.Provider = (*guardedProvider)(nil)

// Guard wraps inner with per-source budget, breaker and metrics.
func Guard(inner ports.Provider, cfg GuardConfig, rec metrics.Recorder) ports.Provider {
	if rec == nil {
		rec = metrics.Nop{}
	}
	trip := cfg.ConsecutiveFailuresToTrip
	if trip == 0 {
		trip = 5
	}
	settings := gobreaker.Settings{
		Name:        inner.Name(),
		MaxRequests: cfg.HalfOpenMaxRequests, // 0 => 1 trial request while half-open
		Timeout:     cfg.OpenTimeout,         // 0 => gobreaker default (60s)
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.ConsecutiveFailures >= trip
		},
		// IsSuccessful decides what counts as a failure for the breaker. A
		// context cancellation caused by the CALLER going away (not the vendor
		// being slow) should NOT be held against the vendor — otherwise a burst
		// of client disconnects could trip a perfectly healthy breaker.
		IsSuccessful: func(err error) bool {
			if err == nil {
				return true
			}
			return errors.Is(err, context.Canceled)
		},
	}
	return &guardedProvider{
		inner:  inner,
		budget: cfg.Budget,
		cb:     gobreaker.NewCircuitBreaker[domain.RiskData](settings),
		rec:    rec,
	}
}

// GuardSet wraps every provider in a Set with the same per-source protections,
// returning a new Set. Each wrapped provider gets its OWN breaker instance, so
// one vendor tripping never affects another.
func GuardSet(set Set, cfg GuardConfig, rec metrics.Recorder) Set {
	out := Set{Independent: make([]ports.Provider, len(set.Independent))}
	for i, p := range set.Independent {
		out.Independent[i] = Guard(p, cfg, rec)
	}
	if set.Dependent != nil {
		out.Dependent = Guard(set.Dependent, cfg, rec)
	}
	return out
}

func (g *guardedProvider) Name() string { return g.inner.Name() }

func (g *guardedProvider) Fetch(ctx context.Context, app domain.Application) (domain.RiskData, error) {
	start := time.Now()

	// Execute runs the call through the breaker. When the breaker is OPEN it
	// returns ErrOpenState immediately WITHOUT invoking our function — that's the
	// fast-fail that stops a sick vendor from costing latency on every request.
	data, err := g.cb.Execute(func() (domain.RiskData, error) {
		// Per-source budget derived from ctx: WithTimeout already caps at the
		// outer deadline, so whichever fires first wins.
		cctx, cancel := context.WithTimeout(ctx, g.budget)
		defer cancel()
		return g.inner.Fetch(cctx, app)
	})

	g.rec.ObserveProvider(g.Name(), classify(err), time.Since(start))
	return data, err
}

// classify maps an error to a metric outcome.
func classify(err error) metrics.Outcome {
	switch {
	case err == nil:
		return metrics.OutcomeSuccess
	case errors.Is(err, gobreaker.ErrOpenState), errors.Is(err, gobreaker.ErrTooManyRequests):
		return metrics.OutcomeBreakerOpen
	case errors.Is(err, context.DeadlineExceeded):
		return metrics.OutcomeTimeout
	default:
		return metrics.OutcomeError
	}
}
