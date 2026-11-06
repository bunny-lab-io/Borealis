package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"testing"
)

func TestK3sTransferReceiver(t *testing.T) {
	ctx := context.Background()
	inventory, pins, assets := k3sStageFixture(t)
	expected := Expected{Repository: inventory.wire.Repository, Release: inventory.wire.Release, SourceSHA: inventory.wire.SourceSHA, AllowQualification: true}
	check := func(context.Context) error { return nil }
	sender, err := stageK3sInputs(ctx, t.TempDir(), inventory, func(_ context.Context, name string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(assets[name])), nil
	}, check, pins, reserveImageFile)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	var stream bytes.Buffer
	if sender.WriteInputs(ctx, &stream, check) != nil {
		t.Fatal("send failed")
	}
	for _, mode := range []string{"complete", "truncated", "wrong source", "wrong version", "wrong magic", "zero inventory", "oversized inventory", "changed binary", "changed installer", "changed archive", "changed trailer", "lost authority", "public pins", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data, want, version := bytes.Clone(stream.Bytes()), expected, pins.Version
			first := len(k3sTransferMagic) + 4 + len(inventory.raw)
			switch mode {
			case "truncated":
				data = data[:len(data)-5]
			case "wrong source":
				want.SourceSHA = strings.Repeat("a", 40)
			case "wrong version":
				version = "different"
			case "wrong magic":
				data[0] ^= 1
			case "zero inventory":
				binary.BigEndian.PutUint32(data[len(k3sTransferMagic):], 0)
			case "oversized inventory":
				binary.BigEndian.PutUint32(data[len(k3sTransferMagic):], MaxK3sInventoryBytes+1)
			case "changed binary":
				data[first] ^= 1
			case "changed installer":
				data[first+int(pins.Binary.Size)] ^= 1
			case "changed archive":
				data[first+int(pins.Binary.Size+pins.Installer.Size)+100] ^= 1
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
			var receiver *K3sSet
			var err error
			if mode == "public pins" {
				receiver, err = ReceiveK3sInputs(ctx, parent, want, version, reader, authority)
			} else {
				receiver, err = receiveK3sInputs(ctx, parent, want, version, reader, authority, pins)
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
				t.Fatal("consumed next protocol frame")
			}
			for name, want := range assets {
				var got bytes.Buffer
				if receiver.WriteAsset(ctx, name, &got, check) != nil || !bytes.Equal(got.Bytes(), want) {
					t.Fatal("changed received asset")
				}
			}
		})
	}
}
