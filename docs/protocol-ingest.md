# Unix Socket Ingestion Protocol

## Overview

The clipwrapd daemon accepts clipboard events from local `clipwrap` processes via a Unix domain socket. The protocol uses length-prefixed JSON frames.

## Frame Format

Each message consists of:

```
4 bytes: big-endian uint32 payload length
N bytes: UTF-8 JSON payload
```

## Request

```json
{
  "version": 1,
  "session": "ssh-session-abc",
  "text": "copied text"
}
```

Fields:
- `version` (integer, required): Protocol version. Only `1` is supported.
- `session` (string, required): Session identifier (max 256 chars).
- `text` (string, required): Clipboard text content.

## Response (Success)

```json
{
  "id": "instance-id:42"
}
```

The `id` field contains the assigned event ID in the format `{instance-id}:{sequence}`.

## Response (Error)

```json
{
  "error": "reason for failure"
}
```

## Error Conditions

| Condition | Error Message |
|-----------|-------------|
| Unsupported version | `unsupported protocol version: N` |
| Empty session ID | `session ID is required` |
| Session ID too long | `session ID too long` |
| Text too large | `clipboard text too large` |
| Invalid UTF-8 | `invalid UTF-8 in clipboard text` |
| Invalid JSON | `invalid JSON` |
| Oversized frame | `payload too large` |

## Limits

- Maximum frame size: 16 MB
- Maximum clipboard text: configurable (default 1 MB)
- Maximum session ID length: 256 characters
- Maximum concurrent connections: configurable (default 64)
