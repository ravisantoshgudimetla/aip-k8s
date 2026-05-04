package main

import (
	"encoding/json"
	"net/http"
	"os"
)

type MCPServer struct {
	Name   string    `json:"name"`
	URL    string    `json:"url"`
	Status string    `json:"status"`
	Tools  []MCPTool `json:"tools"`
}

type MCPTool struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"read_only"`
}

// mcpServerResponse is the public DTO for GET /mcp-registry.
// It omits the internal URL field.
type mcpServerResponse struct {
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Tools  []MCPTool `json:"tools"`
}

func (s *Server) handleMCPRegistry(w http.ResponseWriter, r *http.Request) {
	registry := os.Getenv("MCP_REGISTRY")
	if registry == "" {
		writeJSON(w, http.StatusOK, map[string][]mcpServerResponse{"mcp_servers": {}})
		return
	}
	var servers []MCPServer
	if err := json.Unmarshal([]byte(registry), &servers); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid MCP_REGISTRY")
		return
	}
	resp := make([]mcpServerResponse, len(servers))
	for i, srv := range servers {
		resp[i] = mcpServerResponse{
			Name:   srv.Name,
			Status: srv.Status,
			Tools:  srv.Tools,
		}
	}
	writeJSON(w, http.StatusOK, map[string][]mcpServerResponse{"mcp_servers": resp})
}
