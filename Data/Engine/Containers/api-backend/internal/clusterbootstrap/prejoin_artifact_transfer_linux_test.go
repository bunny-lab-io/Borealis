package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestConsumePrejoinArtifactPreparation(t *testing.T) {
	p, _, _, _ := sourcePublicationFixture(t)
	_, _, install, _, material := joinedIdentityFixture(t)
	expected := p.configuration.wire.Expected
	source := install.Binding
	source.ClusterID = expected.Target.ClusterID
	_, manager, _, _ := p.bundle.ManagerProof()
	a, kp, ep := preparationArtifactsForSourceFixture(t, expected.Source)
	var wire bytes.Buffer
	defer func() { clear(wire.Bytes()) }()
	ctx := context.Background()
	if err := p.WritePrejoinArtifactPreparation(ctx, expected, source, material, a, a.postgresReference, &wire, mutationAuthority); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"complete", "truncated", "trailer", "trailing", "missing artifacts", "wrong postgres", "public pins", "old consumer", "consumer failure", "authority"} {
		t.Run(mode, func(t *testing.T) {
			raw := bytes.Clone(wire.Bytes())
			defer clear(raw)
			switch mode {
			case "truncated":
				raw = raw[:len(raw)-1]
			case "trailer":
				raw[len(raw)-1] ^= 1
			case "trailing":
				raw = append(raw, 'x')
			case "missing artifacts":
				raw = raw[:bytes.Index(raw, []byte(artifactTransferMagic))]
			}
			parent := t.TempDir()
			if os.WriteFile(filepath.Join(parent, "retain"), []byte("caller"), 0600) != nil {
				t.Fatal("fixture")
			}
			pg := a.postgresReference
			if mode == "wrong postgres" {
				pg = PostgresImageRepository + "@sha256:" + mutationDigest("different source")
			}
			called := false
			check := func(context.Context) error {
				if mode == "authority" {
					return ErrSessionAuthority
				}
				return nil
			}
			consume := func(ctx context.Context, inputs *PreparationInputs, keys *engineidentity.Material, artifacts *PreparationArtifacts) error {
				called = true
				if inputs.Verify(ctx, expected, check) != nil || !keys.Equal(material) || artifacts.verifyBindings(ctx, check) != nil {
					t.Fatal("consumer received unproved inputs")
				}
				if mode == "consumer failure" {
					return ErrMutationUnknown
				}
				return nil
			}
			var err error
			switch mode {
			case "public pins":
				err = ConsumePrejoinArtifactPreparation(ctx, parent, expected, manager, source, pg, bytes.NewReader(raw), check, consume)
			case "old consumer":
				err = ConsumePrejoinPreparation(ctx, parent, expected, manager, source, bytes.NewReader(raw), check, func(context.Context, *PreparationInputs, *engineidentity.Material) error { called = true; return nil })
			default:
				err = consumePrejoinArtifactPreparation(ctx, parent, expected, manager, source, pg, bytes.NewReader(raw), check, consume,
					func(ctx context.Context, input io.Reader) (*PreparationArtifacts, error) {
						return receivePreparationArtifacts(ctx, parent, expected.Source, expected.K3sVersion, pg, input, check, kp, ep)
					})
			}
			if (err == nil) != (mode == "complete") || called != (mode == "complete" || mode == "consumer failure") {
				t.Fatal("partial input reached mutation", mode, called, err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 1 || entries[0].Name() != "retain" {
				t.Fatal("owned scratch leaked or caller data removed", err)
			}
		})
	}
}
