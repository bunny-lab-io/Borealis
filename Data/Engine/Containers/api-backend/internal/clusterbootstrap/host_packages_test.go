package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func hostPackageTestPlan(t *testing.T) (HostPackagePlan, string) {
	t.Helper()
	plan := HostPackagePlan{Version: 1, OS: "ubuntu", OSVersion: "24.04", Suite: "noble", Platform: "linux-amd64", DataDirectories: []string{"/etc", "/usr", "/var"}}
	for name, phase := range hostPackageRoots {
		body := []byte("fixture " + name)
		hash := sha256.Sum256(body)
		plan.Packages = append(plan.Packages, HostPackagePin{Name: name, Version: "1.0", Architecture: "all", File: name + "_1.0_all.deb",
			Phase: phase, Size: int64(len(body)), SHA256: hex.EncodeToString(hash[:]), FileBytes: 100, Entries: 3,
			DataPaths: []HostPackagePathContent{{Path: "/usr", FileBytes: 100, LinkBytes: 7, Entries: 2}}, DataRootEntries: 1,
			Control: HostPackageControlContent{FileBytes: 20, Entries: 2, RootEntries: 1},
			URL:     "https://archive.ubuntu.com/ubuntu/pool/main/f/fixture/" + name + "_1.0_all.deb"})
	}
	slices.SortFunc(plan.Packages, func(a, b HostPackagePin) int { return strings.Compare(a.Name, b.Name) })
	raw, _ := json.Marshal(plan)
	parsed, digest, err := parseHostPackagePlan(append(raw, '\n'))
	if err != nil {
		t.Fatal(err)
	}
	return parsed, digest
}

func hostPackageTestArchive(t *testing.T, plan HostPackagePlan, mode string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	packages := slices.Clone(plan.Packages)
	if mode == "order" {
		slices.Reverse(packages)
	}
	if mode == "missing" {
		packages = packages[:len(packages)-1]
	}
	for i, p := range packages {
		body := []byte("fixture " + p.Name)
		h := &tar.Header{Name: p.File, Size: p.Size, Typeflag: tar.TypeReg, Mode: 0644, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}
		if i == 0 {
			switch mode {
			case "body":
				body[0] ^= 1
			case "name":
				h.Name = "../" + h.Name
			case "symlink":
				h.Typeflag, h.Linkname, h.Size, body = tar.TypeSymlink, "other", 0, nil
			case "mode":
				h.Mode = 0755
			case "owner":
				h.Uid = 1000
			case "timestamp":
				h.ModTime = time.Unix(1, 0)
			case "PAX":
				h.Format, h.PAXRecords = tar.FormatPAX, map[string]string{"unreviewed": "extra"}
			}
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "extra" || mode == "duplicate" {
		name := "other.deb"
		if mode == "duplicate" {
			name = packages[0].File
		}
		if err := w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0644, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for out.Len()%10240 != 0 {
		out.WriteByte(0)
	}
	value := out.Bytes()
	if mode == "padding" {
		value[len(value)-1] = 1
	}
	return value
}

func hostPackageTestInventory(t *testing.T, plan HostPackagePlan, digest string, archive []byte) (Expected, hostPackageInventoryWire) {
	t.Helper()
	expected := Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.11.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true}
	hash := sha256.Sum256(archive)
	return expected, hostPackageInventoryWire{Version: 1, Repository: expected.Repository, Release: expected.Release, SourceSHA: expected.SourceSHA,
		OS: plan.OS, OSVersion: plan.OSVersion, Suite: plan.Suite, Platform: plan.Platform, PlanSHA256: digest, Packages: slices.Clone(plan.Packages),
		DataDirectories: slices.Clone(plan.DataDirectories),
		Archive:         HostPackageAsset{Name: HostPackageArchiveName, Size: int64(len(archive)), SHA256: hex.EncodeToString(hash[:])}}
}

func TestHostPackagePinsAndLockBoundaries(t *testing.T) {
	plan, digest, err := HostPackagePins()
	if err != nil || len(plan.Packages) != 82 || digest == "" {
		t.Fatal("reviewed suite pins unavailable", err)
	}
	plan.Packages[0].SHA256 = "changed"
	again, sameDigest, err := HostPackagePins()
	if err != nil || again.Packages[0].SHA256 == "changed" || sameDigest != digest {
		t.Fatal("caller changed compiled trust")
	}
	for _, mode := range []string{"no newline", "two newlines", "suite", "arch", "root phase", "missing root", "order", "duplicate file", "path", "version", "zero digest", "oversized", "unknown", "alias", "duplicate", "null", "URL origin", "URL HTTP", "URL credentials", "URL query", "URL fragment", "URL encoding", "URL traversal", "URL package"} {
		t.Run(mode, func(t *testing.T) {
			p, _ := hostPackageTestPlan(t)
			switch mode {
			case "suite":
				p.Suite = "unknown"
			case "arch":
				p.Packages[0].Architecture = "arm64"
			case "root phase":
				p.Packages[0].Phase = "docker"
			case "missing root":
				p.Packages = p.Packages[1:]
			case "order":
				slices.Reverse(p.Packages)
			case "duplicate file":
				p.Packages[1].File = p.Packages[0].File
			case "path":
				p.Packages[0].File = "../escape.deb"
			case "version":
				p.Packages[0].Version = "1;command"
			case "zero digest":
				p.Packages[0].SHA256 = strings.Repeat("0", 64)
			case "oversized":
				p.Packages[0].Size = MaxImageArchiveBytes + 1
			case "URL origin":
				p.Packages[0].URL = strings.Replace(p.Packages[0].URL, "archive.ubuntu.com", "archive.ubuntu.com.evil", 1)
			case "URL HTTP":
				p.Packages[0].URL = strings.Replace(p.Packages[0].URL, "https:", "http:", 1)
			case "URL credentials":
				p.Packages[0].URL = strings.Replace(p.Packages[0].URL, "https://", "https://user:password@", 1)
			case "URL query":
				p.Packages[0].URL += "?token=fixture"
			case "URL fragment":
				p.Packages[0].URL += "#fragment"
			case "URL encoding":
				p.Packages[0].URL = strings.Replace(p.Packages[0].URL, "/pool/", "/%70ool/", 1)
			case "URL traversal":
				p.Packages[0].URL = strings.Replace(p.Packages[0].URL, "/fixture/", "/../", 1)
			case "URL package":
				p.Packages[0].URL = strings.Replace(p.Packages[0].URL, "_1.0_", "_2.0_", 1)
			}
			raw, _ := json.Marshal(p)
			raw = append(raw, '\n')
			switch mode {
			case "no newline":
				raw = bytes.TrimSpace(raw)
			case "two newlines":
				raw = append(raw, '\n')
			case "unknown":
				raw = bytes.Replace(raw, []byte(`"size":`), []byte(`"other":1,"size":`), 1)
			case "alias":
				raw = bytes.Replace(raw, []byte(`"name":`), []byte(`"Name":`), 1)
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"size":`), []byte(`"size":1,"size":`), 1)
			case "null":
				raw = bytes.Replace(raw, []byte(`"file_bytes":100`), []byte(`"file_bytes":null`), 1)
			}
			if _, _, err := parseHostPackagePlan(raw); err == nil {
				t.Fatal("invalid suite lock accepted")
			}
		})
	}
}

func TestHostPackageDirectoryContract(t *testing.T) {
	directories, err := HostPackageDirectories()
	if err != nil || len(directories) != 360 {
		t.Fatal("locked ancestors unavailable", len(directories), err)
	}
	directories[0] = "/elsewhere"
	again, err := HostPackageDirectories()
	if err != nil || again[0] != "/etc" {
		t.Fatal("caller changed compiled ancestors")
	}
	for _, mode := range []string{"valid", "missing", "duplicate", "order", "noncanonical", "unknown root", "alias", "missing parent", "root", "control", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			plan, _ := hostPackageTestPlan(t)
			switch mode {
			case "missing":
				plan.DataDirectories = nil
			case "duplicate":
				plan.DataDirectories = []string{"/etc", "/usr", "/usr", "/var"}
			case "order":
				slices.Reverse(plan.DataDirectories)
			case "noncanonical":
				plan.DataDirectories = []string{"/etc", "/usr", "/usr/./share", "/var"}
			case "unknown root":
				plan.DataDirectories = []string{"/etc", "/opt", "/usr", "/var"}
			case "alias":
				plan.DataDirectories = []string{"/etc", "/lib", "/usr", "/var"}
			case "missing parent":
				plan.DataDirectories = []string{"/etc", "/usr", "/usr/share/doc", "/var"}
			case "root":
				plan.DataDirectories = append([]string{"/"}, plan.DataDirectories...)
			case "control":
				plan.DataDirectories = []string{"/etc", "/usr", "/usr/bad\npath", "/var"}
			case "oversized":
				plan.DataDirectories = []string{"/etc", "/usr", "/usr/" + strings.Repeat("x", 256), "/var"}
			}
			raw, _ := json.Marshal(plan)
			_, _, err := parseHostPackagePlan(append(raw, '\n'))
			if (err == nil) != (mode == "valid") {
				t.Fatal("ancestor contract decision", err)
			}
		})
	}
	plan, digest := hostPackageTestPlan(t)
	expected, wire := hostPackageTestInventory(t, plan, digest, hostPackageTestArchive(t, plan, "valid"))
	wire.DataDirectories = []string{"/etc", "/usr", "/usr/share", "/var"}
	raw, _ := json.Marshal(wire)
	if _, err := parseHostPackageInventory(raw, expected, plan, digest); err == nil {
		t.Fatal("manifest changed compiled ancestor inventory")
	}
}

func TestHostPackageInventoryBinding(t *testing.T) {
	for _, mode := range []string{"valid", "qualification unauthorized", "repository", "release", "source", "suite", "digest", "phase", "content count", "archive name", "archive size", "alias", "unknown", "missing", "duplicate", "null"} {
		t.Run(mode, func(t *testing.T) {
			p, digest := hostPackageTestPlan(t)
			expected, wire := hostPackageTestInventory(t, p, digest, hostPackageTestArchive(t, p, "valid"))
			switch mode {
			case "qualification unauthorized":
				expected.AllowQualification = false
			case "repository":
				wire.Repository = "other/repository"
			case "release":
				wire.Release = "2026.11.998-rc.1"
			case "source":
				wire.SourceSHA = strings.Repeat("b", 40)
			case "suite":
				wire.Suite = "other"
			case "digest":
				wire.PlanSHA256 = strings.Repeat("f", 64)
			case "phase":
				wire.Packages[0].Phase = "docker"
			case "content count":
				wire.Packages[0].FileBytes++
			case "archive name":
				wire.Archive.Name = "other.tar"
			case "archive size":
				wire.Archive.Size++
			}
			raw, _ := json.Marshal(wire)
			switch mode {
			case "alias":
				raw = bytes.Replace(raw, []byte(`"size":`), []byte(`"Size":`), 1)
			case "unknown":
				raw = bytes.Replace(raw, []byte(`"archive":{`), []byte(`"archive":{"extra":1,`), 1)
			case "missing":
				raw = bytes.Replace(raw, []byte(`"file_bytes":100,`), nil, 1)
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"version":1,`), []byte(`"version":1,"version":1,`), 1)
			case "null":
				raw = bytes.Replace(raw, []byte(`"file_bytes":100`), []byte(`"file_bytes":null`), 1)
			}
			inventory, err := parseHostPackageInventory(raw, expected, p, digest)
			if mode != "valid" {
				if err == nil {
					t.Fatal("unbound package inventory accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw[0] = '!'
			packages := inventory.Packages()
			packages[0].SHA256 = "changed"
			exported, err := inventory.Export()
			if err != nil || exported[0] != '{' || inventory.Packages()[0].SHA256 == "changed" || inventory.Archive() != wire.Archive {
				t.Fatal("caller changed retained package inventory")
			}
		})
	}
}

func TestHostPackageArchiveVerification(t *testing.T) {
	for _, mode := range []string{"valid", "body", "name", "order", "missing", "extra", "duplicate", "symlink", "mode", "owner", "timestamp", "PAX", "padding", "hash", "truncated", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			p, digest := hostPackageTestPlan(t)
			archive := hostPackageTestArchive(t, p, mode)
			expected, wire := hostPackageTestInventory(t, p, digest, archive)
			if mode == "hash" {
				wire.Archive.SHA256 = strings.Repeat("f", 64)
			}
			raw, _ := json.Marshal(wire)
			inventory, err := parseHostPackageInventory(raw, expected, p, digest)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			if mode == "truncated" {
				archive = archive[:len(archive)-1]
			}
			err = inventory.VerifyArchive(ctx, bytes.NewReader(archive), wire.Archive.Size)
			if (err == nil) != (mode == "valid") {
				t.Fatal("archive verification result", err)
			}
		})
	}
	// Optional actual producer output extends this same verification test. The
	// ordinary suite above remains complete without retained development assets.
	if directory := os.Getenv("BOREALIS_TEST_HOST_PACKAGE_ASSETS"); directory != "" {
		raw, err := os.ReadFile(filepath.Join(directory, HostPackageInventoryName))
		if err != nil {
			t.Fatal(err)
		}
		var wire hostPackageInventoryWire
		if json.Unmarshal(raw, &wire) != nil {
			t.Fatal("producer identity unavailable")
		}
		identity := Expected{Repository: wire.Repository, Release: wire.Release, SourceSHA: wire.SourceSHA, AllowQualification: true}
		inventory, err := ParseHostPackageInventory(raw, identity)
		if err != nil {
			t.Fatal(err)
		}
		archive, err := os.Open(filepath.Join(directory, HostPackageArchiveName))
		if err != nil {
			t.Fatal(err)
		}
		defer archive.Close()
		info, err := archive.Stat()
		if err != nil || inventory.VerifyArchive(context.Background(), archive, info.Size()) != nil {
			t.Fatal("actual packaged archives rejected", err)
		}
	}
}
