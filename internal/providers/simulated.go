// Package providers contains adapters that implement ports.Provider.
//
// For Stage 1 these are SIMULATED vendors: they don't make network calls, they
// just sleep for a configurable duration and optionally return an injected
// error. That gives us a deterministic way to demo the two behaviours that
// matter for the interview story: latency-driven graceful degradation and
// (Stage 2) circuit breaking.
package providers

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/ports"
)

// SimConfig configures one simulated vendor. Everything is explicit so tests
// and demos are fully deterministic (no hidden randomness, no time-of-day).
type SimConfig struct {
	ProviderName string        // e.g. "credit_bureau"
	Latency      time.Duration // how long this vendor "takes" to respond
	FailErr      error         // if non-nil, Fetch returns this AFTER Latency
	Weight       float64       // weight in the aggregator (default 1.0)
	Confidence   float64       // self-reported confidence 0..1 (default 0.9)
	BaseScore    float64       // baseline risk this vendor reports, 0..100
}

// Simulated is a fake external risk vendor implementing ports.Provider.
type Simulated struct {
	cfg SimConfig
}

// compile-time check that *Simulated satisfies the port.
var _ ports.Provider = (*Simulated)(nil)

// NewSimulated builds a simulated provider, filling in sane defaults so callers
// only set the fields they care about.
func NewSimulated(cfg SimConfig) *Simulated {
	if cfg.Weight == 0 {
		cfg.Weight = 1.0
	}
	if cfg.Confidence == 0 {
		cfg.Confidence = 0.9
	}
	return &Simulated{cfg: cfg}
}

func (s *Simulated) Name() string { return s.cfg.ProviderName }

// Fetch simulates a vendor call.
//
// The key correctness detail: we race the simulated work timer against the
// caller's context. If ctx is cancelled / hits its deadline first, we return
// ctx.Err() immediately instead of sleeping out the full Latency. This is what
// makes the Scorer's timeout actually bound this vendor and what prevents
// goroutine leaks — without it, a "slow vendor" goroutine would keep running
// after the request gave up on it.
func (s *Simulated) Fetch(ctx context.Context, app domain.Application) (domain.RiskData, error) {
	t := time.NewTimer(s.cfg.Latency)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return domain.RiskData{}, ctx.Err()
	case <-t.C:
		// "work" finished within the deadline.
	}

	if s.cfg.FailErr != nil {
		return domain.RiskData{}, s.cfg.FailErr
	}

	return domain.RiskData{
		Source:     s.cfg.ProviderName,
		Score:      s.deriveScore(app),
		Confidence: s.cfg.Confidence,
		Weight:     s.cfg.Weight,
	}, nil
}

// deriveScore produces a deterministic-but-application-dependent risk score.
//
// A real vendor would run a model; here we anchor on BaseScore and nudge it by a
// stable hash of the applicant id so different applicants get different (but
// repeatable) scores. Determinism is the point: it makes tests assertable.
func (s *Simulated) deriveScore(app domain.Application) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(app.ApplicantID))
	// delta in [-10, +10]
	delta := float64(h.Sum32()%2001)/100.0 - 10.0
	score := s.cfg.BaseScore + delta

	// Two-phase enrichment: a dependent provider (the bureau) is invoked with
	// PriorSignals populated from Stage A. Blending our baseline with the mean of
	// those upstream scores makes the dependency OBSERVABLE — the bureau's answer
	// genuinely depends on the identity/transaction vendors, which is the whole
	// point of modelling this as a pipeline rather than a flat fan-out.
	if len(app.PriorSignals) > 0 {
		var sum float64
		for _, d := range app.PriorSignals {
			sum += d.Score
		}
		priorMean := sum / float64(len(app.PriorSignals))
		score = 0.5*score + 0.5*priorMean
	}
	return clamp(score, 0, 100)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
