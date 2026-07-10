# Teamlancer Voice Engine — Stage V0 Architecture Audit

**Repository:** `grumble` (Teamlancer fork)  
**Date:** 2026-07-09  
**Scope:** Audit only — no production behavior changes  
**Target platform:** Hamravesh (`live.teamlancer.work`)  
**Constraint:** TCP-based deployment preferred; no external UDP dependency

---

## Executive Summary

Grumble is a Mumble-protocol voice server. This fork already includes a **Teamlancer runtime layer** (`TEAMLANCER_MODE=true`) that adapts the upstream architecture for browser-first, containerized deployment on Hamravesh:

- **Browser path:** `wss://live.teamlancer.work/connect` → reverse-proxy TLS termination → plain HTTP/WebSocket on container port `7880`
- **Voice transport:** Mumble `UDPTunnel` over the WebSocket/TCP control stream (UDP intentionally disabled)
- **Native Mumble path (optional):** TLS Mumble TCP on `64738`
- **Operational hooks:** `/health`, `/ready`, structured JSON logs, graceful shutdown (Unix), env-driven configuration

The fork is **not yet a fully Teamlancer-owned voice engine**. Significant Grumble legacy remains (file-based freezer persistence, Mumble channel/ACL model, public server registration, multi-server upstream design). Teamlancer-specific pieces (JWT auth, board voice rooms, permission guards) exist but are gated behind feature flags and incomplete in places.

This document maps current architecture, evaluates Hamravesh fit, identifies risks, and proposes a phased refactor plan without changing runtime behavior.

---

## 1. Repository Architecture

### 1.1 Layout

| Area | Path | Role |
|------|------|------|
| Binary entry | `cmd/grumble/grumble.go` | Process bootstrap, server load, signal handling |
| Core server | `cmd/grumble/server.go` | Listeners, client lifecycle, voice fanout, handler loop |
| Client protocol | `cmd/grumble/client.go` | Per-connection recv loops, UDPTunnel fallback |
| Messages / ACL enforcement | `cmd/grumble/message.go` | Mumble protobuf handlers |
| Channels | `cmd/grumble/channel.go` | Mumble channel tree |
| Persistence | `cmd/grumble/freeze.go` | Freezer snapshot + incremental log |
| Runtime config | `cmd/grumble/runtime_config.go` | Environment variable loading and validation |
| Runtime state | `cmd/grumble/runtime_state.go` | Health/readiness, WebSocket listener factory |
| Teamlancer auth | `cmd/grumble/teamlancer_auth.go` | JWT vs legacy auth routing |
| Teamlancer voice rooms | `cmd/grumble/teamlancer_voice.go` | Board → channel mapping |
| WebSocket adapter | `pkg/web/listener.go` | HTTP upgrade → `net.Conn` shim |
| Teamlancer packages | `pkg/teamlancer/auth/`, `pkg/teamlancer/voice/` | JWT validation, permissions, room manager |
| Mumble protocol | `pkg/mumbleproto/` | Protobuf definitions |
| ACL | `pkg/acl/` | Channel permission evaluation |
| Blob store | `pkg/blobstore/` | Content-addressed blobs (textures, descriptions) |
| Freezer | `pkg/freezer/` | On-disk protobuf persistence format |

### 1.2 Server Startup Flow

```text
main() [cmd/grumble/grumble.go]
  │
  ├─ flag.Parse()                         CLI: --datadir, --log, --regen-keys, --import-murmurdb
  ├─ LoadRuntimeConfig()                    Env vars → RuntimeConfig + Validate()
  ├─ VerifyDataDirWritable()                runtimeState.check["dataDirectory"] = "ok"
  ├─ Logging setup                          Teamlancer: stdout; legacy: log file
  ├─ blobstore.Open($DATA_DIR/blob)
  ├─ ensureRuntimeCertificate()             cert.pem + key.pem (if raw Mumble TCP enabled)
  │
  ├─ Scan $DATA_DIR/servers/<numeric-id>/
  │     NewServerFromFrozen()               Load main.fz + merge log.fz
  │     or NewServer(1) if none exist
  │
  ├─ Teamlancer guard: exactly one virtual server
  │
  └─ For each *Server:
        Server.Start()                      Bind listeners, start goroutines
        markRuntimeReady()                  readiness gate opens

  SignalHandler() [Unix only]               SIGINT/SIGTERM → Server.Stop()
  select {}                                 Block forever
```

**Per-server `Server.Start()`** (`cmd/grumble/server.go:1544`):

1. Resolve bind addresses/ports (Teamlancer overrides frozen per-server config)
2. Optionally bind UDP (`ENABLE_UDP`; forbidden when `TEAMLANCER_MODE=true`)
3. Optionally bind raw Mumble TLS TCP (`ENABLE_RAW_MUMBLE_TCP`)
4. Optionally bind HTTP listener + register `/health`, `/ready`, WebSocket path
5. `openFreezeLog()` — incremental persistence log
6. `initPerLaunchData()` — session pool, channels, `configureAuthenticator()`
7. `go handlerLoop()` — central synchronous event loop for control + voice
8. Launch network goroutines: `udpListenLoop`, `acceptLoop(tlsl)`, `acceptLoop(webwsl)`
9. Schedule `RegisterPublicServer()` after 1 minute (Mumble public list; only if configured)

### 1.3 Configuration Loading

Three layers, no YAML/JSON runtime config file:

| Layer | Source | Examples |
|-------|--------|----------|
| CLI flags | `cmd/grumble/args.go` | `--datadir`, `--log`, `--regen-keys` |
| Environment | `cmd/grumble/runtime_config.go` | `TEAMLANCER_MODE`, `WEB_PORT`, `ENABLE_UDP` |
| Per-server frozen state | `$DATA_DIR/servers/<id>/main.fz` | `Port`, `WebPort`, `ServerPassword`, registered users |

`DATA_DIR` env overrides `--datadir`. Teamlancer mode reads listener ports from env, not from frozen `Port`/`WebPort` keys.

### 1.4 Client Lifecycle

**States** (`cmd/grumble/server.go`):

```text
StateClientConnected
  → StateServerSentVersion
  → StateClientSentVersion
  → StateClientAuthenticated
  → StateClientReady
  → StateClientDead
```

**Connect path:**

1. `acceptLoop(listener)` accepts TLS TCP or WebSocket `net.Conn`
2. Ban check, `reserveConnection()` (Teamlancer connection limits)
3. `handleIncomingClient()` — allocate session, TLS handshake (raw TCP only), spawn `tlsRecvLoop` + `udpRecvLoop`
4. Version exchange → crypto mode negotiation (default `OCB2-AES128`)
5. `Authenticate` protobuf → async goroutine (`handleAuthenticate`)
6. On success: `CryptSetup`, channel resolution (`resolveAuthenticatedChannel`), `finishAuthenticate`, `ServerSync`
7. Voice: inbound via UDP or `UDPTunnel` → `udprecv` channel → `udpRecvLoop` → `handlerLoop`
8. Disconnect: `RemoveClient`, close `udprecv`, broadcast `UserRemove`, leave board voice room, release session

**Teamlancer connection limits** (`runtime_config.go`):

- `MAX_CONNECTIONS` (default 1000)
- `MAX_CONNECTIONS_PER_IP` (default 20)

### 1.5 Authentication Flow

| Mode | Trigger | Mechanism |
|------|---------|-----------|
| Legacy | `TEAMLANCER_AUTH_MODE=legacy` (default) | Mumble SuperUser password, registered-user cert hash, server password |
| Internal | `TEAMLANCER_AUTH_MODE=internal` | Short-lived JWT in `Authenticate.password` or `tokens` |

**Internal JWT flow** (`cmd/grumble/teamlancer_auth.go`, `pkg/teamlancer/auth/`):

1. Browser requests voice access from Teamlancer backend
2. Backend mints voice JWT (HS256)
3. Browser sends JWT during Mumble `Authenticate`
4. `InternalAuthenticator` validates signature, `exp`, `iss`, `aud`, `sub`, `name`
5. Claims map to `UserIdentity` (`team_id`, `board_id`, `permissions`)
6. `JoinVoice` permission required; failures emit `voice_auth_failed` (no secrets in logs)

**Limitations:**

- WebSocket clients cannot present TLS client certificates → legacy registered-user cert auth unavailable on browser path
- `ENABLE_PUBLIC_WEBSOCKET=false` by default — WebSocket upgrades return 503 until explicitly enabled

### 1.6 Persistence Layer

**Primary:** Freezer (protobuf files on disk)

| File | Purpose |
|------|---------|
| `$DATA_DIR/servers/<id>/main.fz` | Full server snapshot |
| `$DATA_DIR/servers/<id>/backup.fz` | Backup snapshot |
| `$DATA_DIR/servers/<id>/log.fz` | Incremental ops log (merged on load) |
| `$DATA_DIR/blob/` | SHA1-addressed blobs |
| `$DATA_DIR/cert.pem`, `key.pem` | Self-signed TLS for raw Mumble TCP |

**Persisted:** server config, channel tree, ACLs, groups, registered users, bans, blob references  
**Not persisted:** active connections, Teamlancer `VoiceRoomManager` state, JWT identities  
**SQLite:** Murmur import only (`cmd/grumble/murmurdb.go`), not a live backend

**Sync triggers:** every 100 config ops in `handlerLoop`; on `Server.Stop()`

### 1.7 TLS Handling

| Path | TLS termination | Certificate |
|------|-----------------|-------------|
| WebSocket (7880) | External reverse proxy (Hamravesh) | Container serves plain HTTP |
| Raw Mumble TCP (64738) | Server-side (`tls.NewListener`) | Self-signed `cert.pem` in data dir |
| Legacy Grumble web | Server-side (`ServeTLS`) | Same cert |

Client certificate extraction occurs only on raw TLS TCP (`server.go:316-340`). WebSocket path explicitly skips this.

---

## Runtime

### Current Listeners (Teamlancer Mode — Production Defaults)

| Port | Protocol | Bind | Purpose | Enabled By |
|------|----------|------|---------|------------|
| **7880** | HTTP | `WEB_BIND_ADDRESS` (`0.0.0.0`) | Liveness (`HEALTH_PATH=/health`) | `ENABLE_WEB=true` |
| **7880** | HTTP | same | Readiness (`READINESS_PATH=/ready`) | `ENABLE_WEB=true` |
| **7880** | WebSocket | same | Mumble control + voice tunnel (`WEBSOCKET_PATH=/connect`, subprotocol `mumble` or `binary`) | `ENABLE_WEB=true` + `ENABLE_PUBLIC_WEBSOCKET=true` |
| **64738** | TLS TCP (Mumble) | `RAW_MUMBLE_TCP_BIND_ADDRESS` | Native Mumble clients | `ENABLE_RAW_MUMBLE_TCP=true` |
| **64738** | UDP | same as Mumble TCP | Native UDP voice/ping | `ENABLE_UDP=true` (**rejected** when `TEAMLANCER_MODE=true`) |

### Current Listeners (Legacy Grumble Mode)

| Port | Protocol | Purpose |
|------|----------|---------|
| `64738 + (serverId - 1)` | TLS TCP + UDP | Per-virtual-server Mumble |
| `443 + (serverId - 1)` | HTTPS + WebSocket at `/` | Browser path with server TLS |

Constants: `DefaultPort = 64738`, `DefaultWebPort = 443` (`server.go:43-45`).

### Listener Implementation Map

| Listener | Created | Accept loop | Key file |
|----------|---------|-------------|----------|
| UDP | `net.ListenUDP` | `udpListenLoop()` | `server.go:1562-1570` |
| Raw Mumble TLS | `net.ListenTCP` → `tls.NewListener` | `acceptLoop(tlsl)` | `server.go:1577-1599` |
| HTTP + WS | `net.ListenTCP` → `http.Server` mux | `acceptLoop(webwsl)` | `server.go:1602-1654`, `pkg/web/listener.go` |

### Health and Readiness

- **`/health`** — always `200 {"status":"ok"}` if process is up (`runtime_state.go:97`)
- **`/ready`** — `200` when `runtimeState.IsReady()`; `503` with check snapshot otherwise

Readiness requires (Teamlancer mode):

- `dataDirectory: ok`
- `virtualServer: ok`
- `udp: disabled`
- `webListener: ok` (if `ENABLE_WEB`)
- `rawMumbleTcpListener: ok` (if `ENABLE_RAW_MUMBLE_TCP`)

### Graceful Shutdown

**Unix** (`signal_unix.go`): SIGINT/SIGTERM → `Server.Stop()` for all servers → `os.Exit(0)`

**Windows** (`signal_windows.go`): `SignalHandler()` is a no-op — no graceful shutdown.

**`Server.Stop()` sequence** (`server.go:1739`):

1. `runtimeState.MarkShuttingDown()` — readiness goes false
2. Signal `handlerLoop` via `server.bye`
3. Disconnect all clients
4. Close WebSocket listener, `webhttp.Shutdown` (timeout: `SHUTDOWN_TIMEOUT_SECONDS`, default 20s)
5. Close TCP/UDP listeners
6. `FreezeToFile()` — persist state
7. `netwg.Wait()` — drain network goroutines

### Structured Logging

Teamlancer mode logs to stdout with JSON events via `emitStructuredEvent` (`teamlancer_logging.go`). Key events: `runtime_starting`, `web_listener_started`, `websocket_accepted`, `voice_auth_failed`, `connection_closed`, `voice_room_*`.

Env: `LOG_LEVEL`, `LOG_FORMAT=json`.

---

## Browser Compatibility

### How Browser Clients Connect

```text
Browser (Teamlancer app)
  │
  │ HTTPS/WSS via live.teamlancer.work (Hamravesh TLS termination)
  v
Reverse proxy
  │
  │ Plain HTTP + WebSocket upgrade → container:7880/connect
  v
pkg/web/listener.go
  │  Upgrade to WebSocket (subprotocol: mumble | binary)
  │  Wrap as net.Conn
  v
acceptLoop(webwsl) → handleIncomingClient()
  │
  │ Standard Mumble protobuf handshake on the stream
  v
Authenticated session (StateClientReady)
```

### WebSocket Flow Details

1. **HTTP GET** to `WEBSOCKET_PATH` with `Upgrade: websocket`
2. **Origin validation** — `ALLOWED_ORIGINS` (+ `ALLOW_DEVELOPMENT_ORIGINS` for localhost)
3. **Subprotocol negotiation** — must offer `mumble` or `binary`
4. **Binary frames only** — text frames cause connection close
5. **Keepalive** — server ping loop; pong resets idle deadline (`WS_PING_INTERVAL_SECONDS`, `WS_IDLE_TIMEOUT_SECONDS`)
6. **Accept queue** — bounded channel (`WS_ACCEPT_QUEUE_SIZE`, default 128); overflow rejects connection

The WebSocket listener implements `net.Listener`; upgraded connections feed the same `acceptLoop` / `handleIncomingClient` path as native TLS clients.

### Is UDP Mandatory?

**No.** Mumble supports tunneling voice as `UDPTunnel` messages on the control connection.

```text
client.udp == false  →  SendUDP() calls sendMessage() (UDPTunnel over TCP/WS)
UDPTunnel received   →  client.udp = false; feed udprecv channel
```

In Teamlancer mode:

- `ENABLE_UDP=false` (default) and validated as incompatible with `TEAMLANCER_MODE=true`
- Browser clients always use WebSocket → always UDPTunnel path
- Readiness explicitly expects `udp: disabled`

### Fallback Behavior

| Scenario | Behavior |
|----------|----------|
| UDP disabled (Teamlancer default) | All voice via UDPTunnel on control stream |
| UDP enabled (legacy only) | First decrypted UDP packet sets `client.udp = true`; subsequent voice uses UDP |
| UDP enabled but `udpconn == nil` | `SendUDP` returns `ErrUDPDisabled` |
| WebSocket disabled (`ENABLE_PUBLIC_WEBSOCKET=false`) | Upgrade requests get HTTP 503 |
| Subprotocol mismatch | HTTP 400 |
| Origin not allowed | Upgrade rejected |
| Accept queue full | Connection closed with `websocket_rejected` log |

**Trade-off:** UDPTunnel over WebSocket adds latency and bandwidth overhead vs native UDP, but is required for browser and Hamravesh TCP-only constraints.

---

## Hamravesh Compatibility

### TCP-Only Deployment

**Fully supported** for the browser production path:

- Public ingress: HTTPS/WSS on `live.teamlancer.work` (443)
- Container exposes **7880/tcp** only for browser + health traffic
- Voice rides Mumble protocol over WebSocket (TCP)
- No UDP port mapping required

Raw Mumble TCP on 64738 is optional for native desktop clients; it is not required for browser voice.

### WebSocket Through HTTPS Domain

**Supported and verified in CI** (`.github/workflows/go.yml` docker smoke test):

```text
Client: wss://live.teamlancer.work/connect
Proxy:  TLS termination, forward to pod:7880/connect (HTTP + WS)
Server: plain HTTP, no container TLS on 7880
```

Requirements for Hamravesh ingress:

- WebSocket upgrade support on `/connect`
- Sticky sessions not required (single replica)
- Reasonable idle/proxy timeouts (≥ `WS_IDLE_TIMEOUT_SECONDS` = 90s default)
- `ALLOWED_ORIGINS` must include production app origins
- `TRUST_PROXY_HEADERS` exists but defaults `false` — evaluate if client IP limits should use `X-Forwarded-For`

### Required Exposed Ports

| Port | Required for browser voice? | Notes |
|------|----------------------------|-------|
| **7880/tcp** | **Yes** | HTTP health + WebSocket; primary production surface |
| **64738/tcp** | No (optional) | Native Mumble clients only |
| **64738/udp** | **No** | Disabled in Teamlancer mode; do not expose |

### Unnecessary Ports / Surfaces

| Surface | Recommendation |
|---------|----------------|
| UDP 64738 | Do not expose; forbidden in Teamlancer mode |
| Raw Mumble 64738/tcp | Disable (`ENABLE_RAW_MUMBLE_TCP=false`) if only browser clients are used — reduces attack surface |
| Mumble public registration | Disable unless intentionally listing on mumble.info (`register.go`) |
| Legacy multi-server ports | Not applicable — Teamlancer enforces single virtual server |
| Self-signed cert on 64738 | Not trusted by browsers; irrelevant for WSS path |

### Hamravesh Deployment Checklist (Current State)

- [x] Single-replica contract documented (`README.md`)
- [x] Non-root container user (`Dockerfile`)
- [x] Persistent volume at `/data`
- [x] `/health` and `/ready` for probes
- [x] Env-driven configuration (12-factor friendly)
- [x] UDP disabled by default
- [ ] `ENABLE_PUBLIC_WEBSOCKET=true` required for production browser traffic (currently false by default)
- [ ] `TEAMLANCER_AUTH_MODE=internal` + JWT secrets for production auth (currently legacy default)
- [ ] Windows graceful shutdown N/A for Linux containers

---

## Required Refactor Plan

Goal: transform the fork from a generic Mumble server into a **Teamlancer-owned voice engine** while preserving Mumble protocol compatibility for browsers.

### Teamlancer Voice Engine Mode — Target Design

#### Environment-Based Configuration

**Current:** Partial — `RuntimeConfig` covers listeners, auth mode, limits, WebSocket policy. Per-server state still in freezer files.

**Target:**

- Single `TEAMLANCER_VOICE_ENGINE=true` (or retain `TEAMLANCER_MODE`) as the primary gate
- All production knobs via env; frozen `Port`/`WebPort` ignored or migrated
- JWT config required when `TEAMLANCER_AUTH_MODE=internal`
- Documented env schema with validation at startup (extend `runtime_config.go`)
- Separate dev/staging/prod origin profiles

#### Health Endpoint

**Current:** `/health` returns static OK.

**Target:**

- Keep liveness lightweight (process up)
- Optionally include build version, uptime, `TEAMLANCER_MODE` (no secrets)

#### Readiness Endpoint

**Current:** Check map for data dir, listeners, virtual server, UDP disabled.

**Target extensions:**

- Authenticator configured (JWT secret present when internal auth)
- `ENABLE_PUBLIC_WEBSOCKET=true` in production profile
- Data directory writable + freezer load succeeded
- Not accepting new connections during shutdown (already via `MarkShuttingDown`)

#### Structured Logs

**Current:** JSON events for connection, auth, listener lifecycle.

**Target:**

- Consistent field schema: `timestamp`, `level`, `event`, `connection_id`, `user_id`, `board_id`, `room_id`
- Correlation ID from reverse proxy (if `TRUST_PROXY_HEADERS`)
- Metrics-friendly counters (connections active, rooms active, auth failures by reason)
- Log level filtering via `LOG_LEVEL`

#### Graceful Shutdown

**Current:** Unix signals only; 20s HTTP shutdown budget.

**Target:**

- Ensure Kubernetes/Hamravesh SIGTERM path matches Unix handler
- Readiness false immediately on shutdown start (already implemented)
- Drain WebSocket connections with timeout
- Optional preStop hook documentation for Hamravesh

#### Auth Integration Point

**Current:** `InternalAuthenticator` validates JWT from env; token minting is external (Teamlancer backend).

**Target:**

- Formal contract document for voice JWT claims (already in README; codify in `pkg/teamlancer/auth`)
- Hook for future JWKS / key rotation (today: shared `TEAMLANCER_JWT_SECRET`)
- Reject legacy auth in production profile
- Enable `ENABLE_PUBLIC_WEBSOCKET` only when internal auth is active

#### Room / Channel Abstraction

**Current:**

- Mumble channels (persistent, freezer-backed)
- Teamlancer `VoiceRoom` (in-memory, keyed by `board_id` → temporary channel `teamlancer-board-{boardId}`)

**Target:**

- Treat `VoiceRoom` as the Teamlancer-facing abstraction; Mumble channels as implementation detail
- API for room lifecycle events consumable by Teamlancer backend (webhooks or log streaming — not present today)
- Cleanup orphaned temporary channels on restart
- Board isolation policy fully enforced (`canReceiveFromClient`, `canMoveBoardScopedClient` — partially implemented)

### Recommended Implementation Phases

#### Phase 1 — Production Enablement (no protocol changes)

- Set production env profile: `ENABLE_PUBLIC_WEBSOCKET=true`, `TEAMLANCER_AUTH_MODE=internal`
- Hamravesh ingress for `wss://live.teamlancer.work/connect` → `:7880`
- Disable raw Mumble TCP if unused (`ENABLE_RAW_MUMBLE_TCP=false`)
- Disable public Mumble registration (ensure `RegisterName` unset)
- Document proxy timeout and origin requirements
- Validate JWT minting integration with Teamlancer backend

**Files:** deployment manifests (external), `README.md`, possibly `runtime_config.go` validation rules

#### Phase 2 — Operational Hardening

- Extend readiness checks (auth configured, websocket public in prod)
- Add build/version to health response
- Evaluate `TRUST_PROXY_HEADERS` for per-IP limits behind reverse proxy
- Metrics export (Prometheus endpoint or structured log aggregation)
- Orphan channel cleanup on startup

**Files:** `runtime_state.go`, `runtime_config.go`, `grumble.go`, `server.go`, `teamlancer_voice.go`

#### Phase 3 — Teamlancer API Surface

- HTTP admin API (room list, participant count, kick/mute) — **does not exist today**
- Event stream for room join/leave to Teamlancer backend
- Session revocation / token denylist (JWT is stateless today)

**Files:** new `cmd/grumble/http_admin.go` or `pkg/teamlancer/api/`, `server.go`, `teamlancer_voice.go`

#### Phase 4 — Persistence and Scale Posture

- Decide: continue freezer vs migrate room metadata to external store
- Explicit single-replica enforcement in orchestrator (already documented)
- If multi-replica ever needed: external room routing + shared state (major redesign)

**Files:** `freeze.go`, `pkg/teamlancer/voice/manager.go`, `grumble.go`

#### Phase 5 — Legacy Grumble Deprecation

- Remove or gate legacy multi-server mode
- Remove Murmur import from production image
- Strip unused UDP code paths in Teamlancer builds (build tags)
- Complete ACL permission sync or replace with Teamlancer permission model only

**Files:** `grumble.go`, `server.go`, `message.go`, `pkg/acl/`

---

## Risks and Limitations

### Persistence

| Risk | Severity | Detail |
|------|----------|--------|
| File-based freezer not designed for K8s multi-writer | High | Single replica required; PVC must be RWO |
| Board voice rooms not persisted | Medium | Restart loses in-memory room map; temporary channels may remain in freezer |
| Incremental log merge on load | Medium | Corrupt `log.fz` can block startup |
| No external DB | Medium | Cannot query users/rooms outside Mumble protocol |
| Self-signed TLS cert regeneration | Low | Raw Mumble clients may see cert changes |

### Missing APIs

| Gap | Impact |
|-----|--------|
| No REST/gRPC admin API | Cannot introspect or moderate rooms from Teamlancer backend without Mumble protocol |
| No webhook/event bus | Room lifecycle only in logs |
| No token revocation | Compromised JWT valid until `exp` |
| No participant list HTTP endpoint | App must infer from Mumble protocol or logs |
| `sendClientPermissions` is stubbed | Clients may not receive ACL bitmask (`server.go:927` — "fixme re-add ACL caching") |

### Authentication Limitations

| Risk | Detail |
|------|--------|
| `ENABLE_PUBLIC_WEBSOCKET=false` default | Production browser connections blocked until explicitly enabled |
| Legacy auth default | `TEAMLANCER_AUTH_MODE=legacy` allows Mumble password/cert auth if WebSocket is open |
| No WebSocket client certs | Registered-user cert auth unavailable for browsers |
| Shared secret JWT | No key rotation / JWKS support |
| Generic rejection messages | Good for security; limited client-side diagnostics |
| SuperUser password in frozen config | Legacy footgun if legacy auth remains enabled |

### Scalability Limitations

| Constraint | Detail |
|------------|--------|
| Single virtual server | Enforced in Teamlancer mode (`grumble.go:187-207`) |
| Single process | No horizontal scaling |
| UDPTunnel overhead | All browser voice on TCP stream — bandwidth and latency vs UDP |
| `handlerLoop` single goroutine | Control + voice fanout serialized — bottleneck at high concurrency |
| Connection limits | 1000 total / 20 per IP defaults — may need tuning |
| WebSocket accept queue | 128 — burst connections can be rejected |

### Security Concerns

| Concern | Detail |
|---------|--------|
| Plain HTTP on 7880 inside cluster | Acceptable if network policy restricts; TLS only at edge |
| Self-signed Mumble cert | Irrelevant for WSS; native clients must trust or ignore |
| Origin header validation | Required; misconfiguration blocks legitimate clients |
| `RegisterPublicServer` | Outbound call to `mumble.info` if public registration configured — likely unwanted |
| Ban list IP-based | Behind proxy, `RemoteAddr` may be proxy IP unless `TRUST_PROXY_HEADERS` implemented for bans/limits |
| OCB2-AES128 crypto | Legacy Mumble crypto; industry scrutiny on OCB mode — protocol constraint |
| JWT in Mumble password field | Standard for Mumble but visible in protocol framing — mitigated by short TTL |

---

## Files Likely to Need Modification (Future Phases)

### Core runtime and listeners

- `cmd/grumble/grumble.go` — startup, single-server policy, readiness timing
- `cmd/grumble/server.go` — listeners, shutdown, voice fanout, connection limits
- `cmd/grumble/client.go` — connection lifecycle, UDPTunnel path
- `cmd/grumble/runtime_config.go` — env schema, production validation
- `cmd/grumble/runtime_state.go` — health/readiness extensions
- `cmd/grumble/runtime_lifecycle.go` — ready marker hooks
- `cmd/grumble/runtime_startup.go` — certificate policy
- `cmd/grumble/signal_unix.go` / `signal_windows.go` — shutdown parity

### Teamlancer domain

- `cmd/grumble/teamlancer_auth.go` — auth integration, production guards
- `cmd/grumble/teamlancer_voice.go` — room/channel abstraction, cleanup
- `cmd/grumble/teamlancer_logging.go` — structured log schema
- `pkg/teamlancer/auth/auth.go` — authenticator interface
- `pkg/teamlancer/auth/jwt/jwt.go` — JWKS / rotation (future)
- `pkg/teamlancer/auth/guard/guard.go` — permission enforcement
- `pkg/teamlancer/auth/permissions.go` — permission model
- `pkg/teamlancer/voice/manager.go` — room lifecycle
- `pkg/teamlancer/voice/room.go` — room model
- `pkg/teamlancer/voice/policy.go` — isolation rules

### Web and protocol

- `pkg/web/listener.go` — WebSocket hardening, proxy awareness
- `cmd/grumble/message.go` — ACL vs Teamlancer permission unification
- `cmd/grumble/channel.go` — temporary channel cleanup

### Persistence

- `cmd/grumble/freeze.go` — snapshot scope, room metadata decision
- `pkg/freezer/` — schema extensions if room state is persisted

### Operations and packaging

- `Dockerfile` — expose ports, build metadata
- `README.md` — deployment contract
- `.github/workflows/go.yml` — CI gates for production profile
- Hamravesh/K8s manifests (external to repo) — ingress, probes, secrets

### Tests

- `cmd/grumble/runtime_http_test.go`
- `cmd/grumble/server_teamlancer_test.go`
- `cmd/grumble/teamlancer_auth_test.go`
- `cmd/grumble/teamlancer_integration_test.go`
- `pkg/teamlancer/auth/guard/guard_test.go`
- `pkg/teamlancer/voice/*_test.go`
- `pkg/web/listener_test.go`

### Possibly deprecated (Phase 5)

- `cmd/grumble/register.go` — public Mumble list registration
- `cmd/grumble/murmurdb.go` — Murmur SQLite import
- `pkg/acl/` — if Teamlancer permissions fully replace Mumble ACL for browser use

---

## Appendix A: Client State Machine

```text
                    ┌─────────────────────┐
                    │ StateClientConnected │
                    └──────────┬──────────┘
                               │ Version
                    ┌──────────▼──────────┐
                    │ StateServerSentVersion │
                    └──────────┬──────────┘
                               │ Version
                    ┌──────────▼──────────┐
                    │ StateClientSentVersion │
                    └──────────┬──────────┘
                               │ Authenticate
                    ┌──────────▼──────────────┐
                    │ StateClientAuthenticated │
                    └──────────┬──────────────┘
                               │ ServerSync
                    ┌──────────▼──────────┐
                    │  StateClientReady    │◄── voice (UDPTunnel/UDP)
                    └──────────┬──────────┘
                               │ Disconnect
                    ┌──────────▼──────────┐
                    │   StateClientDead    │
                    └─────────────────────┘
```

## Appendix B: Environment Variable Reference (Teamlancer)

| Variable | Default | Purpose |
|----------|---------|---------|
| `TEAMLANCER_MODE` | `false` | Enable Teamlancer runtime |
| `TEAMLANCER_AUTH_MODE` | `legacy` | `legacy` or `internal` |
| `TEAMLANCER_JWT_SECRET` | — | HS256 secret (internal auth) |
| `TEAMLANCER_JWT_ISSUER` | — | Expected `iss` claim |
| `TEAMLANCER_JWT_AUDIENCE` | — | Expected `aud` claim |
| `WEB_BIND_ADDRESS` | `0.0.0.0` | HTTP/WS bind |
| `WEB_PORT` | `7880` | HTTP/WS port |
| `ENABLE_WEB` | `true` | HTTP listener |
| `WEBSOCKET_PATH` | `/connect` | WS endpoint |
| `ENABLE_PUBLIC_WEBSOCKET` | `false` | Allow WS upgrades |
| `RAW_MUMBLE_TCP_BIND_ADDRESS` | `0.0.0.0` | Native Mumble bind |
| `RAW_MUMBLE_TCP_PORT` | `64738` | Native Mumble port |
| `ENABLE_RAW_MUMBLE_TCP` | `true` | Native Mumble listener |
| `ENABLE_UDP` | `false` | UDP voice (invalid in Teamlancer mode) |
| `HEALTH_PATH` | `/health` | Liveness path |
| `READINESS_PATH` | `/ready` | Readiness path |
| `DATA_DIR` | `~/.grumble` | Persistent data |
| `LOG_LEVEL` | `info` | Log level |
| `LOG_FORMAT` | `json` | Log format |
| `ALLOWED_ORIGINS` | teamlancer.work origins | WS origin allowlist |
| `ALLOW_DEVELOPMENT_ORIGINS` | `false` | Allow localhost origins |
| `MAX_CONNECTIONS` | `1000` | Global connection cap |
| `MAX_CONNECTIONS_PER_IP` | `20` | Per-IP connection cap |
| `SHUTDOWN_TIMEOUT_SECONDS` | `20` | HTTP shutdown budget |
| `TRUST_PROXY_HEADERS` | `false` | Proxy header trust |
| `WS_*` | various | WebSocket timeouts and limits |

## Appendix C: Audit Constraints Observed

This audit did **not**:

- Rewrite application code
- Add dependencies
- Change ports or protocol behavior
- Modify production configuration defaults in the repository

All findings reflect the codebase as inspected on 2026-07-09.
