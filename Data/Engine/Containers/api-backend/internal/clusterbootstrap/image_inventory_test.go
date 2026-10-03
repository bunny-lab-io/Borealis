package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func imageInventoryFixture(t *testing.T) (Expected, imageInventoryWire) {
	t.Helper()
	e := Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: imageTestSHA, AllowQualification: true}
	w := imageInventoryWire{Version: 1, Repository: e.Repository, Release: e.Release, SourceSHA: e.SourceSHA, Platform: "linux-amd64"}
	for _, role := range ImageRoles() {
		raw := imageFixture(t, "gzip", role)
		p, err := InspectImageArchive(context.Background(), bytes.NewReader(raw), int64(len(raw)), role, e.SourceSHA)
		if err != nil {
			t.Fatal(err)
		}
		w.Images = append(w.Images, p)
	}
	return e, w
}

func TestImageInventoryMeasuredRoundTrip(t *testing.T) {
	e, w := imageInventoryFixture(t)
	raw, _ := json.Marshal(w)
	v, err := ParseImageInventory(raw, e)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range w.Images {
		if v.MatchesArchive(p) != nil {
			t.Fatal("measured archive mismatch")
		}
	}
	copy := v.Images()
	copy[0].Layers[0].FileBytes++
	if v.MatchesArchive(copy[0]) == nil {
		t.Fatal("changed measurement accepted")
	}
	if v.MatchesArchive(w.Images[0]) != nil {
		t.Fatal("exported slice changed retained proof")
	}
	owned, _ := v.Export()
	owned[0] = 'x'
	again, _ := v.Export()
	if !bytes.Equal(raw, again) {
		t.Fatal("exported bytes changed retained inventory")
	}
}

func TestImageInventoryRejectsIncompleteOrChanged(t *testing.T) {
	for _, mode := range []string{"missing role", "extra role", "duplicate role", "reordered", "source", "release", "platform", "reference", "digest", "blob size", "expansion", "file bytes", "entries", "unknown", "duplicate JSON", "null", "qualification", "empty"} {
		t.Run(mode, func(t *testing.T) {
			e, w := imageInventoryFixture(t)
			switch mode {
			case "missing role":
				w.Images = w.Images[:8]
			case "extra role":
				w.Images = append(w.Images, w.Images[0])
			case "duplicate role":
				w.Images[1] = w.Images[0]
			case "reordered":
				w.Images[0], w.Images[1] = w.Images[1], w.Images[0]
			case "source":
				w.SourceSHA = strings.Repeat("a", 40)
			case "release":
				w.Release = "2026.09.1"
			case "platform":
				w.Platform = "linux-arm64"
			case "reference":
				w.Images[0].Image += "-foreign"
			case "digest":
				w.Images[0].ManifestDigest = "sha512:" + strings.Repeat("a", 64)
			case "blob size":
				w.Images[0].Layers[0].BlobBytes = w.Images[0].ArchiveBytes + 1
			case "expansion":
				w.Images[0].Layers[0].TarBytes = MaxImageExpandedBytes + 1
			case "file bytes":
				w.Images[0].Layers[0].FileBytes = w.Images[0].Layers[0].TarBytes + 1
			case "entries":
				w.Images[0].Layers[0].Entries = -1
			case "qualification":
				e.AllowQualification = false
			}
			raw, _ := json.Marshal(w)
			switch mode {
			case "unknown":
				raw = append([]byte(`{"extra":true,`), raw[1:]...)
			case "duplicate JSON":
				raw = append([]byte(`{"version":1,`), raw[1:]...)
			case "null":
				raw = bytes.Replace(raw, []byte(`"images":[`), []byte(`"images":null,"images":[`), 1)
			case "empty":
				raw = nil
			}
			if _, err := ParseImageInventory(raw, e); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
}
