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
type clusterSSHImageInventory struct {
	expected  clusterbootstrap.Expected
	inventory *clusterbootstrap.ImageInventory
	assets    map[string]clusterBootstrapAsset
}

func resolveClusterSSHImageInventory(ctx context.Context, expected clusterbootstrap.Expected) (clusterSSHImageInventory, error) {
	fail := func() (clusterSSHImageInventory, error) {
		return clusterSSHImageInventory{}, clusterbootstrap.ErrImageArchive
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	required := map[string]int64{clusterbootstrap.ImageInventoryName: clusterbootstrap.MaxImageInventoryBytes}
	for _, role := range clusterbootstrap.ImageRoles() {
		required[clusterbootstrap.ImageAssetName(role)] = clusterbootstrap.MaxImageArchiveBytes
	}
	assets, err := resolveClusterPackagedAssets(ctx, expected, required)
	if err != nil {
		return fail()
	}
	asset := assets[clusterbootstrap.ImageInventoryName]
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
	inventory, err := clusterbootstrap.ParseImageInventory(raw, expected)
	if err != nil {
		return fail()
	}
	for _, image := range inventory.Images() {
		a := assets[clusterbootstrap.ImageAssetName(image.Role)]
		if a.Size != image.ArchiveBytes || a.Digest != "sha256:"+image.ArchiveSHA256 {
			return fail()
		}
	}
	if ctx.Err() != nil {
		return fail()
	}
	return clusterSSHImageInventory{expected: expected, inventory: inventory, assets: assets}, nil
}

func (v clusterSSHImageInventory) refresh(ctx context.Context) error {
	if v.inventory == nil {
		return clusterbootstrap.ErrImageArchive
	}
	fresh, err := resolveClusterSSHImageInventory(ctx, v.expected)
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
func (v clusterSSHImageInventory) stage(ctx context.Context, scratchParent string, check func(context.Context) error) (*clusterbootstrap.ImageSet, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if check == nil || v.refresh(ctx) != nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrImageArchive
	}
	set, err := clusterbootstrap.StageImages(ctx, scratchParent, v.inventory,
		func(ctx context.Context, proof clusterbootstrap.ImageArchiveProof) (io.ReadCloser, error) {
			response, err := openClusterPackagedAsset(ctx, v.assets[clusterbootstrap.ImageAssetName(proof.Role)], clusterbootstrap.MaxImageArchiveBytes)
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

// Image demand is conditional, not a complete runtime installation budget.
// No sharing/deduplication credit: two full archive generations, one incoming
// archive, all content blobs and every expanded layer tar count independently.
// Tar expansion is a conservative content bound, not inode/allocation reserve.
func (v clusterSSHImageInventory) demands() ([]clusterSSHFilesystemDemand, error) {
	if v.inventory == nil {
		return nil, clusterbootstrap.ErrImageArchive
	}
	var stage, runtime uint64
	add := func(a *uint64, n int64) bool {
		if n < 0 {
			return false
		}
		next, ok := clusterSSHCapacitySum(*a, uint64(n))
		*a = next
		return ok
	}
	for _, image := range v.inventory.Images() {
		if !add(&stage, image.ArchiveBytes) || !add(&runtime, 2*image.ArchiveBytes) {
			return nil, clusterbootstrap.ErrImageArchive
		}
		for _, layer := range image.Layers {
			if !add(&runtime, layer.BlobBytes) || !add(&runtime, layer.TarBytes) {
				return nil, clusterbootstrap.ErrImageArchive
			}
		}
	}
	if !add(&stage, 2*clusterbootstrap.MaxExpandedBytes+clusterbootstrap.MaxBundleBytes) {
		return nil, clusterbootstrap.ErrImageArchive
	}
	return []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: stage}, {Path: "/var/lib/rancher/k3s", Bytes: runtime}}, nil
}

func withClusterSSHImageStorageCapacity(ctx context.Context, readers clusterSSHNetworkTargetReaders,
	source clusterSSHPreparationSnapshotRead, consume func(context.Context, []clusterSSHTargetStorageCapacity) error) error {
	if source == nil || readers.within == nil || readers.Refresh == nil || consume == nil {
		return clusterbootstrap.ErrImageArchive
	}
	return readers.within(ctx, func(ctx context.Context) error {
		if readers.Refresh(ctx) != nil {
			return clusterbootstrap.ErrImageArchive
		}
		initial, err := source(ctx)
		if err != nil {
			return err
		}
		images, err := resolveClusterSSHImageInventory(ctx, initial.Expected.Source)
		if err != nil {
			return err
		}
		demands, err := images.demands()
		if err != nil {
			return err
		}
		boundSource := func(ctx context.Context) (clusterSSHPreparationSnapshot, error) {
			value, err := source(ctx)
			if err != nil || value.Expected.Source != initial.Expected.Source {
				return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrImageArchive
			}
			return value, nil
		}
		if err := withClusterSSHTargetStorageCapacity(ctx, readers, boundSource, demands, consume); err != nil {
			return err
		}
		if images.refresh(ctx) != nil || readers.Refresh(ctx) != nil {
			return clusterbootstrap.ErrImageArchive
		}
		_, err = boundSource(ctx)
		return err
	})
}
