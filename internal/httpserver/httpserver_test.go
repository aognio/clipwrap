package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/clipwrap/clipwrapd/internal/auth"
	"github.com/clipwrap/clipwrapd/internal/broker"
	"github.com/clipwrap/clipwrapd/internal/config"
	"github.com/clipwrap/clipwrapd/internal/domain"
	"github.com/clipwrap/clipwrapd/internal/history"
)

func newTestServer(t *testing.T, password string) (*Server, *history.Store, *broker.Broker) {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.Auth.Username = "admin"
	if password != "" {
		hash, err := auth.HashPassword(password)
		if err != nil {
			t.Fatalf("hash password: %v", err)
		}
		cfg.Auth.PasswordHash = hash
	}

	inst := domain.NewInstanceID()
	store := history.NewStore(inst, 100)
	b := broker.NewBroker(16)

	srv := NewServer(cfg, store, b)
	return srv, store, b
}

func newTestMux(srv *Server) *http.ServeMux {
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return mux
}

func TestHealthz(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	mux := newTestMux(srv)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}
}

func TestReadyz(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	mux := newTestMux(srv)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}
}

func TestLoginNoPasswordConfigured(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	mux := newTestMux(srv)

	body := strings.NewReader(`{"username":"admin","password":"test"}`)
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.Code)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	body := strings.NewReader(`{"username":"admin","password":"wrong"}`)
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.Code)
	}
}

func TestLoginWrongUsername(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	body := strings.NewReader(`{"username":"root","password":"correct"}`)
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.Code)
	}
}

func TestLoginSuccess(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", body)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d body: %s", resp.Code, resp.Body.String())
	}

	// Check for session cookie
	cookies := resp.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}

	sessionCookie := cookies[0]
	if sessionCookie.Name != auth.CookieName {
		t.Errorf("expected cookie name %s, got %s", auth.CookieName, sessionCookie.Name)
	}
	if !sessionCookie.HttpOnly {
		t.Error("expected HttpOnly cookie")
	}
}

func TestLoginRedirectWhenAuthenticated(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	// Login first
	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(loginResp, loginReq)

	cookies := loginResp.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}

	// GET /login should redirect
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/login", nil)
	req.AddCookie(cookies[0])
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", resp.Code)
	}
}

func TestLogout(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	// Login first
	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(loginResp, loginReq)

	cookies := loginResp.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}

	// Logout
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/logout", nil)
	req.AddCookie(cookies[0])
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}

	// Verify session is invalidated
	protectedResp := httptest.NewRecorder()
	protectedReq := httptest.NewRequest("GET", "/api/v1/status", nil)
	protectedReq.AddCookie(cookies[0])
	mux.ServeHTTP(protectedResp, protectedReq)

	if protectedResp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 after logout, got %d", protectedResp.Code)
	}
}

func TestDashboardRedirectWhenUnauthenticated(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", resp.Code)
	}
}

func TestDashboardWhenAuthenticated(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	// Login
	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(loginResp, loginReq)

	cookies := loginResp.Result().Cookies()

	// Access dashboard
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}
}

func TestStatusUnauthenticated(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.Code)
	}
}

func TestStatusAuthenticated(t *testing.T) {
	srv, store, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	// Login
	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(loginResp, loginReq)

	cookies := loginResp.Result().Cookies()

	// Add an event
	store.Append("test-session", "test-text")

	// Access status
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}

	var status map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}

	if status["event_count"].(float64) != 1 {
		t.Errorf("expected event_count 1, got %v", status["event_count"])
	}
}

func TestEventsUnauthenticated(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.Code)
	}
}

func TestEventsAuthenticated(t *testing.T) {
	srv, store, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	// Login
	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(loginResp, loginReq)

	cookies := loginResp.Result().Cookies()

	// Add events
	store.Append("sess", "text1")
	store.Append("sess", "text2")

	// Access events
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	events := result["events"].([]interface{})
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestEventsWithCursor(t *testing.T) {
	srv, store, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	// Login
	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(loginResp, loginReq)

	cookies := loginResp.Result().Cookies()

	// Add events
	store.Append("sess", "text1")
	ev2 := store.Append("sess", "text2")
	store.Append("sess", "text3")

	// Access events with cursor at ev2
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/events?cursor="+string(ev2.ID), nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	events := result["events"].([]interface{})
	if len(events) != 1 {
		t.Errorf("expected 1 event after cursor, got %d", len(events))
	}
}

func TestEventsInvalidCursor(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	// Login
	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(loginResp, loginReq)

	cookies := loginResp.Result().Cookies()

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/events?cursor=invalid", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.Code)
	}
}

func TestWebSocketUnauthenticated(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/events/ws", nil)
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.Code)
	}
}

func TestSessionExpiration(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.Username = "admin"
	hash, err := auth.HashPassword("correct")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	cfg.Auth.PasswordHash = hash
	cfg.Web.SessionTTL = 1 * time.Millisecond

	inst := domain.NewInstanceID()
	store := history.NewStore(inst, 100)
	b := broker.NewBroker(16)
	srv := NewServer(cfg, store, b)
	mux := newTestMux(srv)

	// Login
	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(loginResp, loginReq)

	cookies := loginResp.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Try to access protected resource
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 after session expires, got %d", resp.Code)
	}
}

func TestInvalidateSessions(t *testing.T) {
	srv, _, _ := newTestServer(t, "correct")
	mux := newTestMux(srv)

	// Login
	body := strings.NewReader(`{"username":"admin","password":"correct"}`)
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest("POST", "/login", body)
	loginReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(loginResp, loginReq)

	cookies := loginResp.Result().Cookies()

	// Invalidate all sessions
	srv.InvalidateSessions()

	// Try to access protected resource
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 after invalidation, got %d", resp.Code)
	}
}

func TestServiceTokenWebSocket(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.Username = "admin"
	token, digest := auth.GenerateServiceToken()
	cfg.Auth.ServiceToken = digest

	inst := domain.NewInstanceID()
	store := history.NewStore(inst, 100)
	b := broker.NewBroker(16)
	srv := NewServer(cfg, store, b)

	// Check that the server accepts the service token
	req := httptest.NewRequest("GET", "/api/v1/events/ws", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	if !srv.isWebSocketAuthenticated(req) {
		t.Error("expected service token to authenticate WebSocket")
	}

	// Wrong token should fail
	req2 := httptest.NewRequest("GET", "/api/v1/events/ws", nil)
	req2.Header.Set("Authorization", "Bearer wrong-token")
	if srv.isWebSocketAuthenticated(req2) {
		t.Error("expected wrong token to fail")
	}
}
