# Architecture Decision Records

Short, dated records of the non-obvious decisions and their trade-offs. Each is
self-contained: Context → Decision → Consequences → Alternatives.

| # | Decision |
|---|---|
| [0001](0001-ecs-over-eks.md) | ECS Fargate over EKS |
| [0002](0002-terraform-over-cloudformation.md) | Terraform over CloudFormation |
| [0003](0003-per-source-vs-shared-timeout.md) | Per-source budgets + breakers vs a shared timeout (the incident) |
| [0004](0004-library-vs-build-circuit-breaker.md) | Library (`gobreaker`) vs building the breaker |
| [0005](0005-two-phase-orchestration.md) | Two-phase orchestration (concurrent independents → enriched bureau) |
| [0006](0006-go-vs-java.md) | Go vs Java for a latency-sensitive container service |
| [0007](0007-rolling-vs-blue-green.md) | Rolling vs blue/green deploys |
| [0008](0008-dynamodb-for-audit.md) | DynamoDB for the immutable audit store |
