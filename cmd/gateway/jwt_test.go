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

func TestJWTManager(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "key.pem")

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
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
	if expiry.Before(fixedTime) {
		t.Error("expiry should be after now")
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

	_ = pub
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

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
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

	_ = pub
}
