// Package reqid carries a per-request correlation id through context. It is
// transport-agnostic on purpose: the HTTP middleware and the gRPC interceptor
// both put an id here, and the scoring/audit code reads it without knowing which
// transport served the request.
package reqid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type ctxKey struct{}

// HeaderName is the canonical header/metadata key used on both HTTP and gRPC.
const HeaderName = "X-Request-ID"

// NewContext returns a copy of ctx carrying the request id.
func NewContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request id, or "" if none is set.
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// Generate returns a random 128-bit hex id. We use crypto/rand rather than a
// UUID dependency — for a correlation id, collision-resistant randomness is all
// we need and it keeps the dependency surface minimal.
func Generate() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read essentially never fails; if it does, an empty id is safer
		// than panicking inside request handling.
		return ""
	}
	return hex.EncodeToString(b[:])
}
