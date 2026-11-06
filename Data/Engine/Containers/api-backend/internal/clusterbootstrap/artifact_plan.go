package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

const prejoinK3sInstaller = "opt/Borealis/Engine/Deploy/k3s-install.sh"
const prejoinImageManifest = "opt/Borealis/Engine/Deploy/image-manifest.json"

// Fixed private publication contract. Paths derive only from reviewed roles,
// compiled asset names and authenticated digests, never wire-supplied paths.
type prejoinArtifactFile struct {
	path   string
	mode   os.FileMode
	size   int64
	digest string
	write  func(context.Context, io.Writer, func(context.Context) error) error
}

type prejoinArtifactPlan struct {
	files  []prejoinArtifactFile
	digest string
}

// Caller holds aggregate ownership for the complete publication transaction.
// This snapshots immutable metadata only; every writer still authenticates its
// owned bytes under current authority before and during the actual copy.
func (a *PreparationArtifacts) publicationPlan(ctx context.Context, check func(context.Context) error) (prejoinArtifactPlan, error) {
	invalid := prejoinArtifactPlan{}
	if a.verifyBindings(ctx, check) != nil {
		return invalid, ErrImageArchive
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
		return invalid, ErrImageArchive
	}
	plan := prejoinArtifactPlan{}
	for _, proof := range a.application.inventory.Images() {
		plan.files = append(plan.files, prejoinArtifactFile{path: filepath.Join("var/lib/rancher/k3s/agent/images", imagePinnedArchive(proof)), mode: 0600, size: proof.ArchiveBytes, digest: proof.ArchiveSHA256,
			write: func(ctx context.Context, out io.Writer, check func(context.Context) error) error {
				return a.application.WriteArchive(ctx, proof.Role, out, check)
			}})
	}
	k3sArchive := a.k3s.assets[K3sArchiveName]
	plan.files = append(plan.files, prejoinArtifactFile{path: filepath.Join("var/lib/rancher/k3s/agent/images", K3sArchiveName), mode: 0600, size: k3sArchive.Size, digest: k3sArchive.SHA256,
		write: func(ctx context.Context, out io.Writer, check func(context.Context) error) error {
			return a.k3s.WriteAsset(ctx, K3sArchiveName, out, check)
		}})
	for _, proof := range a.external.inventory.Images() {
		plan.files = append(plan.files, prejoinArtifactFile{path: filepath.Join("var/lib/rancher/k3s/agent/images", ExternalImageAssetName(proof.Reference)), mode: 0600, size: proof.ArchiveBytes, digest: proof.ArchiveSHA256,
			write: func(ctx context.Context, out io.Writer, check func(context.Context) error) error {
				return a.external.WriteArchive(ctx, proof.Reference, out, check)
			}})
	}
	pg := a.postgres.proof
	plan.files = append(plan.files, prejoinArtifactFile{path: filepath.Join("var/lib/rancher/k3s/agent/images", postgresArchiveName), mode: 0600, size: pg.ArchiveBytes, digest: pg.ArchiveSHA256, write: a.postgres.WriteArchive})
	for _, asset := range []struct {
		name, path string
		mode       os.FileMode
	}{{K3sBinaryName, "usr/local/bin/k3s", 0755}, {K3sInstallerName, prejoinK3sInstaller, 0600}} {
		pin := a.k3s.assets[asset.name]
		plan.files = append(plan.files, prejoinArtifactFile{path: asset.path, mode: asset.mode, size: pin.Size, digest: pin.SHA256,
			write: func(ctx context.Context, out io.Writer, check func(context.Context) error) error {
				return a.k3s.WriteAsset(ctx, asset.name, out, check)
			}})
	}
	manifest, err := a.application.runtimeManifest()
	if err != nil {
		return invalid, err
	}
	digest := sha256.Sum256(manifest)
	plan.files = append(plan.files, prejoinArtifactFile{path: prejoinImageManifest, mode: 0600, size: int64(len(manifest)), digest: hex.EncodeToString(digest[:]),
		write: func(ctx context.Context, out io.Writer, check func(context.Context) error) error {
			if imageBoundary(ctx, check) != nil {
				return ErrSessionAuthority
			}
			if _, err := io.Copy(out, bytes.NewReader(manifest)); err != nil {
				return ErrImageArchive
			}
			return imageBoundary(ctx, check)
		}})
	type identity struct {
		Path   string
		Mode   uint32
		Size   int64
		SHA256 string
	}
	identities := make([]identity, 0, len(plan.files))
	seen := map[string]bool{}
	for _, file := range plan.files {
		if file.path == "" || filepath.IsAbs(file.path) || filepath.Clean(file.path) != file.path || seen[file.path] || file.size < 1 || file.size > MaxImageArchiveBytes || !digestPattern.MatchString(file.digest) || file.write == nil {
			return invalid, ErrImageArchive
		}
		seen[file.path] = true
		identities = append(identities, identity{file.path, uint32(file.mode), file.size, file.digest})
	}
	binding, err := artifactIdentity(a.expected, a.k3sVersion, a.postgresReference)
	if err != nil || len(plan.files) != 37 {
		return invalid, ErrImageArchive
	}
	raw, err := json.Marshal(struct {
		Domain string
		Source artifactTransferIdentity
		Files  []identity
	}{"prejoin-complete-artifacts-v1", binding, identities})
	if err != nil || imageBoundary(ctx, check) != nil {
		return invalid, ErrImageArchive
	}
	digest = sha256.Sum256(raw)
	plan.digest = hex.EncodeToString(digest[:])
	return plan, nil
}
