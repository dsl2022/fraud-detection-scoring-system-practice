package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// makeJWT builds a compact JWS with the given alg/claims, signed (HS256) with
// secret. It mirrors the validator so we don't need a JWT dependency in tests.
func makeJWT(secret []byte, alg string, claims map[string]any) string {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"` + alg + `","typ":"JWT"}`))
	pl, _ := json.Marshal(claims)
	payload := enc.EncodeToString(pl)
	signing := header + "." + payload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + enc.EncodeToString(mac.Sum(nil))
}

func TestHS256_Validate(t *testing.T) {
	secret := []byte("super-secret")
	other := []byte("wrong-secret")
	future := float64(time.Now().Add(time.Hour).Unix())
	past := float64(time.Now().Add(-time.Hour).Unix())

	tests := []struct {
		name    string
		token   string
		wantErr error
		wantSub string
	}{
		{
			name:    "valid token",
			token:   makeJWT(secret, "HS256", map[string]any{"sub": "user-1", "exp": future}),
			wantSub: "user-1",
		},
		{
			name:    "no token",
			token:   "",
			wantErr: ErrNoToken,
		},
		{
			name:    "wrong signature",
			token:   makeJWT(other, "HS256", map[string]any{"sub": "user-1", "exp": future}),
			wantErr: ErrInvalidToken,
		},
		{
			name:    "alg none rejected (downgrade attack)",
			token:   makeJWT(secret, "none", map[string]any{"sub": "user-1"}),
			wantErr: ErrInvalidToken,
		},
		{
			name:    "expired",
			token:   makeJWT(secret, "HS256", map[string]any{"sub": "user-1", "exp": past}),
			wantErr: ErrExpired,
		},
		{
			name:    "malformed (two segments)",
			token:   "aaa.bbb",
			wantErr: ErrInvalidToken,
		},
	}

	v := NewHS256(secret)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := v.Validate(context.Background(), tt.token)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if claims.Subject != tt.wantSub {
				t.Errorf("subject = %q, want %q", claims.Subject, tt.wantSub)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc.def.ghi": "abc.def.ghi",
		"bearer xyz":         "xyz", // scheme is case-insensitive
		"Basic abc":          "",
		"":                   "",
		"Bearer":             "",
	}
	for in, want := range cases {
		if got := BearerToken(in); got != want {
			t.Errorf("BearerToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := NewContext(context.Background(), Claims{Subject: "u1"})
	if SubjectFromContext(ctx) != "u1" {
		t.Errorf("subject not propagated through context")
	}
	if SubjectFromContext(context.Background()) != "" {
		t.Errorf("expected empty subject for bare context")
	}
}
