# ADR 0006 — Go over Java for a latency-sensitive container service

**Status:** Accepted

## Context

The service is a stateless, latency-sensitive scorer with heavy concurrent
fan-out and a strict p99 budget, deployed as a container that autoscales (so it
starts often). Language choice affects tail latency, image size, and the
ergonomics of the concurrency + deadline model.

## Decision

Implement in **Go**.

## Rationale

- **No JVM warmup.** A fresh Fargate task serves a correct, in-budget p99 almost
  immediately. A cold JVM pays interpretation + JIT warmup; on a service that
  scales out during traffic spikes, those first requests can blow the SLO exactly
  when it matters.
- **Static binary, tiny image.** `CGO_ENABLED=0` → an ~19 MB distroless image:
  small attack surface, fast pulls, fast scale-out. A JRE base image is hundreds
  of MB.
- **Goroutines + `context` fit the problem.** The fan-out/fan-in with per-source
  deadlines and cancellation is idiomatic and cheap: one goroutine per vendor, a
  buffered results channel, `select` over results and `ctx.Done()`. Context
  cancellation threads cleanly from the HTTP/gRPC handler to every vendor call.
- **Low-latency GC.** Go's concurrent, low-pause collector keeps GC out of the
  tail for this allocation profile.
- **Predictable footprint.** Lower, flatter memory use → cheaper Fargate sizing.

## Honest counterpoints (where Java has closed the gap)

- **JDK 21 virtual threads (Project Loom)** make massive concurrent I/O ergonomic
  in Java too; the goroutine advantage is narrower than it was.
- **GraalVM native image** removes most JVM warmup and shrinks the binary,
  neutralizing the cold-start argument — at the cost of build complexity and some
  reflection/runtime constraints.
- A mature JVM shop with deep Spring/observability investment may be more
  *productive* in Java even if Go has a thin raw-latency edge.

On balance, for a from-scratch, latency-critical, frequently-restarting container,
Go's defaults (no warmup, tiny static image, native concurrency+context) win
without the extra build machinery GraalVM requires.

## Consequences

- Smaller library ecosystem than the JVM for some enterprise integrations;
  acceptable for this surface.
- Team must be comfortable with Go idioms (error handling, contexts).
