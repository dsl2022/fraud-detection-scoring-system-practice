// Package config loads all runtime configuration from environment variables
// (12-factor). No secrets or environment-specific values are compiled in; the
// same binary runs in dev/stage/prod and is shaped purely by its environment.
package config

import (
	"os"
	"strconv"
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/audit"
	"github.com/blocklocmedia/fraud-signals/internal/providers"
	"github.com/blocklocmedia/fraud-signals/internal/scorer"
)

// Config is the fully-resolved application configuration.
type Config struct {
	Port         string
	GRPCPort     string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// ShutdownTimeout bounds how long we drain in-flight requests on SIGTERM.
	// ShutdownDrainDelay is the pause after marking NOT-ready (so the ALB
	// deregisters) before we actually stop accepting connections.
	ShutdownTimeout    time.Duration
	ShutdownDrainDelay time.Duration

	Scorer scorer.Config
	Guard  providers.GuardConfig

	// AuthEnabled turns on JWT validation at the edge. JWTSecret (HS256) is read
	// from the environment (injected from Secrets Manager / SSM in AWS — never in
	// code). When disabled, the edge is open (local/dev only).
	AuthEnabled bool
	JWTSecret   string

	// PersistMode selects audit persistence: combined (default) or as_you_go.
	PersistMode audit.PersistMode

	// AsyncEnabled publishes scoring events to SQS (off the request path). When
	// false (local default), events go to the structured log instead. QueueURL is
	// the audit/event queue (AUDIT_QUEUE_URL).
	AsyncEnabled bool
	QueueURL     string

	// MetricsEMF emits per-vendor metrics as CloudWatch EMF on stdout (for the
	// per-vendor latency alarm). Off locally (in-memory collector instead).
	MetricsEMF       bool
	MetricsNamespace string

	// DemoEndpoints exposes /demo/naive/score and /demo/guarded/score for the
	// incident before/after demo. On by default for local/staging; set
	// DEMO_ENDPOINTS=false in production.
	DemoEndpoints bool
}

// Load reads configuration from the environment, applying defaults that are safe
// for local development. Each value is overridable so ops can tune timeouts and
// thresholds per environment without a redeploy of new code.
func Load() Config {
	return Config{
		Port:     getenv("PORT", "8080"),
		GRPCPort: getenv("GRPC_PORT", "9090"),

		// HTTP server timeouts. These are a baseline DoS / slow-loris defense and
		// must be set on every production server. WriteTimeout sits above our
		// scoring budget so a legitimately in-budget response is never cut off.
		ReadTimeout:  getdur("HTTP_READ_TIMEOUT", 5*time.Second),
		WriteTimeout: getdur("HTTP_WRITE_TIMEOUT", 10*time.Second),
		IdleTimeout:  getdur("HTTP_IDLE_TIMEOUT", 60*time.Second),

		ShutdownTimeout:    getdur("SHUTDOWN_TIMEOUT", 15*time.Second),
		ShutdownDrainDelay: getdur("SHUTDOWN_DRAIN_DELAY", 2*time.Second),

		Scorer: scorer.Config{
			// 180ms shared outer budget under the 200ms SLO (Stage 1 naive design).
			Budget: getdur("SCORE_BUDGET", 180*time.Millisecond),
			// Stage A (independent fan-out) gets ~120ms, reserving the remaining
			// ~60ms of the outer budget for the Stage B dependent (bureau) call.
			StageABudget:  getdur("STAGE_A_BUDGET", 120*time.Millisecond),
			ApproveBelow:  getfloat("APPROVE_BELOW", 35),
			DeclineAbove:  getfloat("DECLINE_ABOVE", 70),
			MinSignals:    getint("MIN_SIGNALS", 2),
			MinConfidence: getfloat("MIN_CONFIDENCE", 0.6),
			LogicVersion:  getenv("LOGIC_VERSION", "scoring-v1.0.0"),
		},

		// Per-source protections (Stage 2). Each vendor gets its own ~120ms
		// budget and a breaker that trips after a run of failures and stays open
		// briefly before probing — so a sick vendor self-limits instead of
		// dragging every request to the timeout.
		Guard: providers.GuardConfig{
			Budget:                    getdur("PROVIDER_BUDGET", 120*time.Millisecond),
			ConsecutiveFailuresToTrip: uint32(getint("BREAKER_TRIP_FAILURES", 5)),
			HalfOpenMaxRequests:       uint32(getint("BREAKER_HALFOPEN_MAX", 1)),
			OpenTimeout:               getdur("BREAKER_OPEN_TIMEOUT", 30*time.Second),
		},

		AuthEnabled: getbool("AUTH_ENABLED", false),
		JWTSecret:   getenv("JWT_SECRET", ""),

		PersistMode: audit.PersistMode(getenv("PERSIST_MODE", string(audit.PersistCombined))),

		AsyncEnabled: getbool("ASYNC_ENABLED", false),
		QueueURL:     getenv("AUDIT_QUEUE_URL", ""),

		MetricsEMF:       getbool("METRICS_EMF", false),
		MetricsNamespace: getenv("METRICS_NAMESPACE", "FraudSignals"),

		DemoEndpoints: getbool("DEMO_ENDPOINTS", true),
	}
}

func getbool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func getdur(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getint(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getfloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
