package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrejoinPreparationRequestBindsOriginalSource(t *testing.T) {
	expected, _ := preparationFixture()
	source := engineidentity.Binding{ClusterID: expected.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
	for _, action := range []string{"prepare_prejoin_identity_root", "reconcile_prejoin_identity_root", "install_prejoin_identity", "reconcile_prejoin_identity", "prepare_prejoin_host", "complete_prejoin_host", "complete_prejoin_firewall", "resume_fresh_prejoin_host", "resume_repository_prejoin_host", "reconcile_prejoin_host"} {
		r, err := NewPrejoinPreparationSessionRequest(expected, strings.Repeat("a", 64), strings.Repeat("b", 64), action, source)
		if err != nil || r.Session.Action != "verify" || r.Session.IdentityRecovery != nil {
			t.Fatal("pre-join request crossed joined contract", err)
		}
		original, _ := r.Digest()
		changed := *r.IdentitySource
		changed.SourceUID = "99999999-9999-4999-8999-999999999999"
		r.IdentitySource = &changed
		if digest, err := r.Digest(); err != nil || digest == original {
			t.Fatal("original source absent from request digest")
		}
		for _, mode := range []string{"missing", "cluster", "self", "zero", "uid", "source action", "joined action"} {
			invalid := r
			binding := source
			invalid.IdentitySource = &binding
			switch mode {
			case "missing":
				invalid.IdentitySource = nil
			case "cluster":
				binding.ClusterID = binding.SourceID
			case "self":
				binding.SourceID = expected.Target.TargetID
			case "zero":
				binding.SourceID = "00000000-0000-0000-0000-000000000000"
			case "uid":
				binding.SourceUID = "not-a-native-uid"
			case "source action":
				invalid.Action = "stage_source"
			case "joined action":
				invalid.Session.Action = "deliver_identity_recovery"
			}
			if _, err := invalid.Digest(); err == nil {
				t.Fatal("invalid source accepted", mode)
			}
		}
		if _, err := NewPreparationSessionRequest(expected, strings.Repeat("a", 64), strings.Repeat("b", 64), action); err == nil {
			t.Fatal("source-free constructor accepted identity action")
		}
	}
}

func TestConsumePrejoinPreparationRequiresCompletePrivateInput(t *testing.T) {
	p, _, _, _ := sourcePublicationFixture(t)
	_, _, install, _, material := joinedIdentityFixture(t)
	expected := p.configuration.wire.Expected
	source := install.Binding
	source.ClusterID = expected.Target.ClusterID
	_, manager, _, _ := p.bundle.ManagerProof()
	var wire bytes.Buffer
	if err := p.WritePrejoinPreparation(context.Background(), expected, source, material, &wire, mutationAuthority); err != nil {
		t.Fatal(err)
	}
	envelope, _ := material.Export(source)
	defer clear(envelope)
	offset := wire.Len() - len(envelope) - 36
	for _, mode := range []string{"valid", "truncated", "trailing", "digest", "oversized", "wrong source", "wrong helper", "authority", "consumer failure", "source-only consumer"} {
		t.Run(mode, func(t *testing.T) {
			raw := bytes.Clone(wire.Bytes())
			defer clear(raw)
			want, helper := source, manager
			switch mode {
			case "truncated":
				raw = raw[:len(raw)-1]
			case "trailing":
				raw = append(raw, 'x')
			case "digest":
				raw[len(raw)-1] ^= 1
			case "oversized":
				binary.BigEndian.PutUint32(raw[offset:], engineidentity.MaxEnvelopeBytes+1)
			case "wrong source":
				want.SourceUID = "99999999-9999-4999-8999-999999999999"
			case "wrong helper":
				helper = strings.Repeat("f", 64)
			}
			parent := mutationTempDir(t)
			called := false
			check := func(context.Context) error {
				if mode == "authority" {
					return errors.New("private authority")
				}
				return nil
			}
			consume := func(_ context.Context, inputs *PreparationInputs, received *engineidentity.Material) error {
				called = true
				if !received.Equal(material) || inputs == nil {
					t.Fatal("identity changed")
				}
				if mode == "consumer failure" {
					return errors.New("private failure")
				}
				return nil
			}
			var err error
			if mode == "source-only consumer" {
				err = ConsumePreparation(context.Background(), parent, expected, helper, bytes.NewReader(raw), check, func(context.Context, *PreparationInputs) error { called = true; return nil })
			} else {
				err = ConsumePrejoinPreparation(context.Background(), parent, expected, helper, want, bytes.NewReader(raw), check, consume)
			}
			if (err == nil) != (mode == "valid") || called != (mode == "valid" || mode == "consumer failure") {
				t.Fatal("invalid private stream consumed", err)
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 0 {
				t.Fatal("scratch retained")
			}
		})
	}
	// A source-only payload cannot reach the identity consumer either.
	var plain bytes.Buffer
	if p.WritePreparation(context.Background(), expected, &plain, mutationAuthority) != nil {
		t.Fatal("source transfer failed")
	}
	if ConsumePrejoinPreparation(context.Background(), mutationTempDir(t), expected, manager, source, &plain, mutationAuthority, func(context.Context, *PreparationInputs, *engineidentity.Material) error {
		t.Fatal("missing identity consumed")
		return nil
	}) == nil {
		t.Fatal("missing envelope accepted")
	}
	if p.WritePrejoinPreparation(context.Background(), expected, source, nil, io.Discard, mutationAuthority) == nil {
		t.Fatal("missing source material accepted")
	}
}
