// Package httpapi is the REST/JSON driving adapter for the scoring service.
// It translates HTTP <-> domain and delegates all logic to the Scorer; it
// contains no scoring rules itself (transport stays thin).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/blocklocmedia/fraud-signals/internal/domain"
)

// scorerPort is the (tiny) slice of the Scorer that the handler needs. Depending
// on an interface rather than the concrete *scorer.Scorer keeps the handler
// trivially mockable and avoids an import cycle risk.
type scorerPort interface {
	Score(ctx context.Context, app domain.Application) domain.ScoreResult
}

// maxBodyBytes caps request bodies. Even for a small JSON API this is a basic
// resource-exhaustion guard.
const maxBodyBytes = 1 << 20 // 1 MiB

// scoreHandler handles POST /v1/score.
//
// Note the ctx threading: r.Context() carries the request deadline/cancellation
// (and, later, request-id + auth claims) all the way into the Scorer and from
// there into every provider call. If the client disconnects, the whole fan-out
// is cancelled.
func scoreHandler(s scorerPort, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields() // reject typos / unexpected fields loudly

		var app domain.Application
		if err := dec.Decode(&app); err != nil {
			// Distinguish an oversized body from a malformed one for clearer 4xx.
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeError(w, log, http.StatusRequestEntityTooLarge,
					"payload_too_large", "request body exceeds limit")
				return
			}
			writeError(w, log, http.StatusBadRequest,
				"invalid_json", "request body is not valid JSON: "+err.Error())
			return
		}
		if msg, ok := validate(app); !ok {
			writeError(w, log, http.StatusBadRequest, "validation_failed", msg)
			return
		}

		// Measure end-to-end scoring latency and surface it as a header. Besides
		// being useful in production, this is what makes the naive-vs-guarded demo
		// endpoints observable from a plain `curl -i` loop.
		start := time.Now()
		result := s.Score(r.Context(), app)
		w.Header().Set("X-Score-Latency-Ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))

		writeJSON(w, log, http.StatusOK, result)
	}
}

// validate enforces the minimum required fields. Returns (message, ok).
func validate(app domain.Application) (string, bool) {
	if app.ApplicantID == "" {
		return "applicant_id is required", false
	}
	if app.Product == "" {
		return "product is required", false
	}
	return "", true
}
