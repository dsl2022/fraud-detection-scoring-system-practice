# Per-environment Terraform

Each environment has its **own state file and lock** (separate `*.backend.hcl`)
and its **own inputs** (`*.tfvars`). Same root module, isolated blast radius.

```bash
cd deploy/terraform

# init against an env's remote state (S3 + DynamoDB lock)
terraform init -backend-config=envs/dev.backend.hcl

# plan / apply with that env's inputs
terraform plan  -var-file=envs/dev.tfvars
terraform apply -var-file=envs/dev.tfvars
```

## One-time backend bootstrap (chicken-and-egg)

A backend can't create the bucket/table it stores its own state in, so create
them once per account, out of band:

```bash
aws s3api create-bucket --bucket fraud-signals-tfstate-dev --region us-east-1
aws s3api put-bucket-versioning --bucket fraud-signals-tfstate-dev \
  --versioning-configuration Status=Enabled
aws s3api put-bucket-encryption --bucket fraud-signals-tfstate-dev \
  --server-side-encryption-configuration \
  '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"aws:kms"}}]}'
aws dynamodb create-table --table-name fraud-signals-tflock \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST --region us-east-1
```

## Image source of truth (SSM)

The root reads the last-deployed image URIs from SSM
(`/fraud-signals/<env>/{server,consumer}_image`) when no `-var` is given, so a
full `infra.yml` apply doesn't clobber what `app-deploy.yml` deployed.
`app-deploy.yml` writes these params on every deploy — but they must EXIST before
the first full apply. Seed them once (point at any image already in ECR):

```bash
REG=$(aws sts get-caller-identity --query Account --output text).dkr.ecr.us-east-1.amazonaws.com
TAG=<a tag already pushed to ECR, e.g. the current commit sha>
aws ssm put-parameter --name /fraud-signals/dev/server_image   --type String --overwrite \
  --value "$REG/fraud-dev-server:$TAG"   --region us-east-1
aws ssm put-parameter --name /fraud-signals/dev/consumer_image --type String --overwrite \
  --value "$REG/fraud-dev-consumer:$TAG" --region us-east-1
```

## Notes

- `jwt_secret`, `server_image`, `consumer_image` are injected by CI (`-var` /
  `TF_VAR_*`), never committed.
- `prod` enables HA NAT and the account-wide governance plane
  (`enable_account_audit = true`: CloudTrail + AWS Config).
- Rollback: Terraform has no auto-rollback — revert the PR and re-apply the
  previous known-good commit.
