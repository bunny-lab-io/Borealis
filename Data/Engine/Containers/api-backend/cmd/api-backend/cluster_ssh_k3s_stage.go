package main

import (
	"context"
	"io"
	"time"

	"borealis/api-backend/internal/clusterbootstrap"
)

// Acquisition remains inside the original joined preparation scope. Publication
// is refreshed around the complete batch; current claim/source/capacity checks
// bracket each asset. Caller owns cleanup and holds no SQL connection during IO.
func (v clusterSSHK3sInventory) stage(ctx context.Context, scratchParent string, check func(context.Context) error) (*clusterbootstrap.K3sSet, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if check == nil || check(ctx) != nil || v.refresh(ctx) != nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrImageArchive
	}
	set, err := clusterbootstrap.StageK3sInputs(ctx, scratchParent, v.inventory, func(ctx context.Context, name string) (io.ReadCloser, error) {
		asset, exists := v.assets[name]
		if !exists {
			return nil, clusterbootstrap.ErrImageArchive
		}
		response, err := openClusterPackagedAsset(ctx, asset, asset.Size)
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
