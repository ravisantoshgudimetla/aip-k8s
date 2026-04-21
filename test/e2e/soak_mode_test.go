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
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"

	governancev1alpha1 "github.com/agent-control-plane/aip-k8s/api/v1alpha1"
	"github.com/agent-control-plane/aip-k8s/test/utils"
)

var _ = Describe("SoakMode and Accuracy Tracking", Ordered, func() {
	const (
		grName        = "soak-mode-gr"
		reqName       = "soak-mode-req"
		ns            = "default"
		agentIdentity = "soak-mode-agent"
		targetURI     = "k8s://soak/resource"
	)

	BeforeAll(func() {
		By("creating a GovernedResource with soakMode: true")
		grJSON := fmt.Sprintf(`{
			"apiVersion": "governance.aip.io/v1alpha1",
			"kind": "GovernedResource",
			"metadata": {"name": "%s"},
			"spec": {
				"uriPattern": "k8s://soak/*",
				"permittedActions": ["test"],
				"soakMode": true
			}
		}`, grName)

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(grJSON)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		By("cleaning up resources")
		cmd := exec.Command("kubectl", "delete", "governedresource", "--all", "--ignore-not-found")
		_, _ = utils.Run(cmd)
		cmd = exec.Command("kubectl", "delete", "agentrequest", "--all", "-n", ns, "--ignore-not-found")
		_, _ = utils.Run(cmd)
		cmd = exec.Command("kubectl", "delete", "diagnosticaccuracysummary", "--all", "-n", ns, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	It("should route AgentRequest to AwaitingVerdict and update accuracy on verdict", func() {
		By("creating an AgentRequest")
		reqJSON := fmt.Sprintf(`{
			"apiVersion": "governance.aip.io/v1alpha1",
			"kind": "AgentRequest",
			"metadata": {"name": "%s", "namespace": "%s"},
			"spec": {
				"agentIdentity": "%s",
				"action": "test",
				"target": {"uri": "%s"},
				"reason": "soak test"
			}
		}`, reqName, ns, agentIdentity, targetURI)

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(reqJSON)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for Phase=AwaitingVerdict")
		Eventually(func() string {
			return getAgentRequestPhase(reqName, ns)
		}, 30*time.Second).Should(Equal("AwaitingVerdict"))

		By("submitting a verdict via status patch")
		verdictAt := time.Now().Format(time.RFC3339)
		verdictPatch := fmt.Sprintf(`{
			"status": {
				"phase": "Completed",
				"verdict": "correct",
				"verdictBy": "e2e-tester",
				"verdictAt": "%s"
			}
		}`, verdictAt)
		cmd = exec.Command("kubectl", "patch", "agentrequest", reqName, "-n", ns,
			"--subresource=status", "--type=merge", "-p", verdictPatch)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for Phase=Completed")
		Eventually(func() string {
			return getAgentRequestPhase(reqName, ns)
		}, 30*time.Second).Should(Equal("Completed"))

		By("verifying DiagnosticAccuracySummary is updated")
		summaryName := "soak-mode-agent" // sanitizeDNSSegment("soak-mode-agent", 63)
		Eventually(func(g Gomega) {
			var summary governancev1alpha1.DiagnosticAccuracySummary
			err := k8sClient.Get(ctx, types.NamespacedName{Name: summaryName, Namespace: ns}, &summary)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(summary.Status.TotalReviewed).To(Equal(int32(1)))
			g.Expect(summary.Status.CorrectCount).To(Equal(int32(1)))
			g.Expect(summary.Status.DiagnosticAccuracy).NotTo(BeNil())
			g.Expect(*summary.Status.DiagnosticAccuracy).To(Equal(1.0))
		}, 30*time.Second).Should(Succeed())
	})
})
