package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/onsi/gomega"
)

func TestHandleMCPRegistry_Empty(t *testing.T) {
	g := gomega.NewWithT(t)
	_ = os.Unsetenv("MCP_REGISTRY")

	s := &Server{}
	req := httptest.NewRequest("GET", "/mcp-registry", nil)
	rr := httptest.NewRecorder()
	s.handleMCPRegistry(rr, req)

	g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring(`"mcp_servers":[]`))
}

func TestHandleMCPRegistry_Populated(t *testing.T) {
	g := gomega.NewWithT(t)
	if err := os.Setenv("MCP_REGISTRY",
		`[{"name":"github","url":"http://github-mcp","status":"available",`+
			`"tools":[{"name":"create_pull_request","read_only":false},`+
			`{"name":"get_file_contents","read_only":true}]}]`); err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer func() { _ = os.Unsetenv("MCP_REGISTRY") }()

	s := &Server{}
	req := httptest.NewRequest("GET", "/mcp-registry", nil)
	rr := httptest.NewRecorder()
	s.handleMCPRegistry(rr, req)

	g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring("github"))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring("create_pull_request"))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring("get_file_contents"))
}

func TestHandleMCPRegistry_InvalidJSON(t *testing.T) {
	g := gomega.NewWithT(t)
	if err := os.Setenv("MCP_REGISTRY", "not-valid-json"); err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer func() { _ = os.Unsetenv("MCP_REGISTRY") }()

	s := &Server{}
	req := httptest.NewRequest("GET", "/mcp-registry", nil)
	rr := httptest.NewRecorder()
	s.handleMCPRegistry(rr, req)

	g.Expect(rr.Code).To(gomega.Equal(http.StatusInternalServerError))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring("invalid MCP_REGISTRY"))
}

func TestHandleMCPRegistry_URLNotLeaked(t *testing.T) {
	g := gomega.NewWithT(t)
	if err := os.Setenv("MCP_REGISTRY",
		`[{"name":"github","url":"http://internal:8080","status":"available",`+
			`"tools":[{"name":"get_file_contents","read_only":true}]}]`); err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer func() { _ = os.Unsetenv("MCP_REGISTRY") }()

	s := &Server{}
	req := httptest.NewRequest("GET", "/mcp-registry", nil)
	rr := httptest.NewRecorder()
	s.handleMCPRegistry(rr, req)

	g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))
	g.Expect(rr.Body.String()).To(gomega.ContainSubstring("github"))
	g.Expect(rr.Body.String()).NotTo(gomega.ContainSubstring("http://internal:8080"))
}
