package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"mumble.info/grumble/pkg/cryptstate"
	"mumble.info/grumble/pkg/mumbleproto"
	tlauth "mumble.info/grumble/pkg/teamlancer/auth"
	tlvoice "mumble.info/grumble/pkg/teamlancer/voice"
)

func TestNoUDPWebSocketVoiceTunnelIntegration(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	if server.udpconn != nil {
		t.Fatal("expected udp listener to remain disabled")
	}
	if runtimeState.checks["udp"] != "disabled" {
		t.Fatalf("expected udp readiness check to be disabled, got %q", runtimeState.checks["udp"])
	}

	udpProbe, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", runtimeConfig.RawMumbleTCPPort))
	if err != nil {
		t.Fatalf("expected udp port to remain unbound: %v", err)
	}
	_ = udpProbe.Close()

	clientA, sessionA := connectMumbleWebSocketClient(t, "alice")
	clientB, _ := connectMumbleWebSocketClient(t, "bob")

	waitForCondition(t, 3*time.Second, func() bool {
		return server.totalConns.Load() == 2
	}, "two authenticated clients")

	payload := []byte{
		byte(mumbleproto.UDPMessageVoiceOpus << 5),
		0, 0, 0, 1,
		0, 1,
		0x7f,
	}
	if err := writeFramedMessage(clientA, mumbleproto.MessageUDPTunnel, payload); err != nil {
		t.Fatalf("send voice tunnel: %v", err)
	}

	kind, got, err := readUntilMessage(clientB, 3*time.Second, mumbleproto.MessageUDPTunnel)
	if err != nil {
		t.Fatalf("read forwarded voice tunnel: %v", err)
	}
	if kind != mumbleproto.MessageUDPTunnel {
		t.Fatalf("expected UDPTunnel, got %d", kind)
	}
	if len(got) < 5 {
		t.Fatalf("expected forwarded tunnel payload, got %v", got)
	}
	if got[0] != payload[0] {
		t.Fatalf("expected forwarded voice kind byte %d, got %d", payload[0], got[0])
	}

	if got[1] != byte(sessionA) {
		t.Fatalf("expected forwarded payload to contain sender session byte %d, got %d", sessionA, got[1])
	}
	if !bytes.Equal(got[2:], payload[1:]) {
		t.Fatalf("expected forwarded payload tail %v, got %v", payload[1:], got[2:])
	}

	_ = clientA.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = clientA.Close()
	waitForCondition(t, 3*time.Second, func() bool {
		return server.totalConns.Load() == 1
	}, "first client disconnect cleanup")

	_ = clientB.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = clientB.Close()
	waitForCondition(t, 3*time.Second, func() bool {
		return server.totalConns.Load() == 0
	}, "client disconnect cleanup")
}

func TestTeamlancerPublishAudioAllowed(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	server.teamlancerAuthenticator = &routingTeamlancerAuthenticator{
		results: map[string]*tlauth.Result{
			"alice": {Identity: teamlancerIdentity("user-alice", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
			"bob":   {Identity: teamlancerIdentity("user-bob", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
		},
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	clientA, sessionA := connectMumbleWebSocketClient(t, "alice")
	clientB, _ := connectMumbleWebSocketClient(t, "bob")

	sendVoiceTunnel(t, clientA)
	assertReceivesVoiceFrom(t, clientB, sessionA)

	closeMumbleClient(t, clientA, server, 1)
	closeMumbleClient(t, clientB, server, 0)
}

func TestTeamlancerSameBoardUsersJoinSameRoom(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	server.teamlancerAuthenticator = &routingTeamlancerAuthenticator{
		results: map[string]*tlauth.Result{
			"alice": {Identity: teamlancerBoardIdentity("user-alice", "team-1", "board-1", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
			"bob":   {Identity: teamlancerBoardIdentity("user-bob", "team-1", "board-1", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
		},
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	clientA, _ := connectMumbleWebSocketClient(t, "alice")
	clientB, _ := connectMumbleWebSocketClient(t, "bob")

	waitForCondition(t, 3*time.Second, func() bool {
		return server.clientCount() == 2
	}, "board room clients")

	clients := server.clientsSnapshot()
	if len(clients) != 2 {
		t.Fatalf("expected two clients, got %d", len(clients))
	}
	channelA := clients[0].Channel
	channelB := clients[1].Channel
	if channelA == nil || channelB == nil {
		t.Fatal("expected both clients to have a channel")
	}
	if channelA.Id != channelB.Id {
		t.Fatalf("expected same board users in same channel, got %d and %d", channelA.Id, channelB.Id)
	}
	if channelA.Id == server.RootChannel().Id {
		t.Fatal("expected board users to avoid root channel")
	}
	if channelA.Name != "teamlancer-board-board-1" {
		t.Fatalf("expected deterministic room name, got %q", channelA.Name)
	}

	room, ok := server.teamlancerVoiceRooms.Find("board-1")
	if !ok {
		t.Fatal("expected board room to be tracked")
	}
	if room.ChannelID != channelA.Id {
		t.Fatalf("expected room channel id %d, got %d", channelA.Id, room.ChannelID)
	}
	if room.ParticipantCount() != 2 {
		t.Fatalf("expected two room participants, got %d", room.ParticipantCount())
	}

	closeMumbleClient(t, clientA, server, 1)
	closeMumbleClient(t, clientB, server, 0)
}

func TestTeamlancerDifferentBoardsCreateDifferentRooms(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	server.teamlancerAuthenticator = &routingTeamlancerAuthenticator{
		results: map[string]*tlauth.Result{
			"alice": {Identity: teamlancerBoardIdentity("user-alice", "team-1", "board-1", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
			"bob":   {Identity: teamlancerBoardIdentity("user-bob", "team-1", "board-2", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
		},
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	clientA, _ := connectMumbleWebSocketClient(t, "alice")
	clientB, _ := connectMumbleWebSocketClient(t, "bob")

	waitForCondition(t, 3*time.Second, func() bool {
		return server.clientCount() == 2
	}, "isolated board room clients")

	clients := server.clientsSnapshot()
	if len(clients) != 2 {
		t.Fatalf("expected two clients, got %d", len(clients))
	}
	channelIDs := []int{clients[0].Channel.Id, clients[1].Channel.Id}
	if channelIDs[0] == channelIDs[1] {
		t.Fatalf("expected different boards in different channels, got %d", channelIDs[0])
	}
	names := []string{clients[0].Channel.Name, clients[1].Channel.Name}
	slices.Sort(names)
	if names[0] != "teamlancer-board-board-1" || names[1] != "teamlancer-board-board-2" {
		t.Fatalf("expected board channel names, got %v", names)
	}

	closeMumbleClient(t, clientA, server, 1)
	closeMumbleClient(t, clientB, server, 0)
}

func TestTeamlancerBoardUserCannotMoveToAnotherBoard(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal

	server.teamlancerVoiceRooms = tlvoice.NewVoiceRoomManager()

	board1 := server.AddChannel("board-1")
	board2 := server.AddChannel("board-2")
	server.RootChannel().AddChild(board1)
	server.RootChannel().AddChild(board2)

	aliceIdentity := teamlancerBoardIdentity("user-alice", "team-1", "board-1", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})
	if _, _, err := server.teamlancerVoiceRooms.Join(aliceIdentity, board1.Id); err != nil {
		t.Fatalf("join board-1 room: %v", err)
	}

	alice := &Client{server: server, Channel: board1, teamlancerIdentity: aliceIdentity}
	if server.canMoveBoardScopedClient(alice, board2) {
		t.Fatal("expected cross-board move to be denied")
	}
}

func TestTeamlancerBoardUserCanStayInOwnRoom(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal

	server.teamlancerVoiceRooms = tlvoice.NewVoiceRoomManager()

	board1 := server.AddChannel("board-1")
	server.RootChannel().AddChild(board1)

	aliceIdentity := teamlancerBoardIdentity("user-alice", "team-1", "board-1", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})
	if _, _, err := server.teamlancerVoiceRooms.Join(aliceIdentity, board1.Id); err != nil {
		t.Fatalf("join board-1 room: %v", err)
	}

	alice := &Client{server: server, Channel: board1, teamlancerIdentity: aliceIdentity}
	if !server.canMoveBoardScopedClient(alice, board1) {
		t.Fatal("expected same-board room to remain allowed")
	}
}

func TestTeamlancerPublishAudioBlocked(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal

	var logs bytes.Buffer
	server.Logger = log.New(&logs, "", 0)
	server.teamlancerAuthenticator = &routingTeamlancerAuthenticator{
		results: map[string]*tlauth.Result{
			"alice": {Identity: teamlancerIdentity("user-alice", tlauth.Permissions{JoinVoice: true, PublishAudio: false, ReceiveAudio: true})},
			"bob":   {Identity: teamlancerIdentity("user-bob", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
		},
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	clientA, _ := connectMumbleWebSocketClient(t, "alice")
	clientB, _ := connectMumbleWebSocketClient(t, "bob")

	sendVoiceTunnel(t, clientA)
	assertNoMessageOfKindWithin(t, clientB, 500*time.Millisecond, mumbleproto.MessageUDPTunnel)

	events := lifecycleEventsFromBuffer(t, logs.String())
	assertEventPresent(t, events, "voice_publish_denied")
	assertEventPresent(t, events, "voice_permission_denied")
	if got := eventField(t, events, "voice_permission_denied", "user_id"); got != "user-alice" {
		t.Fatalf("expected user_id user-alice, got %q", got)
	}
	if got := eventField(t, events, "voice_permission_denied", "permission"); got != "voice.publish" {
		t.Fatalf("expected voice.publish permission, got %q", got)
	}
	if got := eventField(t, events, "voice_permission_denied", "action"); got != "publish" {
		t.Fatalf("expected publish action, got %q", got)
	}

	closeMumbleClient(t, clientA, server, 1)
	closeMumbleClient(t, clientB, server, 0)
}

func TestTeamlancerReceiveAudioAllowed(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	server.teamlancerAuthenticator = &routingTeamlancerAuthenticator{
		results: map[string]*tlauth.Result{
			"alice": {Identity: teamlancerIdentity("user-alice", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
			"bob":   {Identity: teamlancerIdentity("user-bob", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
		},
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	clientA, sessionA := connectMumbleWebSocketClient(t, "alice")
	clientB, _ := connectMumbleWebSocketClient(t, "bob")

	sendVoiceTunnel(t, clientA)
	assertReceivesVoiceFrom(t, clientB, sessionA)

	closeMumbleClient(t, clientA, server, 1)
	closeMumbleClient(t, clientB, server, 0)
}

func TestTeamlancerReceiveAudioBlocked(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	server.teamlancerAuthenticator = &routingTeamlancerAuthenticator{
		results: map[string]*tlauth.Result{
			"alice": {Identity: teamlancerIdentity("user-alice", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
			"bob":   {Identity: teamlancerIdentity("user-bob", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: false})},
		},
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	clientA, _ := connectMumbleWebSocketClient(t, "alice")
	clientB, _ := connectMumbleWebSocketClient(t, "bob")

	sendVoiceTunnel(t, clientA)
	assertNoMessageOfKindWithin(t, clientB, 500*time.Millisecond, mumbleproto.MessageUDPTunnel)

	closeMumbleClient(t, clientA, server, 1)
	closeMumbleClient(t, clientB, server, 0)
}

func TestTeamlancerCrossBoardReceiveDenied(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal

	server.teamlancerVoiceRooms = tlvoice.NewVoiceRoomManager()

	board1 := server.AddChannel("board-1")
	board2 := server.AddChannel("board-2")
	server.RootChannel().AddChild(board1)
	server.RootChannel().AddChild(board2)

	aliceIdentity := teamlancerBoardIdentity("user-alice", "team-1", "board-1", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})
	bobIdentity := teamlancerBoardIdentity("user-bob", "team-1", "board-2", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})
	if _, _, err := server.teamlancerVoiceRooms.Join(aliceIdentity, board1.Id); err != nil {
		t.Fatalf("join board-1 room: %v", err)
	}
	if _, _, err := server.teamlancerVoiceRooms.Join(bobIdentity, board2.Id); err != nil {
		t.Fatalf("join board-2 room: %v", err)
	}

	alice := &Client{server: server, Channel: board1, teamlancerIdentity: aliceIdentity}
	bob := &Client{server: server, Channel: board1, teamlancerIdentity: bobIdentity}
	if server.canReceiveFromClient(bob, alice) {
		t.Fatal("expected cross-board receive to be denied")
	}
}

func TestTeamlancerVoiceTargetCrossBoardDenied(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal

	var logs bytes.Buffer
	server.Logger = log.New(&logs, "", 0)
	server.teamlancerAuthenticator = &routingTeamlancerAuthenticator{
		results: map[string]*tlauth.Result{
			"alice": {Identity: teamlancerBoardIdentity("user-alice", "team-1", "board-1", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
			"bob":   {Identity: teamlancerBoardIdentity("user-bob", "team-1", "board-2", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
		},
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	clientA, _ := connectMumbleWebSocketClient(t, "alice")
	clientB, sessionB := connectMumbleWebSocketClient(t, "bob")

	if err := writeProtoMessage(clientA, &mumbleproto.VoiceTarget{
		Id: proto.Uint32(1),
		Targets: []*mumbleproto.VoiceTarget_Target{{
			Session: []uint32{sessionB},
		}},
	}); err != nil {
		t.Fatalf("write voice target: %v", err)
	}

	sendVoiceTunnelToTarget(t, clientA, 1)
	assertNoMessageOfKindWithin(t, clientB, 500*time.Millisecond, mumbleproto.MessageUDPTunnel)

	events := lifecycleEventsFromBuffer(t, logs.String())
	assertEventPresent(t, events, "voice_cross_board_access_denied")

	closeMumbleClient(t, clientA, server, 1)
	closeMumbleClient(t, clientB, server, 0)
}

func TestTeamlancerBoardRoomCleanupWhenEmpty(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal

	var logs bytes.Buffer
	server.Logger = log.New(&logs, "", 0)
	server.teamlancerAuthenticator = &routingTeamlancerAuthenticator{
		results: map[string]*tlauth.Result{
			"alice": {Identity: teamlancerBoardIdentity("user-alice", "team-1", "board-cleanup", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
		},
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	client, _ := connectMumbleWebSocketClient(t, "alice")

	waitForCondition(t, 3*time.Second, func() bool {
		room, ok := server.teamlancerVoiceRooms.Find("board-cleanup")
		return ok && room.ParticipantCount() == 1
	}, "board room creation")

	room, ok := server.teamlancerVoiceRooms.Find("board-cleanup")
	if !ok {
		t.Fatal("expected cleanup room to exist before disconnect")
	}
	channelID := room.ChannelID

	closeMumbleClient(t, client, server, 0)

	waitForCondition(t, 3*time.Second, func() bool {
		_, ok := server.teamlancerVoiceRooms.Find("board-cleanup")
		_, exists := server.Channels[channelID]
		return !ok && !exists
	}, "board room cleanup")

	events := lifecycleEventsFromBuffer(t, logs.String())
	assertEventPresent(t, events, "voice_room_created")
	assertEventPresent(t, events, "voice_room_joined")
	assertEventPresent(t, events, "voice_room_left")
	assertEventPresent(t, events, "voice_room_empty")
	assertEventPresent(t, events, "voice_room_destroyed")
	if got := eventField(t, events, "voice_room_destroyed", "board_id"); got != "board-cleanup" {
		t.Fatalf("expected cleanup board id, got %q", got)
	}
}

func TestInternalModeUserWithoutBoardScopeCanMoveChannels(t *testing.T) {
	server, cleanup := newTeamlancerTestServer(t, true)
	defer cleanup()
	runtimeConfig.EnablePublicWebSocket = true
	runtimeConfig.TeamlancerAuthMode = tlauth.ModeInternal
	server.teamlancerAuthenticator = &routingTeamlancerAuthenticator{
		results: map[string]*tlauth.Result{
			"roomless-user": {Identity: teamlancerIdentity("roomless-user", tlauth.Permissions{JoinVoice: true, PublishAudio: true, ReceiveAudio: true})},
		},
	}

	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer func() {
		if err := server.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
	}()

	client, session := connectMumbleWebSocketClient(t, "roomless-user")
	channel := server.AddChannel("movable-temp")
	server.RootChannel().AddChild(channel)

	if err := writeProtoMessage(client, &mumbleproto.UserState{
		Session:   proto.Uint32(session),
		ChannelId: proto.Uint32(uint32(channel.Id)),
	}); err != nil {
		t.Fatalf("write legacy move: %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		return findClientBySession(t, server, session).Channel.Id == channel.Id
	}, "roomless move into temp channel")

	closeMumbleClient(t, client, server, 0)
}

func connectMumbleWebSocketClient(t *testing.T, username string) (*websocket.Conn, uint32) {
	t.Helper()

	wsURL := fmt.Sprintf("ws://127.0.0.1:%d%s", runtimeConfig.WebPort, runtimeConfig.WebSocketPath)
	dialer := websocket.Dialer{Subprotocols: []string{"mumble"}}
	header := http.Header{"Origin": []string{"https://teamlancer.work"}}
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}

	if _, _, err := readOneMessage(conn); err != nil {
		t.Fatalf("read server version: %v", err)
	}
	if err := writeProtoMessage(conn, &mumbleproto.Version{
		Version:     proto.Uint32(0x10205),
		Release:     proto.String("integration-test"),
		CryptoModes: cryptstate.SupportedModes(),
		Os:          proto.String("test"),
		OsVersion:   proto.String("test"),
	}); err != nil {
		t.Fatalf("write client version: %v", err)
	}
	if err := writeProtoMessage(conn, &mumbleproto.Authenticate{
		Username:     proto.String(username),
		Password:     proto.String(testVoiceJWT(t, time.Now().Add(time.Minute), "board-default")),
		CeltVersions: []int32{CeltCompatBitstream},
		Opus:         proto.Bool(true),
	}); err != nil {
		t.Fatalf("write authenticate: %v", err)
	}

	session := waitForServerSync(t, conn)
	return conn, session
}

func teamlancerIdentity(userID string, permissions tlauth.Permissions) *tlauth.UserIdentity {
	return &tlauth.UserIdentity{
		UserID:      userID,
		DisplayName: userID,
		TeamID:      "team-1",
		Permissions: permissions,
	}
}

func teamlancerBoardIdentity(userID, teamID, boardID string, permissions tlauth.Permissions) *tlauth.UserIdentity {
	return &tlauth.UserIdentity{
		UserID:      userID,
		DisplayName: userID,
		TeamID:      teamID,
		BoardID:     boardID,
		Permissions: permissions,
	}
}

func waitForServerSync(t *testing.T, conn *websocket.Conn) uint32 {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var session uint32
	for time.Now().Before(deadline) {
		kind, payload, err := readOneMessage(conn)
		if err != nil {
			t.Fatalf("read post-auth message: %v", err)
		}
		if kind == mumbleproto.MessageServerSync {
			var sync mumbleproto.ServerSync
			if err := proto.Unmarshal(payload, &sync); err != nil {
				t.Fatalf("decode server sync: %v", err)
			}
			session = sync.GetSession()
		}
		if kind == mumbleproto.MessageServerConfig {
			return session
		}
	}
	t.Fatal("timed out waiting for server config")
	return 0
}

func writeProtoMessage(conn *websocket.Conn, msg proto.Message) error {
	payload, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return writeFramedMessage(conn, mumbleproto.MessageType(msg), payload)
}

func writeFramedMessage(conn *websocket.Conn, kind uint16, payload []byte) error {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, kind); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.BigEndian, uint32(len(payload))); err != nil {
		return err
	}
	if _, err := buf.Write(payload); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, buf.Bytes())
}

func sendVoiceTunnel(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	payload := []byte{
		byte(mumbleproto.UDPMessageVoiceOpus << 5),
		0, 0, 0, 1,
		0, 1,
		0x7f,
	}
	if err := writeFramedMessage(conn, mumbleproto.MessageUDPTunnel, payload); err != nil {
		t.Fatalf("send voice tunnel: %v", err)
	}
}

func sendVoiceTunnelToTarget(t *testing.T, conn *websocket.Conn, target byte) {
	t.Helper()
	payload := []byte{
		byte(mumbleproto.UDPMessageVoiceOpus<<5) | target,
		0, 0, 0, 1,
		0, 1,
		0x7f,
	}
	if err := writeFramedMessage(conn, mumbleproto.MessageUDPTunnel, payload); err != nil {
		t.Fatalf("send voice tunnel: %v", err)
	}
}

func assertReceivesVoiceFrom(t *testing.T, conn *websocket.Conn, session uint32) {
	t.Helper()
	kind, got, err := readUntilMessage(conn, 3*time.Second, mumbleproto.MessageUDPTunnel)
	if err != nil {
		t.Fatalf("read forwarded voice tunnel: %v", err)
	}
	if kind != mumbleproto.MessageUDPTunnel {
		t.Fatalf("expected UDPTunnel, got %d", kind)
	}
	if len(got) < 5 {
		t.Fatalf("expected forwarded tunnel payload, got %v", got)
	}
	if got[1] != byte(session) {
		t.Fatalf("expected forwarded payload to contain sender session byte %d, got %d", session, got[1])
	}
}

func assertNoMessageOfKindWithin(t *testing.T, conn *websocket.Conn, timeout time.Duration, want uint16) {
	t.Helper()
	defer func() {
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			t.Fatalf("clear read deadline: %v", err)
		}
	}()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, frame, err := conn.ReadMessage()
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return
		}
		if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			t.Fatalf("connection closed while expecting it to stay alive: %v", err)
		}
		if strings.Contains(err.Error(), "i/o timeout") {
			return
		}
		t.Fatalf("unexpected read error: %v", err)
	}
	reader := bytes.NewReader(frame)
	var kind uint16
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &kind); err != nil {
		t.Fatalf("decode kind: %v", err)
	}
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		t.Fatalf("decode length: %v", err)
	}
	if kind == want {
		t.Fatalf("unexpected message kind %d", want)
	}
}

func readUntilMessage(conn *websocket.Conn, timeout time.Duration, want uint16) (uint16, []byte, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		kind, payload, err := readOneMessage(conn)
		if err != nil {
			return 0, nil, err
		}
		if kind == want {
			return kind, payload, nil
		}
	}
	return 0, nil, fmt.Errorf("timed out waiting for message %d", want)
}

func readOneMessage(conn *websocket.Conn) (uint16, []byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return 0, nil, err
	}
	_, frame, err := conn.ReadMessage()
	if err != nil {
		return 0, nil, err
	}
	reader := bytes.NewReader(frame)
	var kind uint16
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &kind); err != nil {
		return 0, nil, err
	}
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	return kind, payload, nil
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", strings.TrimSpace(description))
}

func findClientBySession(t *testing.T, server *Server, session uint32) *Client {
	t.Helper()
	client, ok := server.getClient(session)
	if !ok {
		t.Fatalf("expected client session %d", session)
	}
	return client
}
