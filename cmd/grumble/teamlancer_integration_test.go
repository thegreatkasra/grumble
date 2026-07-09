package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"mumble.info/grumble/pkg/cryptstate"
	"mumble.info/grumble/pkg/mumbleproto"
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
	defer clientA.Close()
	clientB, _ := connectMumbleWebSocketClient(t, "bob")
	defer clientB.Close()

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
		CeltVersions: []int32{CeltCompatBitstream},
		Opus:         proto.Bool(true),
	}); err != nil {
		t.Fatalf("write authenticate: %v", err)
	}

	session := waitForServerSync(t, conn)
	return conn, session
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
