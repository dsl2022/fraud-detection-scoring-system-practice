# ADR 0005 — Two-phase orchestration (concurrent independents → enriched bureau)

**Status:** Accepted

## Context

The vendors are not peers. The credit bureau produces a better answer when its
request is **augmented** with signals from the identity and transaction vendors.
A flat concurrent fan-out can't express that dependency.

## Decision

Model scoring as a **two-stage pipeline**:

- **Stage A — independent vendors** (`identity_verify`, `txn_history`) take only
  the application and run **concurrently** (fan-out / fan-in).
- **Stage B — the dependent bureau** runs **after**, with the application
  **enriched** by Stage A's results.

The enrichment travels on `Application.PriorSignals` (tagged `json:"-"`, so it's
internal-only and never on the wire). This keeps the `Provider` port a **single
uniform interface** that both vendor classes — and the `guardedProvider` wrapper —
implement identically.

Budgets: one outer `context.WithTimeout` bounds the whole pipeline; Stage A gets a
sub-deadline so time remains for Stage B (A + B < SLO).

## Consequences

- The bureau's score observably depends on upstream signals (a test asserts it
  receives `PriorSignals`), matching the real system's "bureau request augmented
  with identity signals."
- Latency is Stage A (max of independents) + Stage B (bureau), not the max of all
  three — a deliberate cost of the dependency, kept within budget.
- Each stage degrades independently: a slow independent is dropped at the Stage-A
  deadline; a failed bureau just removes one signal.
- Persistence can be **as-you-go** (persist each vendor response as it returns) or
  **combined** (one record after the bureau) — a config toggle, mirroring the real
  flow.

## Alternatives considered

- **Flat fan-out of all three** — simpler, but the bureau can't use upstream
  signals; wrong model.
- **A separate `DependentProvider` interface** — would split the port and the
  guard wrapper in two; the `PriorSignals` channel keeps one clean abstraction.
