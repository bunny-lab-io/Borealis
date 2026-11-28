package clusterbootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Expected values must come from authenticated preparation artifacts, never
// target stdout. This parser grants no start, import, activation or admission.
type ContainerdImageTarget struct {
	Reference string
	Digest    string
}

var containerdImageReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,511}$`)

func containerdImportTargetDigest(targets []ContainerdImageTarget) (string, error) {
	if len(targets) < 1 || len(targets) > 64 {
		return "", ErrImageArchive
	}
	for i, target := range targets {
		if !containerdImageReference.MatchString(target.Reference) || !validImageDigest(target.Digest) || i > 0 && targets[i-1].Reference >= target.Reference {
			return "", ErrImageArchive
		}
	}
	raw, err := json.Marshal(struct {
		Domain  string
		Targets []ContainerdImageTarget
	}{"containerd-import-targets-v1", targets})
	if err != nil {
		return "", ErrImageArchive
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ParseContainerdImportCheck consumes pinned ctr images check output for the
// fixed k8s.io namespace and overlayfs snapshotter. Unlike exit status, each
// complete row proves required content is available and IsUnpacked is true.
// Human-readable size is informational, not capacity or identity evidence.
func ParseContainerdImportCheck(raw []byte, expected []ContainerdImageTarget) ([]ContainerdImageTarget, error) {
	fail := func() ([]ContainerdImageTarget, error) { return nil, ErrImageArchive }
	if len(raw) == 0 || len(raw) > 256<<10 || len(expected) < 1 || len(expected) > 64 || raw[len(raw)-1] != '\n' || bytes.IndexByte(raw, '\r') >= 0 {
		return fail()
	}
	for _, b := range raw {
		if b != '\n' && b != '\t' && (b < 32 || b > 126) {
			return fail()
		}
	}
	want := map[string]string{}
	for _, target := range expected {
		if !containerdImageReference.MatchString(target.Reference) || !validImageDigest(target.Digest) || want[target.Reference] != "" {
			return fail()
		}
		want[target.Reference] = target.Digest
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) > 257 || strings.Join(strings.Fields(lines[0]), " ") != "REF TYPE DIGEST STATUS SIZE UNPACKED" {
		return fail()
	}
	seen := map[string]bool{}
	result := []ContainerdImageTarget{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 6 || len(fields) > 12 || len(line) > 2048 || !containerdImageReference.MatchString(fields[0]) || seen[fields[0]] || !validImageDigest(fields[2]) {
			return fail()
		}
		if !slices.Contains([]string{ociManifestType, dockerManifestType, "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json"}, fields[1]) {
			return fail()
		}
		if fields[len(fields)-1] != "true" && fields[len(fields)-1] != "false" {
			return fail()
		}
		seen[fields[0]] = true
		digest, required := want[fields[0]]
		if !required {
			continue
		}
		if len(fields) < 7 || fields[2] != digest || fields[3] != "complete" || fields[len(fields)-1] != "true" {
			return fail()
		}
		count, ok := strings.CutPrefix(fields[4], "(")
		if !ok {
			return fail()
		}
		count, ok = strings.CutSuffix(count, ")")
		if !ok {
			return fail()
		}
		present, total, ok := strings.Cut(count, "/")
		n, err := strconv.ParseUint(total, 10, 16)
		if !ok || err != nil || n < 2 || n > 4096 || present != total || strconv.FormatUint(n, 10) != total {
			return fail()
		}
		result = append(result, ContainerdImageTarget{fields[0], digest})
	}
	if len(result) != len(want) {
		return fail()
	}
	slices.SortFunc(result, func(a, b ContainerdImageTarget) int { return strings.Compare(a.Reference, b.Reference) })
	return result, nil
}
