package events

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// sqsSendAPI is the slice of the SQS client we use. Depending on an interface
// (not the concrete *sqs.Client) makes the publisher unit-testable with a fake
// and documents exactly which API calls we make.
type sqsSendAPI interface {
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// SQSPublisher sends ScoringEvents to a standard SQS queue.
type SQSPublisher struct {
	client   sqsSendAPI
	queueURL string
}

func NewSQSPublisher(client sqsSendAPI, queueURL string) *SQSPublisher {
	return &SQSPublisher{client: client, queueURL: queueURL}
}

func (p *SQSPublisher) Publish(ctx context.Context, ev ScoringEvent) error {
	body, err := Marshal(ev)
	if err != nil {
		return err
	}
	_, err = p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(p.queueURL),
		MessageBody: aws.String(string(body)),
		// On a STANDARD queue there's no server-side dedup; the consumer dedupes
		// on request id. (A FIFO queue would set MessageGroupId +
		// MessageDeduplicationId here instead.)
	})
	return err
}
