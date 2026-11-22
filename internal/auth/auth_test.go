package auth

import (
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	password := "correct-horse-battery-staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	// Verify the hash format
	if len(hash) < 20 {
		t.Fatalf("hash too short: %q", hash)
	}

	if err := VerifyPassword(password, hash); err != nil {
		t.Fatalf("VerifyPassword with correct password: %v", err)
	}

	// Wrong password should fail
	if err := VerifyPassword("wrong-password", hash); err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestHashUniqueness(t *testing.T) {
	hash1, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword 1: %v", err)
	}

	hash2, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword 2: %v", err)
	}

	if hash1 == hash2 {
		t.Fatal("expected different hashes due to random salt")
	}
}

func TestVerifyPasswordInvalidPHC(t *testing.T) {
	if err := VerifyPassword("password", "invalid-hash"); err == nil {
		t.Fatal("expected error for invalid PHC hash")
	}

	if err := VerifyPassword("password", "$argon2d$m=1,t=1,p=1$abc$def"); err == nil {
		t.Fatal("expected error for wrong algorithm")
	}
}

func TestVerifyPasswordExcessiveParams(t *testing.T) {
	// Create a hash with excessive memory parameter
	hash := "$argon2id$v=19$m=999999999,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA$BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	if err := VerifyPassword("password", hash); err == nil {
		t.Fatal("expected error for excessive memory param")
	}
}

func TestServiceToken(t *testing.T) {
	token, digest := GenerateServiceToken()

	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if digest == "" {
		t.Fatal("expected non-empty digest")
	}

	if !VerifyServiceToken(token, digest) {
		t.Fatal("expected token to verify against its digest")
	}

	if VerifyServiceToken("wrong-token", digest) {
		t.Fatal("expected wrong token to fail verification")
	}
}

func TestServiceTokenUniqueness(t *testing.T) {
	_, digest1 := GenerateServiceToken()
	_, digest2 := GenerateServiceToken()

	if digest1 == digest2 {
		t.Fatal("expected different digests for different tokens")
	}
}

func TestSessionStore(t *testing.T) {
	store := NewSessionStore(1 * time.Hour)

	// Create session
	id := store.Create()
	if id == "" {
		t.Fatal("expected non-empty session ID")
	}

	// Get session
	sess := store.Get(id)
	if sess == nil {
		t.Fatal("expected session to be found")
	}
	if sess.ID != id {
		t.Errorf("expected ID %s, got %s", id, sess.ID)
	}

	// Delete session
	store.Delete(id)
	if store.Get(id) != nil {
		t.Fatal("expected session to be deleted")
	}

	// Invalid session
	if store.Get("invalid-id") != nil {
		t.Fatal("expected nil for invalid session ID")
	}
}

func TestSessionExpiration(t *testing.T) {
	store := NewSessionStore(1 * time.Millisecond)

	id := store.Create()
	time.Sleep(10 * time.Millisecond)

	if store.Get(id) != nil {
		t.Fatal("expected expired session to return nil")
	}
}

func TestSessionInvalidateAll(t *testing.T) {
	store := NewSessionStore(1 * time.Hour)

	id1 := store.Create()
	id2 := store.Create()

	store.InvalidateAll()

	if store.Get(id1) != nil {
		t.Fatal("expected session 1 to be invalidated")
	}
	if store.Get(id2) != nil {
		t.Fatal("expected session 2 to be invalidated")
	}
}

func TestSessionCleanup(t *testing.T) {
	store := NewSessionStore(1 * time.Millisecond)

	id1 := store.Create()
	time.Sleep(10 * time.Millisecond)

	id2 := store.Create()
	removed := store.Cleanup()

	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
	if store.Get(id1) != nil {
		t.Fatal("expected expired session to be cleaned up")
	}
	if store.Get(id2) == nil {
		t.Fatal("expected valid session to remain")
	}
}

func TestSessionCount(t *testing.T) {
	store := NewSessionStore(1 * time.Hour)

	if store.Count() != 0 {
		t.Error("expected 0 sessions")
	}

	store.Create()
	store.Create()
	if store.Count() != 2 {
		t.Errorf("expected 2 sessions, got %d", store.Count())
	}
}

func TestRateLimiter(t *testing.T) {
	limiter := NewRateLimiter(1*time.Second, 3)

	// First 3 attempts should succeed
	if !limiter.Allow("192.168.1.1") {
		t.Error("expected first attempt to be allowed")
	}
	if !limiter.Allow("192.168.1.1") {
		t.Error("expected second attempt to be allowed")
	}
	if !limiter.Allow("192.168.1.1") {
		t.Error("expected third attempt to be allowed")
	}

	// 4th attempt should be rejected
	if limiter.Allow("192.168.1.1") {
		t.Error("expected fourth attempt to be rejected")
	}

	// Different key should still be allowed
	if !limiter.Allow("192.168.1.2") {
		t.Error("expected different key to be allowed")
	}
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	limiter := NewRateLimiter(10*time.Millisecond, 2)

	limiter.Allow("key")
	limiter.Allow("key")
	if limiter.Allow("key") {
		t.Error("expected third attempt to be rejected")
	}

	// Wait for window to expire
	time.Sleep(20 * time.Millisecond)

	if !limiter.Allow("key") {
		t.Error("expected attempt to be allowed after window expires")
	}
}

func TestPHCParsing(t *testing.T) {
	// Test that our generated hash can be parsed
	hash, err := HashPassword("test")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	params, salt, hashBytes, err := parsePHC(hash)
	if err != nil {
		t.Fatalf("parsePHC: %v", err)
	}

	if params.time != Argon2Time {
		t.Errorf("expected time %d, got %d", Argon2Time, params.time)
	}
	if params.memory != Argon2Memory {
		t.Errorf("expected memory %d, got %d", Argon2Memory, params.memory)
	}
	if params.threads != Argon2Threads {
		t.Errorf("expected threads %d, got %d", Argon2Threads, params.threads)
	}
	if len(salt) != Argon2SaltLen {
		t.Errorf("expected salt length %d, got %d", Argon2SaltLen, len(salt))
	}
	if len(hashBytes) != Argon2KeyLen {
		t.Errorf("expected hash length %d, got %d", Argon2KeyLen, len(hashBytes))
	}
}
