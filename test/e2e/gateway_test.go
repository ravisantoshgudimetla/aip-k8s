//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"

	governancev1alpha1 "github.com/agent-control-plane/aip-k8s/api/v1alpha1"
	"github.com/agent-control-plane/aip-k8s/test/utils"
)

// Phase 6: Gateway API — exercises the full agent path through the gateway
// HTTP API. The gateway binary is built from source and started as a subprocess
// pointing at the Kind cluster, so these tests run automatically as part of
// make test-e2e with no extra configuration.

const (
	gwAddr    = ":18081"
	gwBaseURL = "http://localhost:18081"
	gwNS      = "default"
)

var _ = Describe("Phase 6: Gateway API", Ordered, func() {
	var gwProc *exec.Cmd

	BeforeAll(func() {
		projDir, err := utils.GetProjectDir()
		Expect(err).NotTo(HaveOccurred(), "failed to get project dir")
		binPath := projDir + "/bin/gateway"
		cmdPath := projDir + "/cmd/gateway"

		// Controller and CRDs are guaranteed by BeforeSuite. We only need to
		// build and start the gateway subprocess for this test.
		By("building the gateway binary")
		cmd := exec.Command("go", "build", "-o", binPath, cmdPath)
		cmd.Dir = projDir
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "failed to build gateway: %s", string(out))

		By("starting the gateway subprocess")
		// The gateway does not support the --namespace flag
		gwProc = exec.Command(binPath, "--addr="+gwAddr)
		gwProc.Dir = projDir
		gwProc.Stdout = GinkgoWriter
		gwProc.Stderr = GinkgoWriter
		Expect(gwProc.Start()).To(Succeed(), "failed to start gateway")

		By("waiting for gateway /healthz to be ready")
		Eventually(func() int {
			resp, err := http.Get(gwBaseURL + "/healthz") //nolint:noctx
			if err != nil {
				return 0
			}
			defer resp.Body.Close() //nolint:errcheck
			return resp.StatusCode
		}, 30*time.Second, time.Second).Should(Equal(http.StatusOK))

		By("cleaning up any stale resources from previous runs")
		gwCleanup(gwNS)
	})

	AfterAll(func() {
		if gwProc != nil && gwProc.Process != nil {
			_ = gwProc.Process.Kill()
		}
		By("cleaning up gateway e2e resources")
		gwCleanup(gwNS)
	})

	Context("AgentRequest CRUD", func() {
		var createdName string

		It("creates an AgentRequest via POST /agent-requests and returns 201", func() {
			resp, err := gwPost("/agent-requests", `{
				"agentIdentity": "gw-e2e-agent",
				"action":        "gw-e2e-action",
				"targetURI":     "k8s://dev/default/deployment/gw-app",
				"reason":        "gateway e2e smoke test",
				"correlationID": "gw-e2e-corr-001"
			}`)
			Expect(err).NotTo(HaveOccurred())
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusCreated), string(bodyBytes))

			var body map[string]interface{}
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
			createdName, _ = body["name"].(string)
			Expect(createdName).NotTo(BeEmpty())
		})

		It("CRD is visible in the cluster after creation", func() {
			Eventually(func(g Gomega) {
				var ar governancev1alpha1.AgentRequest
				g.Expect(k8sClient.Get(ctx,
					types.NamespacedName{Name: createdName, Namespace: gwNS}, &ar),
				).To(Succeed())
			}, 15*time.Second, time.Second).Should(Succeed())
		})

		It("GET /agent-requests lists the created request", func() {
			Eventually(func(g Gomega) {
				resp, err := http.Get(gwBaseURL + "/agent-requests") //nolint:noctx
				g.Expect(err).NotTo(HaveOccurred())
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				g.Expect(resp.StatusCode).To(Equal(http.StatusOK),
					"gateway returned non-200; body: %s", string(body))
				var items []interface{}
				g.Expect(json.Unmarshal(body, &items)).To(Succeed(),
					"failed to decode response as JSON array; body: %s", string(body))
				g.Expect(len(items)).To(BeNumerically(">=", 1),
					"expected at least 1 item; body: %s", string(body))
			}, 15*time.Second, time.Second).Should(Succeed())
		})

		It("GET /agent-requests/{name} returns the specific request", func() {
			resp, err := http.Get(gwBaseURL + "/agent-requests/" + createdName) //nolint:noctx
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var body map[string]interface{}
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
			// The gateway returns a flat object with "name" at the top level, not nested under "metadata".
			Expect(body["name"]).To(Equal(createdName))
		})

		It("controller reconciles the request to Approved", func() {
			Eventually(func() string {
				return getAgentRequestPhase(createdName, gwNS)
			}, 2*time.Minute, 2*time.Second).Should(Equal("Approved"))
		})

		It("returns 200 OK on a duplicate POST /agent-requests (idempotent)", func() {
			// The dedup key is (agentIdentity, action, targetURI); reason is excluded by design.
			// Using a different reason here intentionally exercises that the gateway does not
			// treat reason as part of the dedup key (see checkDuplicate in cmd/gateway/main.go).
			resp, err := gwPost("/agent-requests", `{
				"agentIdentity": "gw-e2e-agent",
				"action":        "gw-e2e-action",
				"targetURI":     "k8s://dev/default/deployment/gw-app",
				"reason":        "duplicate"
			}`)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})
	})

	Context("Human decision flow", Ordered, func() {
		const policyName = "gw-require-human"
		var pendingName string

		BeforeAll(func() {
			By("creating SafetyPolicy that requires human approval")
			policyJSON := fmt.Sprintf(`{
				"apiVersion": "governance.aip.io/v1alpha1",
				"kind": "SafetyPolicy",
				"metadata": {"name": %q, "namespace": %q},
				"spec": {
					"governedResourceSelector": {},
					"rules": [{"name": "require-human", "type": "StateEvaluation", "action": "RequireApproval", "expression": "true"}],
					"failureMode": "FailClosed"
				}
			}`, policyName, gwNS)
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(policyJSON)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for SafetyPolicy to be visible")
			Eventually(func(g Gomega) {
				var sp governancev1alpha1.SafetyPolicy
				g.Expect(k8sClient.Get(ctx,
					types.NamespacedName{Name: policyName, Namespace: gwNS}, &sp),
				).To(Succeed())
			}, 15*time.Second, time.Second).Should(Succeed())
		})

		It("POST /agent-requests creates a request held for human approval", func() {
			resp, err := gwPost("/agent-requests", `{
				"agentIdentity": "gw-e2e-agent",
				"action":        "gw-human-action",
				"targetURI":     "k8s://dev/default/deployment/human-app",
				"reason":        "gateway human decision e2e"
			}`)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))
			var body map[string]interface{}
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
			pendingName, _ = body["name"].(string)
			Expect(pendingName).NotTo(BeEmpty())
		})

		It("controller sets RequiresApproval condition and holds at Pending", func() {
			Eventually(func(g Gomega) {
				var ar governancev1alpha1.AgentRequest
				g.Expect(k8sClient.Get(ctx,
					types.NamespacedName{Name: pendingName, Namespace: gwNS}, &ar),
				).To(Succeed())
				g.Expect(ar.Status.Phase).To(Equal(governancev1alpha1.PhasePending))
				var hasCondition bool
				for _, c := range ar.Status.Conditions {
					if c.Type == governancev1alpha1.ConditionRequiresApproval && c.Status == "True" {
						hasCondition = true
					}
				}
				g.Expect(hasCondition).To(BeTrue(), "expected RequiresApproval=True condition")
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("POST /agent-requests/{name}/approve transitions to Approved", func() {
			resp, err := gwPost("/agent-requests/"+pendingName+"/approve", `{"reason":"e2e human approval"}`)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			Eventually(func() string {
				return getAgentRequestPhase(pendingName, gwNS)
			}, 30*time.Second, time.Second).Should(Equal("Approved"))
		})

		It("AuditRecord for request.approved is emitted", func() {
			Eventually(func() bool {
				return auditRecordExists(pendingName, gwNS, "request.approved")
			}, 30*time.Second, time.Second).Should(BeTrue())
		})

		It("POST /agent-requests/{name}/deny transitions a pending request to Denied", func() {
			By("creating a second request held for approval")
			resp, err := gwPost("/agent-requests", `{
				"agentIdentity": "gw-e2e-agent",
				"action":        "gw-human-action",
				"targetURI":     "k8s://dev/default/deployment/deny-app",
				"reason":        "gateway human deny e2e"
			}`)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))
			var body map[string]interface{}
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
			denyName, _ := body["name"].(string)
			Expect(denyName).NotTo(BeEmpty())

			By("waiting for RequiresApproval condition")
			Eventually(func() string {
				return getAgentRequestPhase(denyName, gwNS)
			}, 2*time.Minute, 2*time.Second).Should(Equal("Pending"))

			By("denying via gateway")
			resp2, err := gwPost("/agent-requests/"+denyName+"/deny", `{"reason":"e2e human deny"}`)
			Expect(err).NotTo(HaveOccurred())
			defer resp2.Body.Close() //nolint:errcheck
			Expect(resp2.StatusCode).To(Equal(http.StatusOK))

			Eventually(func() string {
				return getAgentRequestPhase(denyName, gwNS)
			}, 30*time.Second, time.Second).Should(Equal("Denied"))
		})
	})

	Context("SSE streaming", Ordered, func() {
		var sseCreatedName string

		BeforeAll(func() {
			By("cleaning up stale resources from previous contexts")
			gwCleanup(gwNS)

			By("creating an AgentRequest for SSE tests")
			resp, err := gwPost("/agent-requests", `{
				"agentIdentity": "gw-sse-agent",
				"action":        "gw-sse-action",
				"targetURI":     "k8s://dev/default/deployment/sse-app",
				"reason":        "sse e2e test"
			}`)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))
			var body map[string]interface{}
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
			sseCreatedName, _ = body["name"].(string)
			Expect(sseCreatedName).NotTo(BeEmpty())

			By("waiting for request to reach Approved")
			Eventually(func() string {
				return getAgentRequestPhase(sseCreatedName, gwNS)
			}, 2*time.Minute, 2*time.Second).Should(Equal("Approved"))
		})

		AfterAll(func() {
			gwCleanup(gwNS)
		})

		It("GET /agent-requests/{name}/watch returns SSE result for approved request", func() {
			resp, err := gwGetSSE("/agent-requests/" + sseCreatedName + "/watch")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(Equal("text/event-stream"))

			bodyBytes, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred())
			events := parseE2ESSEEvents(string(bodyBytes))
			Expect(events).ToNot(BeEmpty())

			last := events[len(events)-1]
			Expect(last.eventType).To(Equal("result"))
			Expect(last.data).To(ContainSubstring(`"phase":"Approved"`))
		})

		It("GET /agent-requests/{name}/watch returns 400 without Accept header", func() {
			resp, err := http.Get(gwBaseURL + "/agent-requests/" + sseCreatedName + "/watch") //nolint:noctx
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("GET /agent-requests/{name}/watch returns 404 for nonexistent request", func() {
			resp, err := gwGetSSE("/agent-requests/does-not-exist-sse/watch")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("SSE stream receives result event during human approval flow", func() {
			const ssePolicyName = "gw-sse-require-human"
			By("creating SafetyPolicy that requires human approval")
			policyJSON := fmt.Sprintf(`{
				"apiVersion": "governance.aip.io/v1alpha1",
				"kind": "SafetyPolicy",
				"metadata": {"name": %q, "namespace": %q},
				"spec": {
					"governedResourceSelector": {},
					"rules": [{"name": "sse-require-human", "type": "StateEvaluation", "action": "RequireApproval", "expression": "true"}],
					"failureMode": "FailClosed"
				}
			}`, ssePolicyName, gwNS)
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(policyJSON)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("creating a request via POST with SSE Accept header")
			type sseResult struct {
				resp *http.Response
				err  error
			}
			resultCh := make(chan sseResult, 1)
			go func() {
				req, reqErr := http.NewRequest("POST", gwBaseURL+"/agent-requests", //nolint:noctx
					strings.NewReader(`{
						"agentIdentity": "gw-sse-agent",
						"action":        "gw-sse-human-action",
						"targetURI":     "k8s://dev/default/deployment/sse-human-app",
						"reason":        "sse human decision e2e"
					}`))
				if reqErr != nil {
					resultCh <- sseResult{err: reqErr}
					return
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "text/event-stream")
				r, e := http.DefaultClient.Do(req)
				resultCh <- sseResult{resp: r, err: e}
			}()

			By("reading SSE response (streams until actionable state)")
			var result sseResult
			Eventually(func() bool {
				select {
				case result = <-resultCh:
					return true
				default:
					return false
				}
			}, 2*time.Minute, time.Second).Should(BeTrue())
			Expect(result.err).NotTo(HaveOccurred())
			defer result.resp.Body.Close() //nolint:errcheck

			Expect(result.resp.Header.Get("Content-Type")).To(Equal("text/event-stream"))
			bodyBytes, err := io.ReadAll(result.resp.Body)
			Expect(err).NotTo(HaveOccurred())
			events := parseE2ESSEEvents(string(bodyBytes))
			Expect(events).ToNot(BeEmpty())

			last := events[len(events)-1]
			Expect(last.eventType).To(Equal("result"))
			Expect(last.data).To(ContainSubstring(`"phase":"Pending"`))
			Expect(last.data).To(ContainSubstring(`RequiresApproval`))
		})
	})

})
}

// gwPost posts JSON to the gateway and returns the response.
func gwPost(path, body string) (*http.Response, error) {
	return http.Post(gwBaseURL+path, "application/json", strings.NewReader(body)) //nolint:noctx
}

// gwCleanup removes all AgentRequests, OpsLock Leases, and the human-approval
// SafetyPolicy in ns. The gateway does not stamp a test-specific label on
// resources it creates, so we delete all rather than relying on a label
// selector.
// OpsLock Leases must be deleted explicitly: deleting the AgentRequest does not
// synchronously release the Lease, so a stale Lease from a previous run blocks
// the next request with "Lock contention" until LOCK_TIMEOUT expires.
func gwCleanup(ns string) {
	cmd := exec.Command("kubectl", "delete", "agentrequest", "--all", "-n", ns, "--ignore-not-found")
	_, _ = utils.Run(cmd)
	// Delete OpsLock Leases (named aip-lock-<hash>).
	cmd := exec.Command("bash", "-c",
		"kubectl get lease -n "+ns+" -o name 2>/dev/null | grep aip-lock- | xargs -r kubectl delete -n "+ns)
	_, _ = utils.Run(cmd)
	cmd = exec.Command("kubectl", "delete", "safetypolicy", "--all", "-n", ns, "--ignore-not-found")
	_, _ = utils.Run(cmd)
}

// gwReadBody reads and closes the response body.
func gwReadBody(resp *http.Response) string {
	defer resp.Body.Close() //nolint:errcheck
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// gwGetSSE sends a GET request with Accept: text/event-stream.
func gwGetSSE(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", gwBaseURL+path, nil) //nolint:noctx
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	return http.DefaultClient.Do(req)
}

type e2eSSEEvent struct {
	eventType string
	data      string
}

func parseE2ESSEEvents(body string) []e2eSSEEvent {
	var events []e2eSSEEvent
	scanner := bufio.NewScanner(strings.NewReader(body))
	var currentType string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			currentType = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			events = append(events, e2eSSEEvent{eventType: currentType, data: after})
			currentType = ""
		}
	}
	return events
}
