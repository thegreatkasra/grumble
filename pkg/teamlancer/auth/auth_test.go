package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInternalAuthenticatorReturnsIdentity(t *testing.T) {
	validator, err := newTestValidator(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}
	authenticator := InternalAuthenticator{validator: validator}
	token := signTestToken(t, "secret-123", map[string]any{
		"sub":      "user-42",
		"name":     "Alice TL",
		"exp":      time.Date(2026, 7, 9, 12, 1, 0, 0, time.UTC).Unix(),
		"iss":      "teamlancer",
		"aud":      "grumble-voice",
		"team_id":  "team-7",
		"board_id": "board-3",
		"permissions": map[string]bool{
			"moderate_voice": true,
		},
	})

	result, err := authenticator.Authenticate(context.Background(), Request{
		Username: "placeholder",
		Password: token,
	})
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if result == nil || result.Identity == nil {
		t.Fatal("expected identity result")
	}
	if result.Identity.UserID != "user-42" {
		t.Fatalf("expected user id user-42, got %q", result.Identity.UserID)
	}
	if result.Identity.DisplayName != "Alice TL" {
		t.Fatalf("expected display name Alice TL, got %q", result.Identity.DisplayName)
	}
	if result.Identity.TeamID != "team-7" {
		t.Fatalf("expected team id team-7, got %q", result.Identity.TeamID)
	}
	if result.Identity.BoardID != "board-3" {
		t.Fatalf("expected board id board-3, got %q", result.Identity.BoardID)
	}
	if !result.Identity.Permissions.JoinVoice || !result.Identity.Permissions.PublishAudio || !result.Identity.Permissions.ReceiveAudio || !result.Identity.Permissions.ModerateVoice {
		t.Fatalf("unexpected permissions: %+v", result.Identity.Permissions)
	}
}

func TestInternalAuthenticatorRejectsMissingToken(t *testing.T) {
	validator, err := newTestValidator(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}
	authenticator := InternalAuthenticator{validator: validator}

	_, err = authenticator.Authenticate(context.Background(), Request{})
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}
