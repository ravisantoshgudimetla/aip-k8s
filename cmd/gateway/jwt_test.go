package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestPrivateKeyFile(t *testing.T, keyPath string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyData, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	f, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: keyData}); err != nil {
		t.Fatalf("encode PEM: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}

func TestJWTManager(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "key.pem")
	writeTestPrivateKeyFile(t, keyPath)

	fixedTime := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	mgr, err := NewJWTManager(keyPath, func() time.Time { return fixedTime })
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	token, expiry, err := mgr.MintToken("agent-123", "pull-request", "owner/repo", "req-001")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
	wantExpiry := fixedTime.Add(30 * time.Minute)
	if !expiry.Equal(wantExpiry) {
		t.Errorf("expiry = %v, want %v", expiry, wantExpiry)
	}

	claims, err := mgr.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.Subject != "agent-123" {
		t.Errorf("subject = %q, want %q", claims.Subject, "agent-123")
	}
	if claims.Action != "pull-request" {
		t.Errorf("action = %q, want %q", claims.Action, "pull-request")
	}
	if claims.Repo != "owner/repo" {
		t.Errorf("repo = %q, want %q", claims.Repo, "owner/repo")
	}
	if claims.Request != "req-001" {
		t.Errorf("request = %q, want %q", claims.Request, "req-001")
	}

	_, err = mgr.ValidateToken("invalid-token")
	if err == nil {
		t.Error("expected error for invalid token")
	}

	publicPEM, err := mgr.PublicKeyPEM()
	if err != nil {
		t.Fatalf("PublicKeyPEM: %v", err)
	}
	if len(publicPEM) == 0 {
		t.Error("expected non-empty public key PEM")
	}
}

func TestGenerateEd25519Key(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test-key.pem")

	if err := GenerateEd25519Key(keyPath); err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	_, ok := key.(ed25519.PrivateKey)
	if !ok {
		t.Error("key is not Ed25519")
	}
}

func TestJWTExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "key.pem")
	writeTestPrivateKeyFile(t, keyPath)

	pastTime := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	mgr, err := NewJWTManager(keyPath, func() time.Time { return pastTime })
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	token, _, err := mgr.MintToken("agent-123", "pull-request", "owner/repo", "req-001")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	laterTime := time.Date(2020, 1, 1, 12, 35, 0, 0, time.UTC)
	expiredMgr, err := NewJWTManager(keyPath, func() time.Time { return laterTime })
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	_, err = expiredMgr.ValidateToken(token)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestJWTKeyReload(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "key.pem")
	writeTestPrivateKeyFile(t, keyPath)

	// Use a fixed clock so we can advance past the grace period
	now := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	mgr, err := NewJWTManager(keyPath, clock)
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	token, _, err := mgr.MintToken("agent-1", "action", "repo", "req-1")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	// Validate with current key succeeds
	_, err = mgr.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken before reload: %v", err)
	}

	// Generate a new key and reload
	if err := GenerateEd25519Key(keyPath); err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	if err := mgr.reloadKey(keyPath); err != nil {
		t.Fatalf("reloadKey: %v", err)
	}

	// New tokens minted with new key validate
	token2, _, err := mgr.MintToken("agent-2", "action", "repo", "req-2")
	if err != nil {
		t.Fatalf("MintToken after reload: %v", err)
	}
	_, err = mgr.ValidateToken(token2)
	if err != nil {
		t.Fatalf("ValidateToken new token after reload: %v", err)
	}

	// Advance past the 5-minute grace period; old token should now fail
	now = now.Add(6 * time.Minute)
	_, err = mgr.ValidateToken(token)
	if err == nil {
		t.Error("expected error for token signed with old key after grace period expired")
	}
}

func TestJWTKeyReloadGracePeriod(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "key.pem")
	writeTestPrivateKeyFile(t, keyPath)

	// Use a fixed clock so we can control time precisely
	startTime := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return startTime }

	mgr, err := NewJWTManager(keyPath, clock)
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	token, _, err := mgr.MintToken("agent-1", "action", "repo", "req-1")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	// Generate new key and reload at T+0
	if err := GenerateEd25519Key(keyPath); err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	if err := mgr.reloadKey(keyPath); err != nil {
		t.Fatalf("reloadKey: %v", err)
	}

	// At T+3min (within 5-min grace), old token should still validate
	startTime = startTime.Add(3 * time.Minute)
	_, err = mgr.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken within grace period: %v", err)
	}

	// At T+6min (past 5-min grace), old token should fail
	startTime = startTime.Add(3 * time.Minute)
	_, err = mgr.ValidateToken(token)
	if err == nil {
		t.Error("expected error for token signed with old key after grace period expired")
	}
}
