package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestExternalImageTransferReceiver(t *testing.T) {
	ctx := context.Background()
	expected := Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: imageTestSHA, AllowQualification: true}
	wire := externalInventoryWire{Version: 1, Repository: expected.Repository, Release: expected.Release, SourceSHA: expected.SourceSHA, Platform: "linux-amd64"}
	archives, pins := map[string][]byte{}, []ExternalImagePin{}
	for _, compiled := range ExternalImagePins() {
		raw, pin := externalFixtureReference(t, "gzip", compiled.Reference)
		proof, err := inspectExternalImageArchive(ctx, bytes.NewReader(raw), int64(len(raw)), pin)
		if err != nil {
			t.Fatal(err)
		}
		archives[pin.Reference], pins = raw, append(pins, pin)
		wire.Images = append(wire.Images, proof)
	}
	raw, _ := json.Marshal(wire)
	inventory, err := parseExternalImageInventory(raw, expected, pins)
	if err != nil {
		t.Fatal(err)
	}
	check := func(context.Context) error { return nil }
	inspect := func(ctx context.Context, input io.ReaderAt, size int64, reference string) (ExternalImageProof, error) {
		for _, pin := range pins {
			if pin.Reference == reference {
				return inspectExternalImageArchive(ctx, input, size, pin)
			}
		}
		return ExternalImageProof{}, ErrImageArchive
	}
	sender, err := stageExternalImages(ctx, t.TempDir(), inventory, func(_ context.Context, proof ExternalImageProof) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(archives[proof.Reference])), nil
	}, check, inspect)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	var stream bytes.Buffer
	if sender.WriteImages(ctx, &stream, check) != nil {
		t.Fatal("sender failed")
	}
	for _, mode := range []string{"complete", "truncated", "wrong source", "wrong magic", "zero inventory", "oversized inventory", "changed archive", "changed trailer", "lost authority", "public pins", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data, want := bytes.Clone(stream.Bytes()), expected
			switch mode {
			case "truncated":
				data = data[:len(data)-5]
			case "wrong source":
				want.SourceSHA = strings.Repeat("a", 40)
			case "wrong magic":
				data[0] ^= 1
			case "zero inventory":
				binary.BigEndian.PutUint32(data[len(externalTransferMagic):], 0)
			case "oversized inventory":
				binary.BigEndian.PutUint32(data[len(externalTransferMagic):], MaxImageInventoryBytes+1)
			case "changed archive":
				data[len(externalTransferMagic)+4+len(raw)+100] ^= 1
			case "changed trailer":
				data[len(data)-1] ^= 1
			case "cancel":
				cancel()
			}
			parent, calls := t.TempDir(), 0
			reader := bytes.NewReader(append(data, []byte("next-frame")...))
			authority := func(context.Context) error {
				calls++
				if mode == "lost authority" && calls == 7 {
					return ErrSessionAuthority
				}
				return nil
			}
			var receiver *ExternalImageSet
			var err error
			if mode == "public pins" {
				receiver, err = ReceiveExternalImages(ctx, parent, want, reader, authority)
			} else {
				receiver, err = receiveExternalImages(ctx, parent, want, reader, authority, pins)
			}
			if mode != "complete" {
				entries, readErr := os.ReadDir(parent)
				if err == nil || receiver != nil || readErr != nil || len(entries) != 0 {
					t.Fatal("invalid receiver escaped", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer receiver.Close()
			left, _ := io.ReadAll(reader)
			if string(left) != "next-frame" {
				t.Fatal("consumed next frame")
			}
			for reference, want := range archives {
				var got bytes.Buffer
				if receiver.WriteArchive(ctx, reference, &got, check) != nil || !bytes.Equal(got.Bytes(), want) {
					t.Fatal("changed dependency archive")
				}
			}
		})
	}
}
