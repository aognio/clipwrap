# WebSocket Protocol

## Overview

Authenticated clients connect via WebSocket to receive real-time clipboard events.

## Connection

```
GET /api/v1/events/ws
```

### Authentication

- **Browser clients**: Session cookie from login
- **Machine clients**: Bearer token in `Authorization` header

```
Authorization: Bearer <service-token>
```

### Replay Cursor (Optional)

```
GET /api/v1/events/ws?cursor=instance-id:42
```

## Messages

### Event Message

```json
{
  "id": "instance-id:42",
  "instance_id": "instance-id",
  "sequence": 42,
  "timestamp": "2026-11-22T13:00:00Z",
  "session": "ssh-session-abc",
  "text": "copied text"
}
```

### Replay Gap

```json
{
  "type": "replay_gap",
  "oldest_sequence": 100
}
```

Sent when the cursor references an evicted event.

### Instance Gap

```json
{
  "type": "instance_gap",
  "oldest_sequence": 1
}
```

Sent when the cursor references a previous daemon instance.

## Behavior

- First connection without cursor: receives future events only
- Reconnection with cursor: receives replay then live events
- Slow subscriber: disconnected; must reconnect with cursor
- Ping/pong keepalive: configurable interval
