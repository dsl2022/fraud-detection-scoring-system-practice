// Package auth provides PLUGGABLE token validation for the edge.
//
// The Validator interface is the contract; everything else (HTTP middleware,
// gRPC interceptor) depends on it, never on a concrete JWT library. That is the
// whole point of "keep it pluggable": today we ship a stdlib HS256 verifier and
// a dev allow-all; in production you swap in an RS256/JWKS (OIDC) verifier — e.g.
// backed by github.com/golang-jwt/jwt — without touching a single call site.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrNoToken      = errors.New("no token provided")
	ErrInvalidToken = errors.New("invalid token")
	ErrExpired      = errors.New("token expired")
)

// Claims is the minimal subset of a token we propagate. Raw keeps the full claim
// set for callers that need more without widening this struct.
type Claims struct {
	Subject string
	Raw     map[string]any
}

// Validator validates a bearer token and returns its claims.
type Validator interface {
	Validate(ctx context.Context, token string) (Claims, error)
}

// --- context propagation -------------------------------------------------

type ctxKey struct{}

func NewContext(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

func FromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(Claims)
	return c, ok
}

// SubjectFromContext is a convenience for the common "who made this decision"
// case (stamped onto the audit record).
func SubjectFromContext(ctx context.Context) string {
	if c, ok := FromContext(ctx); ok {
		return c.Subject
	}
	return ""
}

// --- AllowAll (dev/test only) --------------------------------------------

// AllowAll accepts any non-empty token and uses it as the subject. NEVER enable
// in production — it performs no cryptographic verification.
type AllowAll struct{}

func (AllowAll) Validate(_ context.Context, token string) (Claims, error) {
	if token == "" {
		return Claims{}, ErrNoToken
	}
	return Claims{Subject: token, Raw: map[string]any{"sub": token}}, nil
}

// --- HS256 (stdlib, no external dependency) ------------------------------

// HS256 verifies a compact JWS (header.payload.signature) signed with HMAC-SHA256.
// It deliberately accepts ONLY alg=HS256 to prevent the classic "alg: none" and
// algorithm-confusion downgrade attacks.
type HS256 struct {
	secret []byte
	now    func() time.Time // injectable for tests
}

func NewHS256(secret []byte) *HS256 { return &HS256{secret: secret, now: time.Now} }

func (h *HS256) Validate(_ context.Context, token string) (Claims, error) {
	if token == "" {
		return Claims{}, ErrNoToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}
	enc := base64.RawURLEncoding

	// 1. Header: enforce the algorithm. Reject anything but HS256.
	hdrBytes, err := enc.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil || hdr.Alg != "HS256" {
		return Claims{}, ErrInvalidToken
	}

	// 2. Signature: recompute the HMAC over "header.payload" and constant-time
	//    compare. hmac.Equal avoids timing side channels.
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)
	got, err := enc.DecodeString(parts[2])
	if err != nil || !hmac.Equal(got, expected) {
		return Claims{}, ErrInvalidToken
	}

	// 3. Claims + expiry.
	payload, err := enc.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if exp, ok := raw["exp"].(float64); ok {
		if h.now().After(time.Unix(int64(exp), 0)) {
			return Claims{}, ErrExpired
		}
	}
	sub, _ := raw["sub"].(string)
	return Claims{Subject: sub, Raw: raw}, nil
}

// BearerToken extracts the token from an "Authorization: Bearer <t>" value.
// Returns "" if the scheme is missing/!= Bearer.
func BearerToken(authorization string) string {
	const prefix = "Bearer "
	if len(authorization) > len(prefix) && strings.EqualFold(authorization[:len(prefix)], prefix) {
		return authorization[len(prefix):]
	}
	return ""
}
