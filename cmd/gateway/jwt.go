package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type JWTManager struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	clock      func() time.Time
	mu         sync.RWMutex
}

type AIPClaims struct {
	jwt.RegisteredClaims
	Action  string `json:"action"`
	Repo    string `json:"repo"`
	Request string `json:"request"`
}

func NewJWTManager(keyPath string, clock func() time.Time) (*JWTManager, error) {
	if clock == nil {
		clock = time.Now
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	pk, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 key")
	}
	return &JWTManager{
		privateKey: pk,
		publicKey:  pk.Public().(ed25519.PublicKey),
		clock:      clock,
	}, nil
}

func GenerateEd25519Key(path string) error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	keyData, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create key file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	if err := pem.Encode(file, &pem.Block{Type: "PRIVATE KEY", Bytes: keyData}); err != nil {
		return fmt.Errorf("encode PEM: %w", err)
	}
	return nil
}

func (m *JWTManager) MintToken(agentID, action, repo, requestName string) (string, time.Time, error) {
	now := m.clock()
	expiresAt := now.Add(30 * time.Minute)

	claims := AIPClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "aip-gateway",
			Subject:   agentID,
			ID:        fmt.Sprintf("%s-%d", requestName, now.Unix()),
		},
		Action:  action,
		Repo:    repo,
		Request: requestName,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	m.mu.RLock()
	defer m.mu.RUnlock()
	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return signed, expiresAt, nil
}

func (m *JWTManager) ValidateToken(tokenString string) (*AIPClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &AIPClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodEdDSA {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.publicKey, nil
	},
		jwt.WithTimeFunc(m.clock),
	)
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	claims, ok := token.Claims.(*AIPClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}

func (m *JWTManager) PublicKeyPEM() ([]byte, error) {
	keyData, err := x509.MarshalPKIXPublicKey(m.publicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: "PUBLIC KEY", Bytes: keyData}); err != nil {
		return nil, fmt.Errorf("encode PEM: %w", err)
	}
	return buf.Bytes(), nil
}
