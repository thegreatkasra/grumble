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
		return nil, ErrInvalidToken
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
		return defaultPermissions(), nil
	}

	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		perms := Permissions{}
		for _, name := range names {
			normalized, ok := parsePermissionName(name)
			if !ok {
				continue
			}
			applyPermission(&perms, normalized, true)
		}
		return perms, nil
	}

	var flags map[string]bool
	if err := json.Unmarshal(raw, &flags); err == nil {
		perms := defaultPermissions()
		for name, enabled := range flags {
			normalized, ok := parsePermissionName(name)
			if !ok {
				continue
			}
			applyPermission(&perms, normalized, enabled)
		}
		return perms, nil
	}

	return Permissions{}, fmt.Errorf("invalid permissions claim")
}

func applyPermission(perms *Permissions, name string, enabled bool) {
	switch name {
	case "join_voice":
		perms.JoinVoice = enabled
	case "publish_audio":
		perms.PublishAudio = enabled
	case "receive_audio":
		perms.ReceiveAudio = enabled
	case "moderate_voice":
		perms.ModerateVoice = enabled
	}
}

func defaultPermissions() Permissions {
	return Permissions{
		JoinVoice:    true,
		PublishAudio: true,
		ReceiveAudio: true,
	}
}

func parsePermissionName(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "joinvoice", "join_voice":
		return "join_voice", true
	case "publishaudio", "publish_audio":
		return "publish_audio", true
	case "receiveaudio", "receive_audio":
		return "receive_audio", true
	case "moderatevoice", "moderate_voice":
		return "moderate_voice", true
	default:
		return "", false
	}
}

var (
	ErrInvalidToken    = errors.New("invalid token")
	ErrExpiredToken    = errors.New("expired token")
	ErrMissingClaim    = errors.New("missing claim")
	ErrInvalidIssuer   = errors.New("invalid issuer")
	ErrInvalidAudience = errors.New("invalid audience")
)
