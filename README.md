# Teamlancer Voice Engine

This repository now serves as the Teamlancer Voice Engine: a Teamlancer runtime built on Grumble for browser-to-Mumble voice transport.

## Upstream Grumble

Grumble is an implementation of a server for the Mumble voice chat system. It is an alternative to Murmur, the typical Mumble server.

The original upstream project and protocol behavior remain the foundation of this codebase, but this repository is operated with Teamlancer runtime constraints and CI gates.

## Teamlancer Runtime

Stage 1.3 verified behavior:

```text
Browser
  |
  | wss://live.teamlancer.work/connect
  v
Grumble Teamlancer Runtime
  |
  | Mumble protocol stream
  v
Voice Engine
```

Hamravesh terminates public TLS for `https://live.teamlancer.work` and `wss://live.teamlancer.work/connect`. The container does not terminate TLS on port `7880`. Raw Mumble on `64738` keeps its own TLS when enabled.

### Ports

`7880/tcp`

- HTTP
- `/health`
- `/ready`
- WebSocket endpoint at `/connect`

`64738/tcp`

- Raw Mumble TCP

`UDP`

- disabled

### Runtime environment

Production example:

```env
TEAMLANCER_MODE=true
WEB_BIND_ADDRESS=0.0.0.0
WEB_PORT=7880
ENABLE_WEB=true
WEBSOCKET_PATH=/connect
RAW_MUMBLE_TCP_BIND_ADDRESS=0.0.0.0
RAW_MUMBLE_TCP_PORT=64738
ENABLE_RAW_MUMBLE_TCP=true
ENABLE_UDP=false
HEALTH_PATH=/health
READINESS_PATH=/ready
DATA_DIR=/data
LOG_LEVEL=info
LOG_FORMAT=json
ALLOWED_ORIGINS=https://teamlancer.work,https://app.teamlancer.work
ENABLE_PUBLIC_WEBSOCKET=false
```

Development origin example:

```env
ALLOWED_ORIGINS=https://teamlancer.work,https://app.teamlancer.work,http://localhost:3000
ALLOW_DEVELOPMENT_ORIGINS=true
```

### Docker

Build:

```sh
docker build -t teamlancer-voice-engine .
```

Run:

```sh
docker run \
  -v $HOME/.grumble:/data \
  -p 7880:7880 \
  -p 64738:64738 \
  -e TEAMLANCER_MODE=true \
  -e WEB_BIND_ADDRESS=0.0.0.0 \
  -e WEB_PORT=7880 \
  -e ENABLE_WEB=true \
  -e WEBSOCKET_PATH=/connect \
  -e RAW_MUMBLE_TCP_BIND_ADDRESS=0.0.0.0 \
  -e RAW_MUMBLE_TCP_PORT=64738 \
  -e ENABLE_RAW_MUMBLE_TCP=true \
  -e ENABLE_UDP=false \
  -e HEALTH_PATH=/health \
  -e READINESS_PATH=/ready \
  -e DATA_DIR=/data \
  -e LOG_LEVEL=info \
  -e LOG_FORMAT=json \
  -e ALLOWED_ORIGINS=https://teamlancer.work,https://app.teamlancer.work \
  -e ENABLE_PUBLIC_WEBSOCKET=false \
  teamlancer-voice-engine
```

Verified container behavior:

- image builds successfully with `docker build .`
- runtime user is non-root
- `/data` is writable
- `/health` returns `200`
- `/ready` returns `200`

### Deployment contract

- Repository: `grumble`
- Runtime role: `Teamlancer Voice Engine`
- Domain: `live.teamlancer.work`
- Public WebSocket: `wss://live.teamlancer.work/connect`
- Replica count: `1`

Notes:

- External infrastructure port mappings must not be hard-coded.
- Browser audio remains on Mumble `UDPTunnel` over the stream path.
- Public WebSocket remains disabled by default until Stage 2 authentication exists.
- UDP remains intentionally disabled in Teamlancer mode.
- Horizontal scaling is not supported in this stage.
