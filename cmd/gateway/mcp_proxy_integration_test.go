package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agent-control-plane/aip-k8s/internal/jwt"
	"github.com/onsi/gomega"
)

func generateTestKeyFile(t *testing.T) (string, func()) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp("", "jwt-test-*.key")
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name(), func() {
		if err := os.Remove(f.Name()); err != nil {
			t.Logf("cleanup: %v", err)
		}
	}
}

func TestMCPProxy_FullFlow_ValidJWT(t *testing.T) {
	g := gomega.NewWithT(t)

	// Start mock MCP upstream
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.Expect(r.URL.Path).To(gomega.Equal("/tools/call"))
		body, _ := io.ReadAll(r.Body)
		g.Expect(string(body)).To(gomega.Equal(`{"name":"get_file_contents","owner":"acme","repo":"demo"}`))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"file contents"}`))
	}))
	defer upstream.Close()

	// Set registry to point at mock upstream
	registry := `[{"name":"github","url":"` + upstream.URL +
		`","status":"available","tools":[{"name":"get_file_contents","read_only":true}]}]`
	if err := os.Setenv("MCP_REGISTRY", registry); err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("MCP_REGISTRY"); err != nil {
			t.Logf("cleanup: %v", err)
		}
	}()

	// Generate test key and JWT manager
	keyPath, cleanup := generateTestKeyFile(t)
	defer cleanup()
	mgr, err := jwt.NewManager(keyPath, time.Now)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	// Mint a token for get_file_contents action
	token, _, err := mgr.MintToken("agent-1", "get_file_contents", "acme/demo", "req-123")
	g.Expect(err).NotTo(gomega.HaveOccurred())

	s := &Server{jwtManager: mgr, httpClient: &http.Client{Timeout: 5 * time.Second}}
	req := httptest.NewRequest("POST", "/mcp-proxy/github/get_file_contents",
		strings.NewReader(`{"owner":"acme","repo":"demo"}`))
	req.SetPathValue("server", "github")
	req.SetPathValue("tool", "get_file_contents")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	s.handleMCPProxy(rr, req)

	g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring("file contents"))
}

func TestMCPProxy_InvalidJWT(t *testing.T) {
	g := gomega.NewWithT(t)

	keyPath, cleanup := generateTestKeyFile(t)
	defer cleanup()
	mgr, err := jwt.NewManager(keyPath, time.Now)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	s := &Server{jwtManager: mgr, httpClient: &http.Client{}}
	req := httptest.NewRequest("POST", "/mcp-proxy/github/get_file_contents",
		strings.NewReader("{}"))
	req.SetPathValue("server", "github")
	req.SetPathValue("tool", "get_file_contents")
	req.Header.Set("Authorization", "Bearer invalid-token")
	rr := httptest.NewRecorder()

	s.handleMCPProxy(rr, req)

	g.Expect(rr.Code).To(gomega.Equal(http.StatusUnauthorized))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring("invalid token"))
}

func TestMCPProxy_UnknownServer(t *testing.T) {
	g := gomega.NewWithT(t)

	if err := os.Setenv("MCP_REGISTRY",
		`[{"name":"github","url":"http://example.com","status":"available","tools":[]}]`); err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("MCP_REGISTRY"); err != nil {
			t.Logf("cleanup: %v", err)
		}
	}()

	keyPath, cleanup := generateTestKeyFile(t)
	defer cleanup()
	mgr, err := jwt.NewManager(keyPath, time.Now)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	token, _, err := mgr.MintToken("agent-1", "get_file_contents", "acme/demo", "req-123")
	g.Expect(err).NotTo(gomega.HaveOccurred())

	s := &Server{jwtManager: mgr, httpClient: &http.Client{}}
	req := httptest.NewRequest("POST", "/mcp-proxy/jira/get_file_contents",
		strings.NewReader("{}"))
	req.SetPathValue("server", "jira")
	req.SetPathValue("tool", "get_file_contents")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	s.handleMCPProxy(rr, req)

	g.Expect(rr.Code).To(gomega.Equal(http.StatusNotFound))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring("MCP server not found"))
}

func TestMCPProxy_UnknownTool(t *testing.T) {
	g := gomega.NewWithT(t)

	if err := os.Setenv("MCP_REGISTRY",
		`[{"name":"github","url":"http://example.com","status":"available",`+
			`"tools":[{"name":"get_file_contents","read_only":true}]}]`); err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("MCP_REGISTRY"); err != nil {
			t.Logf("cleanup: %v", err)
		}
	}()

	keyPath, cleanup := generateTestKeyFile(t)
	defer cleanup()
	mgr, err := jwt.NewManager(keyPath, time.Now)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	token, _, err := mgr.MintToken("agent-1", "get_file_contents", "acme/demo", "req-123")
	g.Expect(err).NotTo(gomega.HaveOccurred())

	s := &Server{jwtManager: mgr, httpClient: &http.Client{}}
	req := httptest.NewRequest("POST", "/mcp-proxy/github/create_pull_request",
		strings.NewReader("{}"))
	req.SetPathValue("server", "github")
	req.SetPathValue("tool", "create_pull_request")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	s.handleMCPProxy(rr, req)

	g.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring("tool not allowed"))
}
