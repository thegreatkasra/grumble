package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Canonical voice permission claim values. Keep in sync with
// pkg/teamlancer/auth permission constants.
const (
	PermissionVoiceJoin     = "voice.join"
	PermissionVoicePublish  = "voice.publish"
	PermissionVoiceReceive  = "voice.receive"
	PermissionVoiceModerate = "voice.moderate"
)

type Config struct {
	Secret   string
	Issuer   string
	Audience string
	Now      func() time.Time
}

type Validator struct {
	secret   []byte
	issuer   string
	audience string
	now      func() time.Time
}

type Claims struct {
	Subject     string
	Name        string
	ExpiresAt   time.Time
	Issuer      string
	Audience    []string
	TeamID      string
	BoardID     string
	Permissions Permissions
}

type Permissions struct {
	JoinVoice     bool
	PublishAudio  bool
	ReceiveAudio  bool
	ModerateVoice bool
	Presented     []string
}

type tokenHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type rawClaims struct {
	Subject     string          `json:"sub"`
	Name        string          `json:"name"`
	ExpiresAt   json.RawMessage `json:"exp"`
	Issuer      string          `json:"iss"`
	Audience    json.RawMessage `json:"aud"`
	TeamID      string          `json:"team_id"`
	BoardID     string          `json:"board_id"`
	Permissions json.RawMessage `json:"permissions"`
}

func NewValidator(cfg Config) (*Validator, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return nil, errors.New("TEAMLANCER_JWT_SECRET must be set")
	}
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, errors.New("TEAMLANCER_JWT_ISSUER must be set")
	}
	if strings.TrimSpace(cfg.Audience) == "" {
		return nil, errors.New("TEAMLANCER_JWT_AUDIENCE must be set")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Validator{
		secret:   []byte(cfg.Secret),
		issuer:   cfg.Issuer,
		audience: cfg.Audience,
		now:      now,
	}, nil
}

func (v *Validator) Validate(token string) (*Claims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidToken
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	headerBytes, err := decodeSegment(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var header tokenHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, ErrInvalidToken
	}
	if header.Alg != "HS256" {
		return nil, ErrInvalidToken
	}
	if !verifyHMAC(parts[0]+"."+parts[1], parts[2], v.secret) {
		return nil, ErrInvalidToken
	}

	payloadBytes, err := decodeSegment(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var raw rawClaims
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, ErrInvalidToken
	}

	exp, err := parseExp(raw.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if v.now().After(exp) || v.now().Equal(exp) {
		return nil, ErrExpiredToken
	}
	if strings.TrimSpace(raw.Subject) == "" || strings.TrimSpace(raw.Name) == "" {
		return nil, ErrMissingClaim
	}
	if strings.TrimSpace(raw.TeamID) == "" || strings.TrimSpace(raw.BoardID) == "" || len(raw.Permissions) == 0 || string(raw.Permissions) == "null" {
		return nil, ErrMissingClaim
	}
	if raw.Issuer != v.issuer {
		return nil, ErrInvalidIssuer
	}
	audience, err := parseAudience(raw.Audience)
	if err != nil {
		return nil, ErrInvalidAudience
	}
	if !containsAudience(audience, v.audience) {
		return nil, ErrInvalidAudience
	}
	permissions, err := parsePermissions(raw.Permissions)
	if err != nil {
		return nil, err
	}

	return &Claims{
		Subject:     raw.Subject,
		Name:        raw.Name,
		ExpiresAt:   exp,
		Issuer:      raw.Issuer,
		Audience:    audience,
		TeamID:      raw.TeamID,
		BoardID:     raw.BoardID,
		Permissions: permissions,
	}, nil
}

func decodeSegment(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

func verifyHMAC(signingInput, signature string, secret []byte) bool {
	sigBytes, err := decodeSegment(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signingInput))
	return hmac.Equal(sigBytes, mac.Sum(nil))
}

func parseExp(raw json.RawMessage) (time.Time, error) {
	if len(raw) == 0 {
		return time.Time{}, ErrMissingClaim
	}
	var seconds int64
	if err := json.Unmarshal(raw, &seconds); err != nil {
		var floatSeconds float64
		if err := json.Unmarshal(raw, &floatSeconds); err != nil {
			return time.Time{}, ErrInvalidToken
		}
		seconds = int64(floatSeconds)
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func parseAudience(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, ErrMissingClaim
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			return nil, ErrInvalidAudience
		}
		return []string{single}, nil
	}
	var multi []string
	if err := json.Unmarshal(raw, &multi); err != nil {
		return nil, ErrInvalidAudience
	}
	if len(multi) == 0 {
		return nil, ErrInvalidAudience
	}
	return multi, nil
}

func containsAudience(audience []string, want string) bool {
	for _, candidate := range audience {
		if candidate == want {
			return true
		}
	}
	return false
}

func parsePermissions(raw json.RawMessage) (Permissions, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return Permissions{}, ErrInvalidPermissions
	}

	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) >= 2 && trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return Permissions{}, ErrInvalidPermissions
		}
		encoded = strings.TrimSpace(encoded)
		if encoded == "" {
			return Permissions{}, ErrInvalidPermissions
		}
		return parsePermissions(json.RawMessage(encoded))
	}

	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		perms := Permissions{
			Presented: make([]string, 0, len(names)),
		}
		for _, name := range names {
			trimmedName := strings.TrimSpace(name)
			if trimmedName == "" {
				continue
			}
			perms.Presented = append(perms.Presented, trimmedName)
			normalized, ok := parsePermissionName(trimmedName)
			if !ok {
				continue
			}
			applyPermission(&perms, normalized, true)
		}
		return perms, nil
	}

	var flags map[string]bool
	if err := json.Unmarshal(raw, &flags); err == nil {
		perms := Permissions{
			Presented: make([]string, 0, len(flags)),
		}
		for name, enabled := range flags {
			trimmedName := strings.TrimSpace(name)
			if trimmedName == "" {
				continue
			}
			if enabled {
				perms.Presented = append(perms.Presented, trimmedName)
			}
			normalized, ok := parsePermissionName(trimmedName)
			if !ok {
				continue
			}
			applyPermission(&perms, normalized, enabled)
		}
		return perms, nil
	}

	return Permissions{}, ErrInvalidPermissions
}

func applyPermission(perms *Permissions, name string, enabled bool) {
	switch name {
	case PermissionVoiceJoin:
		perms.JoinVoice = enabled
	case PermissionVoicePublish:
		perms.PublishAudio = enabled
	case PermissionVoiceReceive:
		perms.ReceiveAudio = enabled
	case PermissionVoiceModerate:
		perms.ModerateVoice = enabled
	}
}

func parsePermissionName(name string) (string, bool) {
	normalized := strings.TrimSpace(name)
	switch normalized {
	case PermissionVoiceJoin:
		return PermissionVoiceJoin, true
	case PermissionVoicePublish:
		return PermissionVoicePublish, true
	case PermissionVoiceReceive:
		return PermissionVoiceReceive, true
	case PermissionVoiceModerate:
		return PermissionVoiceModerate, true
	default:
		return "", false
	}
}

var (
	ErrInvalidToken       = errors.New("invalid token")
	ErrExpiredToken       = errors.New("expired token")
	ErrMissingClaim       = errors.New("missing claim")
	ErrInvalidIssuer      = errors.New("invalid issuer")
	ErrInvalidAudience    = errors.New("invalid audience")
	ErrInvalidPermissions = fmt.Errorf("%w: invalid permissions claim", ErrInvalidToken)
)
