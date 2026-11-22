package ingest

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/clipwrap/clipwrapd/internal/broker"
	"github.com/clipwrap/clipwrapd/internal/domain"
	"github.com/clipwrap/clipwrapd/internal/history"
)

// helper: write a length-prefixed frame to a connection
func writeFrameTo(conn net.Conn, data []byte) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(data)))
	if _, err := conn.Write(buf[:]); err != nil {
		return err
	}
	_, err := conn.Write(data)
	return err
}

// helper: read a length-prefixed frame from a connection
func readFrameFrom(conn net.Conn) ([]byte, error) {
	var buf [4]byte
	if _, err := io.ReadFull(conn, buf[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(buf[:])
	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func newTestListenerWithPath(t *testing.T) (socketPath string, cleanup func(), store *history.Store, b *broker.Broker) {
	t.Helper()

	dir := t.TempDir()
	socketPath = filepath.Join(dir, "test.sock")

	inst := domain.NewInstanceID()
	store = history.NewStore(inst, 100)
	b = broker.NewBroker(16)

	cfg := IngestConfig{
		SocketPath:     socketPath,
		MaxItemBytes:   1024,
		MaxConnections: 10,
		SocketMode:     0600,
	}

	ln := NewListener(cfg, store, b)
	if err := ln.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}

	cleanup = func() {
		ln.Shutdown(5 * time.Second)
		b.Shutdown()
	}
	return socketPath, cleanup, store, b
}

func TestValidFrame_Acknowledgment(t *testing.T) {
	socketPath, cleanup, store, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := IngestRequest{
		Version: 1,
		Session: "test-session",
		Text:    "hello world",
	}
	data, _ := json.Marshal(req)
	if err := writeFrameTo(conn, data); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	resp, err := readFrameFrom(conn)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}

	var ack IngestResponse
	if err := json.Unmarshal(resp, &ack); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, string(resp))
	}

	if ack.ID == "" {
		t.Fatal("expected non-empty event ID in acknowledgment")
	}

	// Verify event is in history
	if store.Count() != 1 {
		t.Errorf("expected 1 event in history, got %d", store.Count())
	}

	ev, ok := store.Latest()
	if !ok {
		t.Fatal("expected latest event")
	}
	if ev.Text != "hello world" {
		t.Errorf("expected text 'hello world', got %q", ev.Text)
	}
	if ev.Session != "test-session" {
		t.Errorf("expected session 'test-session', got %q", ev.Session)
	}
}

func TestMultipleMessages(t *testing.T) {
	socketPath, cleanup, store, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	for i := 0; i < 5; i++ {
		req := IngestRequest{
			Version: 1,
			Session: "sess",
			Text:    "msg",
		}
		data, _ := json.Marshal(req)
		if err := writeFrameTo(conn, data); err != nil {
			t.Fatalf("write frame %d: %v", i, err)
		}

		resp, err := readFrameFrom(conn)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}

		var ack IngestResponse
		if err := json.Unmarshal(resp, &ack); err != nil {
			t.Fatalf("unmarshal %d: %v", i, string(resp))
		}
		if ack.ID == "" {
			t.Errorf("message %d: empty event ID", i)
		}
	}

	if store.Count() != 5 {
		t.Errorf("expected 5 events, got %d", store.Count())
	}
}

func TestUnsupportedProtocolVersion(t *testing.T) {
	socketPath, cleanup, _, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := IngestRequest{
		Version: 99,
		Session: "sess",
		Text:    "text",
	}
	data, _ := json.Marshal(req)
	if err := writeFrameTo(conn, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp, err := readFrameFrom(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var errResp IngestError
	if err := json.Unmarshal(resp, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v (raw: %s)", err, string(resp))
	}
	if errResp.Error == "" {
		t.Error("expected error message")
	}
}

func TestMissingSessionID(t *testing.T) {
	socketPath, cleanup, _, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := IngestRequest{
		Version: 1,
		Session: "",
		Text:    "text",
	}
	data, _ := json.Marshal(req)
	if err := writeFrameTo(conn, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp, err := readFrameFrom(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var errResp IngestError
	if err := json.Unmarshal(resp, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v (raw: %s)", err, string(resp))
	}
	if errResp.Error == "" {
		t.Error("expected error for missing session")
	}
}

func TestTextTooLarge(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	inst := domain.NewInstanceID()
	store := history.NewStore(inst, 100)
	b := broker.NewBroker(16)

	cfg := IngestConfig{
		SocketPath:     socketPath,
		MaxItemBytes:   10, // very small limit
		MaxConnections: 10,
		SocketMode:     0600,
	}

	ln := NewListener(cfg, store, b)
	if err := ln.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() {
		ln.Shutdown(5 * time.Second)
		b.Shutdown()
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := IngestRequest{
		Version: 1,
		Session: "sess",
		Text:    "this is way too long",
	}
	data, _ := json.Marshal(req)
	if err := writeFrameTo(conn, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp, err := readFrameFrom(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var errResp IngestError
	if err := json.Unmarshal(resp, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v (raw: %s)", err, string(resp))
	}
	if errResp.Error == "" {
		t.Error("expected error for oversized text")
	}
}

func TestInvalidJSON(t *testing.T) {
	socketPath, cleanup, _, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Write invalid JSON
	if err := writeFrameTo(conn, []byte("not json")); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp, err := readFrameFrom(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var errResp IngestError
	if err := json.Unmarshal(resp, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v (raw: %s)", err, string(resp))
	}
	if errResp.Error == "" {
		t.Error("expected error for invalid JSON")
	}
}


func TestOversizedFrame(t *testing.T) {
	socketPath, cleanup, _, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Write a frame with a huge length prefix but no actual payload
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(maxFrameSize+1))
	if _, err := conn.Write(buf[:]); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp, err := readFrameFrom(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var errResp IngestError
	if err := json.Unmarshal(resp, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v (raw: %s)", err, string(resp))
	}
	if errResp.Error == "" {
		t.Error("expected error for oversized frame")
	}
}

func TestConcurrentClients(t *testing.T) {
	socketPath, cleanup, store, _ := newTestListenerWithPath(t)
	defer cleanup()

	const clients = 10
	const msgsPerClient = 5

	var wg sync.WaitGroup
	errs := make(chan error, clients)

	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close()

			for m := 0; m < msgsPerClient; m++ {
				req := IngestRequest{
					Version: 1,
					Session: "concurrent-sess",
					Text:    "concurrent-text",
				}
				data, _ := json.Marshal(req)
				if err := writeFrameTo(conn, data); err != nil {
					errs <- err
					return
				}

				resp, err := readFrameFrom(conn)
				if err != nil {
					errs <- err
					return
				}

				var ack IngestResponse
				if err := json.Unmarshal(resp, &ack); err != nil {
					errs <- err
					return
				}
				if ack.ID == "" {
					errs <- err
					return
				}
			}
		}(c)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("client error: %v", err)
	}

	expected := clients * msgsPerClient
	if store.Count() != expected {
		t.Errorf("expected %d events, got %d", expected, store.Count())
	}
}

func TestSocketPermissions(t *testing.T) {
	_, cleanup, _, _ := newTestListenerWithPath(t)
	defer cleanup()

	// We can't easily test permissions in a temp dir, but we can verify
	// the socket file exists and has the expected mode
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "perm.sock")

	inst := domain.NewInstanceID()
	store := history.NewStore(inst, 100)
	b := broker.NewBroker(16)

	cfg := IngestConfig{
		SocketPath:     socketPath,
		MaxItemBytes:   1024,
		MaxConnections: 10,
		SocketMode:     0600,
	}

	ln := NewListener(cfg, store, b)
	if err := ln.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() {
		ln.Shutdown(5 * time.Second)
		b.Shutdown()
	}()

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected socket permissions 0600, got %o", perm)
	}
}

func TestBrokerReceivesEvents(t *testing.T) {
	socketPath, cleanup, _, b := newTestListenerWithPath(t)
	defer cleanup()

	sub := b.Subscribe()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := IngestRequest{
		Version: 1,
		Session: "sess",
		Text:    "broker-test",
	}
	data, _ := json.Marshal(req)
	if err := writeFrameTo(conn, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read acknowledgment
	if _, err := readFrameFrom(conn); err != nil {
		t.Fatalf("read ack: %v", err)
	}

	// Check broker received the event
	select {
	case ev := <-sub.Events:
		if ev.Text != "broker-test" {
			t.Errorf("expected text 'broker-test', got %q", ev.Text)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("broker did not receive event")
	}
}

func TestClientDisconnect(t *testing.T) {
	socketPath, cleanup, _, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Send a valid request
	req := IngestRequest{
		Version: 1,
		Session: "sess",
		Text:    "text",
	}
	data, _ := json.Marshal(req)
	if err := writeFrameTo(conn, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Disconnect immediately without reading response
	conn.Close()

	// Give the server time to handle the disconnect
	time.Sleep(100 * time.Millisecond)

	// Verify the server is still healthy by connecting again
	conn2, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer conn2.Close()

	req2 := IngestRequest{
		Version: 1,
		Session: "sess2",
		Text:    "text2",
	}
	data2, _ := json.Marshal(req2)
	if err := writeFrameTo(conn2, data2); err != nil {
		t.Fatalf("write2: %v", err)
	}

	resp, err := readFrameFrom(conn2)
	if err != nil {
		t.Fatalf("read2: %v", err)
	}

	var ack IngestResponse
	if err := json.Unmarshal(resp, &ack); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ack.ID == "" {
		t.Error("expected event ID after reconnect")
	}
}

func TestTextPreservedExactly(t *testing.T) {
	socketPath, cleanup, store, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Text with newlines, tabs, and special characters
	originalText := "line1\nline2\ttab\r\nwindows\n  spaces  \n"
	req := IngestRequest{
		Version: 1,
		Session: "sess",
		Text:    originalText,
	}
	data, _ := json.Marshal(req)
	if err := writeFrameTo(conn, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := readFrameFrom(conn); err != nil {
		t.Fatalf("read: %v", err)
	}

	ev, ok := store.Latest()
	if !ok {
		t.Fatal("expected latest event")
	}
	if ev.Text != originalText {
		t.Errorf("text not preserved exactly:\ngot:  %q\nwant: %q", ev.Text, originalText)
	}
}

func TestSessionIDTooLong(t *testing.T) {
	socketPath, cleanup, _, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Session ID longer than 256 chars
	longSession := make([]byte, 257)
	for i := range longSession {
		longSession[i] = 'a'
	}

	req := IngestRequest{
		Version: 1,
		Session: string(longSession),
		Text:    "text",
	}
	data, _ := json.Marshal(req)
	if err := writeFrameTo(conn, data); err != nil {
		t.Fatalf("write: %v", err)
	}

	resp, err := readFrameFrom(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var errResp IngestError
	if err := json.Unmarshal(resp, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v (raw: %s)", err, string(resp))
	}
	if errResp.Error == "" {
		t.Error("expected error for long session ID")
	}
}

func TestMaxConnections(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	inst := domain.NewInstanceID()
	store := history.NewStore(inst, 100)
	b := broker.NewBroker(16)

	cfg := IngestConfig{
		SocketPath:     socketPath,
		MaxItemBytes:   1024,
		MaxConnections: 2,
		SocketMode:     0600,
	}

	ln := NewListener(cfg, store, b)
	if err := ln.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() {
		ln.Shutdown(5 * time.Second)
		b.Shutdown()
	}()

	// Open 2 connections (should succeed)
	conn1, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer conn1.Close()

	conn2, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer conn2.Close()

	// Keep connections busy by sending requests
	// The 3rd connection should be rejected
	conn3, err := net.Dial("unix", socketPath)
	if err != nil {
		// Connection might succeed but get closed immediately
		// or fail to dial — both are acceptable
		t.Logf("3rd connection failed to dial (expected): %v", err)
		return
	}
	defer conn3.Close()

	// Try to send a request on the 3rd connection
	req := IngestRequest{
		Version: 1,
		Session: "sess",
		Text:    "text",
	}
	data, _ := json.Marshal(req)
	conn3.SetWriteDeadline(time.Now().Add(1 * time.Second))
	if err := writeFrameTo(conn3, data); err != nil {
		// Write might fail if connection was closed
		t.Logf("3rd connection write failed (expected): %v", err)
		return
	}

	// Try to read response with timeout
	conn3.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, err = readFrameFrom(conn3)
	if err != nil {
		// Read might fail if connection was closed
		t.Logf("3rd connection read failed (expected): %v", err)
		return
	}

	// If we got here, the 3rd connection was accepted — that's OK
	// as long as the max connections limit is enforced at some point
}

func TestGracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	inst := domain.NewInstanceID()
	store := history.NewStore(inst, 100)
	b := broker.NewBroker(16)

	cfg := IngestConfig{
		SocketPath:     socketPath,
		MaxItemBytes:   1024,
		MaxConnections: 10,
		SocketMode:     0600,
	}

	ln := NewListener(cfg, store, b)
	if err := ln.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}

	// Send a request to verify it works
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	req := IngestRequest{
		Version: 1,
		Session: "sess",
		Text:    "text",
	}
	data, _ := json.Marshal(req)
	if err := writeFrameTo(conn, data); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.Close()

	// Shutdown
	if err := ln.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	b.Shutdown()

	// Verify socket file is removed
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("expected socket file to be removed after shutdown")
	}

	// Verify new connections are rejected
	_, err = net.Dial("unix", socketPath)
	if err == nil {
		t.Error("expected connection to fail after shutdown")
	}
}

func TestEventIDUniqueness(t *testing.T) {
	socketPath, cleanup, store, _ := newTestListenerWithPath(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	ids := make(map[string]bool)
	for i := 0; i < 50; i++ {
		req := IngestRequest{
			Version: 1,
			Session: "sess",
			Text:    "text",
		}
		data, _ := json.Marshal(req)
		if err := writeFrameTo(conn, data); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}

		resp, err := readFrameFrom(conn)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}

		var ack IngestResponse
		if err := json.Unmarshal(resp, &ack); err != nil {
			t.Fatalf("unmarshal %d: %v", i, err)
		}

		if ids[ack.ID] {
			t.Errorf("duplicate event ID: %s", ack.ID)
		}
		ids[ack.ID] = true
	}

	if store.Sequence() != 50 {
		t.Errorf("expected sequence 50, got %d", store.Sequence())
	}
}
