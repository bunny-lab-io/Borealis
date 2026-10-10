package main

import "borealis/api-backend/internal/clusterbootstrap"

// One preparation attempt retains its owned bootstrap bundle, all application
// and external archives, and the PostgreSQL acquisition's content+archive peak.
// No extracted image layers or target/PGDATA charge belongs on the API worker.
// K3s is metadata-only here; its future bulk staging must extend this contract.
func clusterSSHPreparationWorkerCapacity(bundle clusterbootstrap.PreparationScratchDemand, images clusterSSHImageInventory, external clusterSSHExternalInventory, capacity clusterSSHPreparedImageCapacity) (clusterSSHPreparedImageCapacity, error) {
	fail := func() (clusterSSHPreparedImageCapacity, error) {
		return clusterSSHPreparedImageCapacity{}, clusterbootstrap.ErrImageArchive
	}
	if images.inventory == nil || external.inventory == nil || images.expected != external.expected || images.expected.Validate() != nil ||
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
	// Two owned set directories, then each independently stored archive. Do not
	// deduplicate archives or multiply by future target runtime generations.
	if !add(capacity.PostgresWorkerPeakBytes, capacity.PostgresWorkerPeakEntries) || !add(0, 2) {
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
	capacity.WorkerScratchBytes, capacity.WorkerScratchEntries = bytes, entries
	return capacity, nil
}
