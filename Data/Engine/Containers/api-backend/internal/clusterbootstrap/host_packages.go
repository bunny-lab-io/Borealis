package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const (
	HostPackageInventoryName = "borealis-node-host-packages-ubuntu-noble-linux-amd64.json"
	HostPackageArchiveName   = "borealis-node-host-packages-ubuntu-noble-linux-amd64.tar"
)

// The exact committed bytes, including their one terminal newline, identify
// this reviewed suite plan. Python and Go do not independently canonicalize it.
//
//go:embed host_packages.lock.json
var hostPackageLock []byte

type HostPackagePin struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	File         string `json:"file"`
	Phase        string `json:"phase"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	URL          string `json:"url"`
	FileBytes    int64  `json:"file_bytes"`
	Entries      int64  `json:"entries"`
}

type HostPackagePlan struct {
	Version   int              `json:"version"`
	OS        string           `json:"os"`
	OSVersion string           `json:"os_version"`
	Suite     string           `json:"suite"`
	Platform  string           `json:"platform"`
	Packages  []HostPackagePin `json:"packages"`
}

var hostPackageName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,199}$`)
var hostPackageVersion = regexp.MustCompile(`^[0-9][A-Za-z0-9.+:~\-]{0,199}$`)
var hostPackageFile = regexp.MustCompile(`^[a-z0-9][A-Za-z0-9.+:%_~\-]{0,95}\.deb$`)

var hostPackageRoots = map[string]string{
	"python3": "base", "ca-certificates": "base", "curl": "base", "gnupg": "base",
	"iptables": "base", "open-iscsi": "base", "nfs-common": "base",
	"docker-ce": "docker", "docker-ce-cli": "docker", "containerd.io": "docker", "docker-buildx-plugin": "docker",
}

// HostPackagePins returns independent records, never mutable process trust.
// Only this suite is currently pinned; other platform plans remain unqualified.
func HostPackagePins() (HostPackagePlan, string, error) {
	return parseHostPackagePlan(hostPackageLock)
}

func parseHostPackagePlan(raw []byte) (HostPackagePlan, string, error) {
	var plan HostPackagePlan
	if len(raw) == 0 || len(raw) > MaxImageInventoryBytes || raw[len(raw)-1] != '\n' ||
		bytes.HasSuffix(raw, []byte("\n\n")) || imageFields(raw, []string{"version", "os", "os_version", "suite", "platform", "packages"}, nil) != nil ||
		imageJSON(raw, &plan) != nil || validateHostPackagePlan(plan) != nil {
		return HostPackagePlan{}, "", ErrImageArchive
	}
	var fields struct {
		Packages []json.RawMessage `json:"packages"`
	}
	_ = json.Unmarshal(raw, &fields)
	for _, item := range fields.Packages {
		if imageFields(item, []string{"name", "version", "architecture", "file", "phase", "size", "sha256", "url", "file_bytes", "entries"}, nil) != nil {
			return HostPackagePlan{}, "", ErrImageArchive
		}
	}
	digest := sha256.Sum256(raw)
	return plan, hex.EncodeToString(digest[:]), nil
}

func validateHostPackagePlan(plan HostPackagePlan) error {
	if plan.Version != 1 || plan.OS != "ubuntu" || plan.OSVersion != "24.04" || plan.Suite != "noble" || plan.Platform != "linux-amd64" ||
		len(plan.Packages) < len(hostPackageRoots) || len(plan.Packages) > 256 {
		return ErrImageArchive
	}
	files, roots := map[string]bool{}, map[string]bool{}
	var compressed, expanded, entries int64
	for i, p := range plan.Packages {
		if !hostPackageName.MatchString(p.Name) || !hostPackageVersion.MatchString(p.Version) ||
			(p.Architecture != "amd64" && p.Architecture != "all") || !hostPackageFile.MatchString(p.File) ||
			(p.Phase != "base" && p.Phase != "docker") || (i > 0 && plan.Packages[i-1].Name >= p.Name) || files[p.File] ||
			p.Size < 1 || p.Size > MaxImageArchiveBytes || !digestPattern.MatchString(p.SHA256) || p.SHA256 == strings.Repeat("0", 64) || !validHostPackageURL(p) ||
			p.FileBytes < 0 || p.FileBytes > MaxImageExpandedBytes || p.Entries < 1 || p.Entries > MaxImageEntries {
			return ErrImageArchive
		}
		if phase, ok := hostPackageRoots[p.Name]; ok {
			if p.Phase != phase {
				return ErrImageArchive
			}
			roots[p.Name] = true
		}
		files[p.File] = true
		compressed, expanded, entries = compressed+p.Size, expanded+p.FileBytes, entries+p.Entries
		if compressed > MaxImageArchiveBytes || expanded > MaxImageExpandedBytes || entries > MaxImageEntries {
			return ErrImageArchive
		}
	}
	if len(roots) != len(hostPackageRoots) || hostPackageArchiveSize(plan.Packages) > MaxImageArchiveBytes {
		return ErrImageArchive
	}
	return nil
}

var hostPackageURLSegment = regexp.MustCompile(`^[A-Za-z0-9+._~\-]+$`)

func validHostPackageURL(p HostPackagePin) bool {
	u, err := url.Parse(p.URL)
	if err != nil || len(p.URL) > 2048 || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.RawFragment != "" || u.Opaque != "" || u.RawPath != "" || strings.ContainsAny(p.URL, "%?#") || u.String() != p.URL {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	for _, part := range parts {
		if !hostPackageURLSegment.MatchString(part) || part == "." || part == ".." {
			return false
		}
	}
	version := p.Version
	if _, after, ok := strings.Cut(version, ":"); ok {
		version = after // Repository filenames omit the dpkg epoch.
	}
	if len(parts) == 0 || parts[len(parts)-1] != p.Name+"_"+version+"_"+p.Architecture+".deb" {
		return false
	}
	switch u.Host {
	case "archive.ubuntu.com":
		return len(parts) == 6 && parts[0] == "ubuntu" && parts[1] == "pool" && slices.Contains([]string{"main", "universe", "restricted", "multiverse"}, parts[2])
	case "download.docker.com":
		return len(parts) == 8 && strings.Join(parts[:7], "/") == "linux/ubuntu/dists/noble/pool/stable/amd64"
	}
	return false
}

// Python tarfile's fixed USTAR record framing: one header per package, padded
// bodies, two zero end blocks and terminal padding to a 20-block record.
func hostPackageArchiveSize(packages []HostPackagePin) int64 {
	size := int64(1024)
	for _, p := range packages {
		size += 512 + (p.Size+511)/512*512
	}
	return (size + 10239) / 10240 * 10240
}

type HostPackageAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type hostPackageInventoryWire struct {
	Version    int              `json:"version"`
	Repository string           `json:"repository"`
	Release    string           `json:"release"`
	SourceSHA  string           `json:"source_sha"`
	OS         string           `json:"os"`
	OSVersion  string           `json:"os_version"`
	Suite      string           `json:"suite"`
	Platform   string           `json:"platform"`
	PlanSHA256 string           `json:"plan_sha256"`
	Archive    HostPackageAsset `json:"archive"`
	Packages   []HostPackagePin `json:"packages"`
}

// This source-bound immutable inventory is content evidence only. It supplies
// neither target compatibility, dependency-resolution authority, generated or
// service demand, installation permission, nor persistent-storage readiness.
type HostPackageInventory struct {
	wire hostPackageInventoryWire
	raw  []byte
}

func ParseHostPackageInventory(raw []byte, expected Expected) (*HostPackageInventory, error) {
	plan, digest, err := HostPackagePins()
	if err != nil {
		return nil, err
	}
	return parseHostPackageInventory(raw, expected, plan, digest)
}

func parseHostPackageInventory(raw []byte, expected Expected, plan HostPackagePlan, digest string) (*HostPackageInventory, error) {
	var wire hostPackageInventoryWire
	if expected.Validate() != nil || validateHostPackagePlan(plan) != nil || !digestPattern.MatchString(digest) || len(raw) == 0 || len(raw) > MaxImageInventoryBytes ||
		imageFields(raw, []string{"version", "repository", "release", "source_sha", "os", "os_version", "suite", "platform", "plan_sha256", "archive", "packages"}, nil) != nil ||
		imageJSON(raw, &wire) != nil || wire.Version != 1 || wire.Repository != expected.Repository || wire.Release != expected.Release || wire.SourceSHA != expected.SourceSHA ||
		wire.OS != plan.OS || wire.OSVersion != plan.OSVersion || wire.Suite != plan.Suite || wire.Platform != plan.Platform || wire.PlanSHA256 != digest ||
		!slices.Equal(wire.Packages, plan.Packages) || wire.Archive.Name != HostPackageArchiveName || wire.Archive.Size != hostPackageArchiveSize(plan.Packages) ||
		!digestPattern.MatchString(wire.Archive.SHA256) || wire.Archive.SHA256 == strings.Repeat("0", 64) {
		return nil, ErrImageArchive
	}
	var fields struct {
		Archive  json.RawMessage   `json:"archive"`
		Packages []json.RawMessage `json:"packages"`
	}
	_ = json.Unmarshal(raw, &fields)
	if imageFields(fields.Archive, []string{"name", "size", "sha256"}, nil) != nil {
		return nil, ErrImageArchive
	}
	for _, item := range fields.Packages {
		if imageFields(item, []string{"name", "version", "architecture", "file", "phase", "size", "sha256", "url", "file_bytes", "entries"}, nil) != nil {
			return nil, ErrImageArchive
		}
	}
	return &HostPackageInventory{wire: wire, raw: bytes.Clone(raw)}, nil
}

func (v *HostPackageInventory) Packages() []HostPackagePin {
	if v == nil {
		return nil
	}
	return slices.Clone(v.wire.Packages)
}

func (v *HostPackageInventory) Archive() HostPackageAsset {
	if v == nil {
		return HostPackageAsset{}
	}
	return v.wire.Archive
}

func (v *HostPackageInventory) Export() ([]byte, error) {
	if v == nil || len(v.raw) == 0 {
		return nil, ErrImageArchive
	}
	return bytes.Clone(v.raw), nil
}

// VerifyArchive reads exact ordered package bytes without extracting .deb
// contents or invoking a package manager. Compiled pins authenticate metadata
// and measured content totals; this is not an installation-footprint proof.
func (v *HostPackageInventory) VerifyArchive(ctx context.Context, source io.ReaderAt, size int64) error {
	if v == nil || len(v.raw) == 0 || ctx.Err() != nil || source == nil || size != v.wire.Archive.Size || size < 1 || size > MaxImageArchiveBytes {
		return ErrImageArchive
	}
	digest := sha256.New()
	reader := &imageReadCounter{r: io.TeeReader(io.NewSectionReader(source, 0, size), digest), ctx: ctx, limit: size}
	archive := tar.NewReader(reader)
	for _, p := range v.wire.Packages {
		header, err := archive.Next()
		if err != nil || header.Name != p.File || header.Size != p.Size || header.Typeflag != tar.TypeReg || header.Mode != 0644 ||
			header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" || header.Linkname != "" ||
			header.ModTime.Unix() != 0 || !header.AccessTime.IsZero() || !header.ChangeTime.IsZero() || header.Format != tar.FormatUSTAR || len(header.PAXRecords) != 0 || len(header.Xattrs) != 0 {
			return ErrImageArchive
		}
		body := sha256.New()
		count, err := io.Copy(body, archive)
		if err != nil || count != p.Size || hex.EncodeToString(body.Sum(nil)) != p.SHA256 {
			return ErrImageArchive
		}
	}
	if _, err := archive.Next(); err != io.EOF {
		return ErrImageArchive
	}
	padding := make([]byte, 32<<10)
	for {
		n, err := reader.Read(padding)
		for _, b := range padding[:n] {
			if b != 0 {
				return ErrImageArchive
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrImageArchive
		}
	}
	if reader.n != size || hex.EncodeToString(digest.Sum(nil)) != v.wire.Archive.SHA256 || ctx.Err() != nil {
		return ErrImageArchive
	}
	return nil
}
