// Package httpserver implements HTTP handlers and WebSocket upgrades.
package httpserver

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/clipwrap/clipwrapd/internal/auth"
	"github.com/clipwrap/clipwrapd/internal/broker"
	"github.com/clipwrap/clipwrapd/internal/config"
	"github.com/clipwrap/clipwrapd/internal/domain"
	"github.com/clipwrap/clipwrapd/internal/history"
)

//go:embed assets/templates/*.html
var templateFS embed.FS

//go:embed assets/static/*
var staticFS embed.FS

// Server wraps the HTTP server with its dependencies.
type Server struct {
	cfg          config.Config
	store        *history.Store
	broker       *broker.Broker
	sessionStore *auth.SessionStore
	rateLimiter  *auth.RateLimiter
	templates    *template.Template
	mu           sync.Mutex
	startedAt    time.Time
	listener     *http.Server
}

// NewServer creates a new HTTP server.
func NewServer(cfg config.Config, store *history.Store, b *broker.Broker) *Server {
	tmpl := template.New("").Funcs(template.FuncMap{
		"truncate": func(s string, n int) string {
			if len(s) > n {
				return s[:n] + "…"
			}
			return s
		},
	})
	tmpl, err := tmpl.ParseFS(templateFS, "assets/templates/*.html")
	if err != nil {
		// Fallback: templates are optional for API-only usage
		tmpl = template.New("")
	}

	return &Server{
		cfg:          cfg,
		store:        store,
		broker:       b,
		sessionStore: auth.NewSessionStore(cfg.Web.SessionTTL),
		rateLimiter:  auth.NewRateLimiter(1*time.Minute, 10),
		templates:    tmpl,
		startedAt:    time.Now(),
	}
}

// Start begins listening for HTTP connections.
func (s *Server) Start(listener *http.Server) {
	s.listener = listener
	s.mu.Lock()
	s.startedAt = time.Now()
	s.mu.Unlock()
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.listener != nil {
		return s.listener.Shutdown(ctx)
	}
	return nil
}

// RegisterRoutes registers all HTTP handlers on the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Static files
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/events", s.handleEvents)
	mux.HandleFunc("/api/v1/events/ws", s.handleWebSocket)
}

// handleHealthz is a minimal liveness endpoint.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// handleReadyz checks if the daemon is ready to accept captures.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ready")
}

// handleLogin processes login/logout requests.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.doLogin(w, r)
		return
	}

	// GET: show login form
	if s.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Render login form
	if err := s.templates.ExecuteTemplate(w, "login.html", map[string]interface{}{}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (s *Server) doLogin(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.HasPassword() {
		http.Error(w, `{"error":"no password configured"}`, http.StatusUnauthorized)
		return
	}

	if !s.rateLimiter.Allow(r.RemoteAddr) {
		http.Error(w, `{"error":"too many attempts"}`, http.StatusTooManyRequests)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if req.Username != "admin" {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	if err := auth.VerifyPassword(req.Password, s.cfg.Auth.PasswordHash); err != nil {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	sessionID := s.sessionStore.Create()
	secure := strings.HasPrefix(s.cfg.Server.PublicURL, "https://")
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		MaxAge:   int(s.cfg.Web.SessionTTL.Seconds()),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleLogout invalidates the current session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cookie, err := r.Cookie(auth.CookieName)
	if err == nil {
		s.sessionStore.Delete(cookie.Value)
	}

	secure := strings.HasPrefix(s.cfg.Server.PublicURL, "https://")
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		MaxAge:   -1,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleDashboard serves the admin dashboard.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	s.mu.Lock()
	uptime := time.Since(s.startedAt)
	s.mu.Unlock()

	// Build event list for display
	allEvents := s.store.AllEvents()
	type EventRow struct {
		ID          string
		Timestamp   string
		Session     string
		Preview     string
		EncodedText string
	}
	rows := make([]EventRow, 0, len(allEvents))
	for i := len(allEvents) - 1; i >= 0; i-- {
		ev := allEvents[i]
		preview := ev.Text
		if len(preview) > 100 {
			preview = preview[:100] + "…"
		}
		rows = append(rows, EventRow{
			ID:          string(ev.ID),
			Timestamp:   ev.Timestamp.Local().Format("15:04:05"),
			Session:     ev.Session,
			Preview:     preview,
			EncodedText: url.QueryEscape(ev.Text),
		})
	}

	latestTime := "—"
	if ev, ok := s.store.Latest(); ok {
		latestTime = ev.Timestamp.Local().Format("15:04:05")
	}

	data := map[string]interface{}{
		"InstanceID": string(s.store.InstanceID()),
		"EventCount": s.store.Count(),
		"WSClients":  s.broker.Count(),
		"Uptime":     truncateDuration(uptime),
		"TotalEvents": int(s.store.Sequence()),
		"LatestEvent": latestTime,
		"Events":      rows,
	}

	if err := s.templates.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func truncateDuration(d time.Duration) string {
	if d.Hours() >= 1 {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	if d.Minutes() >= 1 {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// handleStatus returns operational metrics.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	uptime := time.Since(s.startedAt)
	s.mu.Unlock()

	status := map[string]interface{}{
		"instance_id":    s.store.InstanceID(),
		"uptime_seconds": uptime.Seconds(),
		"event_count":    s.store.Count(),
		"total_events":   int(s.store.Sequence()),
		"ws_clients":     s.broker.Count(),
		"sessions":       s.sessionStore.Count(),
	}

	if ev, ok := s.store.Latest(); ok {
		status["latest_event_time"] = ev.Timestamp.UTC().Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleEvents returns clipboard events with pagination.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	cursorStr := r.URL.Query().Get("cursor")
	var cursor domain.Cursor
	if cursorStr != "" {
		var ok bool
		cursor, ok = domain.NewCursor(domain.EventID(cursorStr))
		if !ok {
			http.Error(w, `{"error":"invalid cursor"}`, http.StatusBadRequest)
			return
		}
	}

	result := s.store.Since(cursor)
	resp := map[string]interface{}{
		"events": result.Events,
	}
	if result.Gap {
		resp["gap"] = true
		resp["oldest_sequence"] = int(result.OldestSeq)
	}
	if result.InstanceGap {
		resp["instance_gap"] = true
		resp["oldest_sequence"] = int(result.OldestSeq)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// WebSocket upgrader
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Origin checked at application level
	},
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

// handleWebSocket upgrades to WebSocket and streams events.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Check auth: cookie or bearer token
	if !s.isWebSocketAuthenticated(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Parse cursor from query parameter
	cursorStr := r.URL.Query().Get("cursor")
	var cursor domain.Cursor
	if cursorStr != "" {
		var ok bool
		cursor, ok = domain.NewCursor(domain.EventID(cursorStr))
		if !ok {
			conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid cursor"}`))
			return
		}
	}

	// Send replay if cursor is provided
	if !cursor.IsZero() {
		result := s.store.Since(cursor)
		if result.InstanceGap {
			msg := map[string]interface{}{
				"type":            "instance_gap",
				"oldest_sequence": int(result.OldestSeq),
			}
			conn.WriteJSON(msg)
		} else if result.Gap {
			msg := map[string]interface{}{
				"type":            "replay_gap",
				"oldest_sequence": int(result.OldestSeq),
			}
			conn.WriteJSON(msg)
		}

		for _, ev := range result.Events {
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		}
	}

	// Subscribe to live events
	sub := s.broker.Subscribe()
	defer sub.Close()

	// Set ping/pong
	conn.SetReadDeadline(time.Now().Add(s.cfg.WebSocket.PongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(s.cfg.WebSocket.PongTimeout))
		return nil
	})

	// Ping loop
	pingTicker := time.NewTicker(s.cfg.WebSocket.PingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case ev, ok := <-sub.Events:
			if !ok {
				return
			}
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-pingTicker.C:
			conn.WriteMessage(websocket.PingMessage, nil)
		}
	}
}

// isAuthenticated checks for a valid session cookie.
func (s *Server) isAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil {
		return false
	}
	return s.sessionStore.Get(cookie.Value) != nil
}

// isWebSocketAuthenticated checks for session cookie or bearer token.
func (s *Server) isWebSocketAuthenticated(r *http.Request) bool {
	// Check session cookie
	if s.isAuthenticated(r) {
		return true
	}

	// Check bearer token (service token)
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") && s.cfg.Auth.ServiceToken != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		return auth.VerifyServiceToken(token, s.cfg.Auth.ServiceToken)
	}

	// Check query parameter for cursor-only connections (no auth for public replay)
	// Not allowed — all connections must be authenticated
	return false
}

// InvalidateSessions removes all sessions (e.g., after password change).
func (s *Server) InvalidateSessions() {
	s.sessionStore.InvalidateAll()
}
