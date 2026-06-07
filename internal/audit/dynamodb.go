package audit

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// dynamoPutAPI is the slice of the DynamoDB client we use.
type dynamoPutAPI interface {
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// DynamoSink persists audit records to DynamoDB with WRITE-ONCE semantics.
//
// The table has a single string partition key "pk". We key decision items as
// "DEC#<request_id>" and provider items as "PRV#<request_id>#<source>". Every
// write carries ConditionExpression attribute_not_exists(pk): the store itself
// refuses to overwrite, which (a) makes the audit trail tamper-evident and
// (b) gives the consumer free idempotency — a duplicate delivery returns
// ErrAlreadyExists, not a second row.
type DynamoSink struct {
	client dynamoPutAPI
	table  string
}

func NewDynamoSink(client dynamoPutAPI, table string) *DynamoSink {
	return &DynamoSink{client: client, table: table}
}

func (s *DynamoSink) PutDecision(ctx context.Context, r Record) error {
	return s.putOnce(ctx, "DEC#"+r.RequestID, r)
}

func (s *DynamoSink) PutProviderResponse(ctx context.Context, r ProviderRecord) error {
	return s.putOnce(ctx, "PRV#"+r.RequestID+"#"+r.Source, r)
}

// putOnce marshals item, stamps the partition key, and does a conditional put.
func (s *DynamoSink) putOnce(ctx context.Context, pk string, item any) error {
	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}
	av["pk"] = &types.AttributeValueMemberS{Value: pk}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	if err != nil {
		var cond *types.ConditionalCheckFailedException
		if errors.As(err, &cond) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}
