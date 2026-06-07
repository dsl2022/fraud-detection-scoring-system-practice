# One command per lifecycle step. ENV selects the Terraform workspace inputs.
ENV        ?= dev
AWS_REGION ?= us-east-1
TF_DIR     := deploy/terraform
GOFLAGS    :=

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

.PHONY: deploy
deploy: test docker-build ## Build + (placeholder) deploy; CI does the real push/apply
	@echo "CI (app-deploy.yml) builds, scans, pushes to ECR and applies for ENV=$(ENV)"
