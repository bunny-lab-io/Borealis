package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
		"plan_sha256": digest, "archive": archive, "data_directories": plan.DataDirectories}
	if mode == "foreign source" {
		wire["source_sha"] = strings.Repeat("c", 40)
	}
	if mode == "changed plan" {
		wire["plan_sha256"] = strings.Repeat("c", 64)
	}
	raw, _ := json.Marshal(wire)
	if f.extraBodies == nil {
		f.extraBodies = map[string]string{}
	}
	f.extraBodies[fmt.Sprintf("/repos/%s/releases/assets/600", f.expected.Repository)] = string(raw)
	for i, name := range []string{clusterbootstrap.HostPackageInventoryName, clusterbootstrap.HostPackageArchiveName} {
		id := int64(600 + i)
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

func TestClusterSSHHostPackageOwnedDemand(t *testing.T) {
	if got, err := (clusterSSHHostPackageInventory{}).demands(context.Background(), nil, nil); err == nil || got != nil {
		t.Fatal("missing owned package bytes supplied demand", got, err)
	}
	// Retained actual producer output extends the ordinary ownership tests;
	// committed small native fixtures cover failures without download/install.
	directory := os.Getenv("BOREALIS_TEST_HOST_PACKAGE_ASSETS")
	if directory == "" {
		return
	}
	raw, err := os.ReadFile(filepath.Join(directory, clusterbootstrap.HostPackageInventoryName))
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		Repository string `json:"repository"`
		Release    string `json:"release"`
		SourceSHA  string `json:"source_sha"`
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		t.Fatal(err)
	}
	expected := clusterbootstrap.Expected{Repository: identity.Repository, Release: identity.Release, SourceSHA: identity.SourceSHA, AllowQualification: true}
	inventory, err := clusterbootstrap.ParseHostPackageInventory(raw, expected)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	check := func(ctx context.Context) error { return ctx.Err() }
	for _, mode := range []string{"valid", "cleanup failure", "scope failure"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			packages, err := clusterbootstrap.StageHostPackages(ctx, parent, expected, inventory, func(context.Context) (io.ReadCloser, error) {
				return os.Open(filepath.Join(directory, clusterbootstrap.HostPackageArchiveName))
			}, check)
			if err != nil {
				t.Fatal(err)
			}
			owner := &clusterSSHOwnedPreparationArtifacts{packages: packages}
			defer owner.Close()
			v := clusterSSHHostPackageInventory{expected: expected, inventory: inventory}
			got, err := v.demands(ctx, packages, check)
			want := clusterSSHFilesystemDemand{Path: "/opt/Borealis", Bytes: uint64(inventory.Archive().Size), Entries: uint64(len(inventory.Packages())) + 5}
			for _, pin := range inventory.Packages() {
				want.Bytes += uint64(pin.Size)
			}
			if err != nil || len(got) != 4 || got[1] != want {
				t.Fatal("native package materialization charge", got, want, err)
			}
			var installedBytes, installedEntries uint64
			for _, row := range got {
				if row.Path != "/opt/Borealis" {
					installedBytes += row.Bytes
					installedEntries += row.Entries
				}
			}
			if got[0].Path != "/etc" || got[2].Path != "/usr" || got[3].Path != "/var" || installedBytes != 403735996 || installedEntries != 4744 {
				t.Fatal("measured installed contents lost during filesystem projection", got)
			}
			if mode == "scope failure" {
				d := sshApprovalArtifactDependencies(&sshApprovalArtifactBytes{digest: strings.Repeat("e", 64)})
				d.acquire = func(context.Context, string, func(context.Context) error) (clusterSSHPreparationArtifactBytes, error) {
					return owner, errors.New("fixture acquisition failed after owning packages")
				}
				consumed := false
				err := runClusterSSHPreparationArtifacts(ctx, strings.Repeat("a", 64), d, func(context.Context, clusterSSHPreparationArtifactGate, clusterSSHPreparationArtifactDemandRead) (clusterSSHPreparationReadinessReceipt, error) {
					consumed = true
					return clusterSSHPreparationReadinessReceipt{}, nil
				})
				if err == nil || consumed || owner.packages != nil {
					t.Fatal("failed scope retained package owner or reached consumer", err)
				}
			}
			var unrelated string
			if mode == "cleanup failure" {
				entries, err := os.ReadDir(parent)
				if err != nil || len(entries) != 1 {
					t.Fatal("staged owner unavailable", entries, err)
				}
				unrelated = filepath.Join(parent, entries[0].Name(), "unrelated")
				if err := os.WriteFile(unrelated, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if (owner.Close() == nil) != (mode != "cleanup failure") {
				t.Fatal("artifact owner lost package cleanup result")
			}
			if got, err := v.demands(ctx, packages, check); err == nil || got != nil {
				t.Fatal("artifact owner released live package demand", got, err)
			}
			if mode != "cleanup failure" {
				entries, err := os.ReadDir(parent)
				if err != nil || len(entries) != 0 {
					t.Fatal("artifact owner leaked package bytes", entries, err)
				}
			} else if raw, err := os.ReadFile(unrelated); err != nil || string(raw) != "preserve" {
				t.Fatal("artifact cleanup removed unrelated contents", err)
			}
		})
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
				if err == nil && strings.HasSuffix(r.URL.Path, "/assets/600") {
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
