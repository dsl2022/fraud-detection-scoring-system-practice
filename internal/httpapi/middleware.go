package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/auth"
	"github.com/blocklocmedia/fraud-signals/internal/reqid"
)

// middleware is the standard net/http decorator shape.
type middleware func(http.Handler) http.Handler

// chain applies middlewares so that the FIRST listed runs OUTERMOST.
// chain(h, A, B) => A(B(h)): A sees the request first and the response last.
func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// requestIDMW ensures every request has a correlation id: it honours an inbound
// X-Request-ID (so a trace spans services) or generates one, stashes it in the
// context for the rest of the stack, and echoes it back on the response.
func requestIDMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(reqid.HeaderName)
		if id == "" {
			id = reqid.Generate()
		}
		w.Header().Set(reqid.HeaderName, id)
		next.ServeHTTP(w, r.WithContext(reqid.NewContext(r.Context(), id)))
	})
}

// statusRecorder captures the status code so the logging middleware can report it.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.written {
		s.status = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.written { // a handler that writes without WriteHeader implies 200
		s.status = http.StatusOK
		s.written = true
	}
	return s.ResponseWriter.Write(b)
}

// loggingMW emits one structured access-log line per request, correlated by id.
func loggingMW(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.LogAttrs(r.Context(), slog.LevelInfo, "http_request",
				slog.String("request_id", reqid.FromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

// recoverMW turns a handler panic into a 500 envelope instead of a dropped
// connection, and logs it. Placed INSIDE logging so the access log still records
// the 500.
func recoverMW(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.ErrorContext(r.Context(), "panic recovered",
						"request_id", reqid.FromContext(r.Context()), "panic", rec)
					writeError(w, log, http.StatusInternalServerError,
						"internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// authMW validates the bearer token via the pluggable Validator and puts the
// claims in the context. Applied only to business routes (not health). A nil
// validator means auth is disabled (local/dev) and the middleware is a pass-through.
func authMW(v auth.Validator, log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		if v == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := auth.BearerToken(r.Header.Get("Authorization"))
			claims, err := v.Validate(r.Context(), token)
			if err != nil {
				log.WarnContext(r.Context(), "auth rejected",
					"request_id", reqid.FromContext(r.Context()), "error", err.Error())
				writeError(w, log, http.StatusUnauthorized, "unauthorized", "invalid or missing credentials")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.NewContext(r.Context(), claims)))
		})
	}
}
