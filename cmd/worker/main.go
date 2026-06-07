// Command worker is the LOCAL equivalent of the Lambda consumer: a long-running
// SQS poller that drives the same consumer core. It exists so docker-compose can
// run a true end-to-end pipeline (api -> SQS -> worker -> DynamoDB) without a
// Lambda runtime. In AWS, cmd/consumer (Lambda) does this job instead.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/blocklocmedia/fraud-signals/internal/audit"
	"github.com/blocklocmedia/fraud-signals/internal/awscfg"
	"github.com/blocklocmedia/fraud-signals/internal/consumer"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	queueURL := os.Getenv("AUDIT_QUEUE_URL")
	table := os.Getenv("AUDIT_TABLE")
	if queueURL == "" || table == "" {
		log.Error("AUDIT_QUEUE_URL and AUDIT_TABLE are required")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	awsCfg, err := awscfg.Load(ctx)
	if err != nil {
		log.Error("failed to load AWS config", "error", err.Error())
		os.Exit(1)
	}
	sqsClient := awscfg.SQS(awsCfg)

	h := consumer.New(
		audit.NewDynamoSink(awscfg.Dynamo(awsCfg), table),
		consumer.NewLoggingReviewSink(log),
		consumer.NewLoggingRetrainingSink(log),
		log,
	)

	log.Info("worker polling", "queue_url", queueURL, "table", table)
	poll(ctx, sqsClient, queueURL, h, log)
	log.Info("worker stopped")
}

func poll(ctx context.Context, client *sqs.Client, queueURL string, h *consumer.Handler, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20, // long-poll: cheap, low-latency
		})
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down
			}
			log.Error("receive failed", "error", err.Error())
			continue
		}
		if len(out.Messages) == 0 {
			continue
		}

		msgs := make([]consumer.Message, len(out.Messages))
		byID := make(map[string]types.Message, len(out.Messages))
		for i, m := range out.Messages {
			msgs[i] = consumer.Message{ID: aws.ToString(m.MessageId), Body: aws.ToString(m.Body)}
			byID[aws.ToString(m.MessageId)] = m
		}

		failed := h.ProcessBatch(ctx, msgs)
		failedSet := make(map[string]bool, len(failed))
		for _, id := range failed {
			failedSet[id] = true // leave on the queue -> retried -> DLQ after maxReceiveCount
		}

		// Delete only the successes; failures stay for redelivery/DLQ.
		var toDelete []types.DeleteMessageBatchRequestEntry
		for id, m := range byID {
			if failedSet[id] {
				continue
			}
			toDelete = append(toDelete, types.DeleteMessageBatchRequestEntry{
				Id: m.MessageId, ReceiptHandle: m.ReceiptHandle,
			})
		}
		if len(toDelete) > 0 {
			if _, err := client.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
				QueueUrl: aws.String(queueURL), Entries: toDelete,
			}); err != nil {
				log.Error("delete batch failed", "error", err.Error())
			}
		}
	}
}
