package scorer

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/ports"
)

// Config holds the tunable decision thresholds and the stage budgets.
// All come from env vars at startup (12-factor) — nothing is hard-coded into the
// logic, so behaviour is auditable and changeable without a rebuild.
type Config struct {
	// Budget is the outer timeout for the WHOLE pipeline.
	Budget time.Duration
	// StageABudget bounds the independent fan-out, reserving the remainder of
	// Budget for the Stage-B dependent call so total latency stays under the SLO.
	StageABudget time.Duration

	// Decision thresholds on the aggregated 0..100 score.
	ApproveBelow float64 // score <= this => APPROVE
	DeclineAbove float64 // score >= this => DECLINE

	// Coverage gates for graceful degradation.
	MinSignals    int     // need at least this many vendors to auto-decide
	MinConfidence float64 // need at least this average confidence to auto-decide

	// LogicVersion is stamped on every result + audit record so a decision can
	// always be tied back to the exact ruleset that produced it.
	LogicVersion string
}

// callDependent enriches the application with Stage A's signals and calls the
// dependent (bureau) provider using the remaining outer budget. ok=false means
// the dependent call failed/timed out — non-fatal; we score on independents.
//
// Shared by both the production and naive scorers (Stage B is identical; only
// the Stage-A fan-in differs between the two designs).
func callDependent(ctx context.Context, dep ports.Provider, app domain.Application, prior []domain.RiskData, log *slog.Logger) (domain.RiskData, bool) {
	enriched := app
	enriched.PriorSignals = prior // the dependent vendor augments its request with these

	data, err := dep.Fetch(ctx, enriched)
	if err != nil {
		log.WarnContext(ctx, "stage B dependent provider failed",
			"provider", dep.Name(), "error", err.Error())
		return domain.RiskData{}, false
	}
	return data, true
}

// decide turns the collected per-vendor RiskData into a final decision.
//
// Graceful-degradation rules, in priority order:
//  1. No signals at all => MANUAL_REVIEW. We NEVER auto-DECLINE on no data;
//     absence of evidence is not evidence of fraud.
//  2. Too few signals, or average confidence too low => MANUAL_REVIEW. Some data,
//     but not enough to trust an automated decision.
//  3. Otherwise apply the score thresholds (APPROVE / DECLINE / middle ->
//     MANUAL_REVIEW).
func decide(cfg Config, collected []domain.RiskData) domain.ScoreResult {
	res := domain.ScoreResult{
		LogicVersion: cfg.LogicVersion,
		SignalsUsed:  make([]string, 0, len(collected)),
	}

	if len(collected) == 0 {
		res.Decision = domain.DecisionManualReview
		return res
	}

	var weightedSum, weightTotal, confSum float64
	for _, d := range collected {
		weightedSum += d.Score * d.Weight
		weightTotal += d.Weight
		confSum += d.Confidence
		res.SignalsUsed = append(res.SignalsUsed, d.Source)
	}
	// Sort so the signal list (and thus tests + audit records) is deterministic
	// regardless of the order results raced in.
	sort.Strings(res.SignalsUsed)

	if weightTotal > 0 {
		res.Score = weightedSum / weightTotal
	}
	avgConf := confSum / float64(len(collected))

	// Coverage gate: not enough signal => hand to a human rather than guess.
	if len(collected) < cfg.MinSignals || avgConf < cfg.MinConfidence {
		res.Decision = domain.DecisionManualReview
		return res
	}

	switch {
	case res.Score >= cfg.DeclineAbove:
		res.Decision = domain.DecisionDecline
	case res.Score <= cfg.ApproveBelow:
		res.Decision = domain.DecisionApprove
	default:
		res.Decision = domain.DecisionManualReview
	}
	return res
}
