// Package auth handles authentication, sessions, and service tokens.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/blake2b"
)

// PHCArgon2idPrefix is the PHC identifier for Argon2id.
const PHCArgon2idPrefix = "$argon2id$"

// Argon2id parameters for password hashing.
const (
	Argon2Time    = 1
	Argon2Memory  = 64 * 1024 // 64 MB
	Argon2Threads = 4
	Argon2KeyLen  = 32
	Argon2SaltLen = 16
)

// Maximum accepted Argon2id parameters to prevent DoS via config.
const (
	MaxArgon2Time    = 4
	MaxArgon2Memory  = 256 * 1024 // 256 MB
	MaxArgon2Threads = 8
)

// HashPassword generates an Argon2id PHC hash for the given password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, Argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hashBytes := argon2.IDKey(
		[]byte(password),
		salt,
		Argon2Time,
		Argon2Memory,
		Argon2Threads,
		Argon2KeyLen,
	)

	// Encode as PHC string
	saltHex := hex.EncodeToString(salt)
	hashHex := hex.EncodeToString(hashBytes)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		Argon2Memory, Argon2Time, Argon2Threads, saltHex, hashHex), nil
}

// VerifyPassword checks a password against a PHC Argon2id hash.
func VerifyPassword(password, phcHash string) error {
	params, salt, hashBytes, err := parsePHC(phcHash)
	if err != nil {
		// Return a generic error to avoid leaking info about the hash format
		return fmt.Errorf("invalid credentials")
	}

	// Check parameters are within bounds
	if params.time > MaxArgon2Time || params.memory > MaxArgon2Memory || params.threads > MaxArgon2Threads {
		return fmt.Errorf("invalid credentials")
	}

	computed := argon2.IDKey(
		[]byte(password),
		salt,
		params.time,
		params.memory,
		params.threads,
		uint32(len(hashBytes)),
	)

	if subtle.ConstantTimeCompare(hashBytes, computed) != 1 {
		return fmt.Errorf("invalid credentials")
	}
	return nil
}

// argon2Params holds parsed Argon2id parameters.
type argon2Params struct {
	time    uint32
	memory  uint32
	threads uint8
}

// parsePHC parses a PHC-formatted Argon2id hash string.
func parsePHC(phcHash string) (*argon2Params, []byte, []byte, error) {
	if !strings.HasPrefix(phcHash, PHCArgon2idPrefix) {
		return nil, nil, nil, fmt.Errorf("unsupported hash format")
	}

	// Format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
	// After prefix: v=19$m=65536,t=3,p=4$<salt>$<hash>
	rest := phcHash[len(PHCArgon2idPrefix):]
	parts := strings.SplitN(rest, "$", 4)
	if len(parts) < 4 {
		return nil, nil, nil, fmt.Errorf("invalid PHC format")
	}

	versionStr := parts[0]
	paramsStr := parts[1]
	saltHex := parts[2]
	hashHex := parts[3]

	// Parse version
	if !strings.HasPrefix(versionStr, "v=") {
		return nil, nil, nil, fmt.Errorf("missing version")
	}
	v, err := strconv.ParseUint(versionStr[2:], 10, 8)
	if err != nil || v != 19 {
		return nil, nil, nil, fmt.Errorf("unsupported version: %s", versionStr)
	}

	// Parse parameters
	params := &argon2Params{}
	for _, param := range strings.Split(paramsStr, ",") {
		kv := strings.SplitN(param, "=", 2)
		if len(kv) != 2 {
			return nil, nil, nil, fmt.Errorf("invalid parameter: %s", param)
		}
		switch kv[0] {
		case "m":
			m, err := strconv.ParseUint(kv[1], 10, 32)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("invalid memory param: %s", kv[1])
			}
			params.memory = uint32(m)
		case "t":
			t, err := strconv.ParseUint(kv[1], 10, 32)
			if err != nil || t == 0 {
				return nil, nil, nil, fmt.Errorf("invalid time param: %s", kv[1])
			}
			params.time = uint32(t)
		case "p":
			p, err := strconv.ParseUint(kv[1], 10, 8)
			if err != nil || p == 0 {
				return nil, nil, nil, fmt.Errorf("invalid threads param: %s", kv[1])
			}
			params.threads = uint8(p)
		}
	}

	if params.time == 0 || params.memory == 0 || params.threads == 0 {
		return nil, nil, nil, fmt.Errorf("missing required parameters")
	}

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid salt: %w", err)
	}

	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid hash: %w", err)
	}

	return params, salt, hashBytes, nil
}

// ServiceToken generates a new service token and its Blake2b digest for storage.
func GenerateServiceToken() (token string, digest string) {
	bytes := make([]byte, 32)
	_, _ = rand.Read(bytes)
	token = hex.EncodeToString(bytes)

	h, _ := blake2b.New256(nil)
	h.Write([]byte(token))
	digest = hex.EncodeToString(h.Sum(nil))
	return
}

// VerifyServiceToken checks a token against a stored Blake2b digest using constant-time comparison.
func VerifyServiceToken(token, storedDigest string) bool {
	h, _ := blake2b.New256(nil)
	h.Write([]byte(token))
	computed := hex.EncodeToString(h.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedDigest)) == 1
}

// Session represents an authenticated browser session.
type Session struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionStore manages server-side sessions.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
}

// NewSessionStore creates a new session store with the given TTL.
func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
		ttl:      ttl,
	}
}

// Create generates a new session and returns its ID.
func (s *SessionStore) Create() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		id := generateSessionID()
		if _, exists := s.sessions[id]; !exists {
			s.sessions[id] = &Session{
				ID:        id,
				CreatedAt: time.Now(),
				ExpiresAt: time.Now().Add(s.ttl),
			}
			return id
		}
	}
}

// Get retrieves a session by ID, or nil if not found or expired.
func (s *SessionStore) Get(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		return nil
	}
	return sess
}

// Delete removes a session by ID.
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// InvalidateAll removes all sessions (e.g., after password change).
func (s *SessionStore) InvalidateAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]*Session)
}

// Cleanup removes expired sessions.
func (s *SessionStore) Cleanup() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	removed := 0
	for id, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			delete(s.sessions, id)
			removed++
		}
	}
	return removed
}

// Count returns the number of active sessions.
func (s *SessionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// generateSessionID creates a cryptographically random session ID.
func generateSessionID() string {
	bytes := make([]byte, 32)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// RateLimiter provides simple in-memory rate limiting for login attempts.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	window   time.Duration
	maxCount int
}

// NewRateLimiter creates a rate limiter.
func NewRateLimiter(window time.Duration, maxCount int) *RateLimiter {
	return &RateLimiter{
		attempts: make(map[string][]time.Time),
		window:   window,
		maxCount: maxCount,
	}
}

// Allow checks if a request from the given key (e.g., IP) is allowed.
func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	// Filter old attempts
	var recent []time.Time
	for _, t := range r.attempts[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= r.maxCount {
		r.attempts[key] = recent
		return false
	}

	r.attempts[key] = append(recent, now)
	return true
}

// CookieName is the name of the session cookie.
const CookieName = "clipwrapd_session"

// CookieOptions returns the attributes for a secure session cookie.
func CookieOptions(secure bool) struct {
	Path     string
	HttpOnly bool
	SameSite string
	Secure   bool
	MaxAge   int
} {
	return struct {
		Path     string
		HttpOnly bool
		SameSite string
		Secure   bool
		MaxAge   int
	}{
		Path:     "/",
		HttpOnly: true,
		SameSite: "Strict",
		Secure:   secure,
		MaxAge:   0, // session cookie by default; set explicitly for expiration
	}
}

// Hasher is a configurable hash function creator for blake2b.
type Hasher interface {
	New() hash.Hash
}
