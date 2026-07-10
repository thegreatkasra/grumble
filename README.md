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
TEAMLANCER_AUTH_MODE=internal
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
ENABLE_PUBLIC_WEBSOCKET=true
TEAMLANCER_JWT_SECRET=replace-me
TEAMLANCER_JWT_ISSUER=teamlancer
TEAMLANCER_JWT_AUDIENCE=grumble-voice
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
  -e TEAMLANCER_AUTH_MODE=internal \
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
  -e ENABLE_PUBLIC_WEBSOCKET=true \
  -e TEAMLANCER_JWT_SECRET=replace-me \
  -e TEAMLANCER_JWT_ISSUER=teamlancer \
  -e TEAMLANCER_JWT_AUDIENCE=grumble-voice \
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
- Teamlancer mode now requires `TEAMLANCER_AUTH_MODE=internal`; startup fails otherwise.
- UDP remains intentionally disabled in Teamlancer mode.
- Horizontal scaling is not supported in this stage.

## Production Enablement

Required environment:

```env
TEAMLANCER_MODE=true
TEAMLANCER_AUTH_MODE=internal
ENABLE_PUBLIC_WEBSOCKET=true
```

Production behavior:

- Browser WebSocket sessions reject missing origin, invalid origin, missing JWT, invalid JWT, expired JWT, and denied room joins.
- Browser WebSocket sessions keep origin validation, connection limits, frame size limits, and ping/pong heartbeat enabled.
- Structured browser session logs emit `voice_ws_connected` with `user_id`, `board_id`, and `room_id`.
- Structured browser session rejects emit `voice_ws_rejected` with `reason` and `board_id` when available.
- Structured room lifecycle logs emit `voice_room_created`, `voice_room_joined`, `voice_room_left`, `voice_room_empty`, and `voice_room_destroyed`.
- `/ready` includes `voiceEngine`, `websocket`, and `auth` status fields in addition to the detailed check map.

Deployment checklist:

- Set `TEAMLANCER_MODE=true`
- Set `TEAMLANCER_AUTH_MODE=internal`
- Set `ENABLE_PUBLIC_WEBSOCKET=true`
- Set `TEAMLANCER_JWT_SECRET`, `TEAMLANCER_JWT_ISSUER`, and `TEAMLANCER_JWT_AUDIENCE`
- Ensure `ALLOWED_ORIGINS` includes every production browser origin
- Keep `ENABLE_UDP=false`
- Verify `/ready` returns `voiceEngine=ok`, `websocket=ok`, and `auth=ok`
- Verify browser clients use the backend-issued short-lived voice JWT, not the primary application JWT

## Teamlancer Voice Authentication

When `TEAMLANCER_MODE=true`, Grumble requires `TEAMLANCER_AUTH_MODE=internal` and validates a short-lived Teamlancer voice token instead of using legacy Grumble username/password or certificate authentication.

Authentication flow:

```text
Browser
  |
  | request voice access
  v
Teamlancer Backend
  |
  | short-lived Voice Token (JWT)
  v
Grumble Teamlancer Authenticator
  |
  | validated identity
  v
Grumble session
```

The browser does not send the primary Teamlancer application JWT directly to Grumble. The Teamlancer backend mints a dedicated short-lived voice token, and Grumble validates:

- JWT signature with `TEAMLANCER_JWT_SECRET`
- `exp` expiration
- `iss` issuer against `TEAMLANCER_JWT_ISSUER`
- `aud` audience against `TEAMLANCER_JWT_AUDIENCE`
- required claims: `sub`, `name`, `team_id`, `board_id`, `permissions`, `exp`

Optional claims are mapped into `pkg/teamlancer/auth.UserIdentity`:

- `team_id` -> `TeamID`
- `board_id` -> `BoardID`
- `permissions` -> `Permissions`

Permission model:

- `JoinVoice`
- `PublishAudio`
- `ReceiveAudio`
- `ModerateVoice`

Default permissions when the claim is omitted:

- `JoinVoice=true`
- `PublishAudio=true`
- `ReceiveAudio=true`
- `ModerateVoice=false`

JWT configuration for internal mode:

```env
TEAMLANCER_AUTH_MODE=internal
TEAMLANCER_JWT_SECRET=replace-me
TEAMLANCER_JWT_ISSUER=teamlancer
TEAMLANCER_JWT_AUDIENCE=grumble-voice
```

Failure handling:

- authentication failures emit `voice_auth_failed`
- browser session rejections emit `voice_ws_rejected`
- clients receive a generic authentication rejection
- JWT contents and secrets are never written to structured logs

### Backend Session Contract

The Teamlancer backend session endpoint is expected to mint the browser voice session returned from:

`POST /workspace/boards/:boardId/voice/session`

Expected response body:

```json
{
  "url": "wss://live.teamlancer.work/connect",
  "token": "short-lived-voice-jwt",
  "roomId": "team-7:board-3",
  "expiresAt": "2026-07-10T12:00:00Z"
}
```

Compatibility requirements with this engine:

- `url` must target the Grumble WebSocket endpoint at `/connect`
- `token` must be the short-lived Teamlancer voice JWT consumed by Grumble
- `roomId` must match the Grumble board channel mapping derived from `team_id` and `board_id`
- `expiresAt` must match the JWT `exp` lifetime envelope
- JWT claims consumed by Grumble are `sub`, `name`, `team_id`, `board_id`, `permissions`, and `exp`

## Teamlancer Voice Permissions

Permission flow:

```text
JWT Voice Token
        |
        v
JWT validation
        |
        v
UserIdentity
        |
        v
Permission guard
        |
        +--> JoinVoice -> session authentication
        +--> PublishAudio -> outgoing voice packets
        +--> ReceiveAudio -> voice fanout to listeners
        +--> ModerateVoice -> moderation hook foundation
```

Authentication:

- Teamlancer mints a short-lived voice JWT for Grumble.
- Grumble validates signature, issuer, audience, expiry, and required claims.
- A valid token is mapped into `pkg/teamlancer/auth.UserIdentity`.

Identity:

- `UserIdentity` carries `UserID`, `DisplayName`, `TeamID`, `BoardID`, and `Permissions`.
- Permission checks now read from the identity attached to the authenticated Grumble client.

Authorization:

- `JoinVoice` is enforced before a client session is finalized. Denied joins keep the existing generic auth failure behavior and emit `voice_auth_failed`.
- `PublishAudio` is enforced when a client sends voice. Denied packets are dropped, the client stays connected, and structured logs emit `voice_publish_denied` plus `voice_permission_denied`.
- `ReceiveAudio` is enforced during broadcast fanout. Denied listeners stay connected but do not receive forwarded audio, and structured logs emit `voice_permission_denied`.
- `ModerateVoice` now has guard hooks for future mute, move, and disconnect actions. Board-room-specific authorization is not implemented yet.

## Teamlancer Board Voice Rooms

Board voice routing now resolves authenticated Teamlancer users into an in-memory board room when `board_id` is present on the voice identity.

Architecture:

```text
WorkspaceBoard
        |
        v
BoardVoiceRoom
        |
        v
Grumble Channel
```

Behavior:

- `board_id` maps to exactly one active in-memory `VoiceRoom`.
- The Grumble channel name is deterministic: `teamlancer-board-{boardId}`.
- Users on the same board join the same Grumble channel.
- Different boards never share a Grumble channel.
- If `board_id` is absent, the existing root/default channel flow remains unchanged.
- Empty board rooms are removed automatically when the last participant leaves.

Lifecycle events:

- `voice_room_created`
- `voice_room_joined`
- `voice_room_left`
- `voice_room_empty`
- `voice_room_destroyed`

Structured room logs include `room_id`, `board_id`, `team_id`, and `user_id`. JWTs, tokens, and secrets are never logged.

## Board Voice Isolation

Board voice isolation is now enforced at runtime, not only during initial room assignment.

Flow:

```text
UserIdentity
     |
     v
BoardVoiceRoom
     |
     v
Channel
```

Rules:

- A Teamlancer user with `board_id` can only remain in or move back into the channel owned by that board room.
- Moves into other board channels or unrelated channels are denied when Teamlancer internal auth mode is active.
- Voice target setup and receive fanout both verify that sender and target resolve to the same board room.
- Cross-board audio routing is dropped and emits `voice_cross_board_access_denied`.
- Channel move denials emit `voice_channel_move_denied`.

Structured denial logs include `user_id`, `board_id`, `requested_channel`, and `reason`. JWTs, tokens, and secrets are never logged.
