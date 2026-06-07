# ADR 0004 — Use a library (`sony/gobreaker`) for the circuit breaker

**Status:** Accepted

## Context

Per ADR 0003 each vendor needs a circuit breaker. We could hand-roll the state
machine or adopt a library.

## Decision

Use **`github.com/sony/gobreaker/v2`**, wrapped behind our own `guardedProvider`
so the dependency is isolated and swappable.

## Rationale

- **The state machine is easy to get subtly wrong.** Closed → open → half-open
  transitions, the half-open probe count, consecutive-vs-ratio trip conditions,
  and the cool-down window are exactly the kind of concurrency-sensitive logic
  where a battle-tested implementation beats a bespoke one.
- **Small, focused, generic.** gobreaker is tiny, dependency-light, and the v2
  generics give us `Execute(func() (RiskData, error))` with no `interface{}`
  casting.
- **We still own policy.** `ReadyToTrip`, `IsSuccessful` (we exclude caller
  cancellation), timeout and half-open limits are all configured by us. The
  library provides the mechanism; we provide the judgement.
- **Isolation.** It's used only inside `guardedProvider`; replacing it (or
  building our own later) touches one file.

## Consequences

- One more dependency to track for CVEs (covered by the image scan in CI).
- We're bound to gobreaker's model (per-instance, in-process). For
  *cross-instance* breaking we'd need a distributed breaker — out of scope; the
  per-source latency alarm covers fleet-wide vendor health.

## Alternatives considered

- **Build our own** — full control, but re-deriving well-known logic and tests
  for no real benefit.
- **`gobreaker` v1** — works, but `interface{}` ergonomics; v2's generics are
  cleaner.
- **Heavier resilience frameworks** (e.g. resilience4j-style stacks) — more than
  we need; would pull in retry/bulkhead machinery we deliberately kept explicit.
