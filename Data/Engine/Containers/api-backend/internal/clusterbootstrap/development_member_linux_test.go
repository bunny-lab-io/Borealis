package clusterbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevelopmentMemberPublishesVerifiedGenerationWithoutReplacingServingManifest(t *testing.T) {
	set, old := stagedImagesFixture(t)
	ctx := context.Background()
	w := set.inventory.wire
	w.Release = "dev-src-" + old.SourceSHA
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	b := developmentBinding{Schema: 2, Channel: "development", Platform: "linux-amd64", Repository: old.Repository, Source: old.SourceSHA, Tree: strings.Repeat("a", 40), Manifest: strings.Repeat("c", 64), Inventories: map[string]string{}}
	for _, f := range []string{"bootstrap", "image", "k3s", "host_package", "external"} {
		b.Inventories[f] = developmentHash(raw)
	}
	descriptor, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	e := Expected{Release: "dev-" + developmentHash(descriptor), SourceSHA: old.SourceSHA, Repository: old.Repository, Development: string(descriptor)}
	parent, imports, deploy := mutationTempDir(t), mutationTempDir(t), mutationTempDir(t)
	family := filepath.Join(parent, e.Release, "image")
	if err := os.MkdirAll(family, 0700); err != nil {
		t.Fatal(err)
	}
	catalog := DevelopmentCatalog{expected: e, upgrade: &DevelopmentUpgrade{}, assets: map[string]DevelopmentAsset{}}
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(family, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		catalog.assets[name] = DevelopmentAsset{Path: "image/" + name, Bytes: int64(len(data)), SHA256: developmentHash(data)}
	}
	write(ImageInventoryName, raw)
	for _, p := range w.Images {
		n := ImageAssetName(p.Role)
		bytes, err := os.ReadFile(filepath.Join(set.path, n))
		if err != nil {
			t.Fatal(err)
		}
		write(n, bytes)
	}
	prior := filepath.Join(deploy, "image-manifest.json")
	if err := os.WriteFile(prior, []byte("serving generation"), 0600); err != nil {
		t.Fatal(err)
	}
	denied := func(context.Context) error { return errors.New("lost custody") }
	if _, e := catalog.stageMemberImages(ctx, parent, imports, deploy, denied); e == nil {
		t.Fatal("unauthorized staging")
	}
	last := filepath.Join(family, ImageAssetName(w.Images[len(w.Images)-1].Role))
	good, _ := os.ReadFile(last)
	broken := append([]byte(nil), good...)
	broken[0] ^= 1
	if err := os.WriteFile(last, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if _, e := catalog.stageMemberImages(ctx, parent, imports, deploy, mutationAuthority); e == nil {
		t.Fatal("damaged OCI accepted")
	}
	if files, _ := os.ReadDir(imports); len(files) != 0 {
		t.Fatal("partial publication before complete verification")
	}
	if err := os.WriteFile(last, good, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		name, err := catalog.stageMemberImages(ctx, parent, imports, deploy, mutationAuthority)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := catalog.MemberImageManifest(parent, deploy)
		if err != nil || bound != name {
			t.Fatal("candidate manifest", err)
		}
	}
	if data, _ := os.ReadFile(prior); string(data) != "serving generation" {
		t.Fatal("active generation overwritten")
	}
}
