# ADR 0003 — Per-source budgets + breakers, not a single shared timeout

**Status:** Accepted — this is the core "incident" of the design.

## Context

We fan out to several risk vendors under a tight p99 budget. The naive approach
bounds the **whole** fan-out with one `context.WithTimeout`.

**The incident:** with a single shared timeout, one slow vendor holds a fan-in
slot until the shared deadline on *every* request. Healthy vendors return fast,
but the request can't complete until the deadline — so p99 collapses to the
budget for all traffic, and under load the slow vendor's outstanding calls
exhaust shared resources (goroutines/connections), starving the healthy path.

## Decision

Give **each vendor its own timeout budget and its own circuit breaker**, behind a
`guardedProvider` decorator, and make the fan-in **completion-aware** (advance
when every vendor *resolves*, success or failure — not only on success or the
deadline).

Three mechanisms, each necessary:

1. **Per-source budget** — `context.WithTimeout` per call, capped by the outer
   deadline (whichever is sooner). One vendor can't consume the whole budget.
2. **Per-source breaker** (`sony/gobreaker`) — after a run of failures it opens
   and fails fast (`ErrOpenState`, ~0 ms) instead of paying the timeout every
   request. A persistently sick vendor becomes cheap.
3. **Completion-aware fan-in** — lets the breaker's fast-fail actually shorten the
   request; otherwise the loop still waits for the deadline.

## Consequences

- Measured before/after (`go test ./internal/scorer -run TestIncident`):
  steady-state **162 ms → 21 ms** with one sick vendor; total burst **1.95 s →
  0.59 s**.
- A `caller-cancelled` error is excluded from breaker failure counts, so client
  disconnects don't trip a healthy vendor.
- More moving parts per vendor; mitigated by the decorator (the Scorer is unaware)
  and by emitting per-vendor metrics that feed a per-vendor latency alarm.

## Alternatives considered

- **Single shared timeout** — simplest, and exactly the failure mode above. Kept
  compiled (`NaiveScorer`) only as the "before" in the demo.
- **Hedged requests / retries** — help tail latency for idempotent reads but
  amplify load on an already-sick vendor; the breaker is the safer first move.
