package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHostPackageContentMetadata(t *testing.T) {
	for _, mode := range []string{"valid", "path", "duplicate path", "file total", "entry total", "root count", "negative links", "control root", "control bounds", "unknown nested", "alias", "duplicate JSON", "null", "missing"} {
		t.Run(mode, func(t *testing.T) {
			plan, _ := hostPackageTestPlan(t)
			pin := &plan.Packages[0]
			switch mode {
			case "path":
				pin.DataPaths[0].Path = "/elsewhere"
			case "duplicate path":
				pin.DataPaths = append(pin.DataPaths, pin.DataPaths[0])
			case "file total":
				pin.DataPaths[0].FileBytes++
			case "entry total":
				pin.DataPaths[0].Entries++
			case "root count":
				pin.DataRootEntries++
			case "negative links":
				pin.DataPaths[0].LinkBytes = -1
			case "control root":
				pin.Control.RootEntries = 0
			case "control bounds":
				pin.Control.FileBytes = MaxImageExpandedBytes + 1
			}
			raw, _ := json.Marshal(plan)
			switch mode {
			case "unknown nested":
				raw = bytes.Replace(raw, []byte(`"link_bytes":`), []byte(`"extra":1,"link_bytes":`), 1)
			case "alias":
				raw = bytes.Replace(raw, []byte(`"link_bytes":`), []byte(`"Link_Bytes":`), 1)
			case "duplicate JSON":
				raw = bytes.Replace(raw, []byte(`"link_bytes":`), []byte(`"link_bytes":0,"link_bytes":`), 1)
			case "null":
				raw = bytes.Replace(raw, []byte(`"link_bytes":7`), []byte(`"link_bytes":null`), 1)
			case "missing":
				raw = bytes.Replace(raw, []byte(`"data_root_entries":1,`), nil, 1)
			}
			_, _, err := parseHostPackagePlan(append(raw, '\n'))
			if (err == nil) != (mode == "valid") {
				t.Fatal("content metadata decision", err)
			}
		})
	}
	plan, digest := hostPackageTestPlan(t)
	archive := hostPackageTestArchive(t, plan, "valid")
	expected, wire := hostPackageTestInventory(t, plan, digest, archive)
	raw, _ := json.Marshal(wire)
	inventory, err := parseHostPackageInventory(raw, expected, plan, digest)
	if err != nil {
		t.Fatal(err)
	}
	pins := inventory.Packages()
	pins[0].DataPaths[0].LinkBytes++
	if inventory.Packages()[0].DataPaths[0].LinkBytes != plan.Packages[0].DataPaths[0].LinkBytes {
		t.Fatal("caller changed owned nested measurements")
	}
	// Totals still agree, but release metadata must match every compiled pin.
	wire.Packages = pins
	raw, _ = json.Marshal(wire)
	if _, err := parseHostPackageInventory(raw, expected, plan, digest); err == nil {
		t.Fatal("changed link measurement accepted without lock update")
	}
	aliases := HostPackagePathAliases()
	if !reflect.DeepEqual(aliases, []HostPackagePathAlias{{"/lib", "/usr/lib"}, {"/sbin", "/usr/sbin"}}) {
		t.Fatal("alias requirements changed", aliases)
	}
	aliases[0].Destination = "/elsewhere"
	if HostPackagePathAliases()[0].Destination != "/usr/lib" {
		t.Fatal("caller rewrote alias requirement")
	}
}

func TestHostPackageInstalledContentDemand(t *testing.T) {
	for _, mode := range []string{"valid", "source", "bytes", "cancelled", "authority after proof", "archive replaced after proof", "closed"} {
		t.Run(mode, func(t *testing.T) {
			expected, inventory, archive := hostPackageStageFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			set, err := StageHostPackages(ctx, t.TempDir(), expected, inventory, func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(archive)), nil }, func(ctx context.Context) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			original := expected
			switch mode {
			case "source":
				expected.SourceSHA = strings.Repeat("b", 40)
			case "bytes":
				archive[1024] ^= 1
				if err := os.WriteFile(filepath.Join(set.path, HostPackageArchiveName), archive, 0600); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "closed":
				if err := set.Close(); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			check := func(ctx context.Context) error {
				calls++
				if mode == "archive replaced after proof" && calls == 2 {
					path := filepath.Join(set.path, HostPackageArchiveName)
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, archive, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "authority after proof" && calls == 3 {
					return ErrSessionAuthority
				}
				return ctx.Err()
			}
			got, err := set.InstalledContentDemand(ctx, expected, check)
			if mode == "valid" {
				want := []HostPackageDestinationDemand{{Path: "/usr", Bytes: uint64(len(inventory.Packages()) * 107), Entries: uint64(len(inventory.Packages()) * 2)}}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("installed content lost link bytes or included control/root entries", got, want, err)
				}
				got[0].Bytes = 0
				again, err := set.InstalledContentDemand(ctx, expected, check)
				if err != nil || !reflect.DeepEqual(again, want) {
					t.Fatal("caller changed installed demand", again, err)
				}
				if err := set.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || got != nil {
				t.Fatal("unproven installed content demand", got, err)
			}
			if got, err := set.InstalledContentDemand(context.Background(), original, func(context.Context) error { return nil }); err == nil || got != nil {
				t.Fatal("failed owner revived", got, err)
			}
		})
	}
}

func TestHostPackageInstalledContentActualArchives(t *testing.T) {
	directory := os.Getenv("BOREALIS_TEST_HOST_PACKAGE_ASSETS")
	if directory == "" {
		return
	}
	raw, err := os.ReadFile(filepath.Join(directory, HostPackageInventoryName))
	if err != nil {
		t.Fatal(err)
	}
	var wire hostPackageInventoryWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	expected := Expected{Repository: wire.Repository, Release: wire.Release, SourceSHA: wire.SourceSHA, AllowQualification: true}
	inventory, err := ParseHostPackageInventory(raw, expected)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	check := func(ctx context.Context) error { return ctx.Err() }
	set, err := StageHostPackages(ctx, t.TempDir(), expected, inventory, func(context.Context) (io.ReadCloser, error) {
		return os.Open(filepath.Join(directory, HostPackageArchiveName))
	}, check)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	got, err := set.InstalledContentDemand(ctx, expected, check)
	want := []HostPackageDestinationDemand{{"/etc", 163908, 110}, {"/lib", 584290, 8}, {"/sbin", 63912, 2}, {"/usr", 402923886, 4609}, {"/var", 0, 15}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("reviewed actual destination demand", got, want, err)
	}
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}
}
