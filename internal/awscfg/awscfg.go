// Package awscfg centralises AWS SDK v2 client construction. The one wrinkle it
// hides: honouring AWS_ENDPOINT_URL so the exact same binaries talk to LocalStack
// locally and to real AWS in the cloud (where the var is unset and default
// endpoints apply). Credentials/region come from the standard chain (env, role).
package awscfg

import (
	"context"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

func endpoint() string { return os.Getenv("AWS_ENDPOINT_URL") }

// Load resolves the default AWS config (region + credential chain). In ECS this
// picks up the task role; locally it reads env vars (LocalStack accepts dummies).
func Load(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx)
}

func SQS(cfg aws.Config) *sqs.Client {
	return sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		if e := endpoint(); e != "" {
			o.BaseEndpoint = aws.String(e)
		}
	})
}

func Dynamo(cfg aws.Config) *dynamodb.Client {
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if e := endpoint(); e != "" {
			o.BaseEndpoint = aws.String(e)
		}
	})
}
