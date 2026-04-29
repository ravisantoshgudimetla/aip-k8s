package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func runSSEStreamingTests(t *testing.T, _, directClient client.Client,
	watchClient client.WithWatch, ctx context.Context) {
	t.Run("SSE content negotiation on POST /agent-requests", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       directClient,
			watchClient:  watchClient,
			dedupWindow:  0,
			waitTimeout:  serverWaitTimeout,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		body := createAgentRequestBody{
			AgentIdentity: "agent-sse-cn",
			Action:        "restart",
			TargetURI:     "k8s://prod/default/deployment/sse-content-negotiation",
			Reason:        "test",
			Namespace:     testDefaultNS,
		}
		jsonBody, err := json.Marshal(body)
		gm.Expect(err).NotTo(gomega.HaveOccurred())

		req := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
		req.Header.Set("Accept", "text/event-stream")
		rr := httptest.NewRecorder()

		respCh := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			s.handleCreateAgentRequest(rr, req)
			respCh <- rr
		}()

		var resp *httptest.ResponseRecorder
		gm.Eventually(respCh, eventuallyLongTimeout).Should(gomega.Receive(&resp))
		gm.Expect(resp.Header().Get("Content-Type")).To(gomega.Equal("text/event-stream"))

		events := parseSSEEvents(t, resp.Body.String())
		gm.Expect(events).ToNot(gomega.BeEmpty())

		last := events[len(events)-1]
		gm.Expect(last.Type).To(gomega.Equal("result"))
		gm.Expect(last.Data["phase"]).To(gomega.Equal(string(v1alpha1.PhaseApproved)))

		cleanup(ctx, gm, directClient)
	})

	t.Run("GET /watch returns immediate result for terminal request", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       directClient,
			watchClient:  watchClient,
			dedupWindow:  0,
			waitTimeout:  serverWaitTimeout,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		body := createAgentRequestBody{
			AgentIdentity: "agent-sse-terminal",
			Action:        "restart",
			TargetURI:     "k8s://prod/default/deployment/sse-terminal",
			Reason:        "test",
			Namespace:     testDefaultNS,
		}
		jsonBody, err := json.Marshal(body)
		gm.Expect(err).NotTo(gomega.HaveOccurred())

		createReq := httptest.NewRequest("POST", "/agent-requests", bytes.NewBuffer(jsonBody))
		createRR := httptest.NewRecorder()
		s.handleCreateAgentRequest(createRR, createReq)
		gm.Expect(createRR.Code).To(gomega.Equal(http.StatusCreated))

		var createResp map[string]any
		gm.Expect(json.Unmarshal(createRR.Body.Bytes(), &createResp)).To(gomega.Succeed())
		arName, ok := createResp["name"].(string)
		gm.Expect(ok).To(gomega.BeTrue(), "expected 'name' field to be a string")

		gm.Eventually(func() string {
			var current v1alpha1.AgentRequest
			if err := directClient.Get(ctx, types.NamespacedName{Name: arName, Namespace: testDefaultNS}, &current); err != nil {
				return ""
			}
			return current.Status.Phase
		}, eventuallyTimeout).Should(gomega.Equal(v1alpha1.PhaseApproved))

		watchReq := httptest.NewRequest("GET",
			fmt.Sprintf("/agent-requests/%s/watch?namespace=%s", arName, testDefaultNS), nil)
		watchReq.Header.Set("Accept", "text/event-stream")
		watchReq.SetPathValue("name", arName)
		watchRR := httptest.NewRecorder()

		s.handleWatchAgentRequest(watchRR, watchReq)

		gm.Expect(watchRR.Header().Get("Content-Type")).To(gomega.Equal("text/event-stream"))
		events := parseSSEEvents(t, watchRR.Body.String())
		gm.Expect(events).To(gomega.HaveLen(1))
		gm.Expect(events[0].Type).To(gomega.Equal("result"))
		gm.Expect(events[0].Data["phase"]).To(gomega.Equal(string(v1alpha1.PhaseApproved)))

		cleanup(ctx, gm, directClient)
	})

	t.Run("GET /watch streams phase transition to Approved", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       directClient,
			watchClient:  watchClient,
			dedupWindow:  0,
			waitTimeout:  serverWaitTimeout,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		ar := &v1alpha1.AgentRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sse-stream-transition",
				Namespace: testDefaultNS,
			},
			Spec: v1alpha1.AgentRequestSpec{
				AgentIdentity: "agent-sse-stream",
				Action:        "restart",
				Target:        v1alpha1.Target{URI: "k8s://prod/default/deployment/sse-stream"},
				Reason:        "test",
			},
		}
		gm.Expect(directClient.Create(ctx, ar)).To(gomega.Succeed())

		watchReq := httptest.NewRequest("GET",
			fmt.Sprintf("/agent-requests/sse-stream-transition/watch?namespace=%s", testDefaultNS), nil)
		watchReq.Header.Set("Accept", "text/event-stream")
		watchReq.SetPathValue("name", "sse-stream-transition")
		watchRR := httptest.NewRecorder()

		doneCh := make(chan struct{})
		go func() {
			defer close(doneCh)
			s.handleWatchAgentRequest(watchRR, watchReq)
		}()

		gm.Eventually(doneCh, eventuallyLongTimeout).Should(gomega.BeClosed())

		events := parseSSEEvents(t, watchRR.Body.String())
		gm.Expect(events).ToNot(gomega.BeEmpty())

		last := events[len(events)-1]
		gm.Expect(last.Type).To(gomega.Equal("result"))
		gm.Expect(last.Data["phase"]).To(gomega.Equal(string(v1alpha1.PhaseApproved)))

		cleanup(ctx, gm, directClient)
	})

	t.Run("GET /watch returns 404 for nonexistent request", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       directClient,
			watchClient:  watchClient,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		req := httptest.NewRequest("GET", "/agent-requests/does-not-exist/watch", nil)
		req.Header.Set("Accept", "text/event-stream")
		req.SetPathValue("name", "does-not-exist")
		rr := httptest.NewRecorder()

		s.handleWatchAgentRequest(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusNotFound))
	})

	t.Run("GET /watch returns 400 without Accept header", func(t *testing.T) {
		gm := gomega.NewWithT(t)
		s := &Server{
			client:       directClient,
			watchClient:  watchClient,
			roles:        newRoleConfig("", "", "", "", "", ""),
			authRequired: false,
		}

		req := httptest.NewRequest("GET", "/agent-requests/any-name/watch", nil)
		req.SetPathValue("name", "any-name")
		rr := httptest.NewRecorder()

		s.handleWatchAgentRequest(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusBadRequest))
	})
}
