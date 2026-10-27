package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

func sourceJoinFilesFixture(t *testing.T) (*os.Root, []byte, engineidentity.Binding) {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	if root.MkdirAll("var/lib/rancher/k3s/server/tls", 0700) != nil {
		t.Fatal("source directories")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	digest := sha256.Sum256(ca)
	token := []byte("K10" + hex.EncodeToString(digest[:]) + "::server:private fixture secret\n")
	if root.WriteFile("var/lib/rancher/k3s/server/token", token, 0600) != nil || root.WriteFile("var/lib/rancher/k3s/server/tls/server-ca.crt", ca, 0644) != nil {
		t.Fatal("source files")
	}
	source := engineidentity.Binding{ClusterID: bootstrapHostFilesFixture().Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
	return root, token, source
}

func TestSourceK3sJoinMaterialScopeAndDrift(t *testing.T) {
	for _, mode := range []string{"complete", "token changed", "token replaced", "CA changed", "lost authority", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root, token, source := sourceJoinFilesFixture(t)
			expected := bootstrapHostFilesFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lost := false
			prove := func(context.Context) error {
				if lost {
					return errBootstrapSource
				}
				return nil
			}
			var retained *clusterbootstrap.K3sJoinMaterial
			var refresh func(context.Context) error
			calls := 0
			err := withSourceK3sJoinMaterialAt(ctx, root, expected, source, prove, func(ctx context.Context, m *clusterbootstrap.K3sJoinMaterial, check func(context.Context) error) error {
				calls++
				retained, refresh = m, check
				var output bytes.Buffer
				if m.WriteToken(ctx, expected, source, check, &output) != nil || !bytes.Equal(output.Bytes(), bytes.TrimSuffix(token, []byte{'\n'})) {
					t.Fatal("source export mismatch")
				}
				switch mode {
				case "token changed":
					if root.WriteFile("var/lib/rancher/k3s/server/token", []byte("changed"), 0600) != nil {
						t.Fatal("fixture mutation")
					}
				case "token replaced":
					if root.Rename("var/lib/rancher/k3s/server/token", "var/lib/rancher/k3s/server/old-token") != nil || root.WriteFile("var/lib/rancher/k3s/server/token", token, 0600) != nil {
						t.Fatal("fixture replacement")
					}
				case "CA changed":
					if root.WriteFile("var/lib/rancher/k3s/server/tls/server-ca.crt", []byte("changed"), 0644) != nil {
						t.Fatal("fixture mutation")
					}
				case "lost authority":
					lost = true
				case "cancel":
					cancel()
				}
				return nil
			})
			if (err == nil) != (mode == "complete") || calls != 1 {
				t.Fatal("wrong scoped result", err, calls)
			}
			if refresh(context.Background()) == nil {
				t.Fatal("closed source capability survived")
			}
			var output bytes.Buffer
			if retained.WriteToken(context.Background(), expected, source, func(context.Context) error { return nil }, &output) == nil || output.Len() != 0 {
				t.Fatal("private material escaped source scope")
			}
		})
	}
}

func TestSourceK3sJoinMaterialRejectsForeignFiles(t *testing.T) {
	for _, mode := range []string{"public token", "linked token", "hardlinked token", "unsafe parent", "missing CA", "oversized token", "invalid binding", "initial authority"} {
		t.Run(mode, func(t *testing.T) {
			root, _, source := sourceJoinFilesFixture(t)
			expected := bootstrapHostFilesFixture()
			prove := func(context.Context) error { return nil }
			switch mode {
			case "public token":
				if root.Chmod("var/lib/rancher/k3s/server/token", 0644) != nil {
					t.Fatal("fixture mode")
				}
			case "linked token":
				if root.Remove("var/lib/rancher/k3s/server/token") != nil || root.Symlink("tls/server-ca.crt", "var/lib/rancher/k3s/server/token") != nil {
					t.Fatal("fixture link")
				}
			case "hardlinked token":
				if root.Link("var/lib/rancher/k3s/server/token", "var/lib/rancher/k3s/server/alias") != nil {
					t.Fatal("fixture link")
				}
			case "unsafe parent":
				if root.Chmod("var/lib/rancher/k3s/server", 0777) != nil {
					t.Fatal("fixture mode")
				}
			case "missing CA":
				if root.Remove("var/lib/rancher/k3s/server/tls/server-ca.crt") != nil {
					t.Fatal("fixture removal")
				}
			case "oversized token":
				if root.WriteFile("var/lib/rancher/k3s/server/token", bytes.Repeat([]byte{'x'}, clusterbootstrap.MaxK3sJoinTokenBytes+2), 0600) != nil {
					t.Fatal("fixture bytes")
				}
			case "invalid binding":
				source.SourceID = expected.Target.TargetID
				prove = func(context.Context) error { t.Fatal("invalid binding reached source observation"); return nil }
			case "initial authority":
				prove = func(context.Context) error { return errBootstrapSource }
			}
			called := false
			err := withSourceK3sJoinMaterialAt(context.Background(), root, expected, source, prove, func(context.Context, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error {
				called = true
				return nil
			})
			if err == nil || called {
				t.Fatal("unsafe source exposed private material", err)
			}
		})
	}
}
