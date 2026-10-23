package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"reflect"
	"time"
)

// Private immutable release inventory, independently resolved rather than
// supplied by browser or source runtime cache. Acquisition reads only metadata;
// archives are downloaded and remeasured by guarded preparation later.
type clusterSSHExternalInventory struct {
	expected  clusterbootstrap.Expected
	inventory *clusterbootstrap.ExternalImageInventory
	assets    map[string]clusterBootstrapAsset
}

func resolveClusterSSHExternalInventory(ctx context.Context, expected clusterbootstrap.Expected) (clusterSSHExternalInventory, error) {
	fail := func() (clusterSSHExternalInventory, error) {
		return clusterSSHExternalInventory{}, clusterbootstrap.ErrImageArchive
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	required := map[string]int64{clusterbootstrap.ExternalInventoryName: clusterbootstrap.MaxImageInventoryBytes}
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		required[clusterbootstrap.ExternalImageAssetName(pin.Reference)] = clusterbootstrap.MaxImageArchiveBytes
	}
	assets, err := resolveClusterPackagedAssets(ctx, expected, required)
	if err != nil {
		return fail()
	}
	asset := assets[clusterbootstrap.ExternalInventoryName]
	response, err := openClusterPackagedAsset(ctx, asset, clusterbootstrap.MaxImageInventoryBytes)
	if err != nil {
		return fail()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, asset.Size+1))
	_ = response.Body.Close()
	h := sha256.Sum256(raw)
	if err != nil || int64(len(raw)) != asset.Size || "sha256:"+hex.EncodeToString(h[:]) != asset.Digest {
		return fail()
	}
	inventory, err := clusterbootstrap.ParseExternalImageInventory(raw, expected)
	if err != nil {
		return fail()
	}
	for _, image := range inventory.Images() {
		a := assets[clusterbootstrap.ExternalImageAssetName(image.Reference)]
		if a.Size != image.ArchiveBytes || a.Digest != "sha256:"+image.ArchiveSHA256 {
			return fail()
		}
	}
	if ctx.Err() != nil {
		return fail()
	}
	return clusterSSHExternalInventory{expected: expected, inventory: inventory, assets: assets}, nil
}

func (v clusterSSHExternalInventory) refresh(ctx context.Context) error {
	if v.inventory == nil {
		return clusterbootstrap.ErrImageArchive
	}
	fresh, err := resolveClusterSSHExternalInventory(ctx, v.expected)
	if err != nil || !reflect.DeepEqual(v.assets, fresh.assets) {
		return clusterbootstrap.ErrImageArchive
	}
	a, _ := v.inventory.Export()
	b, _ := fresh.inventory.Export()
	if string(a) != string(b) {
		return clusterbootstrap.ErrImageArchive
	}
	return nil
}

// Stage downloads under the original joined preparation lease scope. The
// caller supplies complete source/configuration/cohort checks and owns cleanup.
// No database connection may remain borrowed during this call. Publication is
// checked once on either side of the batch, while claim checks bracket each
// archive. The immutable asset IDs/digests remain fixed throughout streaming.
func (v clusterSSHExternalInventory) stage(ctx context.Context, scratchParent string, check func(context.Context) error) (*clusterbootstrap.ExternalImageSet, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if check == nil || v.refresh(ctx) != nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrImageArchive
	}
	set, err := clusterbootstrap.StageExternalImages(ctx, scratchParent, v.inventory,
		func(ctx context.Context, proof clusterbootstrap.ExternalImageProof) (io.ReadCloser, error) {
			response, err := openClusterPackagedAsset(ctx, v.assets[clusterbootstrap.ExternalImageAssetName(proof.Reference)], clusterbootstrap.MaxImageArchiveBytes)
			if err != nil {
				return nil, err
			}
			return response.Body, nil
		}, check)
	if err != nil {
		return nil, err
	}
	if v.refresh(ctx) != nil || check(ctx) != nil || ctx.Err() != nil {
		_ = set.Close()
		return nil, clusterbootstrap.ErrImageArchive
	}
	return set, nil
}

// Conditional static-image demand only: incoming archive, current/previous
// archives, content store and every expanded layer. No cross-image deduplication
// credit. Filesystem allocation/inode reserve is added by the shared budgeter.
// Source overrides, PostgreSQL, OS and runtime growth remain separate gates.
func (v clusterSSHExternalInventory) demands() ([]clusterSSHFilesystemDemand, error) {
	if v.inventory == nil {
		return nil, clusterbootstrap.ErrImageArchive
	}
	var stage, runtime, runtimeEntries uint64
	add := func(total *uint64, n int64) bool {
		if n < 0 {
			return false
		}
		next, ok := clusterSSHCapacitySum(*total, uint64(n))
		*total = next
		return ok
	}
	for _, image := range v.inventory.Images() {
		if !add(&stage, image.ArchiveBytes) || !add(&runtime, 2*image.ArchiveBytes) || !add(&runtime, image.ContentBytes) || !add(&runtimeEntries, 2+image.ContentEntries) {
			return nil, clusterbootstrap.ErrImageArchive
		}
		for _, layer := range image.Layers {
			if !add(&runtime, layer.TarBytes) || !add(&runtimeEntries, layer.Entries) {
				return nil, clusterbootstrap.ErrImageArchive
			}
		}
	}
	return []clusterSSHFilesystemDemand{
		{Path: "/opt/Borealis", Bytes: stage, Entries: uint64(len(v.inventory.Images()))},
		{Path: "/var/lib/rancher/k3s", Bytes: runtime, Entries: runtimeEntries},
	}, nil
}
