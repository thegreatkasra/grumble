package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidatorAcceptsValidToken(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"sub":         "user-1",
		"name":        "Alice",
		"exp":         now.Add(time.Minute).Unix(),
		"iss":         "teamlancer",
		"aud":         "grumble-voice",
		"team_id":     "team-7",
		"board_id":    "board-3",
		"permissions": []string{"join_voice", "publish_audio", "receive_audio", "moderate_voice"},
	})

	claims, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims.Subject != "user-1" || claims.Name != "Alice" {
		t.Fatalf("unexpected subject/name: %+v", claims)
	}
	if claims.TeamID != "team-7" || claims.BoardID != "board-3" {
		t.Fatalf("unexpected team/board mapping: %+v", claims)
	}
	if !claims.Permissions.JoinVoice || !claims.Permissions.PublishAudio || !claims.Permissions.ReceiveAudio || !claims.Permissions.ModerateVoice {
		t.Fatalf("unexpected permissions: %+v", claims.Permissions)
	}
}

func TestValidatorRejectsExpiredToken(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"sub":  "user-1",
		"name": "Alice",
		"exp":  now.Add(-time.Minute).Unix(),
		"iss":  "teamlancer",
		"aud":  "grumble-voice",
	})

	_, err := validator.Validate(token)
	if err == nil || !strings.Contains(err.Error(), ErrExpiredToken.Error()) {
		t.Fatalf("expected expired token error, got %v", err)
	}
}

func TestValidatorRejectsInvalidSignature(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "wrong-secret", map[string]any{
		"sub":  "user-1",
		"name": "Alice",
		"exp":  now.Add(time.Minute).Unix(),
		"iss":  "teamlancer",
		"aud":  "grumble-voice",
	})

	_, err := validator.Validate(token)
	if err == nil || !strings.Contains(err.Error(), ErrInvalidToken.Error()) {
		t.Fatalf("expected invalid token error, got %v", err)
	}
}

func TestValidatorRejectsMissingSubject(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"name": "Alice",
		"exp":  now.Add(time.Minute).Unix(),
		"iss":  "teamlancer",
		"aud":  "grumble-voice",
	})

	_, err := validator.Validate(token)
	if err == nil || !strings.Contains(err.Error(), ErrMissingClaim.Error()) {
		t.Fatalf("expected missing claim error, got %v", err)
	}
}

func TestValidatorRejectsInvalidIssuer(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"sub":         "user-1",
		"name":        "Alice",
		"exp":         now.Add(time.Minute).Unix(),
		"iss":         "other",
		"aud":         "grumble-voice",
		"team_id":     "team-1",
		"board_id":    "board-1",
		"permissions": []string{"join_voice", "publish_audio", "receive_audio"},
	})

	_, err := validator.Validate(token)
	if err == nil || !strings.Contains(err.Error(), ErrInvalidIssuer.Error()) {
		t.Fatalf("expected invalid issuer error, got %v", err)
	}
}

func TestValidatorRejectsInvalidAudience(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"sub":         "user-1",
		"name":        "Alice",
		"exp":         now.Add(time.Minute).Unix(),
		"iss":         "teamlancer",
		"aud":         "other-audience",
		"team_id":     "team-1",
		"board_id":    "board-1",
		"permissions": []string{"join_voice", "publish_audio", "receive_audio"},
	})

	_, err := validator.Validate(token)
	if err == nil || !strings.Contains(err.Error(), ErrInvalidAudience.Error()) {
		t.Fatalf("expected invalid audience error, got %v", err)
	}
}

func TestValidatorRejectsMissingBoardID(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"sub":         "user-1",
		"name":        "Alice",
		"exp":         now.Add(time.Minute).Unix(),
		"iss":         "teamlancer",
		"aud":         "grumble-voice",
		"team_id":     "team-1",
		"permissions": []string{"join_voice", "publish_audio", "receive_audio"},
	})

	_, err := validator.Validate(token)
	if err == nil || !strings.Contains(err.Error(), ErrMissingClaim.Error()) {
		t.Fatalf("expected missing claim error, got %v", err)
	}
}

func TestValidatorRejectsMissingPermissions(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"sub":      "user-1",
		"name":     "Alice",
		"exp":      now.Add(time.Minute).Unix(),
		"iss":      "teamlancer",
		"aud":      "grumble-voice",
		"team_id":  "team-1",
		"board_id": "board-1",
	})

	_, err := validator.Validate(token)
	if err == nil || !strings.Contains(err.Error(), ErrMissingClaim.Error()) {
		t.Fatalf("expected missing claim error, got %v", err)
	}
}

func newTestValidator(t *testing.T, now time.Time) *Validator {
	t.Helper()
	validator, err := NewValidator(Config{
		Secret:   "secret-123",
		Issuer:   "teamlancer",
		Audience: "grumble-voice",
		Now: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}
	return validator
}

func signToken(t *testing.T, secret string, claims map[string]any) string {
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
