package clusterbootstrap

import (
	"context"
	"io"
)

// Logical content bounds for one measured image, not disk availability or a
// transferable readiness grant. Target allocation/inode reserves are added by
// its filesystem budgeter. Worker peak is separate from target disks and PGDATA.
type PostgresImageDemand struct {
	Reference                          string
	IncomingBytes, IncomingEntries     uint64
	RuntimeBytes, RuntimeEntries       uint64
	WorkerPeakBytes, WorkerPeakEntries uint64
}

// StorageDemand requires a still-owned archive, rehashes it under current
// authority and serializes with cleanup. Proof() alone cannot grant capacity.
func (v *PostgresImage) StorageDemand(ctx context.Context, check func(context.Context) error) (PostgresImageDemand, error) {
	if v == nil || v.set == nil {
		return PostgresImageDemand{}, ErrImageArchive
	}
	v.set.mu.Lock()
	defer v.set.mu.Unlock()
	if err := v.set.writeArchive(ctx, v.proof.Reference, io.Discard, check); err != nil {
		return PostgresImageDemand{}, err
	}
	p := v.proof
	d := PostgresImageDemand{
		Reference:     p.Reference,
		IncomingBytes: uint64(p.ArchiveBytes), IncomingEntries: 1,
		RuntimeBytes: uint64(2*p.ArchiveBytes + p.ContentBytes), RuntimeEntries: uint64(2 + p.ContentEntries),
		WorkerPeakBytes: uint64(p.ArchiveBytes + p.ContentBytes), WorkerPeakEntries: uint64(p.ContentEntries + 2),
	}
	// Count repeated expanded layers independently, even when content is shared.
	for _, layer := range p.Layers {
		d.RuntimeBytes += uint64(layer.TarBytes)
		d.RuntimeEntries += uint64(layer.Entries)
	}
	return d, nil
}
