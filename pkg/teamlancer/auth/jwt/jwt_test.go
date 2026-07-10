package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
		"permissions": []string{PermissionVoiceJoin, PermissionVoicePublish, PermissionVoiceReceive, PermissionVoiceModerate},
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

func TestValidatorAcceptsOwnerAndMemberPermissionSets(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)

	ownerToken := signToken(t, "secret-123", map[string]any{
		"sub":      "owner-1",
		"name":     "Owner",
		"exp":      now.Add(time.Minute).Unix(),
		"iss":      "teamlancer",
		"aud":      "grumble-voice",
		"team_id":  "team-7",
		"board_id": "board-3",
		"permissions": []string{
			PermissionVoiceJoin,
			PermissionVoicePublish,
			PermissionVoiceReceive,
			PermissionVoiceModerate,
		},
	})
	ownerClaims, err := validator.Validate(ownerToken)
	if err != nil {
		t.Fatalf("owner validate: %v", err)
	}
	if !ownerClaims.Permissions.JoinVoice || !ownerClaims.Permissions.ModerateVoice {
		t.Fatalf("owner permissions: %+v", ownerClaims.Permissions)
	}

	memberToken := signToken(t, "secret-123", map[string]any{
		"sub":      "member-1",
		"name":     "Member",
		"exp":      now.Add(time.Minute).Unix(),
		"iss":      "teamlancer",
		"aud":      "grumble-voice",
		"team_id":  "team-7",
		"board_id": "board-3",
		"permissions": []string{
			PermissionVoiceJoin,
			PermissionVoicePublish,
			PermissionVoiceReceive,
		},
	})
	memberClaims, err := validator.Validate(memberToken)
	if err != nil {
		t.Fatalf("member validate: %v", err)
	}
	if !memberClaims.Permissions.JoinVoice || memberClaims.Permissions.ModerateVoice {
		t.Fatalf("member permissions: %+v", memberClaims.Permissions)
	}
}

func TestValidatorMemberWithoutModerateStillJoins(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"sub":      "member-1",
		"name":     "Member",
		"exp":      now.Add(time.Minute).Unix(),
		"iss":      "teamlancer",
		"aud":      "grumble-voice",
		"team_id":  "team-7",
		"board_id": "board-3",
		"permissions": []string{
			PermissionVoiceJoin,
			PermissionVoicePublish,
			PermissionVoiceReceive,
		},
	})

	claims, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !claims.Permissions.JoinVoice {
		t.Fatal("expected voice.join to authorize connection")
	}
	if claims.Permissions.ModerateVoice {
		t.Fatal("member must not receive voice.moderate")
	}
}

func TestValidatorMissingJoinDoesNotGrantJoin(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"sub":      "user-1",
		"name":     "Alice",
		"exp":      now.Add(time.Minute).Unix(),
		"iss":      "teamlancer",
		"aud":      "grumble-voice",
		"team_id":  "team-7",
		"board_id": "board-3",
		"permissions": []string{
			PermissionVoicePublish,
			PermissionVoiceReceive,
		},
	})

	claims, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims.Permissions.JoinVoice {
		t.Fatal("missing voice.join must not imply join")
	}
	if !claims.Permissions.PublishAudio || !claims.Permissions.ReceiveAudio {
		t.Fatalf("expected publish/receive to remain: %+v", claims.Permissions)
	}
}

func TestValidatorMissingPublishOrReceiveStillParsesJoin(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)

	for _, perms := range [][]string{
		{PermissionVoiceJoin, PermissionVoiceReceive},
		{PermissionVoiceJoin, PermissionVoicePublish},
	} {
		token := signToken(t, "secret-123", map[string]any{
			"sub":         "user-1",
			"name":        "Alice",
			"exp":         now.Add(time.Minute).Unix(),
			"iss":         "teamlancer",
			"aud":         "grumble-voice",
			"team_id":     "team-7",
			"board_id":    "board-3",
			"permissions": perms,
		})
		claims, err := validator.Validate(token)
		if err != nil {
			t.Fatalf("validate %v: %v", perms, err)
		}
		if !claims.Permissions.JoinVoice {
			t.Fatalf("expected join for %v", perms)
		}
	}
}

func TestValidatorRejectsMalformedPermissionsClaim(t *testing.T) {
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
		"permissions": 42,
	})

	_, err := validator.Validate(token)
	if !errors.Is(err, ErrInvalidPermissions) {
		t.Fatalf("expected ErrInvalidPermissions, got %v", err)
	}
}

func TestValidatorParsesPermissionsJSONStringArray(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	encoded, err := json.Marshal([]string{
		PermissionVoiceJoin,
		PermissionVoicePublish,
		PermissionVoiceReceive,
	})
	if err != nil {
		t.Fatalf("marshal permissions: %v", err)
	}
	token := signToken(t, "secret-123", map[string]any{
		"sub":         "user-1",
		"name":        "Alice",
		"exp":         now.Add(time.Minute).Unix(),
		"iss":         "teamlancer",
		"aud":         "grumble-voice",
		"team_id":     "team-7",
		"board_id":    "board-3",
		"permissions": string(encoded),
	})

	claims, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !claims.Permissions.JoinVoice || !claims.Permissions.PublishAudio || !claims.Permissions.ReceiveAudio {
		t.Fatalf("unexpected permissions: %+v", claims.Permissions)
	}
}

func TestValidatorUnrelatedPermissionsDoNotImplyJoin(t *testing.T) {
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
		"permissions": []string{"boards.edit", "team.admin", "join", "voice:join"},
	})

	claims, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims.Permissions.JoinVoice || claims.Permissions.PublishAudio || claims.Permissions.ReceiveAudio || claims.Permissions.ModerateVoice {
		t.Fatalf("unrelated permissions must not grant voice rights: %+v", claims.Permissions)
	}
	if len(claims.Permissions.Presented) != 4 {
		t.Fatalf("expected presented names preserved, got %+v", claims.Permissions.Presented)
	}
}

func TestValidatorTrimsWhitespaceOnly(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	validator := newTestValidator(t, now)
	token := signToken(t, "secret-123", map[string]any{
		"sub":      "user-1",
		"name":     "Alice",
		"exp":      now.Add(time.Minute).Unix(),
		"iss":      "teamlancer",
		"aud":      "grumble-voice",
		"team_id":  "team-7",
		"board_id": "board-3",
		"permissions": []string{
			"  voice.join  ",
			"voice.publish",
		},
	})

	claims, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !claims.Permissions.JoinVoice || !claims.Permissions.PublishAudio {
		t.Fatalf("unexpected permissions: %+v", claims.Permissions)
	}
	if claims.Permissions.Presented[0] != "voice.join" {
		t.Fatalf("expected trimmed presented name, got %q", claims.Permissions.Presented[0])
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
		"permissions": []string{PermissionVoiceJoin, PermissionVoicePublish, PermissionVoiceReceive},
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
		"permissions": []string{PermissionVoiceJoin, PermissionVoicePublish, PermissionVoiceReceive},
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
		"permissions": []string{PermissionVoiceJoin, PermissionVoicePublish, PermissionVoiceReceive},
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

func TestValidatorAcceptsCrossRepoGoldenVoiceJWT(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	fixturePath := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "testdata", "voice_permission_contract_golden.json"))
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}

	var fixture struct {
		Secret string `json:"secret"`
		Issuer string `json:"issuer"`
		Aud    string `json:"audience"`
		Token  string `json:"token"`
		Claims struct {
			Permissions []string `json:"permissions"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode golden fixture: %v", err)
	}

	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	validator, err := NewValidator(Config{
		Secret:   fixture.Secret,
		Issuer:   fixture.Issuer,
		Audience: fixture.Aud,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	claims, err := validator.Validate(fixture.Token)
	if err != nil {
		t.Fatalf("validate golden token: %v", err)
	}
	if !claims.Permissions.JoinVoice {
		t.Fatal("golden token must grant voice.join")
	}
	if !claims.Permissions.PublishAudio {
		t.Fatal("golden token must grant voice.publish")
	}
	if !claims.Permissions.ReceiveAudio {
		t.Fatal("golden token must grant voice.receive")
	}
	if !claims.Permissions.ModerateVoice {
		t.Fatal("golden owner token must grant voice.moderate")
	}
	for _, want := range []string{PermissionVoiceJoin, PermissionVoicePublish, PermissionVoiceReceive, PermissionVoiceModerate} {
		found := false
		for _, got := range fixture.Claims.Permissions {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fixture missing permission %q", want)
		}
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
