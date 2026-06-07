# Fraud Signals Platform

A production-grade, audit-ready **real-time fraud scoring** microservice in Go.
It scores an account-opening / payment application for risk *while the caller
waits*, under a hard **sub-200 ms p99** budget, by fanning out to multiple risk
vendors and degrading gracefully when any of them is slow or down.

REST **and** gRPC at the edge; an async SQS→Lambda tier for durable follow-up
work; an immutable audit trail; and all infrastructure in Terraform on AWS ECS
Fargate.

---

## 1. What it does

`POST /v1/score` takes an application and returns a risk **score**, a
**decision** (`APPROVE | DECLINE | MANUAL_REVIEW`), the **signal sources
actually used**, and a **logic version** tag.

```bash
curl -s -XPOST localhost:8080/v1/score \
  -d '{"applicant_id":"acct-123","product":"checking","requested_amount":5000}'
# {"score":46.4,"decision":"MANUAL_REVIEW","signals_used":["credit_bureau","identity_verify","txn_history"],"logic_version":"scoring-v1.0.0"}
```

### Non-functional acceptance criteria (the NFRs)

| NFR | How it's met |
|---|---|
| sub-200 ms p99 under normal vendor latency | per-stage budgets under a 180 ms outer deadline; concurrent fan-out |
| degrades, never hangs, on a slow/down vendor | per-source timeout + circuit breaker; score on what arrived |
| every decision leaves a reviewable immutable record | hash-of-inputs audit record, write-once in DynamoDB |
| infra reproducible from Terraform | modular TF, remote state, `validate`/`fmt` clean |
| least-privilege IAM (task vs execution split) | three separate roles; OIDC; no long-lived keys |
| `go test -race`, no goroutine leaks | race suite + leak assertion in CI |

---

## 2. Architecture

Hexagonal: the domain + scorer sit at the center and depend on **ports**;
transports and vendor clients are **adapters** on the edges.

```
                 ┌────────────── edge (adapters) ──────────────┐
   client ──▶ REST /v1  ─┐                                      │
   internal ─▶ gRPC      ─┤  request-id · logging · auth (JWT)  │
                          ▼                                      │
                 ┌───────────────────────────────┐             │
                 │     service.ScoringService     │  hash inputs · publish event
                 └───────────────┬───────────────┘             │
                                 ▼                              │
                 ┌───────────────────────────────┐             │
                 │           Scorer (core)        │             │
                 │  Stage A: fan out to           │             │
                 │   INDEPENDENT vendors  ──┐     │             │
                 │  Stage B: enriched call  ▼     │             │
                 │   to DEPENDENT bureau          │             │
                 └───────┬───────────────────────┘             │
                         │  each vendor wrapped in              │
                         ▼  guardedProvider (budget+breaker+EMF)│
            ┌──────────┬──────────┬──────────┐                 │
            │ identity │   txn    │  credit  │  (Provider port) │
            │  verify  │ history  │  bureau  │                  │
            └──────────┴──────────┴──────────┘                 │
                 └──────────────────────────────────────────────┘
                                 │ publish ScoringEvent
                                 ▼
         SQS (events) ─▶ Lambda consumer (idempotent) ─▶ DynamoDB audit
              │  maxReceiveCount                │ MANUAL_REVIEW ─▶ review queue
              ▼                                 └ retraining sink
            DLQ
```

### 2a. Two-phase orchestration

Vendors aren't a flat fan-out — they form a small dependency graph:

- **Stage A — independent vendors** (`identity_verify`, `txn_history`) need only
  the application. They run **concurrently** and we collect whatever arrives by
  the Stage-A sub-deadline.
- **Stage B — the dependent bureau** (`credit_bureau`) is called *after*, with
  the application **enriched** by Stage A's results (think: a bureau request
  augmented with identity signals). Its score genuinely depends on the upstream
  vendors, so order matters.

The whole pipeline is bounded by one outer `context.WithTimeout`; Stage A is
budgeted so time remains for Stage B (A + B < SLO). See
[`internal/scorer/scorer.go`](internal/scorer/scorer.go) and the
[two-phase ADR](docs/adr/0005-two-phase-orchestration.md).

### 2b. Resilience — per-source timeout + circuit breaker (the incident)

Each vendor is wrapped in a **`guardedProvider`** decorator
([`internal/providers/guarded.go`](internal/providers/guarded.go)) that gives it
(1) its **own** timeout budget, (2) a **per-source circuit breaker**
(`sony/gobreaker`) that fails fast when the vendor is unhealthy, and (3)
per-vendor latency/outcome metrics (CloudWatch EMF → the per-vendor alarm).

This is the fix for a real incident: with a *single shared timeout*, one slow
vendor pins **every** request to the deadline; with **per-source** budgets +
breakers, a sick vendor **self-limits** and the request returns as soon as the
healthy vendors answer. See §5 to run the before/after, and the
[ADR](docs/adr/0003-per-source-vs-shared-timeout.md).

### 2c. Graceful degradation

We **never hang** on a vendor and **never auto-DECLINE on incomplete data**:
no signals → `MANUAL_REVIEW`; too few signals / low confidence → `MANUAL_REVIEW`;
otherwise apply the score thresholds. Absence of evidence is not evidence of fraud.

### 2d. Async decoupling + audit

Scoring publishes a `ScoringEvent` to SQS and returns; the **idempotent** Lambda
consumer ([`internal/consumer`](internal/consumer/consumer.go)) writes the
immutable audit record (write-once conditional put on `request_id` → free
dedup under at-least-once delivery), enqueues `MANUAL_REVIEW` cases, and feeds
the retraining sink. Failures redrive to a **DLQ** after `maxReceiveCount`.

The audit record stores a **hash** of the inputs (not raw PII), the signals
used, score, decision, logic version, subject (from the JWT), timestamp and
request id — everything a reviewer needs to reconstruct a decision.

---

## 3. Run it locally

### Just the service (no AWS)

```bash
go run ./cmd/server          # REST :8080, gRPC :9090
make test                    # go test -race ./...
```

### Full end-to-end pipeline (LocalStack)

```bash
docker compose up --build
curl -s -XPOST localhost:8080/v1/score -d '{"applicant_id":"a1","product":"checking"}'
# inspect the audit trail written by the worker:
docker compose exec localstack awslocal dynamodb scan --table-name fraud-audit
```

`POST → SQS → worker → DynamoDB`. The `worker` service is the local stand-in for
the Lambda consumer and drives the **same** consumer core.

---

## 4. Configuration (12-factor)

All via env vars; nothing environment-specific is compiled in. Highlights:

| Var | Default | Meaning |
|---|---|---|
| `PORT` / `GRPC_PORT` | 8080 / 9090 | listen ports |
| `SCORE_BUDGET` | 180ms | outer pipeline deadline |
| `STAGE_A_BUDGET` | 120ms | independent fan-out sub-deadline |
| `PROVIDER_BUDGET` | 120ms | per-vendor timeout (guardedProvider) |
| `BREAKER_TRIP_FAILURES` | 5 | consecutive failures to open a breaker |
| `MIN_SIGNALS` / `MIN_CONFIDENCE` | 2 / 0.6 | degradation gates |
| `APPROVE_BELOW` / `DECLINE_ABOVE` | 35 / 70 | decision thresholds |
| `AUTH_ENABLED` / `JWT_SECRET` | false / "" | edge JWT validation (HS256) |
| `PERSIST_MODE` | combined | `combined` or `as_you_go` |
| `ASYNC_ENABLED` / `AUDIT_QUEUE_URL` | false / "" | publish events to SQS |
| `METRICS_EMF` / `METRICS_NAMESPACE` | false / FraudSignals | per-vendor CloudWatch EMF |
| `DEMO_ENDPOINTS` | true | expose the incident demo routes |

---

## 5. Demo the incident (before / after)

The headline story, runnable two ways.

### As a test (deterministic, with numbers)

```bash
go test ./internal/scorer -run TestIncident -v
# BEFORE (shared timeout):    steady ≈ 162ms   (sick vendor pins every request)
# AFTER  (per-source+breaker): steady ≈  21ms   (breaker trips -> sick vendor self-limits)
```

### Live, over HTTP

With `DEMO_ENDPOINTS=true`, two endpoints score the **same sick vendor set** two
ways. Watch the `X-Score-Latency-Ms` header diverge:

```bash
go run ./cmd/server
# naive: ~130ms on EVERY request
for i in $(seq 8); do curl -s -o /dev/null -D - -XPOST localhost:8080/demo/naive/score \
  -d '{"applicant_id":"a1","product":"checking"}' | grep -i latency; done
# guarded: ~130ms for the first few, then COLLAPSES to ~20ms once the breaker trips
for i in $(seq 8); do curl -s -o /dev/null -D - -XPOST localhost:8080/demo/guarded/score \
  -d '{"applicant_id":"a1","product":"checking"}' | grep -i latency; done
```

Set `BREAKER_TRIP_FAILURES=3` to make the guarded endpoint heal faster in the demo.

---

## 6. Deploy (AWS)

All infra is Terraform ([`deploy/terraform`](deploy/terraform)), three composed
modules (network → platform → app) + a gated governance module (CloudTrail +
Config). Per-env state and inputs:

```bash
make tf-init  ENV=dev     # init against the env's S3 backend + DynamoDB lock
make tf-plan  ENV=dev
make tf-apply ENV=dev
```

CI/CD ([`.github/workflows`](.github/workflows)):

- **app-deploy.yml** — `go vet` · golangci-lint · `go test -race` · build ·
  **Trivy** scan · push to ECR · scoped **rolling** apply.
- **infra.yml** — `fmt`/`validate` · **plan posted on the PR** · apply on merge,
  via separate OIDC plan/apply roles. Protected `main` + required reviews =
  separation-of-duties (ITGC change control).

Rollback: Terraform has no auto-rollback — **revert the PR and re-apply** the
previous known-good commit. Blue/green via CodeDeploy is the
[deployment ADR](docs/adr/0007-rolling-vs-blue-green.md).

---

## 7. Project layout

```
cmd/
  server/      REST + gRPC edge, graceful shutdown
  consumer/    Lambda SQS consumer (AWS)
  worker/      local SQS poller (docker-compose)
internal/
  domain/      core types (no infra imports)
  ports/       Provider interface
  providers/   simulated vendors + guardedProvider (budget/breaker/metrics)
  scorer/      two-phase pipeline (+ naive variant for the incident demo)
  service/     orchestration: hash, score, publish
  events/      ScoringEvent + Publisher (SQS/log/memory)
  consumer/    idempotent consumer core (transport-agnostic)
  audit/       audit records + sinks (DynamoDB/memory/log)
  auth/        pluggable token validation (stdlib HS256)
  metrics/     pluggable Recorder (in-memory + CloudWatch EMF)
  httpapi/     REST adapter + middleware + health
  grpcapi/     gRPC adapter + interceptors + generated stubs
  reqid/ awscfg/ config/
deploy/terraform/  modular IaC + per-env tfvars/backends
docs/adr/      architecture decision records
```

## 8. Architecture Decision Records

See [`docs/adr/`](docs/adr/):

1. [ECS Fargate over EKS](docs/adr/0001-ecs-over-eks.md)
2. [Terraform over CloudFormation](docs/adr/0002-terraform-over-cloudformation.md)
3. [Per-source vs shared timeout](docs/adr/0003-per-source-vs-shared-timeout.md)
4. [Library vs build the circuit breaker](docs/adr/0004-library-vs-build-circuit-breaker.md)
5. [Two-phase orchestration](docs/adr/0005-two-phase-orchestration.md)
6. [Go vs Java for a latency-sensitive service](docs/adr/0006-go-vs-java.md)
7. [Rolling vs blue/green deploys](docs/adr/0007-rolling-vs-blue-green.md)
8. [DynamoDB for the audit store](docs/adr/0008-dynamodb-for-audit.md)
# fraud-detection-scoring-system-practice
