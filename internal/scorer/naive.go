package scorer

import (
	"context"
	"log/slog"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/ports"
)

// NaiveScorer is the "BEFORE" of the incident story, kept compilable alongside
// the production Scorer so the before/after benchmark can run both in one binary.
// (The spec allows a build flag instead; keeping both types makes the comparison
// a single `go test` rather than two builds.)
//
// Two things make it naive, and both are fixed by the production design:
//
//  1. SINGLE SHARED TIMEOUT: it is meant to be fed RAW (unguarded) providers, so
//     the only bound on any vendor is the shared Stage-A deadline. One slow
//     vendor therefore holds a slot until that deadline on EVERY request — there
//     is no per-source budget and no breaker to make a sick vendor cheap.
//
//  2. RESULTS-OR-DEADLINE FAN-IN: the loop waits for successful RESULTS or the
//     deadline. A vendor that produces no result (slow, erroring) never advances
//     the loop, so the request blocks until the shared deadline even though the
//     healthy vendors already answered. This is the latency starvation.
type NaiveScorer struct {
	independent []ports.Provider
	dependent   ports.Provider
	cfg         Config
	log         *slog.Logger
}

func NewNaive(independent []ports.Provider, dependent ports.Provider, cfg Config, log *slog.Logger) *NaiveScorer {
	if log == nil {
		log = slog.Default()
	}
	return &NaiveScorer{independent: independent, dependent: dependent, cfg: cfg, log: log}
}

func (s *NaiveScorer) Score(ctx context.Context, app domain.Application) domain.ScoreResult {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Budget)
	defer cancel()

	collected := s.runStageA(ctx, app)

	if s.dependent != nil {
		if data, ok := callDependent(ctx, s.dependent, app, collected, s.log); ok {
			collected = append(collected, data)
		}
	}

	return decide(s.cfg, collected)
}

// runStageA is the naive fan-in: it only ever advances on a successful result or
// the shared deadline. A non-responding vendor pins the loop until aCtx.Done().
func (s *NaiveScorer) runStageA(ctx context.Context, app domain.Application) []domain.RiskData {
	aCtx, cancel := context.WithTimeout(ctx, s.cfg.StageABudget)
	defer cancel()

	results := make(chan domain.RiskData, len(s.independent))
	for _, p := range s.independent {
		go func(p ports.Provider) {
			data, err := p.Fetch(aCtx, app)
			if err != nil {
				s.log.WarnContext(aCtx, "stage A provider failed",
					"provider", p.Name(), "error", err.Error())
				return // NB: no completion signal — the naive flaw.
			}
			results <- data
		}(p)
	}

	collected := make([]domain.RiskData, 0, len(s.independent)+1)
	for range s.independent {
		select {
		case d := <-results:
			collected = append(collected, d)
		case <-aCtx.Done():
			return collected
		}
	}
	return collected
}
