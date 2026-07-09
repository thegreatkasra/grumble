package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tljwt "mumble.info/grumble/pkg/teamlancer/auth/jwt"
)

const (
	ModeInternal = "internal"
	ModeLegacy   = "legacy"
)

type authError string

func (e authError) Error() string {
	return string(e)
}

func (e authError) Is(target error) bool {
	if target == ErrUnauthorized {
		return true
	}
	typed, ok := target.(authError)
	return ok && e == typed
}

var (
	ErrUnauthorized     = errors.New("teamlancer authentication failed")
	ErrInvalidToken     = authError("invalid token")
	ErrExpiredToken     = authError("expired token")
	ErrMissingClaim     = authError("missing claim")
	ErrInvalidIssuer    = authError("invalid issuer")
	ErrInvalidAudience  = authError("invalid audience")
	ErrPermissionDenied = authError("permission denied")
)

type Authenticator interface {
	Authenticate(ctx context.Context, req Request) (*Result, error)
}

type Request struct {
	ConnectionID      string
	RemoteIP          string
	ListenerType      string
	Username          string
	Password          string
	Tokens            []string
	HasCertificate    bool
	CertificateHash   string
	PasswordSupplied  bool
	RequestedAuthMode string
}

type Result struct {
	Identity *UserIdentity
}

type UserIdentity struct {
	UserID      string
	DisplayName string
	TeamID      string
	BoardID     string
	Permissions Permissions
}

func (req Request) VoiceToken() string {
	password := strings.TrimSpace(req.Password)
	if password != "" {
		return password
	}
	for _, token := range req.Tokens {
		token = strings.TrimSpace(token)
		if token != "" {
			return token
		}
	}
	return ""
}

type InternalAuthenticator struct {
	validator *tljwt.Validator
}

func NewInternalAuthenticatorFromEnv() (InternalAuthenticator, error) {
	secret := strings.TrimSpace(os.Getenv("TEAMLANCER_JWT_SECRET"))
	issuer := strings.TrimSpace(os.Getenv("TEAMLANCER_JWT_ISSUER"))
	audience := strings.TrimSpace(os.Getenv("TEAMLANCER_JWT_AUDIENCE"))

	validator, err := tljwt.NewValidator(tljwt.Config{
		Secret:   secret,
		Issuer:   issuer,
		Audience: audience,
	})
	if err != nil {
		return InternalAuthenticator{}, fmt.Errorf("teamlancer auth config: %w", err)
	}
	return InternalAuthenticator{validator: validator}, nil
}

func (a InternalAuthenticator) Authenticate(ctx context.Context, req Request) (*Result, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if a.validator == nil {
		return nil, ErrInvalidToken
	}

	claims, err := a.validator.Validate(req.VoiceToken())
	if err != nil {
		return nil, mapJWTError(err)
	}
	return &Result{
		Identity: &UserIdentity{
			UserID:      claims.Subject,
			DisplayName: claims.Name,
			TeamID:      claims.TeamID,
			BoardID:     claims.BoardID,
			Permissions: Permissions{
				JoinVoice:     claims.Permissions.JoinVoice,
				PublishAudio:  claims.Permissions.PublishAudio,
				ReceiveAudio:  claims.Permissions.ReceiveAudio,
				ModerateVoice: claims.Permissions.ModerateVoice,
			},
		},
	}, nil
}

func mapJWTError(err error) error {
	switch {
	case errors.Is(err, tljwt.ErrExpiredToken):
		return ErrExpiredToken
	case errors.Is(err, tljwt.ErrMissingClaim):
		return ErrMissingClaim
	case errors.Is(err, tljwt.ErrInvalidIssuer):
		return ErrInvalidIssuer
	case errors.Is(err, tljwt.ErrInvalidAudience):
		return ErrInvalidAudience
	default:
		return ErrInvalidToken
	}
}
