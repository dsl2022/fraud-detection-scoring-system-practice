# One command per lifecycle step. ENV selects the Terraform workspace inputs.
ENV          ?= dev
AWS_REGION   ?= us-east-1
TF_DIR       := deploy/terraform
GOFLAGS      :=
AUTO_APPROVE ?=   # set non-empty (e.g. AUTO_APPROVE=1) for non-interactive CI runs

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## ---- Go ----
.PHONY: run
run: ## Run the API server locally
	go run ./cmd/server

.PHONY: test
test: ## Run unit tests with the race detector
	go test -race ./...

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: lint
lint: ## golangci-lint
	golangci-lint run ./...

.PHONY: fmt
fmt: ## Format Go + Terraform
	gofmt -w cmd internal
	terraform -chdir=$(TF_DIR) fmt -recursive

.PHONY: proto
proto: ## Regenerate gRPC stubs from the .proto
	protoc -I proto \
	  --go_out=. --go_opt=module=github.com/blocklocmedia/fraud-signals \
	  --go-grpc_out=. --go-grpc_opt=module=github.com/blocklocmedia/fraud-signals \
	  proto/scoring/v1/scoring.proto

## ---- Docker ----
.PHONY: docker-build
docker-build: ## Build the service image
	docker build -t fraud-signals-server:local -f Dockerfile .

.PHONY: docker-build-lambda
docker-build-lambda: ## Build the Lambda consumer image
	docker build -t fraud-signals-consumer:local -f Dockerfile.lambda .

.PHONY: compose-up
compose-up: ## Local end-to-end stack (api + worker + LocalStack)
	docker compose up --build

.PHONY: compose-down
compose-down: ## Tear down the local stack
	docker compose down -v

## ---- Terraform (per-env via ENV=dev|stage|prod) ----
.PHONY: tf-init
tf-init: ## terraform init with the env backend
	terraform -chdir=$(TF_DIR) init -backend-config=envs/$(ENV).backend.hcl

.PHONY: tf-plan
tf-plan: ## terraform plan for ENV
	terraform -chdir=$(TF_DIR) plan -var-file=envs/$(ENV).tfvars

.PHONY: tf-apply
tf-apply: ## terraform apply for ENV
	terraform -chdir=$(TF_DIR) apply -var-file=envs/$(ENV).tfvars

.PHONY: tf-validate
tf-validate: ## terraform fmt-check + validate
	terraform -chdir=$(TF_DIR) fmt -check -recursive
	terraform -chdir=$(TF_DIR) validate

.PHONY: tf-destroy
tf-destroy: ## DESTROY all app+infra for ENV (state backend + SSM image params untouched). Guard: CONFIRM=destroy-<ENV>
	@if [ "$(CONFIRM)" != "destroy-$(ENV)" ]; then \
	  echo "Refusing to destroy ENV=$(ENV) — this tears down network, platform, app and the audit table."; \
	  echo "Re-run: make tf-destroy ENV=$(ENV) CONFIRM=destroy-$(ENV)"; \
	  exit 1; \
	fi
	@echo ">> Tearing down app+infra for ENV=$(ENV) (bootstrap state backend is NOT touched)"
	# The audit table carries prevent_destroy (a literal lifecycle flag, so it
	# can't be toggled via a -var). For a full teardown, drop it from state and
	# delete it out of band — this leaves the guard in code intact for normal
	# applies. The image SSM params are data sources, so destroy never touches them.
	@TABLE=$$(terraform -chdir=$(TF_DIR) output -raw audit_table_name 2>/dev/null || echo "fraud-$(ENV)-audit"); \
	  echo ">> Releasing audit table $$TABLE from Terraform and deleting it"; \
	  terraform -chdir=$(TF_DIR) state rm 'module.app.aws_dynamodb_table.audit' 2>/dev/null || true; \
	  aws dynamodb delete-table --region $(AWS_REGION) --table-name "$$TABLE" >/dev/null 2>&1 || true
	terraform -chdir=$(TF_DIR) destroy $(if $(AUTO_APPROVE),-auto-approve) -lock-timeout=5m -var-file=envs/$(ENV).tfvars

.PHONY: tf-destroy-check
tf-destroy-check: ## Verify the billable resources for ENV are gone after a destroy
	@echo ">> Checking leftover billable resources for ENV=$(ENV) in $(AWS_REGION)"
	@LEFT=0; \
	NAT=$$(aws ec2 describe-nat-gateways --region $(AWS_REGION) \
	  --filter "Name=tag:Name,Values=fraud-$(ENV)-nat-*" "Name=state,Values=pending,available,deleting" \
	  --query 'length(NatGateways)' --output text 2>/dev/null || echo "?"); \
	ALB=$$(aws elbv2 describe-load-balancers --region $(AWS_REGION) --names fraud-$(ENV)-alb \
	  --query 'length(LoadBalancers)' --output text 2>/dev/null || echo 0); \
	ECS=$$(aws ecs describe-clusters --region $(AWS_REGION) --clusters fraud-$(ENV) \
	  --query 'length(clusters[?status==`ACTIVE`])' --output text 2>/dev/null || echo 0); \
	EIP=$$(aws ec2 describe-addresses --region $(AWS_REGION) \
	  --filters "Name=tag:Name,Values=fraud-$(ENV)-nat-eip-*" \
	  --query 'length(Addresses)' --output text 2>/dev/null || echo "?"); \
	KMS=$$(aws kms describe-key --region $(AWS_REGION) --key-id alias/fraud-$(ENV) \
	  --query 'KeyMetadata.KeyState' --output text 2>/dev/null || echo "absent"); \
	printf "  %-22s %s\n" "NAT gateways:" "$$NAT (want 0)"; \
	printf "  %-22s %s\n" "ALB:" "$$ALB (want 0)"; \
	printf "  %-22s %s\n" "ECS active clusters:" "$$ECS (want 0)"; \
	printf "  %-22s %s\n" "NAT EIPs:" "$$EIP (want 0)"; \
	printf "  %-22s %s\n" "KMS key state:" "$$KMS (want PendingDeletion or absent)"; \
	for v in "$$NAT" "$$ALB" "$$ECS" "$$EIP"; do [ "$$v" != "0" ] && LEFT=1; done; \
	if [ "$$KMS" != "PendingDeletion" ] && [ "$$KMS" != "absent" ]; then LEFT=1; fi; \
	if [ "$$LEFT" = "0" ]; then echo ">> CLEAN — no billable resources remain for ENV=$(ENV)"; \
	else echo ">> LEFTOVERS found — investigate above"; exit 1; fi

.PHONY: deploy
deploy: test docker-build ## Build + (placeholder) deploy; CI does the real push/apply
	@echo "CI (app-deploy.yml) builds, scans, pushes to ECR and applies for ENV=$(ENV)"
