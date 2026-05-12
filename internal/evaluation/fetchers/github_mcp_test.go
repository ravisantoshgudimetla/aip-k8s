package fetchers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestParseGitHubURI_Basic(t *testing.T) {
	g := gomega.NewWithT(t)

	org, repo, branch, filePath, err := parseGitHubURI("github://myorg/myrepo")
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(org).To(gomega.Equal("myorg"))
	g.Expect(repo).To(gomega.Equal("myrepo"))
	g.Expect(branch).To(gomega.Equal(""))
	g.Expect(filePath).To(gomega.Equal(""))
}

func TestParseGitHubURI_WithFile(t *testing.T) {
	g := gomega.NewWithT(t)

	org, repo, branch, filePath, err := parseGitHubURI("github://myorg/myrepo/files/main/config/app.yaml")
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(org).To(gomega.Equal("myorg"))
	g.Expect(repo).To(gomega.Equal("myrepo"))
	g.Expect(branch).To(gomega.Equal("main"))
	g.Expect(filePath).To(gomega.Equal("config/app.yaml"))
}

func TestParseGitHubURI_WithFileNoPath(t *testing.T) {
	g := gomega.NewWithT(t)

	org, repo, branch, filePath, err := parseGitHubURI("github://myorg/myrepo/files/feature-branch")
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(org).To(gomega.Equal("myorg"))
	g.Expect(repo).To(gomega.Equal("myrepo"))
	g.Expect(branch).To(gomega.Equal("feature-branch"))
	g.Expect(filePath).To(gomega.Equal(""))
}

func TestParseGitHubURI_Invalid(t *testing.T) {
	g := gomega.NewWithT(t)

	_, _, _, _, err := parseGitHubURI("https://github.com/org/repo")
	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(err.Error()).To(gomega.ContainSubstring("invalid GitHub URI"))
}

func TestParseGitHubURI_NoOrg(t *testing.T) {
	g := gomega.NewWithT(t)

	_, _, _, _, err := parseGitHubURI("github://")
	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(err.Error()).To(gomega.ContainSubstring("missing org or repo"))
}

func TestFetchGitHubMCP_FileContents(t *testing.T) {
	g := gomega.NewWithT(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.Expect(r.URL.Path).To(gomega.Equal("/tools/call"))

		var rpcReq struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
			Params  struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		g.Expect(json.NewDecoder(r.Body).Decode(&rpcReq)).To(gomega.Succeed())
		g.Expect(rpcReq.JSONRPC).To(gomega.Equal("2.0"))
		g.Expect(rpcReq.Method).To(gomega.Equal("tools/call"))

		callName := rpcReq.Params.Name
		args := rpcReq.Params.Arguments

		switch callName {
		case "get_file_contents":
			g.Expect(args["owner"]).To(gomega.Equal("myorg"))
			g.Expect(args["repo"]).To(gomega.Equal("myrepo"))
			g.Expect(args["path"]).To(gomega.Equal("config.yaml"))
			g.Expect(args["branch"]).To(gomega.Equal("main"))

			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, "event: message")
			_, _ = fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"resource","resource":{"text":"ok: owner=myorg repo=myrepo path=config.yaml branch=main"}}],"isError":false}}`)
		case "list_pull_requests":
			g.Expect(args["owner"]).To(gomega.Equal("myorg"))
			g.Expect(args["repo"]).To(gomega.Equal("myrepo"))
			g.Expect(args["state"]).To(gomega.Equal("open"))

			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, "event: message")
			_, _ = fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"[{\"id\":1},{\"id\":2}]"}],"isError":false}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	fetcher := NewGitHubMCPFetcher(server.URL, time.Second)
	scheme := newScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	uri := "github://myorg/myrepo/files/main/config.yaml"
	jsonRes, err := fetcher.Fetch(context.Background(), c, uri)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(jsonRes).NotTo(gomega.BeNil())

	var result map[string]any
	g.Expect(json.Unmarshal(jsonRes.Raw, &result)).To(gomega.Succeed())
	g.Expect(result["owner"]).To(gomega.Equal("myorg"))
	g.Expect(result["repo"]).To(gomega.Equal("myrepo"))
	g.Expect(result["branch"]).To(gomega.Equal("main"))
	g.Expect(result["filePath"]).To(gomega.Equal("config.yaml"))
	g.Expect(result["fileContent"]).NotTo(gomega.BeNil())
	g.Expect(result["openPRCount"]).To(gomega.Equal(float64(2)))
}

func TestFetchGitHubMCP_RepoOnly(t *testing.T) {
	g := gomega.NewWithT(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.Expect(r.URL.Path).To(gomega.Equal("/tools/call"))

		var rpcReq struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
			Params  struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		g.Expect(json.NewDecoder(r.Body).Decode(&rpcReq)).To(gomega.Succeed())
		g.Expect(rpcReq.JSONRPC).To(gomega.Equal("2.0"))
		g.Expect(rpcReq.Method).To(gomega.Equal("tools/call"))

		callName := rpcReq.Params.Name
		args := rpcReq.Params.Arguments

		if callName == "list_pull_requests" {
			g.Expect(args["owner"]).To(gomega.Equal("myorg"))
			g.Expect(args["repo"]).To(gomega.Equal("myrepo"))
			g.Expect(args["state"]).To(gomega.Equal("open"))

			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, "event: message")
			_, _ = fmt.Fprintln(w, `data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"[{\"id\":1},{\"id\":2},{\"id\":3}]"}],"isError":false}}`)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	fetcher := NewGitHubMCPFetcher(server.URL, time.Second)
	scheme := newScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	uri := "github://myorg/myrepo"
	jsonRes, err := fetcher.Fetch(context.Background(), c, uri)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(jsonRes).NotTo(gomega.BeNil())

	var result map[string]any
	g.Expect(json.Unmarshal(jsonRes.Raw, &result)).To(gomega.Succeed())
	g.Expect(result["owner"]).To(gomega.Equal("myorg"))
	g.Expect(result["repo"]).To(gomega.Equal("myrepo"))
	g.Expect(result["ciStatus"]).To(gomega.Equal("unknown"))
	g.Expect(result["openPRCount"]).To(gomega.Equal(float64(3)))
}

func TestFetchGitHubMCP_ServerUnreachable(t *testing.T) {
	g := gomega.NewWithT(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	fetcher := NewGitHubMCPFetcher(server.URL, time.Second)
	scheme := newScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	_, err := fetcher.Fetch(context.Background(), c, "github://myorg/myrepo/files/main/config.yaml")
	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(err.Error()).To(gomega.ContainSubstring("MCP server unreachable"))
}

func TestFetchGitHubMCP_InvalidURI(t *testing.T) {
	g := gomega.NewWithT(t)

	fetcher := NewGitHubMCPFetcher("http://example.com", time.Second)
	scheme := newScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	_, err := fetcher.Fetch(context.Background(), c, "not-a-github-uri")
	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(err.Error()).To(gomega.ContainSubstring("invalid GitHub URI"))
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		panic(fmt.Sprintf("failed to add corev1 to scheme: %v", err))
	}
	return s
}
