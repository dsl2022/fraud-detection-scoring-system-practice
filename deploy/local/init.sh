#!/usr/bin/env bash
# Creates the local AWS resources in LocalStack that mirror what Terraform
# provisions in AWS (Stage 5): the audit/event queue, its DLQ with a redrive
# policy, and the DynamoDB audit table. Idempotent — safe to re-run.
set -euo pipefail

ENDPOINT="http://localstack:4566"
REGION="${AWS_DEFAULT_REGION:-us-east-1}"
ACCOUNT="000000000000" # LocalStack's default account id

aws() { command aws --endpoint-url="$ENDPOINT" --region "$REGION" "$@"; }

echo "creating DLQ..."
aws sqs create-queue --queue-name fraud-audit-events-dlq >/dev/null || true
DLQ_ARN=$(aws sqs get-queue-attributes \
  --queue-url "$ENDPOINT/$ACCOUNT/fraud-audit-events-dlq" \
  --attribute-names QueueArn --query 'Attributes.QueueArn' --output text)
echo "DLQ ARN: $DLQ_ARN"

echo "creating main queue with redrive -> DLQ (maxReceiveCount=5)..."
cat > /tmp/redrive.json <<EOF
{ "RedrivePolicy": "{\"deadLetterTargetArn\":\"$DLQ_ARN\",\"maxReceiveCount\":\"5\"}" }
EOF
aws sqs create-queue --queue-name fraud-audit-events \
  --attributes file:///tmp/redrive.json >/dev/null || true

echo "creating DynamoDB audit table (pk hash key)..."
aws dynamodb create-table --table-name fraud-audit \
  --attribute-definitions AttributeName=pk,AttributeType=S \
  --key-schema AttributeName=pk,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST >/dev/null 2>&1 || echo "  (table already exists)"

echo "local AWS resources ready."
