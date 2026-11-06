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

func TestConsumePrejoinK3sJoinPreparation(t *testing.T) {
	p, _, _, _ := sourcePublicationFixture(t)
	_, _, install, _, identity := joinedIdentityFixture(t)
	expected := p.configuration.wire.Expected
	source := install.Binding
	source.ClusterID = expected.Target.ClusterID
	_, manager, _, _ := p.bundle.ManagerProof()
	artifacts, kp, ep := preparationArtifactsForSourceFixture(t, expected.Source)
	_, _, token, ca := joinMaterialFixture(t)
	join, err := NewK3sJoinMaterial(expected, source, token, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer join.Close()
	var wire bytes.Buffer
	defer func() { clear(wire.Bytes()) }()
	ctx := context.Background()
	if p.WritePrejoinK3sJoinPreparation(ctx, expected, source, identity, artifacts, artifacts.postgresReference, join, &wire, mutationAuthority) != nil {
		t.Fatal("composed join frame")
	}
	for _, mode := range []string{"complete", "truncated", "tampered", "trailing", "missing join", "foreign join claim", "old artifact consumer", "consumer failure"} {
		t.Run(mode, func(t *testing.T) {
			raw := bytes.Clone(wire.Bytes())
			start := bytes.Index(raw, []byte(k3sJoinTransferMagic))
			if start < 0 {
				t.Fatal("missing join fixture frame")
			}
			switch mode {
			case "truncated":
				raw = raw[:len(raw)-1]
			case "tampered":
				raw[len(raw)-1] ^= 1
			case "trailing":
				raw = append(raw, 'x')
			case "missing join":
				raw = raw[:start]
			case "foreign join claim":
				foreign := expected
				foreign.Target.Generation++
				m, err := NewK3sJoinMaterial(foreign, source, token, ca)
				if err != nil {
					t.Fatal(err)
				}
				defer m.Close()
				var frame bytes.Buffer
				if m.WriteJoinMaterial(ctx, foreign, source, &frame, mutationAuthority) != nil {
					t.Fatal("foreign frame fixture")
				}
				raw = append(raw[:start], frame.Bytes()...)
				clear(frame.Bytes())
			}
			defer clear(raw)
			parent := t.TempDir()
			if os.WriteFile(filepath.Join(parent, "retain"), []byte("caller"), 0600) != nil {
				t.Fatal("caller fixture")
			}
			called := false
			var retained *K3sJoinMaterial
			consume := func(ctx context.Context, inputs *PreparationInputs, keys *engineidentity.Material, a *PreparationArtifacts, m *K3sJoinMaterial) error {
				called = true
				retained = m
				if inputs.Verify(ctx, expected, mutationAuthority) != nil || !keys.Equal(identity) || a.verifyBindings(ctx, mutationAuthority) != nil || m.Verify(ctx, expected, source, mutationAuthority) != nil {
					t.Fatal("unproved composed input")
				}
				if mode == "consumer failure" {
					return ErrMutationUnknown
				}
				return nil
			}
			receive := func(ctx context.Context, in io.Reader) (*PreparationArtifacts, error) {
				return receivePreparationArtifacts(ctx, parent, expected.Source, expected.K3sVersion, artifacts.postgresReference, in, mutationAuthority, kp, ep)
			}
			var err error
			if mode == "old artifact consumer" {
				err = consumePrejoinArtifactPreparation(ctx, parent, expected, manager, source, artifacts.postgresReference, bytes.NewReader(raw), mutationAuthority,
					func(context.Context, *PreparationInputs, *engineidentity.Material, *PreparationArtifacts) error {
						called = true
						return nil
					}, receive)
			} else {
				err = consumePrejoinK3sJoinPreparation(ctx, parent, expected, manager, source, artifacts.postgresReference, bytes.NewReader(raw), mutationAuthority, consume, receive)
			}
			if (err == nil) != (mode == "complete") || called != (mode == "complete" || mode == "consumer failure") {
				t.Fatal("partial/foreign join input reached consumer", mode, called, err)
			}
			if retained != nil && retained.Verify(ctx, expected, source, mutationAuthority) == nil {
				t.Fatal("private input escaped consumer lifetime")
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 1 || entries[0].Name() != "retain" {
				t.Fatal("incoming owned data leaked", err)
			}
		})
	}
}
