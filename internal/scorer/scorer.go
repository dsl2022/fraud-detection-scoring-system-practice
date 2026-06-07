// Package scorer is the core application service. It runs a TWO-PHASE pipeline:
//
//	Stage A: fan out CONCURRENTLY to the independent providers and fan in over a
//	         results channel under a Stage-A sub-deadline.
//	Stage B: enrich the application with Stage A's results and call the dependent
//	         (aggregating) provider — the credit bureau.
//
// then aggregate everything into one decision. The whole pipeline is bounded by
// an outer context.WithTimeout, with Stage A budgeted so time remains for
// Stage B (A + B < SLO).
//
// This is the PRODUCTION (per-source) design. It is meant to be fed providers
// already wrapped in providers.Guard (per-source budget + circuit breaker +
// metrics). Its Stage-A fan-in is COMPLETION-aware: it advances as soon as every
// provider RESOLVES (success or failure) rather than waiting for the deadline.
// That is what lets a breaker's fast-fail actually shorten the request — the
// crucial difference from the naive design in naive.go.
package scorer

import (
	"context"
	"log/slog"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/ports"
)

// Scorer is the core service. Providers are split by orchestration class:
// independent (Stage A) and a single dependent aggregator (Stage B, may be nil).
type Scorer struct {
	independent []ports.Provider
	dependent   ports.Provider
	cfg         Config
	log         *slog.Logger
}

// New builds a production Scorer. dependent may be nil.
func New(independent []ports.Provider, dependent ports.Provider, cfg Config, log *slog.Logger) *Scorer {
	if log == nil {
		log = slog.Default()
	}
	return &Scorer{independent: independent, dependent: dependent, cfg: cfg, log: log}
}

// Score runs the two-phase pipeline and returns the aggregated decision.
func (s *Scorer) Score(ctx context.Context, app domain.Application) domain.ScoreResult {
	return s.ScoreObserved(ctx, app, nil)
}

// ScoreObserved is Score with an optional per-result observer. obs is invoked
// once for each provider result as it is collected (independent results from
// Stage A, then the dependent result from Stage B). It powers the audit
// "persist-as-you-go" mode without coupling the scorer to the audit package.
//
// IMPORTANT: obs may be called CONCURRENTLY from the Stage-A fan-out goroutines,
// so an obs that writes to shared state must be safe for concurrent use.
func (s *Scorer) ScoreObserved(ctx context.Context, app domain.Application, obs func(domain.RiskData)) domain.ScoreResult {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Budget)
	defer cancel()

	collected := s.runStageA(ctx, app, obs)

	if s.dependent != nil {
		if data, ok := callDependent(ctx, s.dependent, app, collected, s.log); ok {
			if obs != nil {
				obs(data)
			}
			collected = append(collected, data)
		}
	}

	return decide(s.cfg, collected)
}

// stageAResult signals that one provider has RESOLVED — successfully (ok=true,
// data set) or not (ok=false). Sending on completion (not only on success) is
// what makes the fan-in completion-aware.
type stageAResult struct {
	data domain.RiskData
	ok   bool
}

// runStageA fans out to the independent providers and collects results as each
// one RESOLVES, up to the Stage-A sub-deadline.
//
// Concurrency contract:
//   - results is buffered to len(independent): a goroutine can always deposit its
//     resolution and exit even if we've stopped reading — no goroutine leaks.
//   - every goroutine sends EXACTLY ONCE (success or failure), so the loop of
//     len(independent) iterations terminates as soon as all providers resolve.
//     A guarded provider whose breaker is open resolves in ~0ms, so the request
//     no longer pays its timeout — the self-limiting behaviour.
//   - the aCtx.Done() case is defence-in-depth: if a (mis-wrapped) provider
//     blocks past the stage budget, we still stop waiting.
func (s *Scorer) runStageA(ctx context.Context, app domain.Application, obs func(domain.RiskData)) []domain.RiskData {
	aCtx, cancel := context.WithTimeout(ctx, s.cfg.StageABudget)
	defer cancel()

	results := make(chan stageAResult, len(s.independent))
	for _, p := range s.independent {
		go func(p ports.Provider) {
			data, err := p.Fetch(aCtx, app)
			if err != nil {
				s.log.WarnContext(aCtx, "stage A provider failed",
					"provider", p.Name(), "error", err.Error())
				results <- stageAResult{ok: false}
				return
			}
			if obs != nil {
				obs(data) // persist-as-you-go: record this vendor's response now
			}
			results <- stageAResult{data: data, ok: true}
		}(p)
	}

	collected := make([]domain.RiskData, 0, len(s.independent)+1)
	for range s.independent {
		select {
		case r := <-results:
			if r.ok {
				collected = append(collected, r.data)
			}
		case <-aCtx.Done():
			s.log.WarnContext(aCtx, "stage A deadline reached, proceeding with partial signals",
				"collected", len(collected), "expected", len(s.independent))
			return collected
		}
	}
	return collected
}
