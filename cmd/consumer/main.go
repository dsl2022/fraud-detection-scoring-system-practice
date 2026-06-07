// Command consumer is the AWS Lambda entrypoint for the SQS consumer. It is a
// THIN shell: it builds the dependency graph from the environment and adapts the
// Lambda SQS event to the transport-agnostic consumer core, which holds all the
// idempotency logic. Returning SQSEventResponse with BatchItemFailures enables
// partial-batch failure — only failed messages are retried (and eventually DLQ'd).
package main

import (
	"context"
	"log/slog"
	"os"

	lambdaevents "github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/blocklocmedia/fraud-signals/internal/audit"
	"github.com/blocklocmedia/fraud-signals/internal/awscfg"
	"github.com/blocklocmedia/fraud-signals/internal/consumer"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	awsCfg, err := awscfg.Load(context.Background())
	if err != nil {
		log.Error("failed to load AWS config", "error", err.Error())
		os.Exit(1)
	}
	table := os.Getenv("AUDIT_TABLE")
	if table == "" {
		log.Error("AUDIT_TABLE is required")
		os.Exit(1)
	}

	h := consumer.New(
		audit.NewDynamoSink(awscfg.Dynamo(awsCfg), table),
		consumer.NewLoggingReviewSink(log),
		consumer.NewLoggingRetrainingSink(log),
		log,
	)

	lambda.Start(func(ctx context.Context, e lambdaevents.SQSEvent) (lambdaevents.SQSEventResponse, error) {
		msgs := make([]consumer.Message, len(e.Records))
		for i, r := range e.Records {
			msgs[i] = consumer.Message{ID: r.MessageId, Body: r.Body}
		}
		failed := h.ProcessBatch(ctx, msgs)

		resp := lambdaevents.SQSEventResponse{}
		for _, id := range failed {
			resp.BatchItemFailures = append(resp.BatchItemFailures, lambdaevents.SQSBatchItemFailure{ItemIdentifier: id})
		}
		return resp, nil
	})
}
