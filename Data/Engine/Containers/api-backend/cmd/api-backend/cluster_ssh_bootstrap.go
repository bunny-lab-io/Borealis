package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type clusterBootstrapAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	State              string `json:"state"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type clusterBootstrapRelease struct {
	TagName     string                  `json:"tag_name"`
	Draft       *bool                   `json:"draft"`
	Prerelease  *bool                   `json:"prerelease"`
	Immutable   *bool                   `json:"immutable"`
	PublishedAt string                  `json:"published_at"`
	Assets      []clusterBootstrapAsset `json:"assets"`
}

var clusterBootstrapDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (r *clusterBootstrapRelease) UnmarshalJSON(raw []byte) error {
	*r = clusterBootstrapRelease{}
	return clusterBootstrapObject(raw, map[string]any{"tag_name": &r.TagName, "draft": &r.Draft,
		"prerelease": &r.Prerelease, "immutable": &r.Immutable, "published_at": &r.PublishedAt, "assets": &r.Assets})
}

func (a *clusterBootstrapAsset) UnmarshalJSON(raw []byte) error {
	*a = clusterBootstrapAsset{}
	return clusterBootstrapObject(raw, map[string]any{"id": &a.ID, "name": &a.Name, "state": &a.State,
		"size": &a.Size, "digest": &a.Digest, "url": &a.URL, "browser_download_url": &a.BrowserDownloadURL})
}

// GitHub adds unrelated metadata fields. Ignore those while rejecting repeated
// keys and case aliases of the fields used for authorization, at both levels.
func clusterBootstrapObject(raw []byte, fields map[string]any) error {
	invalid := errors.New("node bootstrap GitHub metadata object invalid")
	if !utf8.Valid(raw) {
		return invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return invalid
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return invalid
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return invalid
		}
		if field, known := fields[name]; known {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, field) != nil {
				return invalid
			}
		} else {
			for field := range fields {
				if strings.EqualFold(field, name) {
					return invalid
				}
			}
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || d.Decode(&struct{}{}) != io.EOF {
		return invalid
	}
	return nil
}

// prepareClusterSSHBootstrap refreshes publication/tag proofs independently of
// picker cache, authenticates both assets and stages verified isolated source.
// Caller owns release compatibility/ancestry, operation/lease fences and SSH
// delivery; this function never authorizes admission or executes target code.
// No database connection may remain borrowed while this function runs.
func prepareClusterSSHBootstrap(ctx context.Context, expected clusterbootstrap.Expected, scratchParent string) (*clusterbootstrap.Bundle, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	assets, err := resolveClusterBootstrapAssets(ctx, expected)
	if err != nil {
		return nil, err
	}
	manifestAsset := assets[clusterbootstrap.ManifestName]
	response, err := openClusterBootstrapAsset(ctx, manifestAsset)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, manifestAsset.Size+1))
	_ = response.Body.Close()
	hash := sha256.Sum256(raw)
	if readErr != nil || int64(len(raw)) != manifestAsset.Size || "sha256:"+hex.EncodeToString(hash[:]) != manifestAsset.Digest {
		return nil, errors.New("node bootstrap manifest asset size or digest mismatch")
	}
	manifest, err := clusterbootstrap.ParseManifest(raw, expected)
	if err != nil {
		return nil, err
	}
	archiveAsset := assets[clusterbootstrap.BundleName]
	if manifest.BundleSize() != archiveAsset.Size || "sha256:"+manifest.BundleSHA256() != archiveAsset.Digest {
		return nil, errors.New("node bootstrap manifest does not match published archive asset")
	}
	response, err = openClusterBootstrapAsset(ctx, archiveAsset)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return clusterbootstrap.Stage(ctx, scratchParent, manifest, response.Body, "")
}

func resolveClusterBootstrapAssets(ctx context.Context, expected clusterbootstrap.Expected) (map[string]clusterBootstrapAsset, error) {
	if err := expected.Validate(); err != nil {
		return nil, err
	}
	if expected.Repository != clusterGitHubRepo() {
		return nil, errors.New("node bootstrap repository differs from configured authority")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/releases/tags/%s", clusterGitHubAPIBase(), expected.Repository, expected.Release)
	var release clusterBootstrapRelease
	if clusterGitHubJSON(ctx, endpoint, &release) != nil {
		return nil, errors.New("node bootstrap publication metadata unavailable")
	}
	_, publishedErr := time.Parse(time.RFC3339, release.PublishedAt)
	qualification := strings.Contains(expected.Release, "-rc.")
	if release.TagName != expected.Release || release.Draft == nil || *release.Draft || release.Prerelease == nil ||
		*release.Prerelease != qualification || release.Immutable == nil || !*release.Immutable || publishedErr != nil || len(release.Assets) > 100 {
		return nil, errors.New("node bootstrap release must be published immutable matching channel")
	}
	sha, err := resolveClusterGitHubTagSHA(ctx, expected.Release)
	if err != nil || sha != expected.SourceSHA {
		return nil, errors.New("node bootstrap release tag does not match authorized source")
	}
	assets := map[string]clusterBootstrapAsset{}
	ids := map[int64]bool{}
	for _, asset := range release.Assets {
		if asset.Name != clusterbootstrap.ManifestName && asset.Name != clusterbootstrap.BundleName {
			continue
		}
		maximum := int64(clusterbootstrap.MaxBundleBytes)
		if asset.Name == clusterbootstrap.ManifestName {
			maximum = clusterbootstrap.MaxManifestBytes
		}
		apiURL := fmt.Sprintf("%s/repos/%s/releases/assets/%d", clusterGitHubAPIBase(), expected.Repository, asset.ID)
		if _, exists := assets[asset.Name]; exists || ids[asset.ID] || asset.ID < 1 || asset.State != "uploaded" ||
			asset.Size < 1 || asset.Size > maximum || !clusterBootstrapDigestPattern.MatchString(asset.Digest) ||
			asset.URL != apiURL || asset.BrowserDownloadURL != clusterbootstrap.AssetURL(expected, asset.Name) {
			return nil, errors.New("node bootstrap release assets missing, ambiguous or invalid")
		}
		assets[asset.Name], ids[asset.ID] = asset, true
	}
	if len(assets) != 2 {
		return nil, errors.New("node bootstrap release lacks required packaged assets")
	}
	return assets, nil
}

// GitHub's asset endpoint returns either binary200 or302 to its signed asset
// CDN URL. Follow exactly one approved HTTPS redirect using a NEW request with
// no Authorization, cookies or Referer. Never surface the signed URL in errors.
func openClusterBootstrapAsset(ctx context.Context, asset clusterBootstrapAsset) (*http.Response, error) {
	invalid := errors.New("node bootstrap asset download failed; remote diagnostics withheld")
	apiURL := fmt.Sprintf("%s/repos/%s/releases/assets/%d", clusterGitHubAPIBase(), clusterGitHubRepo(), asset.ID)
	if asset.ID < 1 || asset.URL != apiURL || asset.Size < 1 || asset.Size > clusterbootstrap.MaxBundleBytes {
		return nil, invalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, invalid
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "Borealis-Cluster-Controller")
	if token, _ := ctx.Value(clusterGitHubTokenContextKey{}).(string); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := *http.DefaultClient
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, invalid
	}
	if response.StatusCode == http.StatusFound {
		location := response.Header.Get("Location")
		_ = response.Body.Close()
		target, err := url.Parse(location)
		if err != nil || len(location) > 8192 || target.Scheme != "https" || target.Host != "release-assets.githubusercontent.com" ||
			target.User != nil || target.Fragment != "" || !strings.HasPrefix(target.Path, "/github-production-release-asset/") {
			return nil, invalid
		}
		request, err = http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, invalid
		}
		request.Header.Set("Accept", "application/octet-stream")
		request.Header.Set("Accept-Encoding", "identity")
		request.Header.Set("User-Agent", "Borealis-Cluster-Controller")
		response, err = client.Do(request)
		if err != nil {
			return nil, invalid
		}
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "" ||
		(response.ContentLength >= 0 && response.ContentLength != asset.Size) {
		_ = response.Body.Close()
		return nil, invalid
	}
	return response, nil
}
