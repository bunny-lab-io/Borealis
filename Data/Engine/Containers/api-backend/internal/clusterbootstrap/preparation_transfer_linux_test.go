package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsumePreparationRequiresCompleteBoundInput(t *testing.T) {
	p, journal, _, destination := sourcePublicationFixture(t)
	expected := p.configuration.wire.Expected
	_, manager, _, _ := p.bundle.ManagerProof()
	var wire bytes.Buffer
	if err := p.WritePreparation(context.Background(), expected, &wire, mutationAuthority); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"trailing", "truncated", "helper mismatch", "consumer failure", "valid"} {
		t.Run(mode, func(t *testing.T) {
			raw := bytes.Clone(wire.Bytes())
			digest := manager
			if mode == "trailing" {
				raw = append(raw, 'x')
			}
			if mode == "truncated" {
				raw = raw[:len(raw)-1]
			}
			if mode == "helper mismatch" {
				digest = strings.Repeat("0", 64)
			}
			parent := mutationTempDir(t)
			called := false
			err := ConsumePreparation(context.Background(), parent, expected, digest, bytes.NewReader(raw), mutationAuthority, func(ctx context.Context, inputs *PreparationInputs) error {
				called = true
				if mode == "consumer failure" {
					return errors.New("private consumer failure")
				}
				_, err := inputs.publishSource(ctx, journal, destination)
				return err
			})
			if (err == nil) != (mode == "valid") {
				t.Fatal("unexpected consumption result", err)
			}
			if called != (mode == "valid" || mode == "consumer failure") {
				t.Fatal("unverified stream reached consumer")
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 0 {
				t.Fatal("scratch retained")
			}
		})
	}
}

func TestPreparationTransferAndAtomicPublication(t *testing.T) {
	p, j, _, destination := sourcePublicationFixture(t)
	expected := p.configuration.wire.Expected
	var stream bytes.Buffer
	if err := p.WritePreparation(context.Background(), expected, &stream, mutationAuthority); err != nil {
		t.Fatal(err)
	}
	stream.WriteString("next-frame")
	parent := mutationTempDir(t)
	received, err := ReceivePreparation(context.Background(), parent, expected, &stream, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	defer received.Close()
	if stream.String() != "next-frame" {
		t.Fatal("transfer consumed following frame")
	}
	want, _ := p.configuration.Export()
	got, _ := received.configuration.Export()
	if !bytes.Equal(want, got) {
		t.Fatal("configuration changed")
	}
	if _, err := received.publishSource(context.Background(), j, destination); err != nil {
		t.Fatal(err)
	}
	environment, _ := p.configuration.Environment()
	for _, name := range []string{"compose.env", "runtime.env"} {
		raw, err := os.ReadFile(filepath.Join(destination, "Borealis", "Engine", "Deploy", name))
		if err != nil || !bytes.Equal(raw, environment) {
			t.Fatal("published environment changed", err)
		}
	}
	if err := received.Close(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("receiver scratch retained")
	}
}

func TestPreparationTransferRejectsUnboundOrInterruptedStream(t *testing.T) {
	p, _, _, _ := sourcePublicationFixture(t)
	expected := p.configuration.wire.Expected
	var stream bytes.Buffer
	if err := p.WritePreparation(context.Background(), expected, &stream, mutationAuthority); err != nil {
		t.Fatal(err)
	}
	base := stream.Bytes()
	configStart := len(preparationTransferMagic) + 4
	configSize := int(binary.BigEndian.Uint32(base[len(preparationTransferMagic):]))
	manifestStart := configStart + configSize + 4
	manifestSize := int(binary.BigEndian.Uint32(base[manifestStart-4:]))
	archiveStart := manifestStart + manifestSize
	for _, mode := range []string{"magic", "zero configuration", "oversized configuration", "truncated configuration", "configuration bytes", "oversized manifest", "manifest bytes", "archive bytes", "truncated archive", "trailer", "target claim", "source", "authority", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			raw := bytes.Clone(base)
			want := expected
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "magic":
				raw[0] ^= 1
			case "zero configuration":
				binary.BigEndian.PutUint32(raw[len(preparationTransferMagic):], 0)
			case "oversized configuration":
				binary.BigEndian.PutUint32(raw[len(preparationTransferMagic):], MaxPreparationConfigBytes+1)
			case "truncated configuration":
				raw = raw[:configStart+3]
			case "configuration bytes":
				raw[configStart] = '['
			case "oversized manifest":
				binary.BigEndian.PutUint32(raw[manifestStart-4:], MaxManifestBytes+1)
			case "manifest bytes":
				raw[manifestStart] = '['
			case "archive bytes":
				raw[archiveStart+50] ^= 1
			case "truncated archive":
				raw = raw[:archiveStart+100]
			case "trailer":
				raw[len(raw)-1] ^= 1
			case "target claim":
				want.Target.Generation++
			case "source":
				want.Source.Release = "2026.09.999-rc.2"
			case "cancellation":
				cancel()
			}
			parent := mutationTempDir(t)
			calls := 0
			check := func(context.Context) error {
				calls++
				if mode == "authority" && calls >= 3 {
					return errors.New("private-error")
				}
				return nil
			}
			got, err := ReceivePreparation(ctx, parent, want, bytes.NewReader(raw), check)
			if err == nil || got != nil || err.Error() != "node preparation configuration invalid or changed; private data withheld" {
				t.Fatal("invalid input escaped", err)
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 0 {
				t.Fatal("failed transfer retained scratch")
			}
		})
	}
}

type preparationRejectWriter struct{ bytes int }

func (w *preparationRejectWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	return 0, errors.New("private sink error")
}

func TestPreparationTransferWriterFencesAndPrivacy(t *testing.T) {
	for _, mode := range []string{"changed archive", "changed claim", "authority", "output failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			p, _, _, _ := sourcePublicationFixture(t)
			expected := p.configuration.wire.Expected
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var output bytes.Buffer
			var writer io.Writer = &output
			check := mutationAuthority
			switch mode {
			case "changed archive":
				if err := os.WriteFile(p.bundle.ArchivePath(), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed claim":
				expected.Target.Generation++
			case "authority":
				check = func(context.Context) error { return errors.New("private authority error") }
			case "output failure":
				writer = &preparationRejectWriter{}
			case "cancelled":
				cancel()
			}
			if err := p.WritePreparation(ctx, expected, writer, check); err != ErrPreparationConfig {
				t.Fatal("private diagnostic escaped", err)
			}
			if output.Len() != 0 {
				t.Fatal("unauthorized input was transferred")
			}
		})
	}
}
