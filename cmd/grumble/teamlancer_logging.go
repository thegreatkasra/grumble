package main

import (
	"crypto/tls"
	"encoding/json"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var globalConnectionSeq atomic.Uint64

type structuredLogEmitter func(level, event string, fields map[string]string)

type trackedConn struct {
	net.Conn
	connectionID string
	remoteIP     string
	listenerType string
}

func emitStructuredEvent(logger *log.Logger, level, event string, fields map[string]string) {
	if !runtimeConfig.TeamlancerMode || strings.ToLower(runtimeConfig.LogFormat) != "json" {
		return
	}
	if logger == nil {
		logger = log.New(os.Stdout, "", 0)
	}

	payload := map[string]string{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level,
		"event":     event,
	}
	for key, value := range fields {
		if value == "" {
			continue
		}
		payload[key] = value
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		logger.Printf("event=structured_log_error err=%q", err.Error())
		return
	}
	logger.Print(string(encoded))
}

func emitBootstrapEvent(level, event string, fields map[string]string) {
	if !shouldEmitBootstrapStructuredLogs() {
		return
	}
	logger := log.New(os.Stderr, "", 0)
	payload := map[string]string{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level,
		"event":     event,
	}
	for key, value := range fields {
		if value == "" {
			continue
		}
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		logger.Printf("event=structured_log_error err=%q", err.Error())
		return
	}
	logger.Print(string(encoded))
}

func shouldEmitBootstrapStructuredLogs() bool {
	if !strings.EqualFold(os.Getenv("TEAMLANCER_MODE"), "true") {
		return false
	}
	format := os.Getenv("LOG_FORMAT")
	return format == "" || strings.EqualFold(format, "json")
}

func nextConnectionID(prefix string) string {
	return prefix + "-" + strconv.FormatUint(globalConnectionSeq.Add(1), 10)
}

func hostFromRemoteAddr(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return hostFromAddr(addr)
}

func connectionMetadata(conn net.Conn) (connectionID, remoteIP, listenerType string) {
	if conn == nil {
		return "", "", ""
	}
	type metadata interface {
		ConnectionID() string
		RemoteIP() string
		ListenerType() string
	}
	if meta, ok := conn.(metadata); ok {
		return meta.ConnectionID(), meta.RemoteIP(), meta.ListenerType()
	}
	return "", hostFromRemoteAddr(conn.RemoteAddr()), ""
}

func (c *trackedConn) ConnectionID() string { return c.connectionID }
func (c *trackedConn) RemoteIP() string     { return c.remoteIP }
func (c *trackedConn) ListenerType() string { return c.listenerType }

func unwrapTLSConn(conn net.Conn) (*tls.Conn, bool) {
	switch c := conn.(type) {
	case *tls.Conn:
		return c, true
	case *trackedConn:
		return unwrapTLSConn(c.Conn)
	default:
		return nil, false
	}
}
