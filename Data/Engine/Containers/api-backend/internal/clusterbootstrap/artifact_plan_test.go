package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparationArtifactPublicationPlan(t *testing.T) {
	a, _, _ := preparationArtifactsFixture(t)
	check := func(context.Context) error { return nil }
	a.mu.Lock()
	plan, err := a.publicationPlan(context.Background(), check)
	a.mu.Unlock()
	if err != nil || len(plan.files) != 37 || !digestPattern.MatchString(plan.digest) {
		t.Fatal("incomplete plan", err)
	}
	archives, binaries, installers, manifests := 0, 0, 0, 0
	for _, file := range plan.files {
		var data bytes.Buffer
		if file.write(context.Background(), &data, check) != nil {
			t.Fatal("guarded copy failed", file.path)
		}
		digest := sha256.Sum256(data.Bytes())
		if int64(data.Len()) != file.size || hex.EncodeToString(digest[:]) != file.digest {
			t.Fatal("copy differs from bound plan", file.path)
		}
		switch {
		case strings.HasPrefix(file.path, "var/lib/rancher/k3s/agent/images/"):
			archives++
			if file.mode != 0600 || filepath.Ext(file.path) != ".tar" {
				t.Fatal("unsafe archive destination")
			}
		case file.path == "usr/local/bin/k3s":
			binaries++
			if file.mode != 0755 {
				t.Fatal("wrong binary mode")
			}
		case file.path == prejoinK3sInstaller:
			installers++
			if file.mode != 0600 {
				t.Fatal("installer executable during staging")
			}
		case file.path == prejoinImageManifest:
			manifests++
			var manifest map[string]any
			if file.mode != 0600 || json.Unmarshal(data.Bytes(), &manifest) != nil || manifest["source_sha"] != a.expected.SourceSHA {
				t.Fatal("unbound runtime manifest")
			}
		default:
			t.Fatal("unexpected publication path", file.path)
		}
	}
	if archives != 34 || binaries != 1 || installers != 1 || manifests != 1 {
		t.Fatal("missing runtime dependency", archives, binaries, installers, manifests)
	}
	a.mu.Lock()
	again, err := a.publicationPlan(context.Background(), check)
	a.mu.Unlock()
	if err != nil || again.digest != plan.digest {
		t.Fatal("unstable plan")
	}
	if _, err := a.publicationPlan(context.Background(), func(context.Context) error { return ErrSessionAuthority }); err == nil {
		t.Fatal("lost authority accepted")
	}
	a.application.inventory.wire.Images[0].ArchiveSHA256 = strings.Repeat("a", 64)
	a.mu.Lock()
	changed, err := a.publicationPlan(context.Background(), check)
	a.mu.Unlock()
	if err != nil || changed.digest == plan.digest {
		t.Fatal("archive identity not bound")
	}
}
