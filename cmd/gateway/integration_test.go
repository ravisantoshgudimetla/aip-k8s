package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/ravisantoshgudimetla/aip-k8s/api/v1alpha1"
	"github.com/ravisantoshgudimetla/aip-k8s/internal/controller"
	"github.com/ravisantoshgudimetla/aip-k8s/internal/evaluation"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	testAgentSub    = "agent-sub"
	testReviewerSub = "reviewer-sub"
	testDefaultNS   = "default"
)

func TestGatewayIntegration(t *testing.T) {
	g := gomega.NewWithT(t)

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
	}
	if dir := getFirstFoundEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	cfg, err := testEnv.Start()
	g.Expect(err).NotTo(gomega.HaveOccurred())
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("Failed to stop testEnv: %v", err)
		}
	})

	err = v1alpha1.AddToScheme(scheme.Scheme)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	err = coordinationv1.AddToScheme(scheme.Scheme)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	directClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	g.Expect(err).NotTo(gomega.HaveOccurred())

	mgrClient := startTestManager(t, cfg)
	ctx := context.Background()

	// Wait for cache to be ready
	g.Eventually(func() error {
		var list v1alpha1.AgentRequestList
		return mgrClient.List(ctx, &list)
	}, 5*time.Second, 100*time.Millisecond).Should(gomega.Succeed())

	runBasicTests(t, mgrClient, directClient, ctx)
	runAuthAndApprovalTests(t, mgrClient, directClient, ctx)
}

func runBasicTests(t *testing.T, mgrClient, directClient client.Client, ctx context.Context) {
	t.Run("Full lifecycle - Pending to Approved", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       mgrClient,
			dedupWindow:  0,
			waitTimeout:  10 * time.Second,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		body := createAgentRequestBody{
			AgentIdentity: "agent-1",
			Action:        "restart",
			TargetURI:     "k8s://prod/default/deployment/full-lifecycle",
			Reason:        "test",
			Namespace:     testDefaultNS,
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
		rr := httptest.NewRecorder()

		respCh := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			s.handleCreateAgentRequest(rr, req)
			respCh <- rr
		}()

		var resp *httptest.ResponseRecorder
		gm.Eventually(respCh, 15*time.Second).Should(gomega.Receive(&resp))
		gm.Expect(resp.Code).To(gomega.Equal(http.StatusCreated))

		var respBody map[string]any
		gm.Expect(json.Unmarshal(resp.Body.Bytes(), &respBody)).To(gomega.Succeed())
		gm.Expect(respBody["phase"]).To(gomega.Equal(string(v1alpha1.PhaseApproved)))

		cleanup(ctx, gm, directClient)
	})

	t.Run("Idempotent duplicate - returns 200 immediately", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       mgrClient,
			dedupWindow:  24 * time.Hour,
			waitTimeout:  10 * time.Second,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		const targetURI = "k8s://prod/default/deployment/dup-test"
		policy := createApprovalPolicy(ctx, gm, directClient, "dup-test-policy", targetURI)

		ar := &v1alpha1.AgentRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dup-test",
				Namespace: testDefaultNS,
				Labels:    map[string]string{"aip.io/agentIdentity": "agent-dup"},
			},
			Spec: v1alpha1.AgentRequestSpec{
				AgentIdentity: "agent-dup",
				Action:        "restart",
				Target:        v1alpha1.Target{URI: targetURI},
				Reason:        "test",
			},
		}
		gm.Expect(directClient.Create(ctx, ar)).To(gomega.Succeed())

		gm.Eventually(func() string {
			var current v1alpha1.AgentRequest
			key := types.NamespacedName{Name: "dup-test", Namespace: testDefaultNS}
			if err := mgrClient.Get(ctx, key, &current); err != nil {
				return ""
			}
			return current.Status.Phase
		}, 10*time.Second).Should(gomega.Equal(v1alpha1.PhasePending))

		body := createAgentRequestBody{
			AgentIdentity: "agent-dup",
			Action:        "restart",
			TargetURI:     targetURI,
			Reason:        "test",
			Namespace:     testDefaultNS,
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
		rr := httptest.NewRecorder()

		s.handleCreateAgentRequest(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var respBody map[string]any
		gm.Expect(json.Unmarshal(rr.Body.Bytes(), &respBody)).To(gomega.Succeed())
		gm.Expect(respBody["phase"]).To(gomega.Equal(string(v1alpha1.PhasePending)))

		gm.Expect(directClient.Delete(ctx, policy)).To(gomega.Succeed())
		cleanup(ctx, gm, directClient)
	})

	t.Run("Dedup window expired - new request allowed", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       mgrClient,
			dedupWindow:  100 * time.Millisecond,
			waitTimeout:  1 * time.Second,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		body := createAgentRequestBody{
			AgentIdentity: "agent-old",
			Action:        "restart",
			TargetURI:     "k8s://prod/default/deployment/dedup-expired",
			Reason:        "test",
			Namespace:     testDefaultNS,
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
		rr := httptest.NewRecorder()

		s.handleCreateAgentRequest(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusGatewayTimeout))

		time.Sleep(200 * time.Millisecond)

		rr2 := httptest.NewRecorder()
		req2 := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
		s.handleCreateAgentRequest(rr2, req2)
		gm.Expect(rr2.Code).To(gomega.Equal(http.StatusGatewayTimeout))

		cleanup(ctx, gm, directClient)
	})

	t.Run("Poll loop timeout - returns 504", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       mgrClient,
			dedupWindow:  0,
			waitTimeout:  500 * time.Millisecond,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		body := createAgentRequestBody{
			AgentIdentity: "agent-timeout",
			Action:        "restart",
			TargetURI:     "k8s://prod/default/deployment/timeout",
			Reason:        "test",
			Namespace:     testDefaultNS,
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
		rr := httptest.NewRecorder()

		s.handleCreateAgentRequest(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusGatewayTimeout))

		cleanup(ctx, gm, directClient)
	})
}

func runAuthAndApprovalTests(t *testing.T, mgrClient, directClient client.Client, ctx context.Context) {
	t.Run("RequiresApproval condition - returns 201 early", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       mgrClient,
			dedupWindow:  0,
			waitTimeout:  10 * time.Second,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		const targetURI = "k8s://prod/default/deployment/approval-test"
		policy := createApprovalPolicy(ctx, gm, directClient, "require-approval-policy", targetURI)

		body := createAgentRequestBody{
			AgentIdentity: "agent-approval",
			Action:        "restart",
			TargetURI:     targetURI,
			Reason:        "test",
			Namespace:     testDefaultNS,
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
		rr := httptest.NewRecorder()

		respCh := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			s.handleCreateAgentRequest(rr, req)
			respCh <- rr
		}()

		var resp *httptest.ResponseRecorder
		gm.Eventually(respCh, 15*time.Second).Should(gomega.Receive(&resp))
		gm.Expect(resp.Code).To(gomega.Equal(http.StatusCreated))

		var respBody map[string]any
		gm.Expect(json.Unmarshal(resp.Body.Bytes(), &respBody)).To(gomega.Succeed())
		gm.Expect(respBody["phase"]).To(gomega.Equal(string(v1alpha1.PhasePending)))

		gm.Eventually(func() error {
			var list v1alpha1.AgentRequestList
			if err := directClient.List(ctx, &list, client.InNamespace(testDefaultNS)); err != nil {
				return err
			}
			for _, item := range list.Items {
				if item.Spec.AgentIdentity == "agent-approval" {
					for _, c := range item.Status.Conditions {
						if c.Type == v1alpha1.ConditionRequiresApproval && c.Status == metav1.ConditionTrue {
							return nil
						}
					}
				}
			}
			return errors.New("AgentRequest with RequiresApproval condition not found")
		}, 10*time.Second).Should(gomega.Succeed())

		gm.Expect(directClient.Delete(ctx, policy)).To(gomega.Succeed())
		cleanup(ctx, gm, directClient)
	})

	t.Run("Auth - missing role returns 403", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       mgrClient,
			dedupWindow:  0,
			waitTimeout:  2 * time.Second,
			roles:        newRoleConfig(testAgentSub, testReviewerSub, "", "", "", ""),
			authRequired: true,
		}

		body := createAgentRequestBody{
			AgentIdentity: "agent-auth",
			Action:        "restart",
			TargetURI:     "k8s://prod/default/deployment/auth-fail",
			Reason:        "test",
			Namespace:     testDefaultNS,
		}
		jsonBody, _ := json.Marshal(body)
		ctxWithAuth := withCallerSub(ctx, "some-caller")
		req := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody)).WithContext(ctxWithAuth)
		rr := httptest.NewRecorder()

		s.handleCreateAgentRequest(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
	})

	t.Run("GET /agent-requests/{name} - returns current state", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client: mgrClient,
		}

		ar := &v1alpha1.AgentRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "get-test",
				Namespace: testDefaultNS,
			},
			Spec: v1alpha1.AgentRequestSpec{
				AgentIdentity: "agent-get",
				Action:        "restart",
				Target:        v1alpha1.Target{URI: "k8s://prod/default/deployment/get-test"},
				Reason:        "test",
			},
		}
		gm.Expect(directClient.Create(ctx, ar)).To(gomega.Succeed())

		gm.Eventually(func() string {
			var current v1alpha1.AgentRequest
			key := types.NamespacedName{Name: "get-test", Namespace: testDefaultNS}
			if err := directClient.Get(ctx, key, &current); err != nil {
				return ""
			}
			return current.Status.Phase
		}, 15*time.Second).Should(gomega.Equal(v1alpha1.PhaseApproved))

		req := httptest.NewRequest("GET", "/agent-requests/get-test?namespace=default", nil)
		rr := httptest.NewRecorder()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /agent-requests/{name}", s.handleGetAgentRequest)
		mux.ServeHTTP(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var respBody map[string]any
		gm.Expect(json.Unmarshal(rr.Body.Bytes(), &respBody)).To(gomega.Succeed())
		gm.Expect(respBody["phase"]).To(gomega.Equal(string(v1alpha1.PhaseApproved)))

		cleanup(ctx, gm, directClient)
	})

	runHumanDecisionTests(t, mgrClient, directClient, ctx)
	runAuditTests(t, mgrClient, directClient, ctx)
}

func runHumanDecisionTests(t *testing.T, mgrClient, directClient client.Client, ctx context.Context) {
	t.Run("POST /approve transitions to Approved", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       mgrClient,
			dedupWindow:  0,
			waitTimeout:  10 * time.Second,
			roles:        newRoleConfig(testAgentSub, testReviewerSub, "", "", "", ""),
			authRequired: false,
		}

		const targetURI = "k8s://prod/default/deployment/approve-test"
		policy := createApprovalPolicy(ctx, gm, directClient, "approve-policy", targetURI)

		arName := createRequestAndWaitForPending(ctx, gm, s, targetURI)

		// Approve as reviewer
		body := `{"decision":"approved","reason":"looks good"}`
		path := fmt.Sprintf("/agent-requests/%s/approve?namespace=default", arName)
		approveReq := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
		approveRR := httptest.NewRecorder()

		mux := http.NewServeMux()
		mux.HandleFunc("POST /agent-requests/{name}/approve", s.handleApproveAgentRequest)
		mux.ServeHTTP(approveRR, approveReq.WithContext(withCallerSub(ctx, testReviewerSub)))
		gm.Expect(approveRR.Code).To(gomega.Equal(http.StatusOK))

		gm.Eventually(func() string {
			var current v1alpha1.AgentRequest
			if err := directClient.Get(ctx, types.NamespacedName{Name: arName, Namespace: testDefaultNS}, &current); err != nil {
				return ""
			}
			return current.Status.Phase
		}, 15*time.Second).Should(gomega.Equal(v1alpha1.PhaseApproved))

		gm.Expect(directClient.Delete(ctx, policy)).To(gomega.Succeed())
		cleanup(ctx, gm, directClient)
	})

	t.Run("POST /deny transitions to Denied", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       mgrClient,
			dedupWindow:  0,
			waitTimeout:  10 * time.Second,
			roles:        newRoleConfig(testAgentSub, testReviewerSub, "", "", "", ""),
			authRequired: false,
		}

		const targetURI = "k8s://prod/default/deployment/deny-test"
		policy := createApprovalPolicy(ctx, gm, directClient, "deny-test-policy", targetURI)

		arName := createRequestAndWaitForPending(ctx, gm, s, targetURI)

		// Deny as reviewer
		path := fmt.Sprintf("/agent-requests/%s/deny?namespace=default", arName)
		denyReq := httptest.NewRequest("POST", path, bytes.NewBufferString(`{"reason":"not allowed"}`))
		denyRR := httptest.NewRecorder()

		mux := http.NewServeMux()
		mux.HandleFunc("POST /agent-requests/{name}/deny", s.handleDenyAgentRequest)
		mux.ServeHTTP(denyRR, denyReq.WithContext(withCallerSub(ctx, testReviewerSub)))
		gm.Expect(denyRR.Code).To(gomega.Equal(http.StatusOK))

		gm.Eventually(func() string {
			var current v1alpha1.AgentRequest
			if err := directClient.Get(ctx, types.NamespacedName{Name: arName, Namespace: testDefaultNS}, &current); err != nil {
				return ""
			}
			return current.Status.Phase
		}, 15*time.Second).Should(gomega.Equal(v1alpha1.PhaseDenied))

		gm.Expect(directClient.Delete(ctx, policy)).To(gomega.Succeed())
		cleanup(ctx, gm, directClient)
	})
}

func runAuditTests(t *testing.T, mgrClient, directClient client.Client, ctx context.Context) {
	t.Run("AuditRecord emitted on request.submitted", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       mgrClient,
			dedupWindow:  0,
			waitTimeout:  10 * time.Second,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		body := createAgentRequestBody{
			AgentIdentity: "agent-audit",
			Action:        "restart",
			TargetURI:     "k8s://prod/default/deployment/audit-record",
			Reason:        "test",
			Namespace:     testDefaultNS,
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
		rr := httptest.NewRecorder()

		s.handleCreateAgentRequest(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusCreated))

		gm.Eventually(func() bool {
			var auditList v1alpha1.AuditRecordList
			if err := directClient.List(ctx, &auditList, client.InNamespace(testDefaultNS)); err != nil {
				return false
			}
			for _, item := range auditList.Items {
				if item.Spec.Event == "request.submitted" && item.Spec.AgentIdentity == "agent-audit" {
					return true
				}
			}
			return false
		}, 10*time.Second).Should(gomega.BeTrue())

		cleanup(ctx, gm, directClient)
	})
}

func createApprovalPolicy(
	ctx context.Context, gm *gomega.WithT, c client.Client, name, targetURI string,
) *v1alpha1.SafetyPolicy {
	policy := &v1alpha1.SafetyPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testDefaultNS,
		},
		Spec: v1alpha1.SafetyPolicySpec{
			Rules: []v1alpha1.Rule{
				{
					Name:       "require-approval-rule",
					Type:       "StateEvaluation",
					Action:     "RequireApproval",
					Expression: fmt.Sprintf(`request.spec.target.uri == %q`, targetURI),
				},
			},
		},
	}
	gm.Expect(c.Create(ctx, policy)).To(gomega.Succeed())
	return policy
}

func createRequestAndWaitForPending(ctx context.Context, gm *gomega.WithT, s *Server, targetURI string) string {
	body := createAgentRequestBody{
		AgentIdentity: testAgentSub,
		Action:        "restart",
		TargetURI:     targetURI,
		Reason:        "test",
		Namespace:     testDefaultNS,
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
	req = req.WithContext(withCallerSub(ctx, testAgentSub))
	rr := httptest.NewRecorder()

	respCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		s.handleCreateAgentRequest(rr, req)
		respCh <- rr
	}()

	var resp *httptest.ResponseRecorder
	gm.Eventually(respCh, 15*time.Second).Should(gomega.Receive(&resp))
	gm.Expect(resp.Code).To(gomega.Equal(http.StatusCreated))

	var list v1alpha1.AgentRequestList
	gm.Eventually(func() int {
		_ = s.client.List(ctx, &list, client.InNamespace(testDefaultNS))
		count := 0
		for _, item := range list.Items {
			if item.Spec.AgentIdentity == testAgentSub && item.Status.Phase == v1alpha1.PhasePending {
				count++
			}
		}
		return count
	}, 10*time.Second).Should(gomega.BeNumerically(">=", 1))

	for _, item := range list.Items {
		if item.Spec.AgentIdentity == testAgentSub && item.Status.Phase == v1alpha1.PhasePending {
			return item.Name
		}
	}
	return ""
}

func cleanup(ctx context.Context, gm *gomega.WithT, c client.Client) {
	gm.Expect(c.DeleteAllOf(ctx, &v1alpha1.AgentRequest{}, client.InNamespace(testDefaultNS))).To(gomega.Succeed())
	gm.Expect(c.DeleteAllOf(ctx, &v1alpha1.AuditRecord{}, client.InNamespace(testDefaultNS))).To(gomega.Succeed())
	gm.Expect(c.DeleteAllOf(ctx, &coordinationv1.Lease{}, client.InNamespace(testDefaultNS))).To(gomega.Succeed())
}

func startTestManager(t *testing.T, cfg *rest.Config) client.Client {
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	eval, err := evaluation.NewEvaluator()
	if err != nil {
		t.Fatalf("Failed to create evaluator: %v", err)
	}

	err = (&controller.AgentRequestReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		Evaluator:            eval,
		TargetContextFetcher: &evaluation.KubernetesTargetContextFetcher{Client: mgr.GetAPIReader()},
	}).SetupWithManager(mgr)
	if err != nil {
		t.Fatalf("Failed to setup AgentRequestReconciler: %v", err)
	}

	err = (&controller.SafetyPolicyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	if err != nil {
		t.Fatalf("Failed to setup SafetyPolicyReconciler: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.Start(ctx) }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("manager exited with unexpected error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("manager did not stop within 10s")
		}
	})

	return mgr.GetClient()
}

func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
