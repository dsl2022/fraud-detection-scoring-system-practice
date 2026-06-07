package grpcapi

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
	"github.com/blocklocmedia/fraud-signals/internal/grpcapi/pb"
)

type stubScorer struct {
	result  domain.ScoreResult
	gotApp  domain.Application
	gotCall bool
}

func (s *stubScorer) Score(_ context.Context, app domain.Application) domain.ScoreResult {
	s.gotApp = app
	s.gotCall = true
	return s.result
}

func TestGRPCScore_MapsAndDelegates(t *testing.T) {
	stub := &stubScorer{result: domain.ScoreResult{
		Score: 73, Decision: domain.DecisionDecline,
		SignalsUsed: []string{"credit_bureau"}, LogicVersion: "v1",
	}}
	srv := NewServer(stub)

	resp, err := srv.Score(context.Background(), &pb.ScoreRequest{
		Application: &pb.Application{ApplicantId: "acct-1", Product: "checking", RequestedAmount: 5000},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Request was mapped to the domain and delegated.
	if !stub.gotCall || stub.gotApp.ApplicantID != "acct-1" || stub.gotApp.RequestedAmount != 5000 {
		t.Errorf("request not mapped/delegated correctly: %+v", stub.gotApp)
	}
	// Response mapped back, including the enum.
	if resp.Score != 73 || resp.Decision != pb.Decision_DECISION_DECLINE {
		t.Errorf("response mapping wrong: score=%v decision=%v", resp.Score, resp.Decision)
	}
	if len(resp.SignalsUsed) != 1 || resp.SignalsUsed[0] != "credit_bureau" {
		t.Errorf("signals not mapped: %v", resp.SignalsUsed)
	}
}

func TestGRPCScore_ValidatesInput(t *testing.T) {
	srv := NewServer(&stubScorer{})
	tests := []struct {
		name string
		req  *pb.ScoreRequest
	}{
		{"nil application", &pb.ScoreRequest{}},
		{"missing applicant_id", &pb.ScoreRequest{Application: &pb.Application{Product: "x"}}},
		{"missing product", &pb.ScoreRequest{Application: &pb.Application{ApplicantId: "a1"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := srv.Score(context.Background(), tt.req)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", status.Code(err))
			}
		})
	}
}
