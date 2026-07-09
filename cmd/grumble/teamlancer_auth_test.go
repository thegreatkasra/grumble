package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"mumble.info/grumble/pkg/cryptstate"
	"mumble.info/grumble/pkg/mumbleproto"
	tlauth "mumble.info/grumble/pkg/teamlancer/auth"
)

type stubTeamlancerAuthenticator struct {
	called bool
	result *tlauth.Result
	err    error
}

func (s *stubTeamlancerAuthenticator) Authenticate(_ context.Context, _ tlauth.Request) (*tlauth.Result, error) {
	s.called = true
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

type routingTeamlancerAuthenticator struct {
	calledByUsername map[string]bool
	results          map[string]*tlauth.Result
	errs             map[string]error
}

func (r *routingTeamlancerAuthenticator) Authenticate(_ context.Context, req tlauth.Request) (*tlauth.Result, error) {
	if r.calledByUsername == nil {
		r.calledByUsername = map[string]bool{}
	}
	r.calledByUsername[req.Username] = true
	if err, ok := r.errs[req.Username]; ok && err != nil {
		return nil, err
	}
	if result, ok := r.results[req.Username]; ok {
		return result, nil
	}
	return nil, tlauth.ErrUnauthorized
}

func TestTeamlancerModeSelectsInternalAuthenticator(t *testing.T) {
	t.Setenv("TEAMLANCER_JWT_SECRET", "test-secret")
	t.Setenv("TEAMLANCER_JWT_ISSUER", "teamlancer")
	t.Setenv("TEAMLANCER_JWT_AUDIENCE", "grumble-voice")
	runtimeConfig = RuntimeConfig{
		TeamlancerMode:     true,
		TeamlancerAuthMode: tlauth.ModeInternal,
	}
	server, err := NewServer(1)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	if err := server.initPerLaunchData(); err != nil {
		t.Fatalf("init per launch data: %v", err)
	}
	defer server.cleanPerLaunchData()

	if server.teamlancerAuthenticator == nil {
		t.Fatal("expected Teamlancer authenticator to be configured")
	}
}

func TestLegacyAuthModeKeepsLegacyAuthentication(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()

	runtimeConfig.TeamlancerAuthMode = tlauth.ModeLegacy
	runtimeConfig.EnablePublicWebSocket = true
	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	conn, _ := connectMumbleWebSocketClient(t, "legacy-user")

	waitForCondition(t, 3*time.Second, func() bool {
		return server.clientCount() == 1
	}, "legacy authenticated client")

	client := server.clientsSnapshot()[0]
	if client.teamlancerIdentity != nil {
		t.Fatal("expected legacy authentication to leave teamlancer identity unset")
	}
	if client.Username != "legacy-user" {
		t.Fatalf("expected legacy username to be preserved, got %q", client.Username)
	}

	closeMumbleClient(t, conn, server, 0)
}

func TestFailedTeamlancerAuthenticationDoesNotCreateSession(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()

	stub := &stubTeamlancerAuthenticator{err: tlauth.ErrUnauthorized}
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	runtimeConfig.EnablePublicWebSocket = true
	server.teamlancerAuthenticator = stub

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	conn := openMumbleWebSocketClient(t)
	defer conn.Close()
	performMumbleVersionHandshake(t, conn)
	writeAuthenticateMessage(t, conn, "denied-user", "")

	kind, payload, err := readUntilMessage(conn, 3*time.Second, mumbleproto.MessageReject)
	if err != nil {
		t.Fatalf("read reject: %v", err)
	}
	if kind != mumbleproto.MessageReject {
		t.Fatalf("expected Reject message, got %d", kind)
	}
	var reject mumbleproto.Reject
	if err := proto.Unmarshal(payload, &reject); err != nil {
		t.Fatalf("decode reject: %v", err)
	}
	if reject.GetType() != mumbleproto.Reject_WrongUserPW {
		t.Fatalf("expected wrong user password reject, got %v", reject.GetType())
	}
	if !stub.called {
		t.Fatal("expected Teamlancer authenticator to be called")
	}

	waitForCondition(t, 3*time.Second, func() bool {
		return server.clientCount() == 0
	}, "failed auth cleanup")
}

func TestSuccessfulTeamlancerAuthenticationReturnsIdentity(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()

	stub := &stubTeamlancerAuthenticator{
		result: &tlauth.Result{
			Identity: &tlauth.UserIdentity{
				UserID:      "user-42",
				DisplayName: "Alice TL",
				TeamID:      "team-7",
				BoardID:     "board-3",
				Permissions: tlauth.DefaultPermissions(),
			},
		},
	}
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	runtimeConfig.EnablePublicWebSocket = true
	server.teamlancerAuthenticator = stub

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	conn, _ := connectMumbleWebSocketClient(t, "ignored-by-stub")

	if !stub.called {
		t.Fatal("expected Teamlancer authenticator to be called")
	}

	waitForCondition(t, 3*time.Second, func() bool {
		return server.clientCount() == 1
	}, "successful teamlancer auth")

	client := server.clientsSnapshot()[0]
	if client.teamlancerIdentity == nil {
		t.Fatal("expected teamlancer identity to be attached to client")
	}
	if client.teamlancerIdentity.UserID != "user-42" {
		t.Fatalf("expected user id user-42, got %q", client.teamlancerIdentity.UserID)
	}
	if client.ShownName() != "Alice TL" {
		t.Fatalf("expected shown name Alice TL, got %q", client.ShownName())
	}

	closeMumbleClient(t, conn, server, 0)
}

func TestTeamlancerJoinVoiceDeniedRejectsAuthentication(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()

	stub := &stubTeamlancerAuthenticator{
		result: &tlauth.Result{
			Identity: &tlauth.UserIdentity{
				UserID:      "user-99",
				DisplayName: "Denied User",
				Permissions: tlauth.Permissions{JoinVoice: false, PublishAudio: true, ReceiveAudio: true},
			},
		},
	}
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	runtimeConfig.EnablePublicWebSocket = true
	server.teamlancerAuthenticator = stub

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	conn := openMumbleWebSocketClient(t)
	defer conn.Close()
	performMumbleVersionHandshake(t, conn)
	writeAuthenticateMessage(t, conn, "denied-by-guard", "")

	kind, payload, err := readUntilMessage(conn, 3*time.Second, mumbleproto.MessageReject)
	if err != nil {
		t.Fatalf("read reject: %v", err)
	}
	if kind != mumbleproto.MessageReject {
		t.Fatalf("expected Reject message, got %d", kind)
	}
	var reject mumbleproto.Reject
	if err := proto.Unmarshal(payload, &reject); err != nil {
		t.Fatalf("decode reject: %v", err)
	}
	if reject.GetType() != mumbleproto.Reject_WrongUserPW {
		t.Fatalf("expected wrong user password reject, got %v", reject.GetType())
	}

	waitForCondition(t, 3*time.Second, func() bool {
		return server.clientCount() == 0
	}, "guard denied auth cleanup")
}

func TestFailedTeamlancerAuthenticationDoesNotLeakSecretsInLogs(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()

	var logs bytes.Buffer
	server.Logger = log.New(&logs, "", 0)
	server.teamlancerAuthenticator = &stubTeamlancerAuthenticator{err: tlauth.ErrInvalidToken}
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	runtimeConfig.LogFormat = "json"
	runtimeConfig.EnablePublicWebSocket = true

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	conn := openMumbleWebSocketClient(t)
	defer conn.Close()
	performMumbleVersionHandshake(t, conn)
	password := "voice-token-sensitive"
	writeAuthenticateMessage(t, conn, "placeholder", password)

	if _, _, err := readUntilMessage(conn, 3*time.Second, mumbleproto.MessageReject); err != nil {
		t.Fatalf("read reject: %v", err)
	}

	got := logs.String()
	if strings.Contains(got, password) {
		t.Fatalf("log leaked token: %s", got)
	}
}

func TestLoadRuntimeConfigRejectsInvalidAuthMode(t *testing.T) {
	setValidRuntimeEnv(t)
	t.Setenv("TEAMLANCER_AUTH_MODE", "broken")

	_, err := LoadRuntimeConfig()
	if err == nil {
		t.Fatal("expected invalid auth mode to fail")
	}
	if !strings.Contains(err.Error(), "TEAMLANCER_AUTH_MODE") {
		t.Fatalf("expected auth mode validation error, got %v", err)
	}
}

func openMumbleWebSocketClient(t *testing.T) *websocket.Conn {
	t.Helper()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d%s", runtimeConfig.WebPort, runtimeConfig.WebSocketPath)
	dialer := websocket.Dialer{Subprotocols: []string{"mumble"}}
	header := http.Header{"Origin": []string{"https://teamlancer.work"}}
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func performMumbleVersionHandshake(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	if _, _, err := readOneMessage(conn); err != nil {
		t.Fatalf("read server version: %v", err)
	}
	if err := writeProtoMessage(conn, &mumbleproto.Version{
		Version:     proto.Uint32(0x10205),
		Release:     proto.String("auth-test"),
		CryptoModes: cryptstate.SupportedModes(),
		Os:          proto.String("test"),
		OsVersion:   proto.String("test"),
	}); err != nil {
		t.Fatalf("write client version: %v", err)
	}
}

func writeAuthenticateMessage(t *testing.T, conn *websocket.Conn, username, password string) {
	t.Helper()

	auth := &mumbleproto.Authenticate{
		Username:     proto.String(username),
		CeltVersions: []int32{CeltCompatBitstream},
		Opus:         proto.Bool(true),
	}
	if password != "" {
		auth.Password = proto.String(password)
	}

	if err := writeProtoMessage(conn, auth); err != nil {
		t.Fatalf("write authenticate: %v", err)
	}
}

func closeMumbleClient(t *testing.T, conn *websocket.Conn, server *Server, wantCount int) {
	t.Helper()

	_ = conn.Close()
	waitForCondition(t, 3*time.Second, func() bool {
		return server.clientCount() == wantCount
	}, "client disconnect cleanup")
}
