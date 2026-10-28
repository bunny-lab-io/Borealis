package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func preparationPublicationFixture(t *testing.T) *bootstrapReleaseFixture {
	t.Helper()
	applications := imageReleaseFixture(t)
	f := newBootstrapReleaseFixture(t)
	f.extraBodies = map[string]string{}
	for _, asset := range applications.release.Assets {
		asset.ID += 400
		asset.URL = fmt.Sprintf("%s/repos/%s/releases/assets/%d", clusterGitHubAPIBase(), f.expected.Repository, asset.ID)
		if asset.Name == clusterbootstrap.ImageInventoryName {
			f.extraBodies[fmt.Sprintf("/repos/%s/releases/assets/%d", f.expected.Repository, asset.ID)] = applications.manifest
		}
		f.release.Assets = append(f.release.Assets, asset)
	}
	addK3sReleaseFixture(t, f)
	addExternalReleaseFixture(t, f)
	return f
}

func TestClusterSSHPreparationPublicationNativeInventories(t *testing.T) {
	for _, mode := range []string{"valid", "missing bootstrap", "missing application", "missing external", "missing K3s", "mutable", "moved tag", "manifest tamper", "cross-domain ID", "final drift", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := preparationPublicationFixture(t)
			missing := map[string]string{"missing bootstrap": clusterbootstrap.BundleName,
				"missing application": clusterbootstrap.ImageAssetName(clusterbootstrap.ImageRoles()[0]),
				"missing external":    clusterbootstrap.ExternalImageAssetName(clusterbootstrap.ExternalImagePins()[0].Reference),
				"missing K3s":         clusterbootstrap.K3sBinaryName}[mode]
			for i, asset := range f.release.Assets {
				if asset.Name == missing {
					f.release.Assets = append(f.release.Assets[:i], f.release.Assets[i+1:]...)
					break
				}
			}
			if mode == "mutable" {
				no := false
				f.release.Immutable = &no
			}
			if mode == "moved tag" {
				f.sha = strings.Repeat("b", 40)
			}
			if mode == "manifest tamper" {
				f.manifestReply = f.manifest + " "
			}
			if mode == "cross-domain ID" {
				// Give one application archive the bootstrap archive's ID/URL.
				f.release.Assets[3].ID, f.release.Assets[3].URL = f.release.Assets[1].ID, f.release.Assets[1].URL
			}
			previous := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = previous })
			publicationReads := 0
			http.DefaultClient = &http.Client{Transport: bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/releases/tags/") {
					publicationReads++
					if mode == "final drift" && publicationReads == 5 {
						f.release.Assets[0].Digest = "sha256:" + strings.Repeat("f", 64)
					}
				}
				return http.DefaultTransport.RoundTrip(r)
			})}
			ctx, cancel := context.WithCancel(bootstrapContext())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			digest, err := readClusterSSHPreparationPublication(ctx, f.expected, clusterbootstrap.K3sPins().Version)
			if (err == nil) != (mode == "valid") || (err != nil && digest != "") {
				t.Fatal("unexpected publication result", err)
			}
			if mode == "valid" {
				repeated, err := readClusterSSHPreparationPublication(ctx, f.expected, clusterbootstrap.K3sPins().Version)
				if err != nil || repeated != digest || !validClusterSSHPreparationDigest(digest) || publicationReads != 10 {
					t.Fatal("publication did not reobserve complete immutable release")
				}
			}
			if f.archiveReads != 0 {
				t.Fatal("publication read downloaded archive")
			}
		})
	}
}
