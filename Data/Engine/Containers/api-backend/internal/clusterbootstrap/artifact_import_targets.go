package clusterbootstrap

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
)

// ImportTargets derives immutable containerd targets from owned authenticated
// inputs. It never treats target inventory or archive filenames as import proof.
func (a *PreparationArtifacts) ImportTargets(ctx context.Context, check func(context.Context) error) ([]ContainerdImageTarget, error) {
	return a.importTargets(ctx, check, K3sPins())
}

func (a *PreparationArtifacts) importTargets(ctx context.Context, check func(context.Context) error, pins K3sInputPins) ([]ContainerdImageTarget, error) {
	if a == nil {
		return nil, ErrImageArchive
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.verifyBindings(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	a.application.mu.Lock()
	defer a.application.mu.Unlock()
	a.k3s.mu.Lock()
	defer a.k3s.mu.Unlock()
	a.external.mu.Lock()
	defer a.external.mu.Unlock()
	a.postgres.set.mu.Lock()
	defer a.postgres.set.mu.Unlock()
	if a.application.root == nil || a.application.inventory == nil || a.k3s.root == nil || a.k3s.inventory == nil || a.external.root == nil || a.external.inventory == nil || a.postgres.set.root == nil || a.postgres.set.inventory == nil {
		return nil, ErrImageArchive
	}
	if a.k3s.assets[K3sArchiveName] != pins.Archive || a.k3s.inventory.wire.Archive.ArchiveSHA256 != pins.Archive.SHA256 || a.k3s.inventory.wire.Archive.ArchiveBytes != pins.Archive.Size {
		return nil, ErrImageArchive
	}
	result, err := readStagedK3sImportTargets(ctx, a.k3s.root, pins, check)
	if err != nil {
		return nil, err
	}
	for _, proof := range a.application.inventory.Images() {
		result = append(result, ContainerdImageTarget{proof.Image, proof.ManifestDigest})
	}
	for _, proof := range append(a.external.inventory.Images(), a.postgres.proof) {
		digest := proof.IndexDigest
		if digest == "" {
			digest = proof.ManifestDigest
		}
		result = append(result, ContainerdImageTarget{proof.Reference, digest})
	}
	if len(result) != len(ImageRoles())+len(pins.Images)+len(ExternalImagePins())+1 || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	slices.SortFunc(result, func(a, b ContainerdImageTarget) int { return strings.Compare(a.Reference, b.Reference) })
	for i, target := range result {
		if !containerdImageReference.MatchString(target.Reference) || !validImageDigest(target.Digest) || i > 0 && result[i-1].Reference == target.Reference {
			return nil, ErrImageArchive
		}
	}
	return result, nil
}

func readStagedK3sImportTargets(ctx context.Context, root *os.Root, pins K3sInputPins, check func(context.Context) error) ([]ContainerdImageTarget, error) {
	if root == nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	before, err := root.Lstat(K3sArchiveName)
	if err != nil || !before.Mode().IsRegular() || before.Mode() != 0600 || before.Size() != pins.Archive.Size {
		return nil, ErrImageArchive
	}
	f, err := root.Open(K3sArchiveName)
	if err != nil {
		return nil, ErrImageArchive
	}
	defer f.Close()
	held, err := f.Stat()
	if err != nil || !os.SameFile(before, held) {
		return nil, ErrImageArchive
	}
	targets, err := inspectK3sImportTargets(ctx, f, pins)
	if err != nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	after, err := root.Lstat(K3sArchiveName)
	last, lastErr := f.Stat()
	if err != nil || lastErr != nil || !os.SameFile(held, after) || !os.SameFile(held, last) || held.Size() != after.Size() || held.Size() != last.Size() || held.Mode() != after.Mode() || held.Mode() != last.Mode() || !held.ModTime().Equal(after.ModTime()) || !held.ModTime().Equal(last.ModTime()) {
		return nil, ErrImageArchive
	}
	return targets, nil
}

// Whole-archive authentication covers the upstream OCI index without changing
// the existing serialized K3s inventory format. Docker manifest.json is already
// independently measured by staging; containerd imports the OCI root targets.
func inspectK3sImportTargets(ctx context.Context, source io.ReaderAt, pins K3sInputPins) ([]ContainerdImageTarget, error) {
	fail := func() ([]ContainerdImageTarget, error) { return nil, ErrImageArchive }
	if source == nil || ctx.Err() != nil || pins.Archive.Size < 1 || pins.Archive.Size > MaxImageArchiveBytes || !digestPattern.MatchString(pins.Archive.SHA256) || len(pins.Images) < 1 || len(pins.Images) > 32 {
		return fail()
	}
	h := sha256.New()
	count := &imageReadCounter{r: io.TeeReader(io.NewSectionReader(source, 0, pins.Archive.Size), h), ctx: ctx, limit: pins.Archive.Size}
	tr := tar.NewReader(count)
	seen := map[string]bool{}
	var index []byte
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil || len(seen) >= 4096 || header.Size < 0 || header.Size > pins.Archive.Size || len(header.PAXRecords) != 0 {
			return fail()
		}
		name := strings.TrimSuffix(header.Name, "/")
		if seen[name] {
			return fail()
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 || name != "blobs" && name != "blobs/sha256" {
				return fail()
			}
			continue
		}
		blob := strings.HasPrefix(name, "blobs/sha256/") && digestPattern.MatchString(strings.TrimPrefix(name, "blobs/sha256/"))
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA || !blob && name != "manifest.json" && name != "index.json" && name != "oci-layout" {
			return fail()
		}
		if name == "index.json" {
			if header.Size < 1 || header.Size > 1<<20 {
				return fail()
			}
			index, err = io.ReadAll(tr)
			if err != nil {
				return fail()
			}
		}
	}
	padding := make([]byte, 32<<10)
	for {
		n, err := count.Read(padding)
		for _, b := range padding[:n] {
			if b != 0 {
				return fail()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail()
		}
	}
	if ctx.Err() != nil || count.n != pins.Archive.Size || hex.EncodeToString(h.Sum(nil)) != pins.Archive.SHA256 {
		return fail()
	}
	return parseK3sImportIndex(index, pins.Images)
}

func parseK3sImportIndex(raw []byte, names []string) ([]ContainerdImageTarget, error) {
	fail := func() ([]ContainerdImageTarget, error) { return nil, ErrImageArchive }
	var index struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Manifests     []json.RawMessage `json:"manifests"`
	}
	if imageFields(raw, []string{"schemaVersion", "manifests"}, []string{"mediaType", "annotations"}) != nil || imageJSON(raw, &index) != nil || index.SchemaVersion != 2 || index.MediaType != "" && index.MediaType != ociIndexType || len(index.Manifests) != len(names) || len(names) == 0 || len(names) > 32 {
		return fail()
	}
	want := map[string]bool{}
	for _, name := range names {
		if !strings.HasPrefix(name, "rancher/") || !containerdImageReference.MatchString(name) || want["docker.io/"+name] {
			return fail()
		}
		want["docker.io/"+name] = true
	}
	result := []ContainerdImageTarget{}
	for _, raw := range index.Manifests {
		var d imageDescriptor
		if imageFields(raw, []string{"mediaType", "digest", "size", "annotations"}, []string{"platform"}) != nil || imageJSON(raw, &d) != nil || !validImageDigest(d.Digest) || d.Size < 1 || d.Size > 1<<20 || !slices.Contains([]string{ociManifestType, dockerManifestType, ociIndexType, dockerIndexType}, d.MediaType) {
			return fail()
		}
		// Pinned containerd exporter stores the full name in this annotation;
		// org.opencontainers.image.ref.name may be only a tag, never use it alone.
		name := d.Annotations["io.containerd.image.name"]
		if !want[name] {
			return fail()
		}
		delete(want, name)
		result = append(result, ContainerdImageTarget{name, d.Digest})
	}
	if len(want) != 0 {
		return fail()
	}
	slices.SortFunc(result, func(a, b ContainerdImageTarget) int { return strings.Compare(a.Reference, b.Reference) })
	return result, nil
}
