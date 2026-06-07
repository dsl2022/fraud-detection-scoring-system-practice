# ADR 0008 — DynamoDB for the immutable audit store

**Status:** Accepted

## Context

Every decision must leave an **immutable, reviewable** audit record (an ITGC
requirement). The dominant query is "show me decision X" by request id, and the
async consumer writes records under **at-least-once** delivery, so writes must be
idempotent.

## Decision

Store audit records in **DynamoDB**, written with a **conditional put**
(`attribute_not_exists(pk)`), CMK-encrypted, with point-in-time recovery.

## Rationale

- **Write-once is enforced by the store.** The conditional put refuses to
  overwrite an existing key, so immutability doesn't depend on application
  discipline — and it gives the consumer **free idempotency**: a duplicate
  delivery returns `ConditionalCheckFailed` → we treat it as a no-op.
- **Fast point reads.** "Fetch the decision for request id R" is a primary-key
  `GetItem` — no scan. Matches the natural audit query.
- **Low-latency writes.** Single-digit-ms puts kept us within budget even when we
  (pre-async) wrote on the request path.
- **Encryption + recovery.** SSE with our CMK; PITR for accidental-deletion
  protection. `prevent_destroy` in Terraform stops the table being torn down.

## Consequences

- DynamoDB enforces *write-once*, not full WORM. For regulatory tamper-proofing we
  note **S3 Object Lock** as the alternative (below). CloudTrail provides the
  who-did-what layer (ADR-adjacent).
- Single-key access pattern only; richer audit analytics would use a stream →
  S3/Athena export rather than DynamoDB scans.

## Alternatives considered

- **S3 with Object Lock (WORM)** — true compliance-grade immutability and cheap at
  scale; better when a regulator demands tamper-proof retention. Weaker for
  low-latency point lookups by id, which is our primary access pattern. A good
  *complement* (stream records to S3) rather than the primary store.
- **RDS/Postgres** — strong querying, but heavier to operate, and "append-only"
  would be a convention enforced by triggers/permissions rather than the storage
  primitive.
