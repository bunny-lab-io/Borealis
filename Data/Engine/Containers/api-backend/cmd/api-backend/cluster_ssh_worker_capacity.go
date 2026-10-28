package main

import "borealis/api-backend/internal/clusterbootstrap"

// One preparation attempt retains its owned bootstrap bundle, all application
// and external archives, all three K3s assets, and the PostgreSQL acquisition's content+archive peak.
// No extracted image layers or target/PGDATA charge belongs on the API worker.
func clusterSSHPreparationWorkerCapacity(bundle clusterbootstrap.PreparationScratchDemand, images clusterSSHImageInventory, external clusterSSHExternalInventory, k3s clusterSSHK3sInventory, capacity clusterSSHPreparedImageCapacity) (clusterSSHPreparedImageCapacity, error) {
	fail := func() (clusterSSHPreparedImageCapacity, error) {
		return clusterSSHPreparedImageCapacity{}, clusterbootstrap.ErrImageArchive
	}
	if images.inventory == nil || external.inventory == nil || k3s.inventory == nil || images.expected != external.expected || images.expected != k3s.expected || k3s.version != clusterbootstrap.K3sPins().Version || images.expected.Validate() != nil ||
		bundle.Bytes == 0 || bundle.Bytes > clusterbootstrap.MaxBundleBytes+clusterbootstrap.MaxExpandedBytes || bundle.Entries < 3 || bundle.Entries > clusterbootstrap.MaxEntries+3 ||
		capacity.PostgresWorkerPeakBytes == 0 || capacity.PostgresWorkerPeakEntries == 0 {
		return fail()
	}
	bytes, entries := bundle.Bytes, bundle.Entries
	add := func(b, e uint64) bool {
		var ok bool
		if bytes, ok = clusterSSHCapacitySum(bytes, b); !ok {
			return false
		}
		entries, ok = clusterSSHCapacitySum(entries, e)
		return ok
	}
	// Three owned set directories, then each independently stored archive. Do not
	// deduplicate archives or multiply by future target runtime generations.
	if !add(capacity.PostgresWorkerPeakBytes, capacity.PostgresWorkerPeakEntries) || !add(0, 3) {
		return fail()
	}
	for _, p := range images.inventory.Images() {
		if !add(uint64(p.ArchiveBytes), 1) {
			return fail()
		}
	}
	for _, p := range external.inventory.Images() {
		if !add(uint64(p.ArchiveBytes), 1) {
			return fail()
		}
	}
	pins := clusterbootstrap.K3sPins()
	for _, size := range []int64{pins.Binary.Size, pins.Installer.Size, k3s.inventory.Archive().ArchiveBytes} {
		if size < 1 || size > clusterbootstrap.MaxImageArchiveBytes || !add(uint64(size), 1) {
			return fail()
		}
	}
	capacity.WorkerScratchBytes, capacity.WorkerScratchEntries = bytes, entries
	return capacity, nil
}
