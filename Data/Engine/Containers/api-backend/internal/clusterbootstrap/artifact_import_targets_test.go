package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func k3sImportArchiveFixture(t *testing.T, mode string) ([]byte, K3sInputPins, ContainerdImageTarget) {
	t.Helper()
	raw, pins := k3sArchiveFixture(t, "gzip")
	tr := tar.NewReader(bytes.NewReader(raw))
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[h.Name] = data
	}
	var index map[string]any
	if json.Unmarshal(files["index.json"], &index) != nil {
		t.Fatal("index fixture")
	}
	d := index["manifests"].([]any)[0].(map[string]any)
	name := "docker.io/" + pins.Images[0]
	target := ContainerdImageTarget{name, d["digest"].(string)}
	d["annotations"] = map[string]any{"io.containerd.image.name": name, "org.opencontainers.image.ref.name": "v1"}
	switch mode {
	case "short annotation":
		d["annotations"].(map[string]any)["io.containerd.image.name"] = pins.Images[0]
	case "tag only":
		delete(d["annotations"].(map[string]any), "io.containerd.image.name")
	case "foreign name":
		d["annotations"].(map[string]any)["io.containerd.image.name"] = "docker.io/foreign/image:v1"
	case "foreign digest":
		d["digest"] = "sha512:" + strings.Repeat("a", 64)
	case "missing image":
		index["manifests"] = []any{}
	case "duplicate image":
		index["manifests"] = []any{d, d}
	case "bad media":
		d["mediaType"] = "application/unknown"
	case "extra field":
		d["foreign"] = true
	}
	files["index.json"], _ = json.Marshal(index)
	if mode == "duplicate key" {
		files["index.json"] = bytes.Replace(files["index.json"], []byte(`"schemaVersion":2`), []byte(`"schemaVersion":2,"schemaVersion":2`), 1)
	}
	if mode == "oversized index" {
		files["index.json"] = bytes.Repeat([]byte(" "), (1<<20)+1)
	}
	var output bytes.Buffer
	tw := tar.NewWriter(&output)
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		data := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if tw.Close() != nil {
		t.Fatal("tar close")
	}
	raw = output.Bytes()
	sum := sha256.Sum256(raw)
	pins.Archive.Size = int64(len(raw))
	pins.Archive.SHA256 = hex.EncodeToString(sum[:])
	if mode == "changed bytes" {
		raw[len(raw)-1] = 1
	}
	return raw, pins, target
}

func TestK3sAuthenticatedImportTargets(t *testing.T) {
	for _, mode := range []string{"complete", "short annotation", "tag only", "foreign name", "foreign digest", "missing image", "duplicate image", "bad media", "extra field", "duplicate key", "oversized index", "changed bytes", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			raw, pins, want := k3sImportArchiveFixture(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			got, err := inspectK3sImportTargets(ctx, bytes.NewReader(raw), pins)
			if mode == "complete" {
				if err != nil || !reflect.DeepEqual(got, []ContainerdImageTarget{want}) {
					t.Fatal("authenticated index rejected", got, err)
				}
			} else if err == nil || got != nil {
				t.Fatal("unsafe index admitted", got, err)
			}
		})
	}
}

func TestPreparationArtifactImportTargets(t *testing.T) {
	a, pins, _ := preparationArtifactsFixture(t)
	raw, archivePins, k3sTarget := k3sImportArchiveFixture(t, "complete")
	pins.Archive = archivePins.Archive
	if a.k3s.root.WriteFile(K3sArchiveName, raw, 0600) != nil {
		t.Fatal("replace owned fixture")
	}
	a.k3s.assets[K3sArchiveName] = pins.Archive
	proof, err := inspectK3sArchive(context.Background(), bytes.NewReader(raw), int64(len(raw)), pins)
	if err != nil {
		t.Fatal(err)
	}
	a.k3s.inventory.wire.Archive = proof
	a.k3s.inventory.raw, _ = json.Marshal(a.k3s.inventory.wire)
	check := func(context.Context) error { return nil }
	got, err := a.importTargets(context.Background(), check, pins)
	if err != nil || len(got) != len(ImageRoles())+len(pins.Images)+len(ExternalImagePins())+1 || !slices.Contains(got, k3sTarget) {
		t.Fatal("incomplete artifact target plan", len(got), err)
	}
	want := map[string]string{}
	for _, p := range a.application.inventory.Images() {
		want[p.Image] = p.ManifestDigest
	}
	for _, p := range append(a.external.inventory.Images(), a.postgres.proof) {
		d := p.IndexDigest
		if d == "" {
			d = p.ManifestDigest
		}
		want[p.Reference] = d
	}
	want[k3sTarget.Reference] = k3sTarget.Digest
	for _, p := range got {
		if want[p.Reference] != p.Digest {
			t.Fatal("wrong runtime target", p)
		}
	}
	got[0].Digest = "foreign"
	fresh, err := a.importTargets(context.Background(), check, pins)
	if err != nil || fresh[0].Digest == "foreign" {
		t.Fatal("borrowed target plan escaped ownership")
	}
	if got, err := a.ImportTargets(context.Background(), check); err == nil || got != nil {
		t.Fatal("public path accepted unreviewed K3s fixture pin")
	}
	if got, err := a.importTargets(context.Background(), func(context.Context) error { return ErrSessionAuthority }, pins); err == nil || got != nil {
		t.Fatal("lost authority admitted")
	}
	raw[len(raw)-1] = 1
	if a.k3s.root.WriteFile(K3sArchiveName, raw, 0600) != nil {
		t.Fatal("tamper fixture")
	}
	if got, err := a.importTargets(context.Background(), check, pins); err == nil || got != nil {
		t.Fatal("changed archive admitted")
	}
	if a.Close() != nil {
		t.Fatal("close fixture")
	}
	if got, err := a.importTargets(context.Background(), check, pins); err == nil || got != nil {
		t.Fatal("closed ownership admitted")
	}
}
