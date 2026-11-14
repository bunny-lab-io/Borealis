package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestHostPackagePreparationTransfer(t *testing.T) {
	p, _, _, _ := sourcePublicationFixture(t)
	_, _, install, _, material := joinedIdentityFixture(t)
	expected := p.configuration.wire.Expected
	source := install.Binding
	source.ClusterID = expected.Target.ClusterID
	_, manager, _, _ := p.bundle.ManagerProof()
	plan, digest := hostPackageTestPlan(t)
	archive := hostPackageTestArchive(t, plan, "valid")
	_, wire := hostPackageTestInventory(t, plan, digest, archive)
	wire.Repository, wire.Release, wire.SourceSHA = expected.Source.Repository, expected.Source.Release, expected.Source.SourceSHA
	raw, _ := json.Marshal(wire)
	parse := func(raw []byte, expected Expected) (*HostPackageInventory, error) {
		return parseHostPackageInventory(raw, expected, plan, digest)
	}
	inventory, err := parse(raw, expected.Source)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	set, err := StageHostPackages(ctx, t.TempDir(), expected.Source, inventory,
		func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(archive)), nil }, mutationAuthority)
	if err != nil || p.BindHostPackages(ctx, expected, set, mutationAuthority) != nil {
		t.Fatal("package owner not bound", err)
	}
	defer p.Close()
	if p.BindHostPackages(ctx, expected, set, mutationAuthority) == nil {
		t.Fatal("owner could be replaced")
	}
	var stream bytes.Buffer
	if err := p.WritePrejoinHostPreparation(ctx, expected, source, material, &stream, mutationAuthority); err != nil {
		t.Fatal(err)
	}
	defer clear(stream.Bytes())
	for _, mode := range []string{"valid", "truncated", "trailer", "extra bytes", "missing packages", "archive drift", "old receiver", "public pins", "consumer failure", "authority loss"} {
		t.Run(mode, func(t *testing.T) {
			input := bytes.Clone(stream.Bytes())
			defer clear(input)
			switch mode {
			case "truncated":
				input = input[:len(input)-1]
			case "trailer":
				input[len(input)-1] ^= 1
			case "extra bytes":
				input = append(input, 1)
			case "missing packages":
				var prefix bytes.Buffer
				if p.WritePrejoinPreparation(ctx, expected, source, material, &prefix, mutationAuthority) != nil {
					t.Fatal("source fixture failed")
				}
				input = prefix.Bytes()
				defer clear(input)
			case "archive drift":
				input[len(input)-33] ^= 1
			}
			parent := t.TempDir()
			if os.WriteFile(filepath.Join(parent, "caller"), []byte("keep"), 0600) != nil {
				t.Fatal("fixture")
			}
			called := false
			check := func(context.Context) error {
				if mode == "authority loss" {
					return ErrSessionAuthority
				}
				return nil
			}
			consume := func(ctx context.Context, got *PreparationInputs, keys *engineidentity.Material) error {
				called = true
				if !keys.Equal(material) {
					t.Fatal("shared identity changed")
				}
				if err := got.WithHostPackages(ctx, expected, check, func(ctx context.Context, packages *HostPackageSet) error {
					var body bytes.Buffer
					if packages.WriteArchive(ctx, &body, check) != nil || !bytes.Equal(body.Bytes(), archive) {
						t.Fatal("consumer received changed package bytes")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if mode == "consumer failure" {
					return ErrMutationUnknown
				}
				return nil
			}
			var err error
			switch mode {
			case "old receiver":
				err = ConsumePrejoinPreparation(ctx, parent, expected, manager, source, bytes.NewReader(input), check, consume)
			case "public pins":
				err = ConsumePrejoinHostPreparation(ctx, parent, expected, manager, source, bytes.NewReader(input), check, consume)
			default:
				err = consumePrejoinHostPreparation(ctx, parent, expected, manager, source, bytes.NewReader(input), check, consume, parse)
			}
			if (err == nil) != (mode == "valid") || called != (mode == "valid" || mode == "consumer failure") {
				t.Fatal("incomplete package input reached host consumer", called, err)
			}
			left, err := os.ReadDir(parent)
			if err != nil || len(left) != 1 || left[0].Name() != "caller" {
				t.Fatal("ownership cleanup failed", err)
			}
		})
	}
}
