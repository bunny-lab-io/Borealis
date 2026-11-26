package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"net/http"
	"os"
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
