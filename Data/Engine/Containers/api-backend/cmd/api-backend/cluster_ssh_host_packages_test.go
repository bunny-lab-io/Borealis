package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func addHostPackageReleaseFixture(t *testing.T, f *bootstrapReleaseFixture, mode string) {
	t.Helper()
	plan, digest, err := clusterbootstrap.HostPackagePins()
	if err != nil {
		t.Fatal(err)
	}
	// Fixed tar framing supplies only manifest metadata here. Actual pinned
	// archive bytes are exercised by the package producer/native verifier.
	size := int64(1024)
	for _, p := range plan.Packages {
		size += 512 + (p.Size+511)/512*512
	}
	size = (size + 10239) / 10240 * 10240
	archive := clusterbootstrap.HostPackageAsset{Name: clusterbootstrap.HostPackageArchiveName, Size: size, SHA256: strings.Repeat("b", 64)}
	wire := map[string]any{"version": 1, "repository": f.expected.Repository, "release": f.expected.Release, "source_sha": f.expected.SourceSHA,
		"os": plan.OS, "os_version": plan.OSVersion, "suite": plan.Suite, "platform": plan.Platform, "packages": plan.Packages,
		"plan_sha256": digest, "archive": archive}
	if mode == "foreign source" {
		wire["source_sha"] = strings.Repeat("c", 40)
	}
	if mode == "changed plan" {
		wire["plan_sha256"] = strings.Repeat("c", 64)
	}
	raw, _ := json.Marshal(wire)
	f.extraBodies = map[string]string{fmt.Sprintf("/repos/%s/releases/assets/200", f.expected.Repository): string(raw)}
	for i, name := range []string{clusterbootstrap.HostPackageInventoryName, clusterbootstrap.HostPackageArchiveName} {
		id := int64(200 + i)
		a := clusterBootstrapAsset{ID: id, Name: name, State: "uploaded", Size: archive.Size, Digest: "sha256:" + archive.SHA256,
			URL: fmt.Sprintf("%s/repos/%s/releases/assets/%d", clusterGitHubAPIBase(), f.expected.Repository, id), BrowserDownloadURL: clusterbootstrap.AssetURL(f.expected, name)}
		if i == 0 {
			a.Size, a.Digest = int64(len(raw)), bootstrapDigest(string(raw))
		}
		if mode == "archive mismatch" && i == 1 {
			a.Digest = "sha256:" + strings.Repeat("c", 64)
		}
		if mode != "missing archive" || i == 0 {
			f.release.Assets = append(f.release.Assets, a)
		}
	}
}

type hostPackageHTTPTransport func(*http.Request) (*http.Response, error)

func (f hostPackageHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type hostPackageHTTPBody struct {
	io.ReadCloser
	closed func() error
}

func (b hostPackageHTTPBody) Close() error { return errors.Join(b.ReadCloser.Close(), b.closed()) }

func TestClusterSSHHostPackagePublication(t *testing.T) {
	for _, mode := range []string{"valid", "missing archive", "foreign source", "changed plan", "archive mismatch", "publication changed", "close failure", "cancel during close"} {
		t.Run(mode, func(t *testing.T) {
			f := newBootstrapReleaseFixture(t)
			addHostPackageReleaseFixture(t, f, mode)
			ctx, cancel := context.WithCancel(bootstrapContext())
			defer cancel()
			previous := http.DefaultClient
			client := *previous
			transport := client.Transport
			if transport == nil {
				transport = http.DefaultTransport
			}
			closes := 0
			client.Transport = hostPackageHTTPTransport(func(r *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(r)
				if err == nil && strings.HasSuffix(r.URL.Path, "/assets/200") {
					response.Body = hostPackageHTTPBody{response.Body, func() error {
						closes++
						if mode == "cancel during close" {
							cancel()
						}
						if mode == "close failure" {
							return errors.New("fixture Close")
						}
						return nil
					}}
				}
				return response, err
			})
			http.DefaultClient = &client
			t.Cleanup(func() { http.DefaultClient = previous })
			value, err := resolveClusterSSHHostPackageInventory(ctx, f.expected)
			valid := mode == "valid" || mode == "publication changed"
			if (err == nil) != valid {
				t.Fatal("wrong immutable package decision", err)
			}
			if (mode == "close failure" || mode == "cancel during close") && closes != 1 {
				t.Fatal("response cleanup fault never exercised", closes)
			}
			if !valid {
				return
			}
			if mode == "publication changed" {
				f.release.Assets[len(f.release.Assets)-1].Size--
			}
			if (value.refresh(ctx) == nil) != (mode == "valid") {
				t.Fatal("changed immutable publication retained")
			}
			if f.archiveReads != 0 {
				t.Fatal("manifest resolution acquired unrelated source bundle")
			}
		})
	}
}
