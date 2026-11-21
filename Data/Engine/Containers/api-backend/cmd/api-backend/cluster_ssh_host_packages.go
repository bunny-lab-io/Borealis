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

type clusterSSHHostPackageInventory struct {
	expected  clusterbootstrap.Expected
	inventory *clusterbootstrap.HostPackageInventory
	assets    map[string]clusterBootstrapAsset
}

// Resolve only immutable release assets using existing publication custody.
// Package versions and origins come from compiled reviewed pins, never from
// current upstream repositories or a caller-supplied package list.
func resolveClusterSSHHostPackageInventory(parent context.Context, expected clusterbootstrap.Expected) (clusterSSHHostPackageInventory, error) {
	fail := func() (clusterSSHHostPackageInventory, error) {
		return clusterSSHHostPackageInventory{}, clusterbootstrap.ErrImageArchive
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	assets, err := resolveClusterPackagedAssets(ctx, expected, map[string]int64{
		clusterbootstrap.HostPackageInventoryName: clusterbootstrap.MaxImageInventoryBytes,
		clusterbootstrap.HostPackageArchiveName:   clusterbootstrap.MaxImageArchiveBytes,
	})
	if err != nil {
		return fail()
	}
	asset := assets[clusterbootstrap.HostPackageInventoryName]
	response, err := openClusterPackagedAsset(ctx, asset, clusterbootstrap.MaxImageInventoryBytes)
	if err != nil {
		return fail()
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, asset.Size+1))
	closeErr := response.Body.Close()
	hash := sha256.Sum256(raw)
	if readErr != nil || closeErr != nil || ctx.Err() != nil || int64(len(raw)) != asset.Size || "sha256:"+hex.EncodeToString(hash[:]) != asset.Digest {
		return fail()
	}
	inventory, err := clusterbootstrap.ParseHostPackageInventory(raw, expected)
	if err != nil {
		return fail()
	}
	archive := inventory.Archive()
	asset = assets[clusterbootstrap.HostPackageArchiveName]
	if asset.Size != archive.Size || asset.Digest != "sha256:"+archive.SHA256 || ctx.Err() != nil {
		return fail()
	}
	return clusterSSHHostPackageInventory{expected, inventory, assets}, nil
}

func (v clusterSSHHostPackageInventory) refresh(ctx context.Context) error {
	if v.inventory == nil {
		return clusterbootstrap.ErrImageArchive
	}
	fresh, err := resolveClusterSSHHostPackageInventory(ctx, v.expected)
	if err != nil || !reflect.DeepEqual(v.assets, fresh.assets) {
		return clusterbootstrap.ErrImageArchive
	}
	before, beforeErr := v.inventory.Export()
	after, afterErr := fresh.inventory.Export()
	if beforeErr != nil || afterErr != nil || string(before) != string(after) || ctx.Err() != nil {
		return clusterbootstrap.ErrImageArchive
	}
	return nil
}

func (v clusterSSHHostPackageInventory) stage(ctx context.Context, parent string, check func(context.Context) error) (*clusterbootstrap.HostPackageSet, error) {
	if v.inventory == nil || check == nil || check(ctx) != nil {
		return nil, clusterbootstrap.ErrImageArchive
	}
	return clusterbootstrap.StageHostPackages(ctx, parent, v.expected, v.inventory, func(ctx context.Context) (io.ReadCloser, error) {
		if check(ctx) != nil {
			return nil, clusterbootstrap.ErrImageArchive
		}
		response, err := openClusterPackagedAsset(ctx, v.assets[clusterbootstrap.HostPackageArchiveName], clusterbootstrap.MaxImageArchiveBytes)
		if err != nil {
			return nil, err
		}
		return response.Body, nil
	}, check)
}

// Materialization stays under the owned /opt bootstrap wrapper. Installed data
// is charged to /etc, /usr and /var only under the persistent observer's fixed
// usrmerge/ancestor proof. This remains a subtotal: dpkg/generated/service
// demand is separate and cannot be replaced by these content measurements.
func (v clusterSSHHostPackageInventory) demands(ctx context.Context, owned *clusterbootstrap.HostPackageSet, check func(context.Context) error) ([]clusterSSHFilesystemDemand, error) {
	if v.inventory == nil || owned == nil {
		return nil, clusterbootstrap.ErrImageArchive
	}
	demand, err := owned.StorageDemand(ctx, v.expected, check)
	if err != nil {
		return nil, err
	}
	installed, err := owned.InstalledContentDemand(ctx, v.expected, check)
	if err != nil {
		return nil, err
	}
	rows := []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: demand.Bytes, Entries: demand.Entries}}
	for _, content := range installed {
		destination := content.Path
		switch destination {
		case "/lib", "/sbin":
			// Fixed Noble aliases are requirements, never inferred observations.
			// RequirePersistent proves both aliases and destination ancestry.
			destination = "/usr"
		case "/etc", "/usr", "/var":
		default:
			return nil, clusterbootstrap.ErrImageArchive
		}
		rows = append(rows, clusterSSHFilesystemDemand{Path: destination, Bytes: content.Bytes, Entries: content.Entries})
	}
	return mergeClusterSSHFilesystemDemands(rows)
}
