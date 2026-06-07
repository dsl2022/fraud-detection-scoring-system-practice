# ADR 0001 — ECS Fargate over EKS

**Status:** Accepted

## Context

We need to run a latency-sensitive Go container service (plus an async consumer)
on AWS, with autoscaling, rolling deploys, and least-privilege IAM. The team is
small and there is no existing Kubernetes platform or dedicated platform team.

## Decision

Run on **ECS Fargate**, not EKS.

## Rationale

- **No control plane / nodes to own.** Fargate is serverless containers: no node
  AMIs to patch, no cluster autoscaler, no Kubernetes version upgrades. EKS adds
  a control-plane cost and a standing operational burden that a single service
  doesn't justify.
- **IAM is first-class and simple.** Task role vs execution role maps cleanly to
  "what the app may call" vs "what the agent needs to start the task." On EKS the
  equivalent (IRSA, OIDC provider, service accounts) is more moving parts for the
  same outcome.
- **Tight AWS integration out of the box.** ALB target groups, CloudWatch logs,
  Secrets/SSM injection, and autoscaling are native ECS concepts with direct
  Terraform resources — less glue than the EKS equivalents.
- **Latency profile fits.** Our workload is a stateless HTTP/gRPC service; we
  don't need pod-level scheduling features, service meshes, or operators.

## Consequences

- We forgo Kubernetes portability and its ecosystem (Helm, operators, CRDs). If
  we later need multi-cloud or rich scheduling, revisit.
- Fargate per-vCPU pricing is higher than well-packed EC2; acceptable for the
  operational savings at this scale.

## Alternatives considered

- **EKS / EKS-on-Fargate** — strongest if we already ran K8s or needed its
  ecosystem; overkill here.
- **Lambda for the synchronous API** — cold starts and the request/response
  model fight a strict p99 budget with fan-out; we *do* use Lambda for the async
  consumer, where it shines.
