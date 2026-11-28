package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// StageMemberImages is called by the authorized rolling stage on an existing
// member. It publishes only immutable, independently verified import files and
// a private per-candidate manifest. The active deployment remains separately owned.
func (c *DevelopmentCatalog) StageMemberImages(ctx context.Context, parent, deploy string, check func(context.Context) error) (string, error) {
	return c.stageMemberImages(ctx, parent, k3sImageDirectory, deploy, check)
}

func (c *DevelopmentCatalog) stageMemberImages(ctx context.Context, parent, imports, deploy string, check func(context.Context) error) (string, error) {
	if c.upgrade == nil || imageBoundary(ctx, check) != nil {
		return "", ErrSessionAuthority
	}
	file, err := c.OpenAsset(parent, ImageInventoryName)
	if err != nil {
		return "", err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, MaxImageInventoryBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return "", ErrImageArchive
	}
	inv, err := ParseImageInventory(raw, c.expected)
	if err != nil {
		return "", err
	}
	images, err := imageInstallRoot(imports)
	if err != nil {
		return "", err
	}
	defer images.Close()
	target, err := imageInstallRoot(deploy)
	if err != nil {
		return "", err
	}
	defer target.Close()
	manifest, err := inv.runtimeManifest()
	if err != nil {
		return "", err
	}
	var missing []ImageArchiveProof
	demand := PreparationScratchDemand{Entries: 1}
	layers := map[string]bool{}
	for _, proof := range inv.Images() {
		obj, ok := c.Asset(ImageAssetName(proof.Role))
		if !ok || obj.SHA256 != proof.ArchiveSHA256 || obj.Bytes != proof.ArchiveBytes {
			return "", ErrImageArchive
		}
		name := memberArchiveName(proof)
		if _, err := images.Lstat(name); err == nil {
			if verifyPublishedImageFile(ctx, images, name, obj.Bytes, obj.SHA256) != nil {
				return "", ErrImageArchive
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		missing = append(missing, proof)
		demand.Bytes += uint64(obj.Bytes)
		demand.Entries++
		for _, l := range proof.Layers {
			if !layers[l.Digest] {
				layers[l.Digest] = true
				demand.Bytes += uint64(l.BlobBytes + l.FileBytes)
				demand.Entries += uint64(1 + l.Entries)
			}
		}
	}
	if len(missing) > 0 && checkRootScratchCapacity(images, demand) != nil {
		return "", errScratchCapacity
	}
	// Verify the entire missing cohort before exposing the first K3s import.
	for _, proof := range missing {
		if imageBoundary(ctx, check) != nil {
			return "", ErrSessionAuthority
		}
		f, err := c.OpenAsset(parent, ImageAssetName(proof.Role))
		if err != nil {
			return "", err
		}
		measured, err := InspectImageArchive(ctx, f, proof.ArchiveBytes, proof.Role, inv.wire.buildSource(proof.Role))
		_ = f.Close()
		if err != nil || inv.MatchesArchive(measured) != nil {
			return "", ErrImageArchive
		}
	}
	for _, proof := range missing {
		name := memberArchiveName(proof)
		err := publishImageFile(ctx, images, name, func(out io.Writer) error {
			f, err := c.OpenAsset(parent, ImageAssetName(proof.Role))
			if err != nil {
				return err
			}
			defer f.Close()
			dest, ok := out.(*os.File)
			if !ok || reserveImageFile(dest, proof.ArchiveBytes) != nil {
				return errScratchCapacity
			}
			return copyVerifiedDevelopmentArchive(ctx, f, dest, proof)
		}, check)
		if err != nil && verifyPublishedImageFile(ctx, images, name, proof.ArchiveBytes, proof.ArchiveSHA256) != nil {
			return "", err
		}
	}
	if imageBoundary(ctx, check) != nil {
		return "", ErrSessionAuthority
	}
	// The name is selected from the bound digest, never from a client path.
	name := "image-manifest-" + c.expected.Release + ".json"
	if err := publishImageFile(ctx, target, name, func(out io.Writer) error { _, err := io.Copy(out, bytes.NewReader(manifest)); return err }, check); err != nil {
		if verifyPublishedImageFile(ctx, target, name, int64(len(manifest)), developmentHash(manifest)) != nil {
			return "", err
		}
	}
	return filepath.Join(deploy, name), imageBoundary(ctx, check)
}

func memberArchiveName(p ImageArchiveProof) string {
	return "borealis-development-" + p.ArchiveSHA256 + ".tar"
}

func copyVerifiedDevelopmentArchive(ctx context.Context, in io.Reader, out io.Writer, p ImageArchiveProof) error {
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(contextReader{ctx, in}, p.ArchiveBytes+1))
	if err != nil || n != p.ArchiveBytes || hex.EncodeToString(h.Sum(nil)) != p.ArchiveSHA256 {
		return ErrImageArchive
	}
	return ctx.Err()
}

// Re-open the digest-bound runtime manifest before each rollout phase.
func (c *DevelopmentCatalog) MemberImageManifest(parent, config string) (string, error) {
	file, err := c.OpenAsset(parent, ImageInventoryName)
	if err != nil {
		return "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, MaxImageInventoryBytes+1))
	if err != nil {
		return "", err
	}
	inv, err := ParseImageInventory(raw, c.expected)
	if err != nil {
		return "", err
	}
	manifest, err := inv.runtimeManifest()
	if err != nil {
		return "", err
	}
	root, err := imageInstallRoot(config)
	if err != nil {
		return "", err
	}
	defer root.Close()
	name := "image-manifest-" + c.expected.Release + ".json"
	data, err := readCandidateFile(root, name, MaxImageInventoryBytes)
	if err != nil || !bytes.Equal(data, manifest) {
		return "", ErrImageArchive
	}
	return filepath.Join(config, name), nil
}
