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
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type clusterBootstrapAsset struct {
	development        clusterbootstrap.Expected
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

// Deployed workers stage source artifacts on a Pod-local disk volume. Standalone
// callers retain the process temporary directory when no volume is configured.
func clusterSSHScratchParent() string {
	if path := os.Getenv("BOREALIS_CLUSTER_SCRATCH_DIR"); path != "" {
		return path
	}
	return os.TempDir()
}

// prepareClusterSSHBootstrap refreshes publication/tag proofs independently of
// picker cache, authenticates both assets and stages verified isolated source.
// Caller owns release compatibility/ancestry, operation/lease fences and SSH
// delivery; this function never authorizes admission or executes target code.
// No database connection may remain borrowed while this function runs.
func prepareClusterSSHBootstrap(ctx context.Context, expected clusterbootstrap.Expected, scratchParent string) (*clusterbootstrap.Bundle, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	manifest, assets, err := readClusterSSHBootstrapPublication(ctx, expected)
	if err != nil {
		return nil, err
	}
	response, err := openClusterBootstrapAsset(ctx, assets[clusterbootstrap.BundleName])
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return clusterbootstrap.Stage(ctx, scratchParent, manifest, response.Body, "")
}

// Publication proof authenticates the manifest and exact archive identity. It
// does not download, validate or execute archive contents; staging does that.
func readClusterSSHBootstrapPublication(ctx context.Context, expected clusterbootstrap.Expected) (*clusterbootstrap.Manifest, map[string]clusterBootstrapAsset, error) {
	assets, err := resolveClusterBootstrapAssets(ctx, expected)
	if err != nil {
		return nil, nil, err
	}
	manifestAsset := assets[clusterbootstrap.ManifestName]
	response, err := openClusterBootstrapAsset(ctx, manifestAsset)
	if err != nil {
		return nil, nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, manifestAsset.Size+1))
	_ = response.Body.Close()
	hash := sha256.Sum256(raw)
	if readErr != nil || int64(len(raw)) != manifestAsset.Size || "sha256:"+hex.EncodeToString(hash[:]) != manifestAsset.Digest {
		return nil, nil, errors.New("node bootstrap manifest asset size or digest mismatch")
	}
	manifest, err := clusterbootstrap.ParseManifest(raw, expected)
	if err != nil {
		return nil, nil, err
	}
	archiveAsset := assets[clusterbootstrap.BundleName]
	if manifest.BundleSize() != archiveAsset.Size || "sha256:"+manifest.BundleSHA256() != archiveAsset.Digest {
		return nil, nil, errors.New("node bootstrap manifest does not match published archive asset")
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return manifest, assets, nil
}

func resolveClusterBootstrapAssets(ctx context.Context, expected clusterbootstrap.Expected) (map[string]clusterBootstrapAsset, error) {
	return resolveClusterPackagedAssets(ctx, expected, map[string]int64{
		clusterbootstrap.ManifestName: clusterbootstrap.MaxManifestBytes,
		clusterbootstrap.BundleName:   clusterbootstrap.MaxBundleBytes,
	})
}

func resolveClusterPackagedAssets(ctx context.Context, expected clusterbootstrap.Expected, required map[string]int64) (map[string]clusterBootstrapAsset, error) {
	if err := expected.Validate(); err != nil {
		return nil, err
	}
	if expected.Repository != clusterGitHubRepo() {
		return nil, errors.New("node bootstrap repository differs from configured authority")
	}
	if expected.Development != "" {
		return resolveClusterDevelopmentAssets(ctx, expected, required)
	}
	var release clusterBootstrapRelease
	var sha string
	var err error
	if reader, ok := ctx.Value(clusterPreparationReleaseKey{}).(*clusterPreparationReleaseReader); ok {
		var reply clusterPreparationReleaseReply
		reply, err = reader.read(ctx, expected, nil)
		if err == nil && reply.Release != nil {
			release, sha = *reply.Release, reply.SHA
		}
	} else {
		release, sha, err = fetchClusterPackagedRelease(ctx, expected)
	}
	if err != nil {
		return nil, err
	}
	if err := validateClusterPackagedRelease(expected, release, sha); err != nil {
		return nil, err
	}
	return selectClusterPackagedAssets(expected, release, required)
}

func validateClusterPackagedRelease(expected clusterbootstrap.Expected, release clusterBootstrapRelease, sha string) error {
	_, publishedErr := time.Parse(time.RFC3339, release.PublishedAt)
	qualification := strings.Contains(expected.Release, "-rc.")
	if expected.Development != "" || release.TagName != expected.Release || release.Draft == nil || *release.Draft || release.Prerelease == nil ||
		*release.Prerelease != qualification || release.Immutable == nil || !*release.Immutable || publishedErr != nil || len(release.Assets) > 100 {
		return errors.New("node bootstrap release must be published immutable matching channel")
	}
	if sha != expected.SourceSHA {
		return errors.New("node bootstrap release tag does not match authorized source")
	}
	return nil
}

func selectClusterPackagedAssets(expected clusterbootstrap.Expected, release clusterBootstrapRelease, required map[string]int64) (map[string]clusterBootstrapAsset, error) {
	assets := map[string]clusterBootstrapAsset{}
	ids := map[int64]bool{}
	for _, asset := range release.Assets {
		maximum, needed := required[asset.Name]
		if !needed {
			continue
		}
		apiURL := clusterPackagedAssetAPIURL(asset.ID)
		if _, exists := assets[asset.Name]; exists || ids[asset.ID] || asset.ID < 1 || asset.State != "uploaded" ||
			asset.Size < 1 || asset.Size > maximum || !clusterBootstrapDigestPattern.MatchString(asset.Digest) ||
			asset.URL != apiURL || asset.BrowserDownloadURL != clusterbootstrap.AssetURL(expected, asset.Name) {
			return nil, errors.New("node bootstrap release assets missing, ambiguous or invalid")
		}
		assets[asset.Name], ids[asset.ID] = asset, true
	}
	if len(assets) != len(required) {
		return nil, errors.New("node bootstrap release lacks required packaged assets")
	}
	return assets, nil
}

func fetchClusterPackagedRelease(ctx context.Context, expected clusterbootstrap.Expected) (clusterBootstrapRelease, string, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/releases/tags/%s", clusterGitHubAPIBase(), expected.Repository, expected.Release)
	var release clusterBootstrapRelease
	if clusterGitHubJSON(ctx, endpoint, &release) != nil {
		return release, "", errors.New("node bootstrap publication metadata unavailable")
	}
	sha, err := resolveClusterGitHubTagSHA(ctx, expected.Release)
	return release, sha, err
}

// GitHub's asset endpoint returns either binary200 or302 to its signed asset
// CDN URL. Follow exactly one approved HTTPS redirect using a NEW request with
// no Authorization, cookies or Referer. Never surface the signed URL in errors.
func openClusterBootstrapAsset(ctx context.Context, asset clusterBootstrapAsset) (*http.Response, error) {
	return openClusterPackagedAsset(ctx, asset, clusterbootstrap.MaxBundleBytes)
}

func openClusterPackagedAsset(ctx context.Context, asset clusterBootstrapAsset, maximum int64) (*http.Response, error) {
	invalid := errors.New("node bootstrap asset download failed; remote diagnostics withheld")
	if asset.development.Development != "" {
		if asset.Size < 1 || asset.Size > maximum || maximum > clusterbootstrap.MaxImageArchiveBytes {
			return nil, invalid
		}
		if reader, ok := ctx.Value(clusterPreparationReleaseKey{}).(*clusterPreparationReleaseReader); ok {
			reply, err := reader.read(ctx, asset.development, &asset)
			if err != nil || !reply.Direct || reply.assetResponse == nil {
				return nil, invalid
			}
			return reply.assetResponse, nil
		}
		catalog, _, err := clusterLocalDevelopmentCatalog(asset.development)
		if err != nil {
			return nil, err
		}
		return openClusterDevelopmentAsset(catalog, asset)
	}
	apiURL := clusterPackagedAssetAPIURL(asset.ID)
	if asset.ID < 1 || asset.URL != apiURL || asset.Size < 1 || maximum < 1 || maximum > clusterbootstrap.MaxImageArchiveBytes || asset.Size > maximum {
		return nil, invalid
	}
	var response *http.Response
	var err error
	if reader, ok := ctx.Value(clusterPreparationReleaseKey{}).(*clusterPreparationReleaseReader); ok {
		var reply clusterPreparationReleaseReply
		reply, err = reader.read(ctx, reader.expected, &asset)
		if err == nil {
			response = reply.assetResponse
		}
	} else {
		response, err = openClusterAssetFirstHop(ctx, apiURL)
	}
	if err != nil || response == nil {
		return nil, invalid
	}
	if response.StatusCode == http.StatusFound {
		location := response.Header.Get("Location")
		// The next acquisition cannot outlive or ignore the first response's
		// cleanup. Close is synchronous even when the request has expired.
		if closeErr := response.Body.Close(); closeErr != nil || ctx.Err() != nil {
			return nil, invalid
		}
		if !validClusterAssetRedirect(location) {
			return nil, invalid
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
		if err != nil {
			return nil, invalid
		}
		response, err = doClusterAssetRequest(request)
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

func validClusterAssetRedirect(location string) bool {
	target, err := url.Parse(location)
	return err == nil && len(location) <= 8192 && target.Scheme == "https" && target.Host == "release-assets.githubusercontent.com" &&
		target.User == nil && target.Fragment == "" && strings.HasPrefix(target.Path, "/github-production-release-asset/")
}

func clusterPackagedAssetAPIURL(id int64) string {
	return fmt.Sprintf("%s/repos/%s/releases/assets/%d", clusterGitHubAPIBase(), clusterGitHubRepo(), id)
}

func openClusterAssetFirstHop(ctx context.Context, apiURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, errClusterUnavailable
	}
	if token, _ := ctx.Value(clusterGitHubTokenContextKey{}).(string); token != "" && clusterGitHubAPIEndpoint(apiURL) {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return doClusterAssetRequest(request)
}

func doClusterAssetRequest(request *http.Request) (*http.Response, error) {
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "Borealis-Cluster-Controller")
	client := *http.DefaultClient
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(request)
}
