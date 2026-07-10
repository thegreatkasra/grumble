package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"mumble.info/grumble/pkg/mumbleproto"
	tlauth "mumble.info/grumble/pkg/teamlancer/auth"
	tlguard "mumble.info/grumble/pkg/teamlancer/auth/guard"
)

func (server *Server) effectiveAuthMode() string {
	if runtimeConfig.TeamlancerMode {
		return runtimeConfig.EffectiveTeamlancerAuthMode()
	}
	return tlauth.ModeLegacy
}

func (server *Server) configureAuthenticator() error {
	if !runtimeConfig.TeamlancerMode || runtimeConfig.EffectiveTeamlancerAuthMode() != tlauth.ModeInternal {
		server.teamlancerAuthenticator = nil
		return nil
	}
	if server.teamlancerAuthenticator == nil {
		authenticator, err := tlauth.NewInternalAuthenticatorFromEnv()
		if err != nil {
			return err
		}
		server.teamlancerAuthenticator = authenticator
	}
	return nil
}

func (server *Server) logAuthEvent(level, event string, client *Client, userID string, extra map[string]string) {
	fields := map[string]string{
		"connection_id": client.connectionID,
		"auth_mode":     server.effectiveAuthMode(),
	}
	if userID != "" {
		fields["user_id"] = userID
	}
	for key, value := range extra {
		fields[key] = value
	}
	emitStructuredEvent(server.Logger, level, event, fields)
}

func (server *Server) logVoiceWebSocketRejected(client *Client, reason, boardID string) {
	if client == nil || client.listenerType != "websocket" {
		return
	}
	emitStructuredEvent(server.Logger, "warn", "voice_ws_rejected", map[string]string{
		"reason":   reason,
		"board_id": strings.TrimSpace(boardID),
	})
}

func (server *Server) logVoiceWebSocketConnected(client *Client, identity *tlauth.UserIdentity, roomID string) {
	if client == nil || client.listenerType != "websocket" || identity == nil {
		return
	}
	emitStructuredEvent(server.Logger, "info", "voice_ws_connected", map[string]string{
		"user_id":  strings.TrimSpace(identity.UserID),
		"board_id": strings.TrimSpace(identity.BoardID),
		"room_id":  strings.TrimSpace(roomID),
	})
}

func (server *Server) handleTeamlancerAuthenticate(client *Client, auth *mumbleproto.Authenticate) bool {
	req := tlauth.Request{
		ConnectionID:      client.connectionID,
		RemoteIP:          client.remoteIP,
		ListenerType:      client.listenerType,
		Username:          auth.GetUsername(),
		Password:          auth.GetPassword(),
		Tokens:            auth.Tokens,
		HasCertificate:    client.HasCertificate(),
		CertificateHash:   client.CertHash(),
		PasswordSupplied:  auth.Password != nil,
		RequestedAuthMode: server.effectiveAuthMode(),
	}

	result, err := server.teamlancerAuthenticator.Authenticate(context.Background(), req)
	if err != nil {
		reason := classifyAuthFailure(err)
		server.logVoiceWebSocketRejected(client, reason, "")
		server.logAuthEvent("warn", "voice_auth_failed", client, "", map[string]string{
			"reason": reason,
		})
		client.RejectAuth(mumbleproto.Reject_WrongUserPW, "Authentication failed")
		return false
	}
	if result == nil || result.Identity == nil {
		server.logVoiceWebSocketRejected(client, "missing_identity", "")
		server.logAuthEvent("warn", "voice_auth_failed", client, "", map[string]string{
			"reason": "missing_identity",
		})
		client.RejectAuth(mumbleproto.Reject_WrongUserPW, "Authentication failed")
		return false
	}
	if !tlguard.CanJoinVoice(result.Identity) {
		server.logVoiceWebSocketRejected(client, "permission_denied", result.Identity.BoardID)
		presented, _ := json.Marshal(result.Identity.Permissions.PresentedNames())
		server.logAuthEvent("warn", "voice_auth_failed", client, result.Identity.UserID, map[string]string{
			"reason":                 "permission_denied",
			"required_permission":    tlauth.PermissionVoiceJoin,
			"presented_permissions":  string(presented),
			"board_id":               strings.TrimSpace(result.Identity.BoardID),
		})
		client.RejectAuth(mumbleproto.Reject_WrongUserPW, "Authentication failed")
		return false
	}

	client.teamlancerIdentity = result.Identity
	client.Username = result.Identity.DisplayName
	if client.Username == "" {
		client.Username = auth.GetUsername()
	}
	return true
}

func classifyAuthFailure(err error) string {
	switch {
	case errors.Is(err, tlauth.ErrExpiredToken):
		return "expired_token"
	case errors.Is(err, tlauth.ErrMissingClaim):
		return "missing_claim"
	case errors.Is(err, tlauth.ErrInvalidIssuer):
		return "invalid_issuer"
	case errors.Is(err, tlauth.ErrInvalidAudience):
		return "invalid_audience"
	case errors.Is(err, tlauth.ErrPermissionDenied):
		return "permission_denied"
	case errors.Is(err, tlauth.ErrInvalidToken), errors.Is(err, tlauth.ErrUnauthorized):
		return "invalid_token"
	default:
		return "authentication_failed"
	}
}

func (server *Server) handleLegacyAuthenticate(client *Client, auth *mumbleproto.Authenticate) bool {
	client.Username = *auth.Username

	if client.Username == "SuperUser" {
		if auth.Password == nil {
			server.logAuthEvent("warn", "voice_auth_failed", client, "", map[string]string{
				"reason": "missing_superuser_password",
			})
			client.RejectAuth(mumbleproto.Reject_WrongUserPW, "")
			return false
		} else {
			if server.CheckSuperUserPassword(*auth.Password) {
				ok := false
				client.user, ok = server.UserNameMap[client.Username]
				if !ok {
					server.logAuthEvent("warn", "voice_auth_failed", client, "", map[string]string{
						"reason": "invalid_superuser_mapping",
					})
					client.RejectAuth(mumbleproto.Reject_InvalidUsername, "")
					return false
				}
			} else {
				server.logAuthEvent("warn", "voice_auth_failed", client, "", map[string]string{
					"reason": "wrong_superuser_password",
				})
				client.RejectAuth(mumbleproto.Reject_WrongUserPW, "")
				return false
			}
		}
	} else {
		user, exists := server.UserNameMap[client.Username]
		if exists {
			if client.HasCertificate() && user.CertHash == client.CertHash() {
				client.user = user
			} else {
				server.logAuthEvent("warn", "voice_auth_failed", client, "", map[string]string{
					"reason": "wrong_certificate_hash",
				})
				client.RejectAuth(mumbleproto.Reject_WrongUserPW, "Wrong certificate hash")
				return false
			}
		}

		if client.user == nil && client.HasCertificate() {
			user, exists := server.UserCertMap[client.CertHash()]
			if exists {
				client.user = user
			}
		}
	}

	if client.user == nil && server.hasServerPassword() {
		if auth.Password == nil || !server.CheckServerPassword(*auth.Password) {
			server.logAuthEvent("warn", "voice_auth_failed", client, "", map[string]string{
				"reason": "wrong_server_password",
			})
			client.RejectAuth(mumbleproto.Reject_WrongServerPW, "Invalid server password")
			return false
		}
	}
	return true
}

func (server *Server) authenticationUsername(client *Client) string {
	if client.teamlancerIdentity != nil && client.teamlancerIdentity.UserID != "" {
		return client.teamlancerIdentity.UserID
	}
	if client.user != nil {
		return strconv.FormatUint(uint64(client.user.Id), 10)
	}
	return ""
}

func authRejectForInvalidUsername(client *Client) {
	client.RejectAuth(mumbleproto.Reject_InvalidUsername, "Please specify a username to log in")
}

func readyClientAuthentication(client *Client) error {
	err := client.crypt.GenerateKey(client.CryptoMode)
	if err != nil {
		return err
	}

	client.lastResync = time.Now().Unix()
	return client.sendMessage(&mumbleproto.CryptSetup{
		Key:         client.crypt.Key,
		ClientNonce: client.crypt.DecryptIV,
		ServerNonce: client.crypt.EncryptIV,
	})
}
