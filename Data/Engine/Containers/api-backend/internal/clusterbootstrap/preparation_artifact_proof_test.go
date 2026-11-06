package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparationArtifactProofWithoutTargetClaim(t *testing.T) {
	e, manifest, members := archiveFixture(t)
	archive := pack(t, members)
	for _, mode := range []string{"valid", "source bytes", "archive bytes", "helper bytes", "extra source", "wrong release", "wrong K3s", "different manifest", "authority", "cancelled", "closed"} {
		t.Run(mode, func(t *testing.T) {
			b, err := stageFixture(t, context.Background(), e, manifest, archive, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			expected, version := e, K3sPins().Version
			publication, err := ParseManifest(marshal(t, manifest), e)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			check := func(context.Context) error {
				calls++
				if mode == "authority" && calls == 2 {
					return ErrSessionAuthority
				}
				return nil
			}
			path := map[string]string{"source bytes": filepath.Join(b.SourcePath(), "Engine.sh"), "archive bytes": b.ArchivePath(), "helper bytes": b.ManagerPath(), "extra source": filepath.Join(b.SourcePath(), "unexpected")}[mode]
			if path != "" {
				if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "wrong release":
				expected.SourceSHA = strings.Repeat("f", 40)
			case "wrong K3s":
				version = "v0.0.0+k3s1"
			case "different manifest":
				publication.asset.SHA256 = strings.Repeat("f", 64)
			case "cancelled":
				cancel()
			case "closed":
				if err := b.Close(); err != nil {
					t.Fatal(err)
				}
			}
			proof, err := b.PreparationArtifactProof(ctx, expected, version, publication, check)
			if (err == nil) != (mode == "valid") || (err != nil && proof != "") {
				t.Fatal(mode, proof, err)
			}
			if mode == "valid" {
				again, err := b.PreparationArtifactProof(ctx, expected, version, publication, check)
				if err != nil || again != proof || len(proof) != 64 || calls != 4 {
					t.Fatal("reproof failed", err)
				}
			}
		})
	}
}

// The preclaim adapter consumes the same complete byte writer as native transfer.
// Mutating an owned archive after its first proof must invalidate every family.
func TestPreparationArtifactProofRechecksEveryFamily(t *testing.T) {
	for _, family := range []string{"application", "K3s", "external", "PostgreSQL"} {
		t.Run(family, func(t *testing.T) {
			a, _, _ := preparationArtifactsFixture(t)
			ctx, check := context.Background(), func(context.Context) error { return nil }
			first, second := sha256.New(), sha256.New()
			if a.WriteArtifacts(ctx, first, check) != nil || a.WriteArtifacts(ctx, second, check) != nil || string(first.Sum(nil)) != string(second.Sum(nil)) {
				t.Fatal("complete byte proof unstable")
			}
			paths := []string{a.application.path, a.k3s.path, a.external.path, a.postgres.set.path}
			var path string
			switch family {
			case "application":
				path = filepath.Join(a.application.path, ImageAssetName(a.application.inventory.Images()[0].Role))
			case "K3s":
				path = filepath.Join(a.k3s.path, K3sBinaryName)
			case "external":
				for _, name := range a.external.names {
					path = filepath.Join(a.external.path, name)
					break
				}
			case "PostgreSQL":
				path = filepath.Join(a.postgres.set.path, a.postgres.set.names[a.postgresReference])
			}
			raw, err := os.ReadFile(path)
			if err != nil || len(raw) == 0 {
				t.Fatal("fixture bytes", err)
			}
			raw[0] ^= 1
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if a.WriteArtifacts(ctx, io.Discard, check) == nil {
				t.Fatal("changed owned bytes accepted")
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("scratch retained", err)
				}
			}
		})
	}
}
