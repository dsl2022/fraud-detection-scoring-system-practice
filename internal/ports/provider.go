// Package ports defines the interfaces (the "ports" of the hexagon) that the
// core depends on. Concrete adapters (simulated vendors now; real HTTP/gRPC
// vendor clients later) live elsewhere and satisfy these interfaces.
package ports

import (
	"context"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
)

// Provider is the driven port for a single external risk-signal vendor.
//
// The interface is intentionally tiny — Name() for identification in
// metrics/audit/logs, and Fetch() for the actual call. Keeping it small means:
//   - it's trivial to add a new vendor (implement two methods),
//   - the Scorer can treat every vendor uniformly in its fan-out,
//   - we can wrap any Provider in a decorator (Stage 2's guardedProvider adds a
//     per-vendor timeout + circuit breaker without the Scorer knowing).
//
// Fetch MUST honour ctx: when ctx is cancelled or its deadline passes, Fetch
// should return promptly with ctx.Err(). The Scorer relies on this to bound
// latency and to avoid leaking goroutines.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, app domain.Application) (domain.RiskData, error)
}
