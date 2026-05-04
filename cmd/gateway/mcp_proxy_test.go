package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onsi/gomega"
)

func TestMCPProxy_MissingToken(t *testing.T) {
	g := gomega.NewWithT(t)
	s := &Server{jwtManager: nil, httpClient: &http.Client{}}
	req := httptest.NewRequest("POST", "/mcp-proxy/github/create_pull_request",
		strings.NewReader("{}"))
	req.SetPathValue("server", "github")
	req.SetPathValue("tool", "create_pull_request")
	rr := httptest.NewRecorder()
	s.handleMCPProxy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusServiceUnavailable))
}

func TestToolAllowed_ReadOnlyTool(t *testing.T) {
	g := gomega.NewWithT(t)
	server := &MCPServer{
		Name: "github",
		Tools: []MCPTool{
			{Name: "get_file_contents", ReadOnly: true},
		},
	}
	srv := &Server{}
	g.Expect(srv.toolAllowed(server, "get_file_contents", "create_pull_request")).
		To(gomega.BeTrue())
}

func TestToolAllowed_WriteTool_ActionMatches(t *testing.T) {
	g := gomega.NewWithT(t)
	server := &MCPServer{
		Name: "github",
		Tools: []MCPTool{
			{Name: "create_pull_request", ReadOnly: false},
		},
	}
	srv := &Server{}
	g.Expect(srv.toolAllowed(server, "create_pull_request", "create_pull_request")).
		To(gomega.BeTrue())
}

func TestToolAllowed_WriteTool_ActionMismatch(t *testing.T) {
	g := gomega.NewWithT(t)
	server := &MCPServer{
		Name: "github",
		Tools: []MCPTool{
			{Name: "create_pull_request", ReadOnly: false},
		},
	}
	srv := &Server{}
	g.Expect(srv.toolAllowed(server, "create_pull_request", "list_pull_requests")).
		To(gomega.BeFalse())
}

func TestFindMCPServer_Found(t *testing.T) {
	g := gomega.NewWithT(t)
	if err := os.Setenv("MCP_REGISTRY",
		`[{"name":"github","url":"http://github-mcp:8080","status":`+
			`"available","tools":[{"name":"create_pull_request","read_only":false}]}]`); err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer func() { _ = os.Unsetenv("MCP_REGISTRY") }()

	srv := &Server{}
	server, err := srv.findMCPServer("github")
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(server).NotTo(gomega.BeNil())
	g.Expect(server.URL).To(gomega.Equal("http://github-mcp:8080"))
}

func TestFindMCPServer_NotFound(t *testing.T) {
	g := gomega.NewWithT(t)
	if err := os.Setenv("MCP_REGISTRY",
		`[{"name":"github","url":"http://github-mcp:8080"}]`); err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer func() { _ = os.Unsetenv("MCP_REGISTRY") }()

	srv := &Server{}
	server, err := srv.findMCPServer("nonexistent")
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(server).To(gomega.BeNil())
}

func TestFindMCPServer_EmptyRegistry(t *testing.T) {
	g := gomega.NewWithT(t)
	_ = os.Unsetenv("MCP_REGISTRY")

	srv := &Server{}
	server, err := srv.findMCPServer("github")
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(server).To(gomega.BeNil())
}

func TestFindMCPServer_InvalidJSON(t *testing.T) {
	g := gomega.NewWithT(t)
	if err := os.Setenv("MCP_REGISTRY", "not-valid-json"); err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer func() { _ = os.Unsetenv("MCP_REGISTRY") }()

	srv := &Server{}
	server, err := srv.findMCPServer("github")
	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(server).To(gomega.BeNil())
}
