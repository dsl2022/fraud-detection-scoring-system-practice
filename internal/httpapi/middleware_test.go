package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blocklocmedia/fraud-signals/internal/auth"
	"github.com/blocklocmedia/fraud-signals/internal/reqid"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRequestID_GeneratedAndEchoed(t *testing.T) {
	router := NewRouter(RouterDeps{Scorer: stubScorer{}, Log: discardLog()})

	// No inbound id => one is generated and echoed.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Header().Get(reqid.HeaderName) == "" {
		t.Error("expected a generated X-Request-ID on the response")
	}

	// Inbound id => preserved (trace continuity across services).
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(reqid.HeaderName, "trace-abc")
	router.ServeHTTP(rec, req)
	if got := rec.Header().Get(reqid.HeaderName); got != "trace-abc" {
		t.Errorf("X-Request-ID = %q, want trace-abc", got)
	}
}

func TestReadinessProbe(t *testing.T) {
	probe := NewProbe()
	router := NewRouter(RouterDeps{Scorer: stubScorer{}, Probe: probe, Log: discardLog()})

	// Not ready yet => 503 (ALB won't route).
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz before ready = %d, want 503", rec.Code)
	}

	probe.SetReady(true)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("readyz after ready = %d, want 200", rec.Code)
	}

	// Liveness is independent of readiness.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want 200", rec.Code)
	}
}

func TestAuthMiddleware(t *testing.T) {
	// AllowAll requires a non-empty bearer token; good enough to exercise the
	// middleware wiring (401 vs pass-through) without a real JWT.
	router := NewRouter(RouterDeps{Scorer: stubScorer{}, Auth: auth.AllowAll{}, Log: discardLog()})
	body := `{"applicant_id":"a1","product":"checking"}`

	// Missing token => 401.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/score", strings.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no-token status = %d, want 401", rec.Code)
	}

	// Valid bearer => 200.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/score", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer some-token")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("with-token status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	// Health endpoints stay open even with auth enabled.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz with auth enabled = %d, want 200", rec.Code)
	}
}
