package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func (s *Server) handleMCPProxy(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server")
	toolName := r.PathValue("tool")

	if s.jwtManager == nil {
		writeError(w, http.StatusServiceUnavailable, "JWT signing not configured")
		return
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	claims, err := s.jwtManager.ValidateToken(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid token: "+err.Error())
		return
	}

	mcpServer, err := s.findMCPServer(serverName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid MCP_REGISTRY: "+err.Error())
		return
	}
	if mcpServer == nil {
		writeError(w, http.StatusNotFound, "MCP server not found: "+serverName)
		return
	}

	if !s.toolAllowed(mcpServer, toolName, claims.Action) {
		writeError(w, http.StatusForbidden, "tool not allowed for this action")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body: "+err.Error())
		return
	}

	// Bind validated toolName into the request body so the MCP server
	// receives the correct tool name even if the caller sent a different one.
	body, err = bindToolName(body, toolName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	mcpURL := strings.TrimSuffix(mcpServer.URL, "/") + "/tools/call"
	req, err := http.NewRequestWithContext(ctx, "POST", mcpURL, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create request")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "MCP server unavailable: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read MCP response")
		return
	}

	s.emitMCPLog(claims.Subject, serverName, toolName, claims.Action,
		resp.StatusCode, claims.Request, mcpURL)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

func (s *Server) findMCPServer(name string) (*MCPServer, error) {
	registry := os.Getenv("MCP_REGISTRY")
	if registry == "" {
		return nil, nil
	}
	var servers []MCPServer
	if err := json.Unmarshal([]byte(registry), &servers); err != nil {
		return nil, fmt.Errorf("parse MCP_REGISTRY: %w", err)
	}
	for i := range servers {
		if servers[i].Name == name {
			return &servers[i], nil
		}
	}
	return nil, nil
}

func (s *Server) toolAllowed(server *MCPServer, toolName, action string) bool {
	for _, tool := range server.Tools {
		if tool.Name == toolName {
			if tool.ReadOnly {
				return true
			}
			return tool.Name == action
		}
	}
	return false
}

// bindToolName injects the validated toolName into the JSON body under the
// "name" key. It rejects requests where the body already contains a different
// tool name to prevent parameter mismatches.
func bindToolName(body []byte, toolName string) ([]byte, error) {
	var payload map[string]any
	if len(body) == 0 {
		payload = map[string]any{"name": toolName}
		return json.Marshal(payload)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if existing, ok := payload["name"].(string); ok && existing != "" && existing != toolName {
		return nil, fmt.Errorf("tool name mismatch: body has %q, path has %q", existing, toolName)
	}
	payload["name"] = toolName
	return json.Marshal(payload)
}

type mcpProxyLog struct {
	Timestamp  string `json:"timestamp"`
	Agent      string `json:"agent"`
	Server     string `json:"server"`
	Tool       string `json:"tool"`
	Action     string `json:"action"`
	Status     int    `json:"status"`
	RequestRef string `json:"requestRef,omitempty"`
	TargetURI  string `json:"targetURI"`
}

func (s *Server) emitMCPLog(agent, server, tool, action string,
	status int, requestRef, targetURI string) {
	entry := mcpProxyLog{
		Timestamp:  time.Now().Format(time.RFC3339),
		Agent:      agent,
		Server:     server,
		Tool:       tool,
		Action:     action,
		Status:     status,
		RequestRef: requestRef,
		TargetURI:  targetURI,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("failed to marshal MCP proxy log: %v", err)
		return
	}
	log.Printf("MCP_PROXY %s", string(data))
}
