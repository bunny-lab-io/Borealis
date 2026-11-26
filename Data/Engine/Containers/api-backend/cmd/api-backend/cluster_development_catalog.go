package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

func clusterCandidateDirectory() string { return os.Getenv("BOREALIS_ENGINE_DEVELOPMENT_CANDIDATES") }

// Resolve only a database-selected baseline. The full candidate ID binds both
// small catalog files. Missing media leaves the expected identity invalid.
func clusterBaselineExpected(release, sha string) clusterbootstrap.Expected {
	expected := clusterbootstrap.Expected{Repository: clusterGitHubRepo(), Release: release, SourceSHA: sha, AllowQualification: strings.Contains(release, "-rc.")}
	if strings.HasPrefix(release, "dev-") {
		if catalog, _, err := clusterbootstrap.ReadDevelopmentCatalog(clusterCandidateDirectory(), release, sha, expected.Repository); err == nil {
			return catalog.Expected()
		}
	}
	return expected
}

func clusterLocalDevelopmentCatalog(expected clusterbootstrap.Expected) (*clusterbootstrap.DevelopmentCatalog, []byte, error) {
	catalog, raw, err := clusterbootstrap.ReadDevelopmentCatalog(clusterCandidateDirectory(), expected.Release, expected.SourceSHA, expected.Repository)
	if err != nil || catalog.Expected() != expected {
		return nil, nil, errClusterUnavailable
	}
	return catalog, raw, nil
}

func clusterDevelopmentAsset(catalog *clusterbootstrap.DevelopmentCatalog, name string, maxBytes int64) (clusterBootstrapAsset, error) {
	obj, ok := catalog.Asset(name)
	if !ok || obj.Bytes > maxBytes {
		return clusterBootstrapAsset{}, errClusterUnavailable
	}
	return clusterBootstrapAsset{ID: obj.ID, Name: name, Size: obj.Bytes, Digest: "sha256:" + obj.SHA256, State: "development", development: catalog.Expected()}, nil
}

func resolveClusterDevelopmentAssets(ctx context.Context, expected clusterbootstrap.Expected, required map[string]int64) (map[string]clusterBootstrapAsset, error) {
	var catalog *clusterbootstrap.DevelopmentCatalog
	var err error
	if reader, ok := ctx.Value(clusterPreparationReleaseKey{}).(*clusterPreparationReleaseReader); ok {
		reply, err := reader.read(ctx, expected, nil)
		if err != nil {
			return nil, err
		}
		catalog, err = clusterbootstrap.ParseDevelopmentCatalog(expected, reply.Catalog)
		if err != nil {
			return nil, err
		}
	} else {
		catalog, _, err = clusterLocalDevelopmentCatalog(expected)
		if err != nil {
			return nil, err
		}
	}
	assets := make(map[string]clusterBootstrapAsset, len(required))
	for name, maximum := range required {
		asset, err := clusterDevelopmentAsset(catalog, name, maximum)
		if err != nil {
			return nil, err
		}
		assets[name] = asset
	}
	return assets, nil
}

func openClusterDevelopmentAsset(catalog *clusterbootstrap.DevelopmentCatalog, asset clusterBootstrapAsset) (*http.Response, error) {
	selected, err := clusterDevelopmentAsset(catalog, asset.Name, clusterbootstrap.MaxImageArchiveBytes)
	if err != nil || selected != asset {
		return nil, errClusterUnavailable
	}
	file, err := catalog.OpenAsset(clusterCandidateDirectory(), asset.Name)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: asset.Size, Body: file}, nil
}

func localSnapshotRelease(id, baseSHA, k3s, versionFloor string) (map[string]any, error) {
	c, err := clusterbootstrap.SelectDevelopmentCatalog(clusterCandidateDirectory(), id, clusterGitHubRepo())
	if err != nil {
		return nil, errors.New("local development catalog unavailable")
	}
	meta, ok := c.Upgrade()
	var m clusterReleaseManifest
	if !ok || meta.BaseSHA != baseSHA || json.Unmarshal(meta.Compatibility, &m) != nil || !validRollingManifest(m, "development") ||
		!clusterReleaseRE.MatchString(versionFloor) || compareClusterReleases(versionFloor, m.MinimumRollingVersion) < 0 ||
		m.RequiredK3sBaseline != k3s || m.MaximumVersionSkewReleases < 1 {
		return nil, errors.New("development candidate does not match the current rolling baseline")
	}
	return map[string]any{"tag": id, "title": id, "commit_sha": c.Expected().SourceSHA, "channel": "development", "selectable": true, "compatibility": m, "validation_status": "pending"}, nil
}

func localSnapshotReleases(baseSHA, k3s, floor string) ([]map[string]any, error) {
	out := []map[string]any{}
	if clusterCandidateDirectory() == "" {
		return out, nil
	}
	dir, err := os.Open(clusterCandidateDirectory())
	if err != nil {
		return nil, errClusterUnavailable
	}
	defer dir.Close()
	entries, err := dir.ReadDir(257)
	if (err != nil && err != io.EOF) || len(entries) > 256 {
		return nil, errClusterUnavailable
	}
	for _, item := range entries {
		if !item.IsDir() || !clusterSnapshotReleaseRE.MatchString(item.Name()) {
			continue
		}
		v, e := localSnapshotRelease(item.Name(), baseSHA, k3s, floor)
		if e != nil {
			v = map[string]any{"tag": item.Name(), "channel": "development", "selectable": false, "reason": e.Error()}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return cleanText(out[i]["tag"]) < cleanText(out[j]["tag"]) })
	return out, nil
}

func developmentVersionFloor(snapshot map[string]any) string {
	if base := clusterReleaseBase(cleanText(snapshot["baseline_release"])); base != "" {
		return base
	}
	return cleanText(mapStringAny(snapshot["config"])["development_version_floor"])
}

// Keep calendar-version compatibility across a local snapshot, while GitHub
// ancestry is always checked against the actually serving development commit.
func supportedSnapshotExit(snapshot, item map[string]any) bool {
	if !clusterSnapshotReleaseRE.MatchString(cleanText(snapshot["baseline_release"])) {
		return true
	}
	floor := developmentVersionFloor(snapshot)
	raw, err := json.Marshal(item["compatibility"])
	var manifest clusterReleaseManifest
	target := clusterReleaseBase(cleanText(item["tag"]))
	return err == nil && json.Unmarshal(raw, &manifest) == nil && clusterReleaseRE.MatchString(floor) &&
		target != "" && clusterReleaseRE.MatchString(manifest.MinimumRollingVersion) &&
		compareClusterReleases(floor, manifest.MinimumRollingVersion) >= 0 && compareClusterReleases(target, floor) >= 0
}
