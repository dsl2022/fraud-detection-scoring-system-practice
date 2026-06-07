module github.com/blocklocmedia/fraud-signals

go 1.25.0

// Stage 1 intentionally has ZERO external dependencies — it is pure standard
// library (net/http with Go 1.22+ method routing, slog, context). External
// deps (gobreaker, AWS SDK v2, gRPC) are introduced only in the stages that
// need them, so each stage stays auditable and the dependency surface grows
// deliberately rather than all at once.

require (
	github.com/sony/gobreaker/v2 v2.4.0
	google.golang.org/grpc v1.81.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/aws/aws-lambda-go v1.54.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.41.12 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.32.23 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.22 // indirect
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.20.45 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.28 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.28 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.28 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.29 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.57.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.32.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.12.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.28 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.1.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sqs v1.43.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.31.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.36.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.43.2 // indirect
	github.com/aws/smithy-go v1.27.1 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
)
