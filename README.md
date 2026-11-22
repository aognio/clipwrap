# clipwrapd

Lightweight, secure, headless clipboard capture and distribution daemon written in Go.

Clipwrapd receives clipboard captures from local terminal processes via a Unix domain socket, maintains an in-memory event history, and distributes clipboard events to authenticated clients via WebSocket. It also provides a minimal browser-based administration dashboard.

## Quick start

```bash
# Build
go build ./cmd/clipwrapd

# Set administrator password
./clipwrapd --set-admin-password

# Run
./clipwrapd --config clipwrap-server.toml
```

## Documentation

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [Ingestion Protocol](docs/protocol-ingest.md)
- [WebSocket Protocol](docs/protocol-websocket.md)
- [Operations](docs/operations.md)

## Deployment

See `deploy/` for systemd unit files and Caddy reverse proxy examples.
