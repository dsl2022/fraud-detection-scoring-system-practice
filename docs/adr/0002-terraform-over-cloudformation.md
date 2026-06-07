# ADR 0002 — Terraform over CloudFormation

**Status:** Accepted

## Context

All infrastructure must be code (no console clicks), parameterized per
environment, with a reviewable change process for ITGC.

## Decision

Use **Terraform (HCL)** with an S3 remote-state backend and DynamoDB state
locking.

## Rationale

- **`plan` is a real, reviewable diff.** `terraform plan` shows exactly what will
  change before it changes; we post it on the PR. CloudFormation change sets exist
  but the diff is coarser and the review ergonomics are worse.
- **Modules compose cleanly.** `network → platform → app` are modules wired by
  output values, with `for_each`/`count` removing duplication. Reusable, testable
  units.
- **Ecosystem + multi-provider.** One tool/language for AWS today and (e.g.)
  Datadog, GitHub, Cloudflare tomorrow. CloudFormation is AWS-only.
- **Explicit state we control.** Remote state + locking is a deliberate, visible
  mechanism; we can inspect, import, and move resources.

## Consequences

- **We own state.** State can drift or be corrupted if mishandled; we mitigate
  with remote state, locking, and `prevent_destroy`/`create_before_destroy` on
  stateful resources. CloudFormation manages state for you.
- A backend can't create the bucket/table it stores state in → a one-time,
  out-of-band bootstrap (documented in `deploy/terraform/envs/README.md`).
- No native auto-rollback (see ADR 0007): rollback = revert the PR and re-apply.

## Alternatives considered

- **CloudFormation / CDK** — no state to manage and deep AWS support; weaker diff
  review and AWS-only. CDK adds a programming layer but compiles down to CFN.
- **Pulumi** — general-purpose languages; smaller ecosystem and another runtime
  to standardize on.
