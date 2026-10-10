package engineidentity

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"
)

func fixture(t *testing.T) (*Material, Binding) {
	t.Helper()
	_, jwt, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, script, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 64)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	material := &Material{secret: base64.RawURLEncoding.EncodeToString(secret), jwt: jwt, script: script}
	binding := Binding{ClusterID: "10000000-0000-4000-8000-000000000001",
		SourceID: "20000000-0000-4000-8000-000000000002", SourceUID: "30000000-0000-4000-8000-000000000003"}
	return material, binding
}

func TestMaterialRoundTripPreservesExistingTrust(t *testing.T) {
	original, binding := fixture(t)
	raw, err := original.Export(binding)
	if err != nil {
		t.Fatal(err)
	}
	replica, err := Import(raw, binding)
	if err != nil || !original.Equal(replica) {
		t.Fatalf("identity round trip failed: %v", err)
	}
	message := []byte("existing token or signed Agent work")
	for name, keys := range map[string][2]ed25519.PrivateKey{
		"agent JWT": {original.jwt, replica.jwt}, "script signing": {original.script, replica.script},
	} {
		signature := ed25519.Sign(keys[0], message)
		if !ed25519.Verify(keys[1].Public().(ed25519.PublicKey), message, signature) {
			t.Errorf("existing %s trust changed", name)
		}
	}
	mac := func(secret string) []byte {
		h := hmac.New(sha256.New, []byte(secret))
		h.Write(message)
		return h.Sum(nil)
	}
	if !hmac.Equal(mac(original.secret), mac(replica.secret)) {
		t.Fatal("session/passkey HMAC identity changed")
	}
	other, _ := fixture(t)
	if original.Equal(other) {
		t.Fatal("independent node identity accepted as matching")
	}
	files := replica.Files()
	files[EngineSecretPath][0] ^= 1
	if !original.Equal(replica) {
		t.Fatal("exported file mutation changed retained material")
	}
}

func TestMaterialRejectsMalformedAndInconsistentFiles(t *testing.T) {
	valid, _ := fixture(t)
	other, _ := fixture(t)
	wrongType, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongDER, err := x509.MarshalPKCS8PrivateKey(wrongType)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string][]byte){
		"missing":               func(f map[string][]byte) { delete(f, AgentJWTPath) },
		"unknown path":          func(f map[string][]byte) { f["extra"] = []byte("unexpected") },
		"path replacement":      func(f map[string][]byte) { f["../other"] = f[AgentJWTPath]; delete(f, AgentJWTPath) },
		"empty":                 func(f map[string][]byte) { f[EngineSecretPath] = nil },
		"short secret":          func(f map[string][]byte) { f[EngineSecretPath] = f[EngineSecretPath][:4] },
		"secret controls":       func(f map[string][]byte) { f[EngineSecretPath][4] = '\n' },
		"oversized":             func(f map[string][]byte) { f[AgentJWTPath] = make([]byte, MaxEnvelopeBytes) },
		"PEM trailing material": func(f map[string][]byte) { f[AgentJWTPath] = append(f[AgentJWTPath], f[AgentJWTPath]...) },
		"PEM leading garbage":   func(f map[string][]byte) { f[AgentJWTPath] = append([]byte("garbage\n"), f[AgentJWTPath]...) },
		"PKCS8 wrong algorithm": func(f map[string][]byte) {
			f[AgentJWTPath] = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: wrongDER})
		},
		"mismatched public": func(f map[string][]byte) { f[ScriptPublicPath] = other.Files()[ScriptPublicPath] },
		"malformed public":  func(f map[string][]byte) { f[ScriptPublicPath] = []byte("invalid") },
	} {
		t.Run(name, func(t *testing.T) {
			files := valid.Files()
			mutate(files)
			if _, err := ParseFiles(files); err == nil {
				t.Fatal("invalid material accepted")
			} else if strings.Contains(err.Error(), valid.secret) {
				t.Fatal("error disclosed material")
			}
		})
	}
}

func TestEnvelopeRejectsAmbiguityAndChangedSource(t *testing.T) {
	material, binding := fixture(t)
	raw, err := material.Export(binding)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	changed := func(name string, value string) []byte {
		copy := map[string]json.RawMessage{}
		for key, rawValue := range fields {
			copy[key] = rawValue
		}
		if value == "" {
			delete(copy, name)
		} else {
			copy[name] = json.RawMessage(value)
		}
		data, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for name, input := range map[string][]byte{
		"empty":               nil,
		"array":               []byte("[]"),
		"oversized":           bytes.Repeat([]byte(" "), MaxEnvelopeBytes+1),
		"trailing document":   append(append([]byte{}, raw...), []byte("{}")...),
		"duplicate":           append([]byte(`{"version":1,`), raw[1:]...),
		"escaped duplicate":   append([]byte(`{"\u0076ersion":1,`), raw[1:]...),
		"unknown":             changed("extra", "true"),
		"wrong case":          changed("Version", "1"),
		"missing":             changed("script_public_der", ""),
		"null":                changed("version", "null"),
		"wrong version":       changed("version", "2"),
		"version type":        changed("version", `"1"`),
		"different cluster":   changed("cluster_id", `"90000000-0000-4000-8000-000000000001"`),
		"different source":    changed("source_node_id", `"90000000-0000-4000-8000-000000000002"`),
		"reused hostname UID": changed("source_node_uid", `"90000000-0000-4000-8000-000000000003"`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Import(input, binding); err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
	if _, err := Import(raw, Binding{}); err == nil {
		t.Fatal("missing independent authority accepted")
	}
}

func TestMaterialCanonicalizationAndRedaction(t *testing.T) {
	material, binding := fixture(t)
	files := material.Files()
	files[AgentJWTPath] = bytes.ReplaceAll(files[AgentJWTPath], []byte("\n"), []byte("\r\n"))
	files[EngineSecretPath] = append([]byte(" \t"), files[EngineSecretPath]...)
	canonical, err := ParseFiles(files)
	if err != nil || !material.Equal(canonical) {
		t.Fatalf("formatting changed identity: %v", err)
	}
	for _, formatted := range []string{fmt.Sprint(*material), fmt.Sprintf("%+v", material), fmt.Sprintf("%#v", material)} {
		if strings.Contains(formatted, material.secret) || !strings.Contains(formatted, "redacted") {
			t.Fatal("material formatting must redact private data")
		}
	}
	for _, invalid := range []*Material{nil, {}} {
		if invalid.Files() != nil || invalid.Digest() != "" || invalid.Equal(material) {
			t.Fatal("zero material must fail closed")
		}
		if _, err := invalid.Export(binding); err == nil {
			t.Fatal("zero material exported")
		}
	}
}
