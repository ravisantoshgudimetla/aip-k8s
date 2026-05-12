package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

type MCPServer struct {
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Status      string    `json:"status"`
	Tools       []MCPTool `json:"tools"`
	BearerToken string    `json:"bearer_token,omitempty"`
}

type MCPTool struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"read_only"`
}

type mcpServerResponse struct {
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Tools  []MCPTool `json:"tools"`
}

func loadMCPRegistry() ([]MCPServer, error) {
	registry := os.Getenv("MCP_REGISTRY")
	if registry == "" {
		return nil, nil
	}
	var servers []MCPServer
	if err := json.Unmarshal([]byte(registry), &servers); err != nil {
		return nil, fmt.Errorf("parse MCP_REGISTRY: %w", err)
	}
	return servers, nil
}

func (s *Server) handleMCPRegistry(w http.ResponseWriter, r *http.Request) {
	resp := make([]mcpServerResponse, len(s.mcpServers))
	for i, srv := range s.mcpServers {
		resp[i] = mcpServerResponse{
			Name:   srv.Name,
			Status: srv.Status,
			Tools:  srv.Tools,
		}
	}
	writeJSON(w, http.StatusOK, map[string][]mcpServerResponse{"mcp_servers": resp})
}
