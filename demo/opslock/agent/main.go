package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

type denial struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type pollStatus struct {
	Phase       string   `json:"phase"`
	Denial      *denial  `json:"denial"`
	AuditEvents []string `json:"auditEvents"`
}

func poll(gateway, name, namespace string) pollStatus {
	url := fmt.Sprintf("%s/agent-requests/%s?namespace=%s", gateway, name, namespace)
	resp, err := http.Get(url)
	if err != nil {
		log.Fatalf("Failed to poll status: %v", err)
	}
	var s pollStatus
	_ = json.NewDecoder(resp.Body).Decode(&s)
	_ = resp.Body.Close()
	return s
}

var auditIcons = map[string]string{
	"request.submitted": "📥",
	"request.approved":  "✅",
	"request.denied":    "🚫",
	"policy.evaluated":  "⚖️ ",
	"lock.acquired":     "🔒",
	"lock.released":     "🔓",
	"lock.expired":      "⏰",
	"request.executing": "▶️ ",
	"request.completed": "✅",
	"request.failed":    "❌",
	"request.expired":   "⏰",
}

func printAuditTrail(logger *log.Logger, events []string) {
	if len(events) == 0 {
		return
	}
	logger.Printf("Audit trail:")
	for _, e := range events {
		icon := "○ "
		if ic, ok := auditIcons[e]; ok {
			icon = ic
		}
		logger.Printf("  %s %s", icon, e)
	}
}

func main() {
	agentID := flag.String("agent-id", "", "ID of the agent (required)")
	target := flag.String("target", "", "Target URI (required)")
	gateway := flag.String("gateway", "http://localhost:8080", "Gateway URL")
	namespace := flag.String("namespace", "default", "Kubernetes namespace")
	flag.Parse()

	if *agentID == "" || *target == "" {
		flag.Usage()
		os.Exit(1)
	}

	log.SetFlags(0)
	logger := log.New(os.Stdout, fmt.Sprintf("[%s] ", *agentID), 0)

	body := map[string]string{
		"agentIdentity": *agentID,
		"action":        "scale",
		"targetURI":     *target,
		"reason":        "autonomous scale operation",
		"namespace":     *namespace,
	}
	b, _ := json.Marshal(body)

	resp, err := http.Post(*gateway+"/agent-requests", "application/json", bytes.NewBuffer(b))
	if err != nil {
		logger.Fatalf("Failed to reach AIP Gateway: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		var errResp map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		logger.Fatalf("Gateway returned error: %v (Status %d)", errResp["error"], resp.StatusCode)
	}

	var arResp struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&arResp); err != nil {
		logger.Fatalf("Failed to decode gateway response: %v", err)
	}
	logger.Printf("→ Submitted AgentRequest: %s", arResp.Name)

	// Poll until the controller resolves the request.
	var status pollStatus
	for {
		status = poll(*gateway, arResp.Name, *namespace)
		switch status.Phase {
		case "Approved", "Denied", "Failed", "Expired":
			goto resolved
		}
		time.Sleep(1 * time.Second)
	}
resolved:

	switch status.Phase {
	case "Denied":
		printAuditTrail(logger, status.AuditEvents)
		if status.Denial != nil {
			logger.Fatalf("✗ Denied — code: %s, message: %s", status.Denial.Code, status.Denial.Message)
		}
		logger.Fatalf("✗ Denied by AIP (OpsLock held by another agent)")
	case "Failed":
		printAuditTrail(logger, status.AuditEvents)
		logger.Fatalf("✗ Failed — request timed out or lock expired")
	case "Expired":
		printAuditTrail(logger, status.AuditEvents)
		logger.Fatalf("✗ Expired — request was not graded in time")
	}

	logger.Printf("✓ Approved — OpsLock acquired, signalling Executing...")

	execURL := fmt.Sprintf("%s/agent-requests/%s/executing?namespace=%s", *gateway, arResp.Name, *namespace)
	execResp, err := http.Post(execURL, "application/json", nil)
	if err != nil {
		logger.Fatalf("Failed to signal executing: %v", err)
	}
	if execResp.StatusCode >= 400 {
		var errBody map[string]any
		_ = json.NewDecoder(execResp.Body).Decode(&errBody)
		_ = execResp.Body.Close()
		logger.Fatalf("Failed to transition to Executing (HTTP %d): %v", execResp.StatusCode, errBody["error"])
	}
	_ = execResp.Body.Close()

	time.Sleep(10 * time.Second)

	compURL := fmt.Sprintf("%s/agent-requests/%s/completed?namespace=%s", *gateway, arResp.Name, *namespace)
	compResp, err := http.Post(compURL, "application/json", nil)
	if err != nil {
		logger.Fatalf("Failed to signal completed: %v", err)
	}
	if compResp.StatusCode >= 400 {
		var errBody map[string]any
		_ = json.NewDecoder(compResp.Body).Decode(&errBody)
		_ = compResp.Body.Close()
		logger.Fatalf("Failed to transition to Completed (HTTP %d): %v", compResp.StatusCode, errBody["error"])
	}
	_ = compResp.Body.Close()

	// Fetch final audit trail after completion so lock.released is included.
	final := poll(*gateway, arResp.Name, *namespace)
	printAuditTrail(logger, final.AuditEvents)
	logger.Printf("✓ Completed — OpsLock released")
}
