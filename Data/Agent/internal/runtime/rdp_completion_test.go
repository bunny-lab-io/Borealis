package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/rdp"
)

func TestWorkerOperationRDPReadinessOriginalCompletion(t *testing.T) {
	for _, mode := range []string{"completed", "failed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			c, original, path := workerOperationTestFixture(t)
			original.dropTerminal = true
			payload := workerOperationTestPayload()
			payload["rdp_port"], payload["agent_id"] = 3389, "original-agent"
			var complete func(error)
			calls := 0
			handler := c.async("rdp_start", func(_ context.Context, owned any, callback func(error)) (any, error) {
				calls++
				complete = callback
				if owned.(map[string]any)["agent_id"] != "original-agent" {
					t.Fatal("request binding changed")
				}
				if mode == "unknown" {
					return map[string]any{"status": "pending", "ready": false}, nil
				}
				return map[string]any{"status": "ok", "ready": true, "port": 3389}, nil
			})
			if _, err := handler(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			if workerOperationTestRecord(t, path)["state"] != "granted" || len(original.receipts) != 0 {
				t.Fatal("readiness ACK manufactured terminal")
			}
			if _, err := handler(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || original.preflights != 1 {
				t.Fatal("uncertain readiness replayed effects")
			}
			if mode == "unknown" {
				return
			}
			c.close()
			payload["job_id"], payload["agent_id"] = 99, "changed-agent"
			payload["context"].(map[string]any)["worker_operation"].(map[string]any)["token"] = strings.Repeat("b", 32)
			replacementServer := &workerOperationTestServer{grants: original.grants, committed: original.committed}
			replacement := workerOperationTestConnection(t, path, replacementServer, c.outbox)
			var resultErr error
			if mode == "failed" {
				resultErr = errors.New("joined readiness result mismatch")
			}
			complete(resultErr)
			if len(original.receipts) != 1 || len(replacementServer.receipts) != 0 {
				t.Fatal("readiness callback adopted replacement emitter")
			}
			first := original.receipts[0]
			if !strings.Contains(first, `"job_id":17`) || !strings.Contains(first, strings.Repeat("a", 32)) {
				t.Fatal("readiness callback adopted changed request", first)
			}
			status := "Completed"
			if mode == "failed" {
				status = "Failed"
			}
			if !strings.Contains(first, `"status":"`+status+`"`) {
				t.Fatal("wrong terminal", first)
			}
			replacement.replay(context.Background())
			if len(replacementServer.receipts) != 1 || replacementServer.receipts[0] != first {
				t.Fatal("receipt replay changed bytes")
			}
			complete(errors.New("late conflict"))
			workerOperationTestPending(t, replacement, 0)
		})
	}
}

func TestWorkerOperationRDPReadinessPreworkRejection(t *testing.T) {
	c, server, _ := workerOperationTestFixture(t)
	var manager *rdp.Manager
	payload := workerOperationTestPayload()
	payload["agent_id"] = "original-agent"
	response, err := c.async("rdp_start", manager.HandleStartWithCompletion)(context.Background(), payload)
	if err != nil || response.(map[string]any)["ready"] != false || len(server.receipts) != 1 || !strings.Contains(server.receipts[0], `"status":"Failed"`) {
		t.Fatal("prework readiness rejection lost terminal", response, err, server.receipts)
	}
}
