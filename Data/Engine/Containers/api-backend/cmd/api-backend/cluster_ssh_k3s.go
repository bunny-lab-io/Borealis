package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"reflect"
	"sort"
	"time"
)

func mergeClusterSSHFilesystemDemands(groups ...[]clusterSSHFilesystemDemand) ([]clusterSSHFilesystemDemand, error) {
	byPath := map[string]clusterSSHFilesystemDemand{}
	for _, group := range groups {
		for _, d := range group {
			p, ok := clusterSSHStoragePath(d.Path)
			if !ok || p != d.Path || d.Bytes == 0 {
				return nil, clusterbootstrap.ErrImageArchive
			}
			current := byPath[p]
			current.Path = p
			current.Bytes, ok = clusterSSHCapacitySum(current.Bytes, d.Bytes)
			if !ok {
				return nil, clusterbootstrap.ErrImageArchive
			}
			current.Entries, ok = clusterSSHCapacitySum(current.Entries, d.Entries)
			if !ok {
				return nil, clusterbootstrap.ErrImageArchive
			}
			byPath[p] = current
		}
	}
	if len(byPath) < 1 || len(byPath) > 7 {
		return nil, clusterbootstrap.ErrImageArchive
	}
	result := make([]clusterSSHFilesystemDemand, 0, len(byPath))
	for _, d := range byPath {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

type clusterSSHK3sInventory struct {
	expected  clusterbootstrap.Expected
	version   string
	inventory *clusterbootstrap.K3sInventory
	assets    map[string]clusterBootstrapAsset
}

func resolveClusterSSHK3sInventory(ctx context.Context, expected clusterbootstrap.Expected, version string) (clusterSSHK3sInventory, error) {
	fail := func() (clusterSSHK3sInventory, error) {
		return clusterSSHK3sInventory{}, clusterbootstrap.ErrImageArchive
	}
	pins := clusterbootstrap.K3sPins()
	if version != pins.Version {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	assets, err := resolveClusterPackagedAssets(ctx, expected, map[string]int64{
		clusterbootstrap.K3sInventoryName: clusterbootstrap.MaxK3sInventoryBytes,
		clusterbootstrap.K3sBinaryName:    pins.Binary.Size,
		clusterbootstrap.K3sArchiveName:   pins.Archive.Size,
	})
	if err != nil {
		return fail()
	}
	for name, pin := range map[string]clusterbootstrap.K3sAssetPin{clusterbootstrap.K3sBinaryName: pins.Binary, clusterbootstrap.K3sArchiveName: pins.Archive} {
		a := assets[name]
		if a.Size != pin.Size || a.Digest != "sha256:"+pin.SHA256 {
			return fail()
		}
	}
	a := assets[clusterbootstrap.K3sInventoryName]
	response, err := openClusterPackagedAsset(ctx, a, clusterbootstrap.MaxK3sInventoryBytes)
	if err != nil {
		return fail()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, a.Size+1))
	_ = response.Body.Close()
	h := sha256.Sum256(raw)
	if err != nil || int64(len(raw)) != a.Size || "sha256:"+hex.EncodeToString(h[:]) != a.Digest {
		return fail()
	}
	inventory, err := clusterbootstrap.ParseK3sInventory(raw, expected, version)
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	return clusterSSHK3sInventory{expected: expected, version: version, inventory: inventory, assets: assets}, nil
}

func (v clusterSSHK3sInventory) refresh(ctx context.Context) error {
	if v.inventory == nil {
		return clusterbootstrap.ErrImageArchive
	}
	fresh, err := resolveClusterSSHK3sInventory(ctx, v.expected, v.version)
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

func (v clusterSSHK3sInventory) demands() ([]clusterSSHFilesystemDemand, error) {
	if v.inventory == nil {
		return nil, clusterbootstrap.ErrImageArchive
	}
	a, p := v.inventory.Archive(), clusterbootstrap.K3sPins()
	// Bounds were checked by the private inventory parser. Keep every incoming
	// input, two image-archive generations, full content store and each expanded
	// layer; do not discount duplicate layers or retained deployed content.
	return []clusterSSHFilesystemDemand{
		{Path: "/opt/Borealis", Bytes: uint64(p.Binary.Size + a.ArchiveBytes), Entries: 3},
		{Path: "/usr/local/bin", Bytes: uint64(2 * p.Binary.Size), Entries: 2},
		{Path: "/var/lib/rancher/k3s", Bytes: uint64(2*a.ArchiveBytes + a.ContentBytes + a.ExpandedBytes), Entries: uint64(2 + a.ContentEntries + a.ExpandedEntries)},
	}, nil
}
