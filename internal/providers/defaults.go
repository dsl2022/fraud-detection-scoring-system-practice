package providers

import (
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/ports"
)

// Provider name constants — used as map keys, metric labels and audit fields, so
// they are defined once here rather than as scattered string literals.
const (
	NameCreditBureau   = "credit_bureau"
	NameIdentityVendor = "identity_verify"
	NameTxnHistory     = "txn_history"
)

// Set groups the providers by their orchestration class for the two-phase
// pipeline:
//
//   - Independent: vendors that need only the raw application payload. They run
//     CONCURRENTLY in Stage A (fan-out). Here: identity verification + the
//     transaction-history source.
//   - Dependent: a vendor that needs the independent vendors' results as input.
//     It runs in Stage B with the application ENRICHED by Stage A. Here: the
//     credit bureau, modelling "bureau request augmented with identity signals."
//
// Modelling the classes explicitly (rather than a flat []Provider) is what lets
// the Scorer express the real dependency graph.
type Set struct {
	Independent []ports.Provider
	Dependent   ports.Provider
}

// DefaultProviders returns the baseline simulated vendors with realistic,
// slightly different latency profiles. Tests and demos construct their own with
// NewSimulated to inject slowness or failures.
//
// Latencies sit comfortably under the per-stage budgets so the happy path
// returns all signals, while leaving headroom to demonstrate degradation by
// bumping one vendor up.
func DefaultProviders() Set {
	return Set{
		// Stage A — independent, run concurrently.
		Independent: []ports.Provider{
			NewSimulated(SimConfig{
				ProviderName: NameIdentityVendor,
				Latency:      60 * time.Millisecond,
				Weight:       1.0,
				Confidence:   0.9,
				BaseScore:    30,
			}),
			NewSimulated(SimConfig{
				ProviderName: NameTxnHistory,
				Latency:      55 * time.Millisecond,
				Weight:       1.0,
				Confidence:   0.85,
				BaseScore:    50,
			}),
		},
		// Stage B — dependent aggregator, enriched with Stage A results.
		Dependent: NewSimulated(SimConfig{
			ProviderName: NameCreditBureau,
			Latency:      40 * time.Millisecond,
			Weight:       1.5, // bureau carries the most signal
			Confidence:   0.95,
			BaseScore:    45,
		}),
	}
}

// DemoSickProviders returns a provider set with ONE deliberately sick
// independent vendor (latency far beyond any budget). It backs the before/after
// demo endpoints: under the naive design this vendor pins every request to the
// shared deadline; under the guarded design its breaker trips and it self-limits.
func DemoSickProviders() Set {
	return Set{
		Independent: []ports.Provider{
			NewSimulated(SimConfig{ProviderName: NameIdentityVendor, Latency: 10 * time.Millisecond, Confidence: 0.9, BaseScore: 30}),
			// The sick vendor — 800ms, an order of magnitude over its budget.
			NewSimulated(SimConfig{ProviderName: NameTxnHistory, Latency: 800 * time.Millisecond, Confidence: 0.85, BaseScore: 50}),
		},
		Dependent: NewSimulated(SimConfig{ProviderName: NameCreditBureau, Latency: 10 * time.Millisecond, Weight: 1.5, Confidence: 0.95, BaseScore: 45}),
	}
}
