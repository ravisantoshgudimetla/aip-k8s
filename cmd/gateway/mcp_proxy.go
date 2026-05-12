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

	"github.com/agent-control-plane/aip-k8s/internal/jwt"
	"github.com/agent-control-plane/aip-k8s/internal/mcp"
)

func (s *Server) handleMCPProxy(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server")
	toolName := r.PathValue("tool")

	mcpServer := s.findMCPServer(serverName)
	if mcpServer == nil {
		writeError(w, http.StatusNotFound, "MCP server not found: "+serverName)
		return
	}

	tool := s.findTool(mcpServer, toolName)
	if tool == nil {
		writeError(w, http.StatusNotFound, "tool not found: "+toolName)
		return
	}

	var claims *jwt.Claims
	var agent, action, requestRef string
	if !tool.ReadOnly {
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
		var err error
		claims, err = s.jwtManager.ValidateToken(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token: "+err.Error())
			return
		}
		if claims.Action != toolName {
			writeError(w, http.StatusForbidden, "tool not allowed for this action")
			return
		}
		agent = claims.Subject
		action = claims.Action
		requestRef = claims.Request
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body: "+err.Error())
		return
	}

	rpcBody, args, err := buildJSONRPCRequest(body, toolName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if !tool.ReadOnly {
		if repoErr := enforceRepoClaim(claims.Repo, args); repoErr != "" {
			writeError(w, http.StatusForbidden, repoErr)
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	mcpURL := strings.TrimSuffix(mcpServer.URL, "/") + "/tools/call"
	req, err := http.NewRequestWithContext(ctx, "POST", mcpURL, bytes.NewReader(rpcBody))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	bearerToken := mcpServer.BearerToken
	if bearerToken == "" {
		bearerToken = os.Getenv("AIP_MCP_TOKEN")
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

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

	s.emitMCPLog(agent, serverName, toolName, action, resp.StatusCode, requestRef, mcpURL)

	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("MCP server returned status %d", resp.StatusCode))
		return
	}

	result, rpcErr := extractMCPResult(respBody)
	if rpcErr != "" {
		writeError(w, http.StatusBadGateway, rpcErr)
		return
	}

	if result.IsError {
		contentStr := "MCP tool returned an error"
		if len(result.Content) > 0 {
			var textBlock struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(result.Content[0], &textBlock) == nil && textBlock.Text != "" {
				contentStr = textBlock.Text
			}
		}
		writeError(w, http.StatusBadGateway, contentStr)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// mcpProxyResult is the subset of a JSON-RPC tools/call result returned to callers.
type mcpProxyResult struct {
	Content []json.RawMessage `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

// extractMCPResult parses an SSE response body from the MCP server, unwraps the
// JSON-RPC envelope, and returns the tool result. Returns ("", errMsg) on failure.
func extractMCPResult(body []byte) (mcpProxyResult, string) {
	dataLine, err := mcp.ExtractSSEDataLine(body)
	if err != nil {
		return mcpProxyResult{}, err.Error()
	}

	var rpc struct {
		Result *mcpProxyResult `json:"result,omitempty"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(dataLine), &rpc); err != nil {
		return mcpProxyResult{}, "MCP server returned invalid JSON-RPC response"
	}
	if rpc.Error != nil {
		return mcpProxyResult{}, "MCP server error: " + rpc.Error.Message
	}
	if rpc.Result == nil {
		return mcpProxyResult{}, "MCP server returned empty result"
	}
	return *rpc.Result, ""
}

// enforceRepoClaim validates that the JWT's Repo claim (a github:// URI)
// matches the owner and repo arguments in the proxy request body.
func enforceRepoClaim(claimsRepo string, args map[string]any) string {
	if claimsRepo == "" {
		return "token missing repo claim"
	}
	claimsRepo = strings.TrimPrefix(claimsRepo, "github://")
	parts := strings.SplitN(claimsRepo, "/", 3)
	if len(parts) < 2 {
		return "token has invalid repo claim"
	}
	claimsOwner := parts[0]
	claimsRepoName := parts[1]

	argOwner, _ := args["owner"].(string)
	argRepo, _ := args["repo"].(string)

	if argOwner == "" || argRepo == "" {
		return "request body missing owner or repo arguments"
	}
	if argOwner != claimsOwner {
		return fmt.Sprintf("owner mismatch: token has %q, request has %q", claimsOwner, argOwner)
	}
	if argRepo != claimsRepoName {
		return fmt.Sprintf("repo mismatch: token has %q, request has %q", claimsRepoName, argRepo)
	}
	return ""
}

func (s *Server) findMCPServer(name string) *MCPServer {
	for i := range s.mcpServers {
		if s.mcpServers[i].Name == name {
			return &s.mcpServers[i]
		}
	}
	return nil
}

func (s *Server) findTool(server *MCPServer, toolName string) *MCPTool {
	for _, tool := range server.Tools {
		if tool.Name == toolName {
			return &tool
		}
	}
	return nil
}

// buildJSONRPCRequest converts the caller's tool-call body to a JSON-RPC 2.0
// request suitable for forwarding to the MCP server.
func buildJSONRPCRequest(body []byte, toolName string) ([]byte, map[string]any, error) {
	var payload struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, err
	}
	if payload.Name != "" && payload.Name != toolName {
		return nil, nil, fmt.Errorf("tool name mismatch: body has %q, path has %q", payload.Name, toolName)
	}
	payload.Name = toolName

	rpcReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": payload.Arguments,
		},
	}
	encoded, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, nil, err
	}
	return encoded, payload.Arguments, nil
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
