package scripts

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"strings"
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

func TestBuildPowerShellScriptSuppressesNoisyStreams(t *testing.T) {
	wrapped := BuildPowerShellScript("Write-Output 'ok'", nil)
	for _, expected := range []string{
		"$ProgressPreference = 'SilentlyContinue'",
		"$InformationPreference = 'SilentlyContinue'",
		"$VerbosePreference = 'SilentlyContinue'",
		"$DebugPreference = 'SilentlyContinue'",
	} {
		if !strings.Contains(wrapped, expected) {
			t.Fatalf("missing PowerShell prelude %q in %q", expected, wrapped)
		}
	}
}

func TestBuildPowerShellScriptPreservesAdvancedScriptPreamble(t *testing.T) {
	content := "[CmdletBinding()]\nparam(\n    [string]$Version = '2501'\n)\n\nWrite-Host $Version\nWrite-Host $env:VERSION\n"
	wrapped := BuildPowerShellScript(content, map[string]string{"VERSION": "2501"})
	marker := "$__BorealisScript = {\n"
	parts := strings.SplitN(wrapped, marker, 2)
	if len(parts) != 2 {
		t.Fatalf("missing script block marker in %q", wrapped)
	}
	if !strings.Contains(parts[0], "SetEnvironmentVariable('VERSION', '2501', 'Process')") {
		t.Fatalf("environment prelude missing before script block: %q", parts[0])
	}
	bodyAndTail := strings.SplitN(parts[1], "\n}\n", 2)
	if len(bodyAndTail) != 2 {
		t.Fatalf("script block was not closed: %q", parts[1])
	}
	if !strings.HasPrefix(bodyAndTail[0], content) {
		t.Fatalf("advanced preamble no longer starts script body: %q", bodyAndTail[0])
	}
	if strings.Contains(bodyAndTail[0], "SetEnvironmentVariable('VERSION', '2501', 'Process')") {
		t.Fatalf("environment prelude was injected inside advanced script body: %q", bodyAndTail[0])
	}
	if bodyAndTail[1] != "& $__BorealisScript\n" {
		t.Fatalf("unexpected script invocation tail: %q", bodyAndTail[1])
	}
}

func TestCleanPowerShellStreamDecodesCliXMLAndDropsProgressOnlyPayloads(t *testing.T) {
	progressOnly := "#< CLIXML\r\n<Objs Version=\"1.1.0.1\" xmlns=\"http://schemas.microsoft.com/powershell/2004/04\"><Obj S=\"progress\"><MS><PR N=\"Record\"><AV>Preparing modules for first use.</AV></PR></MS></Obj></Objs>"
	if got := CleanPowerShellStream(progressOnly); got != "" {
		t.Fatalf("progress-only CLIXML should clean to empty string, got %q", got)
	}
	withError := "#< CLIXML\r\n<Objs Version=\"1.1.0.1\" xmlns=\"http://schemas.microsoft.com/powershell/2004/04\"><S S=\"Error\">Out-File : Access denied._x000D__x000A_</S><S S=\"Error\">At line:1 char:1_x000D__x000A_</S></Objs>"
	got := CleanPowerShellStream(withError)
	if !strings.Contains(got, "Out-File : Access denied.") || !strings.Contains(got, "At line:1 char:1") || strings.Contains(got, "#< CLIXML") {
		t.Fatalf("decoded CLIXML mismatch: %q", got)
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
