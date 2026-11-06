package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	patchmanagement "github.com/bunny-lab-io/borealis/go-agent/internal/roles/patch_management"
)

func TestWorkerOperationPatchPolicyOriginalCompletion(t *testing.T) {
	for _, mode := range []string{"completed", "failed", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			c, original, path := workerOperationTestFixture(t)
			original.dropTerminal = true
			payload := workerOperationTestPayload()
			payload["mode"], payload["request_id"] = "managed", "original-policy"
			var complete func(error)
			calls := 0
			handler := c.async("patch_policy_enforcement_request", func(_ context.Context, owned any, callback func(error)) (any, error) {
				calls++
				complete = callback
				if owned.(map[string]any)["request_id"] != "original-policy" {
					t.Fatal("request binding changed")
				}
				if mode == "unknown" {
					return map[string]any{"ok": false, "status": "pending", "request_id": "original-policy"}, nil
				}
				return map[string]any{"ok": true, "status": "applied", "request_id": "original-policy"}, nil
			})
			if _, err := handler(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			if workerOperationTestRecord(t, path)["state"] != "granted" || len(original.receipts) != 0 {
				t.Fatal("policy ACK manufactured terminal")
			}
			if _, err := handler(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || original.preflights != 1 {
				t.Fatal("uncertain policy replayed effects")
			}
			if mode == "unknown" {
				return
			}
			c.close()
			payload["job_id"], payload["request_id"] = 99, "changed-policy"
			payload["context"].(map[string]any)["worker_operation"].(map[string]any)["token"] = strings.Repeat("b", 32)
			replacementServer := &workerOperationTestServer{grants: original.grants, committed: original.committed}
			replacement := workerOperationTestConnection(t, path, replacementServer, c.outbox)
			var resultErr error
			if mode == "failed" {
				resultErr = errors.New("joined policy result mismatch")
			}
			complete(resultErr)
			if len(original.receipts) != 1 || len(replacementServer.receipts) != 0 {
				t.Fatal("policy callback adopted replacement emitter")
			}
			first := original.receipts[0]
			if !strings.Contains(first, `"job_id":17`) || !strings.Contains(first, strings.Repeat("a", 32)) {
				t.Fatal("policy callback adopted changed request", first)
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

func TestWorkerOperationPatchPolicyPreworkRejection(t *testing.T) {
	c, server, _ := workerOperationTestFixture(t)
	manager := patchmanagement.New(nil, "original-host", "system")
	payload := workerOperationTestPayload()
	payload["hostname"], payload["mode"] = "wrong-host", "managed"
	response, err := c.async("patch_policy_enforcement_request", manager.HandlePolicyEnforcementRequestWithCompletion)(context.Background(), payload)
	if err != nil || response.(map[string]any)["ok"] != false || len(server.receipts) != 1 || !strings.Contains(server.receipts[0], `"status":"Failed"`) {
		t.Fatal("prework policy rejection lost terminal", response, err, server.receipts)
	}
}
