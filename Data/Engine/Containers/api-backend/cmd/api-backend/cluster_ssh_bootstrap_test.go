package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

const bootstrapTestToken = "fixture-github-token-must-not-reach-cdn"

func bootstrapDigest(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(h[:])
}

type bootstrapReleaseFixture struct {
	expected      clusterbootstrap.Expected
	release       clusterBootstrapRelease
	sha           string
	manifest      string
	publication   string
	manifestReply string
	archiveReads  int
}

func newBootstrapReleaseFixture(t *testing.T) *bootstrapReleaseFixture {
	t.Helper()
	e := clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true}
	yes, no := true, false
	f := &bootstrapReleaseFixture{expected: e, sha: e.SourceSHA}
	f.release = clusterBootstrapRelease{TagName: e.Release, Draft: &no, Prerelease: &yes, Immutable: &yes, PublishedAt: "2026-09-07T00:00:00Z"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+bootstrapTestToken {
			t.Error("GitHub API authentication missing")
		}
		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			if f.publication != "" {
				_, _ = io.WriteString(w, f.publication)
			} else {
				_ = json.NewEncoder(w).Encode(f.release)
			}
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"type": "commit", "sha": f.sha}})
		case strings.HasSuffix(r.URL.Path, "/assets/1"):
			if f.manifestReply != "" {
				_, _ = io.WriteString(w, f.manifestReply)
			} else {
				_, _ = io.WriteString(w, f.manifest)
			}
		case strings.HasSuffix(r.URL.Path, "/assets/2"):
			f.archiveReads++
			_, _ = io.WriteString(w, "bundle")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("BOREALIS_ENGINE_GITHUB_REPOSITORY", e.Repository)
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)
	manifest := map[string]any{
		"schema_version": 1, "repository": e.Repository, "release": e.Release, "source_sha": e.SourceSHA,
		"source_tree": strings.Repeat("b", 40), "platform": "linux-amd64", "go_version": "go1.25.12",
		"node_manager": map[string]any{"path": clusterbootstrap.ManagerPath, "size": 2, "sha256": strings.TrimPrefix(bootstrapDigest("nm"), "sha256:")},
		"asset":        map[string]any{"name": clusterbootstrap.BundleName, "size": 6, "sha256": strings.TrimPrefix(bootstrapDigest("bundle"), "sha256:"), "url": clusterbootstrap.AssetURL(e, clusterbootstrap.BundleName)},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	f.manifest = string(raw)
	for index, name := range []string{clusterbootstrap.ManifestName, clusterbootstrap.BundleName} {
		content := "bundle"
		if index == 0 {
			content = f.manifest
		}
		id := int64(index + 1)
		f.release.Assets = append(f.release.Assets, clusterBootstrapAsset{ID: id, Name: name, State: "uploaded", Size: int64(len(content)), Digest: bootstrapDigest(content),
			URL: fmt.Sprintf("%s/repos/%s/releases/assets/%d", server.URL, e.Repository, id), BrowserDownloadURL: clusterbootstrap.AssetURL(e, name)})
	}
	return f
}

func bootstrapContext() context.Context {
	return context.WithValue(context.Background(), clusterGitHubTokenContextKey{}, bootstrapTestToken)
}

func TestClusterSSHBootstrapRefreshesPublicationAndRejectsInvalidAssets(t *testing.T) {
	for _, name := range []string{"valid", "mutable", "draft", "missing immutable", "missing draft", "channel", "unpublished", "wrong tag", "moved SHA", "duplicate name", "duplicate ID", "missing asset", "wrong URL", "wrong browser URL", "missing digest", "oversized manifest", "oversized bundle", "nonuploaded", "duplicate JSON", "alias JSON", "nested duplicate JSON", "null JSON"} {
		t.Run(name, func(t *testing.T) {
			f := newBootstrapReleaseFixture(t)
			if _, err := resolveClusterBootstrapAssets(bootstrapContext(), f.expected); err != nil {
				t.Fatal(err)
			}
			original, _ := json.Marshal(f.release)
			yes, no := true, false
			switch name {
			case "mutable":
				f.release.Immutable = &no
			case "draft":
				f.release.Draft = &yes
			case "missing immutable":
				f.release.Immutable = nil
			case "missing draft":
				f.release.Draft = nil
			case "channel":
				f.release.Prerelease = &no
			case "unpublished":
				f.release.PublishedAt = ""
			case "wrong tag":
				f.release.TagName = "2026.09.999-rc.2"
			case "moved SHA":
				f.sha = strings.Repeat("c", 40)
			case "duplicate name":
				f.release.Assets = append(f.release.Assets, f.release.Assets[0])
			case "duplicate ID":
				f.release.Assets[1].ID = 1
			case "missing asset":
				f.release.Assets = f.release.Assets[:1]
			case "wrong URL":
				f.release.Assets[0].URL += "?elsewhere"
			case "wrong browser URL":
				f.release.Assets[0].BrowserDownloadURL = "https://example.invalid/manifest"
			case "missing digest":
				f.release.Assets[0].Digest = ""
			case "oversized manifest":
				f.release.Assets[0].Size = clusterbootstrap.MaxManifestBytes + 1
			case "oversized bundle":
				f.release.Assets[1].Size = clusterbootstrap.MaxBundleBytes + 1
			case "nonuploaded":
				f.release.Assets[0].State = "new"
			case "duplicate JSON":
				f.publication = `{"immutable":false,` + string(original[1:])
			case "alias JSON":
				f.publication = strings.Replace(string(original), `"immutable"`, `"Immutable"`, 1)
			case "nested duplicate JSON":
				f.publication = strings.Replace(string(original), `"id":1`, `"id":1,"id":1`, 1)
			case "null JSON":
				f.publication = strings.Replace(string(original), `"immutable":true`, `"immutable":null`, 1)
			}
			_, err := resolveClusterBootstrapAssets(bootstrapContext(), f.expected)
			if (name == "valid") != (err == nil) {
				t.Fatalf("publication check: %v", err)
			}
		})
	}
}

func TestClusterSSHBootstrapRejectsManifestBeforeArchiveDownload(t *testing.T) {
	for _, name := range []string{"digest", "length", "wrong identity", "wrong archive digest"} {
		t.Run(name, func(t *testing.T) {
			f := newBootstrapReleaseFixture(t)
			switch name {
			case "digest":
				f.manifestReply = strings.Replace(f.manifest, "linux-amd64", "linux-arm64", 1)
			case "length":
				f.manifestReply = f.manifest + " "
			case "wrong identity":
				f.manifest = strings.Replace(f.manifest, f.expected.SourceSHA, strings.Repeat("c", 40), 1)
				f.release.Assets[0].Digest = bootstrapDigest(f.manifest)
			case "wrong archive digest":
				f.release.Assets[1].Digest = bootstrapDigest("other archive")
			}
			parent := t.TempDir()
			b, err := prepareClusterSSHBootstrap(bootstrapContext(), f.expected, parent)
			if err == nil || b != nil || f.archiveReads != 0 {
				t.Fatalf("invalid manifest reached archive: %v reads%d", err, f.archiveReads)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid manifest created scratch")
			}
		})
	}
}

type bootstrapRoundTripper func(*http.Request) (*http.Response, error)

func (f bootstrapRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClusterSSHBootstrapDownloadPinsRedirectAndWithholdsCredentials(t *testing.T) {
	const cdn = "https://release-assets.githubusercontent.com/github-production-release-asset/123/fixture?signature=private-fixture-value"
	for _, name := range []string{"direct", "redirect", "wrong host", "subdomain", "http", "userinfo", "port", "fragment", "wrong path", "second redirect", "encoded content", "wrong length", "cancelled", "remote error"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("BOREALIS_GITHUB_API_BASE_URL", "https://api.github.com")
			t.Setenv("BOREALIS_ENGINE_GITHUB_REPOSITORY", "bunny-lab-io/Borealis")
			previous := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = previous })
			jar, _ := cookiejar.New(nil)
			cookieURL, _ := url.Parse(cdn)
			jar.SetCookies(cookieURL, []*http.Cookie{{Name: "private-cookie", Value: "do-not-forward"}})
			calls := 0
			http.DefaultClient = &http.Client{Jar: jar, Transport: bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Context().Err() != nil {
					return nil, r.Context().Err()
				}
				if calls == 1 {
					if r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer "+bootstrapTestToken || r.Header.Get("Accept") != "application/octet-stream" {
						t.Error("API request lost pinned authority or auth")
					}
				} else if r.URL.String() != cdn || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
					t.Error("CDN received credentials or unexpected destination")
				}
				response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("binary")), ContentLength: 6, Request: r}
				if name == "remote error" {
					return nil, fmt.Errorf("remote failure %s", cdn)
				}
				if name == "encoded content" {
					response.Header.Set("Content-Encoding", "gzip")
				}
				if name == "wrong length" {
					response.ContentLength = 7
				}
				if name != "direct" && (calls == 1 || name == "second redirect") {
					location := cdn
					switch name {
					case "wrong host":
						location = "https://example.invalid/private"
					case "subdomain":
						location = strings.Replace(cdn, "release-assets.", "attacker.release-assets.", 1)
					case "http":
						location = strings.Replace(cdn, "https:", "http:", 1)
					case "userinfo":
						location = strings.Replace(cdn, "https://", "https://userinfo@", 1)
					case "port":
						location = strings.Replace(cdn, ".com/", ".com:444/", 1)
					case "fragment":
						location += "#fragment"
					case "wrong path":
						location = strings.Replace(cdn, "github-production-release-asset", "other", 1)
					}
					response.StatusCode = http.StatusFound
					response.Header.Set("Location", location)
				}
				return response, nil
			})}
			ctx, cancel := context.WithTimeout(bootstrapContext(), time.Second)
			defer cancel()
			if name == "cancelled" {
				cancel()
			}
			asset := clusterBootstrapAsset{ID: 1, Size: 6, URL: "https://api.github.com/repos/bunny-lab-io/Borealis/releases/assets/1"}
			response, err := openClusterBootstrapAsset(ctx, asset)
			if name == "direct" || name == "redirect" {
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || string(raw) != "binary" {
					t.Fatal("binary response not preserved")
				}
			} else if err == nil || response != nil || strings.Contains(err.Error(), "private-fixture-value") || strings.Contains(err.Error(), bootstrapTestToken) {
				t.Fatalf("unsafe download or exposed diagnostics: %v", err)
			}
			if name != "direct" && name != "redirect" && name != "second redirect" && name != "encoded content" && name != "wrong length" && calls > 1 {
				t.Fatal("unapproved destination contacted")
			}
		})
	}
}
