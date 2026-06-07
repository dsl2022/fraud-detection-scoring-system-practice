// Package grpcapi is the gRPC driving adapter. Like the REST adapter it is thin:
// it maps proto <-> domain and delegates to the same scoring service, so REST
// and gRPC callers always get identical decisions.
package grpcapi

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/grpcapi/pb"
)

// scorerPort is the same tiny contract the REST handler uses — the scoring
// service satisfies it, so both transports share one implementation.
type scorerPort interface {
	Score(ctx context.Context, app domain.Application) domain.ScoreResult
}

// Server implements the generated ScoringServiceServer.
type Server struct {
	pb.UnimplementedScoringServiceServer
	scorer scorerPort
}

func NewServer(scorer scorerPort) *Server { return &Server{scorer: scorer} }

// Score maps the request to the domain, scores it, and maps the result back.
func (s *Server) Score(ctx context.Context, req *pb.ScoreRequest) (*pb.ScoreResponse, error) {
	if req == nil || req.GetApplication() == nil {
		return nil, status.Error(codes.InvalidArgument, "application is required")
	}
	a := req.GetApplication()
	if a.GetApplicantId() == "" || a.GetProduct() == "" {
		return nil, status.Error(codes.InvalidArgument, "applicant_id and product are required")
	}

	result := s.scorer.Score(ctx, toDomain(a))

	return &pb.ScoreResponse{
		Score:        result.Score,
		Decision:     toProtoDecision(result.Decision),
		SignalsUsed:  result.SignalsUsed,
		LogicVersion: result.LogicVersion,
	}, nil
}

func toDomain(a *pb.Application) domain.Application {
	return domain.Application{
		ApplicantID:     a.GetApplicantId(),
		Product:         a.GetProduct(),
		FullName:        a.GetFullName(),
		Email:           a.GetEmail(),
		Country:         a.GetCountry(),
		AgeYears:        int(a.GetAgeYears()),
		RequestedAmount: a.GetRequestedAmount(),
		AccountAgeDays:  int(a.GetAccountAgeDays()),
		RecentTxnCount:  int(a.GetRecentTxnCount()),
	}
}

func toProtoDecision(d domain.Decision) pb.Decision {
	switch d {
	case domain.DecisionApprove:
		return pb.Decision_DECISION_APPROVE
	case domain.DecisionDecline:
		return pb.Decision_DECISION_DECLINE
	case domain.DecisionManualReview:
		return pb.Decision_DECISION_MANUAL_REVIEW
	default:
		return pb.Decision_DECISION_UNSPECIFIED
	}
}

// Register attaches the server to a grpc.Server.
func Register(gs *grpc.Server, srv *Server) {
	pb.RegisterScoringServiceServer(gs, srv)
}
