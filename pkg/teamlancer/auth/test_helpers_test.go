package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	tljwt "mumble.info/grumble/pkg/teamlancer/auth/jwt"
)

func newTestValidator(now time.Time) (*tljwt.Validator, error) {
	return tljwt.NewValidator(tljwt.Config{
		Secret:   "secret-123",
		Issuer:   "teamlancer",
		Audience: "grumble-voice",
		Now: func() time.Time {
			return now
		},
	})
}

func signTestToken(t *testing.T, secret string, claims map[string]any) string {
	t.Helper()
	headerBytes, err := json.Marshal(map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(headerBytes)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claimBytes)
	signingInput := encodedHeader + "." + encodedClaims

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature
}
