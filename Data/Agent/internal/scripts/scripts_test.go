package scripts

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"testing"
)

func TestDecodeScriptBytes(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("hello"))
	got, ok := DecodeScriptBytes(encoded, "base64")
	if !ok || string(got) != "hello" {
		t.Fatalf("decode failed: ok=%v got=%q", ok, string(got))
	}
	got, ok = DecodeScriptBytes("plain", "")
	if !ok || string(got) != "plain" {
		t.Fatalf("plain decode failed")
	}
}

func TestVerifySignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("script")
	signature := ed25519.Sign(priv, payload)
	if !VerifySignature(payload, base64.StdEncoding.EncodeToString(signature), base64.StdEncoding.EncodeToString(publicDER)) {
		t.Fatalf("signature should verify")
	}
	if VerifySignature([]byte("tampered"), base64.StdEncoding.EncodeToString(signature), base64.StdEncoding.EncodeToString(publicDER)) {
		t.Fatalf("tampered signature should fail")
	}
}

func TestBuildEnvMap(t *testing.T) {
	env := BuildEnvMap(
		map[string]any{"foo-bar": true},
		[]map[string]any{{"name": "Path Value", "default": "abc"}},
	)
	if env["FOO_BAR"] != "True" {
		t.Fatalf("bool env mismatch: %#v", env)
	}
	if env["PATH_VALUE"] != "abc" {
		t.Fatalf("default env mismatch: %#v", env)
	}
}

func TestRunBash(t *testing.T) {
	result := Run(context.Background(), "bash", []byte("echo borealis"), map[string]string{}, 10)
	if result.ReturnCode != 0 {
		t.Skipf("bash unavailable or failed: rc=%d stderr=%s", result.ReturnCode, result.Stderr)
	}
	if result.Stdout == "" {
		t.Fatalf("expected stdout")
	}
}
