package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sshIdentitySourceMaterial(t *testing.T, root string) *engineidentity.Material {
	t.Helper()
	_, jwt, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, script, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwtDER, _ := x509.MarshalPKCS8PrivateKey(jwt)
	scriptDER, _ := x509.MarshalPKCS8PrivateKey(script)
	public, _ := x509.MarshalPKIXPublicKey(script.Public())
	files := map[string][]byte{
		engineidentity.EngineSecretPath: []byte(strings.Repeat("s", 64) + "\n"),
		engineidentity.AgentJWTPath:     pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: jwtDER}),
		engineidentity.ScriptKeyPath:    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: scriptDER}),
		engineidentity.ScriptPublicPath: public,
	}
	for name, raw := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("write identity fixture %s: %v", name, err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatalf("write identity fixture %s: %v", name, err)
		}
	}
	material, err := engineidentity.ParseFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	return material
}

func TestClusterSSHSourceIdentityMaterialStaysLockedAndAuthorityBound(t *testing.T) {
	cohort, source, lease, baseline := sshPreparationFixture(t)
	current := clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: "v1.36.3+k3s1"}
	expected := engineidentity.Binding{ClusterID: source.ClusterID, SourceID: source.Members[0].NodeID, SourceUID: source.Members[0].NodeUID}
	root := t.TempDir()
	want := sshIdentitySourceMaterial(t, root)
	consumer, err := engineidentity.AcquireConsumer(root)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	calls := 0
	authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
		calls++
		value := current
		value.Cohort.ObservedAt += int64(calls)
		return value, nil
	}
	used := false
	err = withClusterSSHSourceIdentityMaterial(context.Background(), consumer, expected, authority,
		func(_ context.Context, got *engineidentity.Material) error {
			used = got.Equal(want)
			install := engineidentity.Installation{Root: root, Journal: filepath.Join(t.TempDir(), "journal"), Binding: expected,
				Target: engineidentity.TargetBinding{ProvisioningID: newClusterUUID(), NodeUID: newClusterUUID(), SSHFingerprint: "SHA256:" + strings.Repeat("A", 43)},
				UID:    os.Geteuid(), GID: os.Getegid(), Check: func() error { return nil }, Quiesced: func() error { return nil }}
			if _, installErr := install.InstallFenced(got); installErr == nil {
				t.Fatal("source acquisition released consumer lock during callback")
			}
			return nil
		})
	if err != nil || !used || calls != 3 {
		t.Fatalf("identity acquisition failed: err=%v used=%t authority_calls=%d", err, used, calls)
	}
}

func TestClusterSSHSourceIdentityMaterialFailsClosed(t *testing.T) {
	for _, mode := range []string{"nil consumer", "closed consumer", "zero binding", "wrong cluster", "wrong member", "wrong Node UID", "authority error", "authority drift", "source drift after use", "consumer callback", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			cohort, source, lease, baseline := sshPreparationFixture(t)
			current := clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: "v1.36.3+k3s1"}
			expected := engineidentity.Binding{ClusterID: source.ClusterID, SourceID: source.Members[0].NodeID, SourceUID: source.Members[0].NodeUID}
			root := t.TempDir()
			sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			if mode == "nil consumer" {
				consumer = nil
			} else if mode == "closed consumer" {
				consumer.Close()
			}
			switch mode {
			case "zero binding":
				expected.SourceID = "00000000-0000-0000-0000-000000000000"
			case "wrong cluster":
				expected.ClusterID = newClusterUUID()
			case "wrong member":
				expected.SourceID = newClusterUUID()
			case "wrong Node UID":
				expected.SourceUID = newClusterUUID()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				calls++
				value := current
				value.Cohort.ObservedAt += int64(calls)
				if mode == "authority error" {
					return value, errors.New("private authority detail")
				}
				if mode == "authority drift" && calls > 1 {
					value.Lease.Generation++
				}
				if mode == "source drift after use" && calls > 2 {
					value.Source.Members[0].BootID = newClusterUUID()
				}
				return value, nil
			}
			used := false
			consume := func(context.Context, *engineidentity.Material) error {
				used = true
				if mode == "cancelled" {
					cancel()
				}
				if mode == "consumer callback" {
					return errors.New("private transport detail")
				}
				return nil
			}
			err = withClusterSSHSourceIdentityMaterial(ctx, consumer, expected, authority, consume)
			if !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || strings.Contains(err.Error(), "authority detail") || strings.Contains(err.Error(), "transport detail") {
				t.Fatalf("unsafe failure result: %v", err)
			}
			if used && mode != "source drift after use" && mode != "consumer callback" && mode != "cancelled" {
				t.Fatal("consumer ran without complete acquisition authority")
			}
		})
	}
}
