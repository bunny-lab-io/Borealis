package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestImageTransferReceiver(t *testing.T) {
	ctx := context.Background()
	expected, wire := imageInventoryFixture(t)
	archives := map[string][]byte{}
	for i, proof := range wire.Images {
		raw := imageFixture(t, "gzip", proof.Role)
		archives[proof.Role] = raw
		wire.Images[i], _ = InspectImageArchive(ctx, bytes.NewReader(raw), int64(len(raw)), proof.Role, expected.SourceSHA)
	}
	raw, _ := json.Marshal(wire)
	inventory, err := ParseImageInventory(raw, expected)
	if err != nil {
		t.Fatal(err)
	}
	check := func(context.Context) error { return nil }
	sender, err := StageImages(ctx, t.TempDir(), inventory, func(_ context.Context, proof ImageArchiveProof) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(archives[proof.Role])), nil
	}, check)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	var stream bytes.Buffer
	if err := sender.WriteImages(ctx, &stream, check); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"complete", "truncated", "wrong source", "changed archive", "changed trailer", "lost authority"} {
		t.Run(mode, func(t *testing.T) {
			data := bytes.Clone(stream.Bytes())
			want := expected
			switch mode {
			case "truncated":
				data = data[:len(data)-5]
			case "wrong source":
				want.SourceSHA = strings.Repeat("a", 40)
			case "changed archive":
				data[len(imageTransferMagic)+4+len(raw)+100] ^= 1
			case "changed trailer":
				data[len(data)-1] ^= 1
			}
			parent := t.TempDir()
			calls := 0
			reader := bytes.NewReader(append(data, []byte("next-frame")...))
			receiver, err := ReceiveImages(ctx, parent, want, reader, func(context.Context) error {
				calls++
				if mode == "lost authority" && calls == 7 {
					return ErrSessionAuthority
				}
				return nil
			})
			if mode != "complete" {
				entries, _ := os.ReadDir(parent)
				if err == nil || receiver != nil || len(entries) != 0 {
					t.Fatalf("invalid receiver escaped: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer receiver.Close()
			left, _ := io.ReadAll(reader)
			if string(left) != "next-frame" {
				t.Fatal("receiver consumed next protocol frame")
			}
			for _, role := range ImageRoles() {
				var received bytes.Buffer
				if receiver.WriteArchive(ctx, role, &received, check) != nil || !bytes.Equal(received.Bytes(), archives[role]) {
					t.Fatal("archive changed in transfer")
				}
			}
			manifest, err := receiver.RuntimeManifest(ctx, check)
			if err != nil {
				t.Fatal(err)
			}
			var m struct {
				Schema   int    `json:"schema_version"`
				Mode     string `json:"mode"`
				Services map[string]struct {
					Image string `json:"image"`
					Hash  string `json:"hash"`
				} `json:"services"`
			}
			if json.Unmarshal(manifest, &m) != nil || m.Schema != 1 || m.Mode != "prod" || len(m.Services) != 9 {
				t.Fatal("invalid node workload manifest")
			}
			for _, proof := range wire.Images {
				repository, _, _ := strings.Cut(proof.Image, ":")
				if m.Services[proof.Role].Image != repository+"@"+proof.ManifestDigest || m.Services[proof.Role].Hash != strings.TrimPrefix(proof.ManifestDigest, "sha256:") {
					t.Fatal("manifest identity changed")
				}
			}
		})
	}
}
