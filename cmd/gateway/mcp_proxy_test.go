package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onsi/gomega"
)

func TestMCPProxy_JWTNotConfigured(t *testing.T) {
	g := gomega.NewWithT(t)
	s := &Server{jwtManager: nil, httpClient: &http.Client{},
		mcpServers: []MCPServer{
			{Name: "github", URL: "http://example.com", Status: "available",
				Tools: []MCPTool{{Name: "create_pull_request", ReadOnly: false}}},
		},
	}
	req := httptest.NewRequest("POST", "/mcp-proxy/github/create_pull_request",
		strings.NewReader("{}"))
	req.SetPathValue("server", "github")
	req.SetPathValue("tool", "create_pull_request")
	rr := httptest.NewRecorder()
	s.handleMCPProxy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusServiceUnavailable))
}

func TestFindMCPServer_Found(t *testing.T) {
	g := gomega.NewWithT(t)
	srv := &Server{
		mcpServers: []MCPServer{
			{Name: "github", URL: "http://github-mcp:8080", Status: "available",
				Tools: []MCPTool{{Name: "create_pull_request", ReadOnly: false}}},
		},
	}
	server := srv.findMCPServer("github")
	g.Expect(server).NotTo(gomega.BeNil())
	g.Expect(server.URL).To(gomega.Equal("http://github-mcp:8080"))
}

func TestFindMCPServer_NotFound(t *testing.T) {
	g := gomega.NewWithT(t)
	srv := &Server{
		mcpServers: []MCPServer{
			{Name: "github", URL: "http://github-mcp:8080"},
		},
	}
	server := srv.findMCPServer("nonexistent")
	g.Expect(server).To(gomega.BeNil())
}

func TestFindMCPServer_EmptyRegistry(t *testing.T) {
	g := gomega.NewWithT(t)
	srv := &Server{}
	server := srv.findMCPServer("github")
	g.Expect(server).To(gomega.BeNil())
}

func TestEnforceRepoClaim_Match(t *testing.T) {
	g := gomega.NewWithT(t)
	errStr := enforceRepoClaim("github://acme/demo", map[string]any{"owner": "acme", "repo": "demo"})
	g.Expect(errStr).To(gomega.BeEmpty())
}

func TestEnforceRepoClaim_OwnerMismatch(t *testing.T) {
	g := gomega.NewWithT(t)
	errStr := enforceRepoClaim("github://acme/demo", map[string]any{"owner": "evil", "repo": "demo"})
	g.Expect(errStr).To(gomega.ContainSubstring("owner mismatch"))
}

func TestEnforceRepoClaim_RepoMismatch(t *testing.T) {
	g := gomega.NewWithT(t)
	errStr := enforceRepoClaim("github://acme/demo", map[string]any{"owner": "acme", "repo": "evil"})
	g.Expect(errStr).To(gomega.ContainSubstring("repo mismatch"))
}

func TestEnforceRepoClaim_MissingOwnerOrRepo(t *testing.T) {
	g := gomega.NewWithT(t)
	errStr := enforceRepoClaim("github://acme/demo", map[string]any{"owner": "acme"})
	g.Expect(errStr).To(gomega.ContainSubstring("missing owner or repo"))
}

func TestEnforceRepoClaim_EmptyRepo(t *testing.T) {
	g := gomega.NewWithT(t)
	errStr := enforceRepoClaim("", map[string]any{"owner": "acme", "repo": "demo"})
	g.Expect(errStr).To(gomega.ContainSubstring("missing repo claim"))
}

func TestEnforceRepoClaim_InvalidURI(t *testing.T) {
	g := gomega.NewWithT(t)
	errStr := enforceRepoClaim("github://acme", map[string]any{"owner": "acme", "repo": "demo"})
	g.Expect(errStr).To(gomega.ContainSubstring("invalid repo claim"))
}

func TestFindTool_Found(t *testing.T) {
	g := gomega.NewWithT(t)
	server := &MCPServer{Tools: []MCPTool{{Name: "get_file_contents", ReadOnly: true}}}
	srv := &Server{}
	tool := srv.findTool(server, "get_file_contents")
	g.Expect(tool).NotTo(gomega.BeNil())
	g.Expect(tool.ReadOnly).To(gomega.BeTrue())
}

func TestFindTool_NotFound(t *testing.T) {
	g := gomega.NewWithT(t)
	server := &MCPServer{Tools: []MCPTool{{Name: "get_file_contents", ReadOnly: true}}}
	srv := &Server{}
	tool := srv.findTool(server, "create_pull_request")
	g.Expect(tool).To(gomega.BeNil())
}
