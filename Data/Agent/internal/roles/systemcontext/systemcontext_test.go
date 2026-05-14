package systemcontext

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"testing"

	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
)

type fakeEmitter struct {
	events   []string
	payloads []any
}

func (f *fakeEmitter) Emit(event string, payload any) error {
	f.events = append(f.events, event)
	f.payloads = append(f.payloads, payload)
	return nil
}

type fakeKeys struct {
	key string
}

func (f *fakeKeys) LoadServerSigningKey() string { return f.key }
func (f *fakeKeys) StoreServerSigningKey(value string) error {
	f.key = value
	return nil
}

func TestHandleQuickJobSuccess(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	scriptBytes := []byte("echo hi")
	signature := ed25519.Sign(priv, scriptBytes)
	emitter := &fakeEmitter{}
	role := New(emitter, &fakeKeys{}, nil)
	role.Hostname = "host"
	role.Runner = func(ctx context.Context, scriptType string, content []byte, envMap map[string]string, timeoutSeconds int) scripts.Result {
		if scriptType != "bash" {
			t.Fatalf("scriptType = %q", scriptType)
		}
		if string(content) != string(scriptBytes) {
			t.Fatalf("content mismatch")
		}
		return scripts.Result{ReturnCode: 0, Stdout: "ok"}
	}
	_, err = role.HandleQuickJob(context.Background(), map[string]any{
		"job_id":          9,
		"target_hostname": "host",
		"run_mode":        "system",
		"script_type":     "bash",
		"script_content":  base64.StdEncoding.EncodeToString(scriptBytes),
		"script_encoding": "base64",
		"signature":       base64.StdEncoding.EncodeToString(signature),
		"signing_key":     base64.StdEncoding.EncodeToString(publicDER),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(emitter.events) != 2 || emitter.events[1] != "quick_job_result" {
		t.Fatalf("events mismatch: %#v", emitter.events)
	}
	result := emitter.payloads[1].(map[string]any)
	if result["status"] != "Success" {
		t.Fatalf("status = %#v", result)
	}
}

func TestHandleQuickJobRejectsBadSignature(t *testing.T) {
	emitter := &fakeEmitter{}
	role := New(emitter, &fakeKeys{}, nil)
	role.Hostname = "host"
	_, err := role.HandleQuickJob(context.Background(), map[string]any{
		"job_id":          10,
		"target_hostname": "host",
		"run_mode":        "system",
		"script_type":     "bash",
		"script_content":  base64.StdEncoding.EncodeToString([]byte("echo hi")),
		"script_encoding": "base64",
		"signature":       "bad",
		"signing_key":     "bad",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("expected result event, got %#v", emitter.events)
	}
	result := emitter.payloads[0].(map[string]any)
	if result["status"] != "Failed" {
		t.Fatalf("status = %#v", result)
	}
}
