// Package ingest handles Unix domain socket clipboard ingestion protocol.
//
// Protocol format (version 1):
//
// Each message is a length-prefixed JSON frame:
//
//	4 bytes: big-endian uint32 payload length
//	N bytes: UTF-8 JSON payload
//
// Request payload:
//
//	{"version": 1, "session": "...", "text": "..."}
//
// Response payload:
//
//	{"id": "instance:seq"}  (success)
//	{"error": "reason"}     (failure)
package ingest

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/clipwrap/clipwrapd/internal/broker"
	"github.com/clipwrap/clipwrapd/internal/history"
)

// IngestConfig holds the configuration for the ingestion listener.
type IngestConfig struct {
	// SocketPath is the filesystem path for the Unix domain socket.
	SocketPath string

	// MaxItemBytes is the maximum allowed clipboard text size in bytes.
	MaxItemBytes int

	// MaxConnections is the maximum number of simultaneous connections.
	MaxConnections int

	// SocketMode is the filesystem permission mode for the socket.
	SocketMode os.FileMode
}

// Listener accepts and handles Unix socket connections for clipboard ingestion.
type Listener struct {
	cfg      IngestConfig
	store    *history.Store
	broker   *broker.Broker
	listener *net.UnixListener
	wg       sync.WaitGroup
	mu       sync.Mutex
	accepting bool
}

// NewListener creates a new ingestion listener.
func NewListener(cfg IngestConfig, store *history.Store, b *broker.Broker) *Listener {
	return &Listener{
		cfg:    cfg,
		store:  store,
		broker: b,
	}
}

// Listen starts accepting connections on the Unix socket.
func (l *Listener) Listen() error {
	addr := &net.UnixAddr{
		Name: l.cfg.SocketPath,
		Net:  "unix",
	}

	// Remove existing socket if present
	if err := os.Remove(l.cfg.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing socket: %w", err)
	}

	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("listen unix: %w", err)
	}

	// Set socket permissions
	if err := os.Chmod(l.cfg.SocketPath, l.cfg.SocketMode); err != nil {
		ln.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	l.listener = ln
	l.accepting = true

	l.wg.Add(1)
	go l.acceptLoop()

	return nil
}

// acceptLoop accepts incoming connections and handles them in separate goroutines.
func (l *Listener) acceptLoop() {
	defer l.wg.Done()

	var active int64
	var activeMu sync.Mutex

	for {
		conn, err := l.listener.Accept()
		if err != nil {
			if !l.isAccepting() {
				return
			}
			// Listener closed; exit accept loop
			return
		}

		// Enforce max connections
		if l.cfg.MaxConnections > 0 {
			activeMu.Lock()
			if int(active) >= l.cfg.MaxConnections {
				activeMu.Unlock()
				conn.Close()
				continue
			}
			active++
			activeMu.Unlock()
		}

		l.wg.Add(1)
		go func(c net.Conn) {
			defer l.wg.Done()
			defer func() {
				if l.cfg.MaxConnections > 0 {
					activeMu.Lock()
					active--
					activeMu.Unlock()
				}
			}()
			l.handleConnection(c)
		}(conn)
	}
}

func (l *Listener) isAccepting() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accepting
}

// handleConnection processes a single client connection.
func (l *Listener) handleConnection(conn net.Conn) {
	defer conn.Close()

	for {
		payload, err := readFrame(conn)
		if err != nil {
			if err == errOversizedFrame {
				// Send error response for oversized frame
				writeError(conn, "payload too large")
				continue
			}
			// Connection error or EOF
			return
		}

		// Parse JSON request
		var req IngestRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			writeError(conn, "invalid JSON")
			continue
		}

		resp, err := l.handleRequest(req)
		if err != nil {
			writeError(conn, err.Error())
			continue
		}

		if err := writeFrame(conn, resp); err != nil {
			return
		}
	}
}

// IngestRequest represents an incoming clipboard submission.
type IngestRequest struct {
	Version int    `json:"version"`
	Session string `json:"session"`
	Text    string `json:"text"`
}

// IngestResponse is the acknowledgment sent after a successful ingestion.
type IngestResponse struct {
	ID string `json:"id"`
}

// IngestError is the error response sent on failure.
type IngestError struct {
	Error string `json:"error"`
}

func (l *Listener) handleRequest(req IngestRequest) ([]byte, error) {
	// Validate protocol version
	if req.Version != 1 {
		return nil, fmt.Errorf("unsupported protocol version: %d", req.Version)
	}

	// Validate session ID
	if req.Session == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	if len(req.Session) > 256 {
		return nil, fmt.Errorf("session ID too long")
	}

	// Validate text size
	if l.cfg.MaxItemBytes > 0 && len(req.Text) > l.cfg.MaxItemBytes {
		return nil, fmt.Errorf("clipboard text too large")
	}

	// Validate UTF-8
	if !utf8.ValidString(req.Text) {
		return nil, fmt.Errorf("invalid UTF-8 in clipboard text")
	}

	// Append to history and publish
	event := l.store.Append(req.Session, req.Text)
	l.broker.Publish(event)

	// Return acknowledgment
	resp, _ := json.Marshal(IngestResponse{ID: string(event.ID)})
	return resp, nil
}

// Shutdown stops accepting new connections and waits for existing ones to finish.
func (l *Listener) Shutdown(timeout time.Duration) error {
	l.mu.Lock()
	l.accepting = false
	l.mu.Unlock()

	if l.listener != nil {
		if err := l.listener.Close(); err != nil {
			return err
		}
	}

	// Wait for connections with timeout
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Remove socket file
		os.Remove(l.cfg.SocketPath)
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("shutdown timed out after %v", timeout)
	}
}

// readFrame reads a length-prefixed frame from the connection.
func readFrame(r io.Reader) ([]byte, error) {
	// Read 4-byte length prefix
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return nil, fmt.Errorf("read length prefix: %w", err)
	}

	length := binary.BigEndian.Uint32(buf[:])
	if length > maxFrameSize {
		return nil, errOversizedFrame
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	return payload, nil
}

// writeFrame writes a length-prefixed frame to the connection.
func writeFrame(w io.Writer, data []byte) error {
	// Write 4-byte length prefix
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(data)))
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("write length prefix: %w", err)
	}

	// Write payload
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}

	return nil
}

// writeError sends an error response frame.
func writeError(w io.Writer, msg string) error {
	data, _ := json.Marshal(IngestError{Error: msg})
	return writeFrame(w, data)
}

const (
	// maxFrameSize is the maximum allowed frame size (16 MB).
	maxFrameSize = 16 * 1024 * 1024
)

var errOversizedFrame = fmt.Errorf("oversized frame")
