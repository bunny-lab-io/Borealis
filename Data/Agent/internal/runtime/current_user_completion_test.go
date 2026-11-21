package agentruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"

	systemcontext "github.com/bunny-lab-io/borealis/go-agent/internal/roles/system_context"
	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
)

type workerCurrentUserDispatcher struct {
	complete func(scripts.Result)
	calls    int
}

func (d *workerCurrentUserDispatcher) DispatchCurrentUserQuickJob(context.Context, map[string]any) (scripts.Result, bool, string) {
	panic("tracked job used legacy dispatcher")
}
func (d *workerCurrentUserDispatcher) DispatchCurrentUserQuickJobWithCompletion(_ context.Context, _ map[string]any, complete func(scripts.Result)) (bool, string) {
	d.calls++
	d.complete = complete
	return true, ""
}

func TestWorkerOperationCurrentUserOriginalGrantAndReplay(t *testing.T) {
	c, original, path := workerOperationTestFixture(t)
	original.dropTerminal = true
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("Write-Output fixture")
	payload := workerOperationTestPayload()
	payload["run_mode"], payload["target_hostname"], payload["script_type"] = "currentuser", "host", "powershell"
	payload["script_content"], payload["script_encoding"] = base64.StdEncoding.EncodeToString(content), "base64"
	payload["signature"], payload["signing_key"] = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, content)), base64.StdEncoding.EncodeToString(der)
	dispatcher := &workerCurrentUserDispatcher{}
	role := systemcontext.New(c, nil, dispatcher)
	role.Hostname = "host"
	handler := c.scripts(role.HandleQuickJob, "host")
	if _, err := handler(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if dispatcher.complete == nil || dispatcher.calls != 1 || workerOperationTestRecord(t, path)["state"] != "granted" || len(original.receipts) != 0 {
		t.Fatal("accepted native process became terminal")
	}
	if _, err := handler(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if dispatcher.calls != 1 || original.preflights != 1 {
		t.Fatal("uncertain grant replayed effects")
	}
	c.close()
	replacementServer := &workerOperationTestServer{grants: original.grants, committed: original.committed}
	replacement := workerOperationTestConnection(t, path, replacementServer, c.outbox)
	role.Emitter = replacement
	dispatcher.complete(scripts.Result{ReturnCode: 0, Stdout: "original joined output"})
	if len(original.receipts) != 1 || len(replacementServer.receipts) != 0 || workerOperationTestRecord(t, path)["state"] != "terminal" {
		t.Fatal("completion replaced original emitter/journal")
	}
	first := original.receipts[0]
	if !strings.Contains(first, `"status":"Completed"`) || !strings.Contains(first, "original joined output") {
		t.Fatal("original terminal lost", first)
	}
	replacement.replay(context.Background())
	if len(replacementServer.receipts) != 1 || replacementServer.receipts[0] != first {
		t.Fatal("reconnect changed terminal receipt")
	}
	dispatcher.complete(scripts.Result{ReturnCode: -1, Stderr: "late conflict"})
	if len(original.receipts) != 1 || len(replacementServer.receipts) != 1 {
		t.Fatal("duplicate callback emitted replacement result")
	}
	workerOperationTestPending(t, replacement, 0)
}
