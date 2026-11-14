package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

func joinCAFixture(t *testing.T) ([]byte, *x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, SubjectKeyId: []byte{1, 2, 3}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cert, private
}

func joinMaterialFixture(t *testing.T) (PreparationExpected, engineidentity.Binding, []byte, []byte) {
	t.Helper()
	r := artifactSessionRequestFixture(t)
	expected, err := r.Expected()
	if err != nil {
		t.Fatal(err)
	}
	ca, _, _ := joinCAFixture(t)
	digest := sha256.Sum256(ca)
	token := []byte("K10" + hex.EncodeToString(digest[:]) + "::server:private:password$`with spaces")
	return expected, *r.IdentitySource, token, ca
}

func TestK3sJoinMaterialBindingOwnershipAndRedaction(t *testing.T) {
	expected, source, token, ca := joinMaterialFixture(t)
	want := bytes.Clone(token)
	m, err := NewK3sJoinMaterial(expected, source, token, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	clear(token)
	clear(ca)
	for _, value := range []any{m, *m} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			if got := fmt.Sprintf(format, value); strings.Contains(got, "private") || strings.Contains(got, "K10") || !strings.Contains(got, "redacted") {
				t.Fatal("private formatting escaped")
			}
		}
		if raw, err := json.Marshal(value); err == nil || bytes.Contains(raw, want) {
			t.Fatal("generic JSON exported secret")
		}
	}
	var output bytes.Buffer
	checks := 0
	check := func(context.Context) error { checks++; return nil }
	if m.WriteToken(context.Background(), expected, source, check, &output) != nil || !bytes.Equal(output.Bytes(), want) || checks != 2 {
		t.Fatal("guarded export lost input ownership")
	}
	changed := expected
	changed.Target.Generation++
	takeover, err := NewK3sJoinMaterial(changed, source, want, m.owned.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer takeover.Close()
	if takeover.owned.digest != m.owned.digest {
		t.Fatal("new claim changed immutable effect identity")
	}
	output.Reset()
	if m.WriteToken(context.Background(), changed, source, check, &output) == nil || output.Len() != 0 {
		t.Fatal("foreign claim received token")
	}
	changedSource := source
	changedSource.SourceUID = "99999999-9999-4999-8999-999999999999"
	if m.WriteToken(context.Background(), expected, changedSource, check, &output) == nil || output.Len() != 0 {
		t.Fatal("foreign source received token")
	}
	owned := m.owned.token
	if m.Close() != nil || m.Close() != nil || !bytes.Equal(owned, make([]byte, len(owned))) {
		t.Fatal("close failed to clear private material")
	}
	if m.WriteToken(context.Background(), expected, source, check, &output) == nil || output.Len() != 0 {
		t.Fatal("closed material exported")
	}
}

func TestK3sJoinMaterialRejectsUnsafeInputs(t *testing.T) {
	for _, mode := range []string{"short", "agent", "bootstrap", "wrong CA", "empty password", "newline", "NUL", "oversized token", "invalid CA", "trailing CA junk", "oversized CA", "wrong version", "target is source"} {
		t.Run(mode, func(t *testing.T) {
			expected, source, token, ca := joinMaterialFixture(t)
			switch mode {
			case "short":
				token = []byte("password")
			case "agent":
				token = bytes.Replace(token, []byte("::server:"), []byte("::node:"), 1)
			case "bootstrap":
				token = append(token[:67], []byte("::abcdef.0123456789abcdef")...)
			case "wrong CA":
				ca, _, _ = joinCAFixture(t)
			case "empty password":
				token = token[:76]
			case "newline":
				token = append(token, '\n')
			case "NUL":
				token = append(token, 0)
			case "oversized token":
				token = append(token, bytes.Repeat([]byte{'x'}, MaxK3sJoinTokenBytes)...)
			case "invalid CA":
				ca = []byte("invalid")
			case "trailing CA junk":
				ca = append(ca, []byte("junk")...)
			case "oversized CA":
				ca = bytes.Repeat([]byte{'x'}, MaxK3sJoinCABytes+1)
			case "wrong version":
				expected.K3sVersion = "v1.36.4+k3s1"
			case "target is source":
				source.SourceID = expected.Target.TargetID
			}
			if m, err := NewK3sJoinMaterial(expected, source, token, ca); err == nil || m != nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
}

func TestK3sJoinMaterialCustomCAChain(t *testing.T) {
	ca, root, key := joinCAFixture(t)
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "fixture intermediate"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, SubjectKeyId: []byte{4, 5, 6}, AuthorityKeyId: root.SubjectKeyId}
	der, err := x509.CreateCertificate(rand.Reader, template, root, public, key)
	if err != nil {
		t.Fatal(err)
	}
	bundle := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ca...)
	hash, err := k3sJoinCAHash(bundle)
	want := sha256.Sum256(root.Raw)
	if err != nil || hash != hex.EncodeToString(want[:]) {
		t.Fatal("custom chain did not bind root DER", err)
	}
	foreign, _, _ := joinCAFixture(t)
	if _, err := k3sJoinCAHash(append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), foreign...)); err == nil {
		t.Fatal("invalid chain fell back to unverified PEM hash")
	}
}
