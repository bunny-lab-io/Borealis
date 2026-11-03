package systemcontext

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
)

type completionDispatcher struct {
	fakeCurrentUserDispatcher
	run func(context.Context, map[string]any, func(scripts.Result)) (bool, string)
}

func (d *completionDispatcher) DispatchCurrentUserQuickJobWithCompletion(ctx context.Context, payload map[string]any, complete func(scripts.Result)) (bool, string) {
	return d.run(ctx, payload, complete)
}

func currentUserSignedPayload(t *testing.T) map[string]any {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("Write-Output fixture")
	return map[string]any{"job_id": 11, "target_hostname": "host", "run_mode": "currentuser", "script_type": "powershell", "script_content": base64.StdEncoding.EncodeToString(content), "script_encoding": "base64", "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(priv, content)), "signing_key": base64.StdEncoding.EncodeToString(der), "context": map[string]any{"queue_lane": "software_management", "worker_operation": map[string]any{"version": 1, "token": strings.Repeat("a", 32)}}}
}

func TestTrackedCurrentUserRequiresActualCompletion(t *testing.T) {
	for _, mode := range []string{"joined", "joined_failed", "unknown", "prelaunch", "legacy_only", "bad_signature"} {
		t.Run(mode, func(t *testing.T) {
			emitter := &fakeEmitter{}
			dispatcher := &completionDispatcher{}
			calls := 0
			dispatcher.run = func(_ context.Context, _ map[string]any, complete func(scripts.Result)) (bool, string) {
				calls++
				switch mode {
				case "joined":
					complete(scripts.Result{ReturnCode: 0, Stdout: "done"})
				case "joined_failed":
					complete(scripts.Result{ReturnCode: -1, Stderr: "result query failed"})
				case "prelaunch":
					return false, "not launched"
				}
				return true, ""
			}
			role := New(emitter, &fakeKeys{}, dispatcher)
			role.Hostname = "host"
			refresh := &fakeSoftwareRefresh{}
			role.SoftwareRefresh = refresh
			if mode == "legacy_only" {
				role.CurrentUserDispatcher = &dispatcher.fakeCurrentUserDispatcher
			}
			payload := currentUserSignedPayload(t)
			if mode == "bad_signature" {
				payload["script_content"] = base64.StdEncoding.EncodeToString([]byte("changed"))
			}
			if _, err := role.HandleQuickJob(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			terminals := 0
			for i, event := range emitter.events {
				if event == "quick_job_result" {
					terminals++
					result := emitter.payloads[i].(map[string]any)
					want := "Failed"
					if mode == "joined" {
						want = "Success"
					}
					if result["status"] != want {
						t.Fatal("wrong terminal", result)
					}
				}
			}
			want := 1
			if mode == "unknown" {
				want = 0
			}
			if terminals != want || dispatcher.called {
				t.Fatal("legacy/unknown result substituted for completion", terminals, dispatcher.called)
			}
			if refresh.called != (mode == "joined" || mode == "joined_failed") {
				t.Fatal("prelaunch or unknown result triggered software refresh")
			}
			if (mode == "legacy_only" || mode == "bad_signature") && calls != 0 {
				t.Fatal("prework guard reached native dispatch")
			}
		})
	}
}

func TestTrackedCurrentUserFreezesOriginalTerminalOwner(t *testing.T) {
	original, replacement := &fakeEmitter{}, &fakeEmitter{}
	refresh, replacementRefresh := &fakeSoftwareRefresh{}, &fakeSoftwareRefresh{}
	dispatcher := &completionDispatcher{}
	role := New(original, &fakeKeys{}, dispatcher)
	role.Hostname = "host"
	role.SoftwareRefresh = refresh
	payload := currentUserSignedPayload(t)
	var completion func(scripts.Result)
	dispatcher.run = func(_ context.Context, body map[string]any, complete func(scripts.Result)) (bool, string) {
		completion = complete
		body["job_id"] = 99
		ctx := body["context"].(map[string]any)
		ctx["queue_lane"] = "changed"
		ctx["worker_operation"].(map[string]any)["token"] = strings.Repeat("b", 32)
		role.Emitter = replacement
		role.SoftwareRefresh = replacementRefresh
		return true, ""
	}
	if _, err := role.HandleQuickJob(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if completion == nil || len(original.events) != 1 || len(replacement.events) != 0 {
		t.Fatal("acceptance became terminal")
	}
	completion(scripts.Result{ReturnCode: 0, Stdout: "original"})
	completion(scripts.Result{ReturnCode: -1, Stderr: "late conflict"})
	if len(original.events) != 2 || len(replacement.events) != 0 || !refresh.called || replacementRefresh.called {
		t.Fatal("callback adopted replacement owner or repeated effect")
	}
	result := original.payloads[1].(map[string]any)
	ctx := result["context"].(map[string]any)
	if result["job_id"] != 11 || result["stdout"] != "original" || ctx["queue_lane"] != "software_management" || ctx["worker_operation"].(map[string]any)["token"] != strings.Repeat("a", 32) {
		t.Fatal("mutable dispatcher changed retained receipt", result)
	}
}
