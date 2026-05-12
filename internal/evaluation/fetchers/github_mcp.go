package fetchers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/agent-control-plane/aip-k8s/internal/mcp"
)

const (
	defaultMCPHTTPTimeout = 15 * time.Second
	defaultPRPerPage      = 100
)

// GitHubMCPFetcher fetches provider context from a GitHub MCP server.
type GitHubMCPFetcher struct {
	baseURL    string
	httpClient *http.Client
}

// NewGitHubMCPFetcher creates a new fetcher pointing at the given MCP server URL.
func NewGitHubMCPFetcher(baseURL string, timeout time.Duration) *GitHubMCPFetcher {
	if timeout <= 0 {
		timeout = defaultMCPHTTPTimeout
	}
	return &GitHubMCPFetcher{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: timeout},
	}
}

// DefaultGitHubMCPFetcher returns a fetcher configured for the in-cluster
// github-mcp service.
func DefaultGitHubMCPFetcher() *GitHubMCPFetcher {
	return NewGitHubMCPFetcher("http://github-mcp.aip-k8s-system.svc", defaultMCPHTTPTimeout)
}

type mcpJSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  mcpToolCall `json:"params"`
}

type mcpToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type mcpJSONRPCResponse struct {
	ID     int              `json:"id"`
	Result *mcpToolResult   `json:"result,omitempty"`
	Error  *mcpToolRPCError `json:"error,omitempty"`
}

type mcpToolResult struct {
	Content []mcpContentBlock `json:"content"`
	IsError bool              `json:"isError"`
}

type mcpContentBlock struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	Resource *mcpResource `json:"resource,omitempty"`
}

type mcpResource struct {
	Text string `json:"text"`
}

type mcpToolRPCError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func parseGitHubURI(targetURI string) (org, repo, branch, filePath string, err error) {
	u, perr := url.Parse(targetURI)
	if perr != nil || u.Scheme != "github" {
		return "", "", "", "", fmt.Errorf("parseGitHubURI: invalid GitHub URI %q", targetURI)
	}

	org = u.Host
	rest := strings.TrimPrefix(u.Path, "/")
	if org == "" || rest == "" {
		return "", "", "", "", fmt.Errorf("invalid GitHub URI: %s (missing org or repo)", targetURI)
	}

	if strings.Contains(rest, "/files/") {
		parts := strings.SplitN(rest, "/files/", 2)
		repo = parts[0]
		rest = parts[1]
		branch, filePath, _ = strings.Cut(rest, "/")
	} else {
		repo = rest
	}

	return org, repo, branch, filePath, nil
}

func mcpToken() string {
	return os.Getenv("AIP_MCP_TOKEN")
}

func (f *GitHubMCPFetcher) callMCPTool(ctx context.Context, toolName string, args map[string]any) (*mcpToolResult, error) {
	rpcReq := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  mcpToolCall{Name: toolName, Arguments: args},
	}
	encoded, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("failed to encode MCP tool call: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", f.baseURL+"/tools/call", bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("failed to build MCP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if tok := mcpToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("MCP server unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MCP server returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read MCP response body: %w", err)
	}

	dataLine, err := mcp.ExtractSSEDataLine(body)
	if err != nil {
		return nil, err
	}

	var rpcResp mcpJSONRPCResponse
	if err := json.Unmarshal([]byte(dataLine), &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to decode MCP response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("MCP tool error: %s", rpcResp.Error.Message)
	}
	if rpcResp.Result == nil {
		return nil, fmt.Errorf("MCP response missing result")
	}
	if rpcResp.Result.IsError {
		msg := "MCP tool returned an error"
		if len(rpcResp.Result.Content) > 0 {
			msg = rpcResp.Result.Content[0].Text
		}
		return nil, fmt.Errorf("MCP tool error: %s", msg)
	}

	return rpcResp.Result, nil
}

func extractTextContent(resp *mcpToolResult) string {
	for _, block := range resp.Content {
		if block.Type == "resource" && block.Resource != nil {
			return block.Resource.Text
		}
	}
	for _, block := range resp.Content {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

// Fetch fetches provider context from the GitHub MCP server for a given target URI.
func (f *GitHubMCPFetcher) Fetch(ctx context.Context, _ client.Client, targetURI string) (*apiextensionsv1.JSON, error) {
	org, repoName, branch, filePath, err := parseGitHubURI(targetURI)
	if err != nil {
		return nil, err
	}

	result := map[string]any{}

	if filePath != "" {
		args := map[string]any{
			"owner": org,
			"repo":  repoName,
			"path":  filePath,
		}
		if branch != "" {
			args["branch"] = branch
		}

		resp, err := f.callMCPTool(ctx, "get_file_contents", args)
		if err != nil {
			return nil, fmt.Errorf("MCP get_file_contents failed: %w", err)
		}

		text := extractTextContent(resp)
		var content any
		if json.Unmarshal([]byte(text), &content) == nil {
			result["fileContent"] = content
		} else {
			result["fileContent"] = text
		}
		result["owner"] = org
		result["repo"] = repoName
		if branch != "" {
			result["branch"] = branch
		}
		result["filePath"] = filePath
	} else {
		result["owner"] = org
		result["repo"] = repoName
		result["ciStatus"] = "unknown"
	}

	prResp, err := f.callMCPTool(ctx, "list_pull_requests", map[string]any{
		"owner":    org,
		"repo":     repoName,
		"state":    "open",
		"per_page": defaultPRPerPage,
	})
	if err == nil {
		text := extractTextContent(prResp)
		var prs []any
		if json.Unmarshal([]byte(text), &prs) == nil {
			result["openPRCount"] = len(prs)
		}
	}

	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result failed: %w", err)
	}
	return &apiextensionsv1.JSON{Raw: raw}, nil
}
