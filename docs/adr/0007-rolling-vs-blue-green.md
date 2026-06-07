# ADR 0007 — Rolling deploys by default, blue/green available

**Status:** Accepted

## Context

ECS deploys must not drop in-flight requests, and we need a clear rollback path.

## Decision

Default to **ECS rolling updates**; provide **CodeDeploy blue/green** as an opt-in
(`deployment_controller = "CODE_DEPLOY"`).

## Rolling (default)

- `deployment_minimum_healthy_percent = 100`, `maximum_percent = 200`: ECS starts
  new tasks before draining old ones — zero-downtime.
- The app cooperates: on `SIGTERM` it flips `/readyz` to 503 **before** draining,
  so the ALB deregisters the task and stops routing new requests while in-flight
  ones finish (see `gracefulShutdown` in `cmd/server`).
- **Rollback:** revert the PR (which pins the previous image tag) and re-apply.
  Immutable, SHA-tagged images make "the previous known-good" unambiguous.

## Blue/green (opt-in)

CodeDeploy stands up a **green** task set alongside **blue**, shifts ALB traffic
(all-at-once or canary), runs validation, and keeps blue warm for a fast
**rollback = shift traffic back to the old task set** — no rebuild, near-instant.
This needs a second target group + a test listener and CodeDeploy app/deployment
group resources, and the ECS service must `ignore_changes` on the task definition
(CodeDeploy drives it). Adopt it for higher-stakes envs where instant rollback and
canaries justify the extra machinery.

## On Terraform and rollback

Terraform itself has **no auto-rollback**. A failed apply leaves partially-applied
state; recovery is to fix forward or revert the commit and re-apply the prior
state. Blue/green's traffic-shift rollback is an *application-deploy* safety net,
not an infrastructure one — they're complementary.

## Consequences

- Rolling is simple and adequate for most changes; brief version-mixing during
  the roll is fine for a stateless, backward-compatible API.
- Blue/green doubles task capacity during a deploy (cost) and adds resources;
  worth it where rollback speed/canaries matter.
