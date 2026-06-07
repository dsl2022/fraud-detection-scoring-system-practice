package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
)

// stubScorer lets us test the HTTP layer in isolation from the real fan-out.
type stubScorer struct{ result domain.ScoreResult }

func (s stubScorer) Score(_ context.Context, _ domain.Application) domain.ScoreResult {
	return s.result
}

func newTestRouter(res domain.ScoreResult) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRouter(RouterDeps{Scorer: stubScorer{result: res}, Log: log})
}

func TestScoreHandler(t *testing.T) {
	want := domain.ScoreResult{
		Score:        42,
		Decision:     domain.DecisionManualReview,
		SignalsUsed:  []string{"credit_bureau"},
		LogicVersion: "test-v1",
	}
	router := newTestRouter(want)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string // error code expected, empty for success
	}{
		{
			name:       "valid request returns score",
			body:       `{"applicant_id":"a1","product":"checking"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing applicant_id => 400 validation_failed",
			body:       `{"product":"checking"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_failed",
		},
		{
			name:       "missing product => 400 validation_failed",
			body:       `{"applicant_id":"a1"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_failed",
		},
		{
			name:       "malformed json => 400 invalid_json",
			body:       `{not json`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "unknown field => 400 invalid_json",
			body:       `{"applicant_id":"a1","product":"checking","oops":true}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/score", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantCode == "" {
				var got domain.ScoreResult
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode success body: %v", err)
				}
				if got.Decision != want.Decision || got.Score != want.Score {
					t.Errorf("body = %+v, want %+v", got, want)
				}
				return
			}
			var env errorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if env.Error.Code != tt.wantCode {
				t.Errorf("error code = %q, want %q", env.Error.Code, tt.wantCode)
			}
		})
	}
}

func TestHealthEndpoints(t *testing.T) {
	router := newTestRouter(domain.ScoreResult{})
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, rec.Code)
		}
	}
}

// GET on the score endpoint should 405 (method routing), not 200.
func TestScoreHandler_MethodNotAllowed(t *testing.T) {
	router := newTestRouter(domain.ScoreResult{})
	req := httptest.NewRequest(http.MethodGet, "/v1/score", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
