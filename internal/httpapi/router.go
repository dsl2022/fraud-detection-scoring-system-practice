package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/blocklocmedia/fraud-signals/internal/auth"
)

// Demo holds the optional naive/guarded scorers for the before/after incident
// demo endpoints. Pass nil to disable them (e.g. in production).
type Demo struct {
	Naive   scorerPort // single shared timeout (the "before")
	Guarded scorerPort // per-source budget + breaker (the "after")
}

// RouterDeps is the wiring for the HTTP edge. Using a struct (rather than a long
// positional parameter list) keeps the call site readable and lets us add edge
// concerns over time without churning the signature.
type RouterDeps struct {
	Scorer scorerPort     // production scoring service
	Demo   *Demo          // optional incident demo endpoints
	Auth   auth.Validator // nil => auth disabled on business routes (dev)
	Probe  *Probe         // readiness probe (nil => always-ready)
	Log    *slog.Logger
}

// NewRouter wires routes with Go 1.22+ method+pattern routing. URI versioning
// (/v1) lives in the path so a future /v2 can be added without breaking /v1.
//
// Middleware layering (outer -> inner): request-id -> access log -> recover.
// Auth is applied PER-ROUTE on business endpoints only, so health checks (hit
// by the ALB with no token) never require credentials.
func NewRouter(d RouterDeps) http.Handler {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	probe := d.Probe
	if probe == nil {
		probe = NewProbe()
		probe.SetReady(true)
	}

	mux := http.NewServeMux()

	// Business endpoint, guarded by auth.
	protected := authMW(d.Auth, log)
	mux.Handle("POST /v1/score", protected(scoreHandler(d.Scorer, log)))

	// Health checks — NO auth (the ALB has no token).
	mux.HandleFunc("GET /healthz", livenessHandler(log))
	mux.HandleFunc("GET /readyz", readinessHandler(probe, log))

	// Optional incident demo (also behind auth, same contract as /v1/score).
	if d.Demo != nil {
		if d.Demo.Naive != nil {
			mux.Handle("POST /demo/naive/score", protected(scoreHandler(d.Demo.Naive, log)))
		}
		if d.Demo.Guarded != nil {
			mux.Handle("POST /demo/guarded/score", protected(scoreHandler(d.Demo.Guarded, log)))
		}
	}

	return chain(mux, requestIDMW, loggingMW(log), recoverMW(log))
}
