package httpapi

import (
	"log/slog"
	"net/http"
	"sync/atomic"
)

// Probe separates LIVENESS from READINESS — the distinction that makes rolling
// ECS deploys safe:
//
//   - /healthz (liveness): "is the process alive?" Always 200 while we're up. If
//     it fails, the orchestrator restarts the task.
//   - /readyz (readiness): "should the ALB send me traffic right now?" We flip
//     this to NOT-ready on SIGTERM BEFORE draining, so the ALB deregisters the
//     task and stops sending new requests while in-flight ones finish. Without
//     this, a rolling deploy would 5xx the requests that arrive during drain.
type Probe struct {
	ready atomic.Bool
}

func NewProbe() *Probe { return &Probe{} }

func (p *Probe) SetReady(v bool) { p.ready.Store(v) }
func (p *Probe) Ready() bool     { return p.ready.Load() }

func livenessHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, log, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func readinessHandler(p *Probe, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !p.Ready() {
			writeJSON(w, log, http.StatusServiceUnavailable, map[string]string{"status": "draining"})
			return
		}
		writeJSON(w, log, http.StatusOK, map[string]string{"status": "ready"})
	}
}
