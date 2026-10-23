package clusterbootstrap

import (
	"archive/tar"
	"context"
	"io"
	"os"
)

// Measured bytes and entries retained by this owned bootstrap bundle. Logical
// content only: filesystem allocation, runtime growth and RAM are separate.
type PreparationScratchDemand struct{ Bytes, Entries uint64 }

func (p *PreparationInputs) StorageDemand(ctx context.Context, expected PreparationExpected, check func(context.Context) error) (PreparationScratchDemand, error) {
	fail := func() (PreparationScratchDemand, error) { return PreparationScratchDemand{}, ErrPreparationConfig }
	if err := p.Verify(ctx, expected, check); err != nil {
		return PreparationScratchDemand{}, err
	}
	f, err := os.Open(p.bundle.ArchivePath())
	if err != nil {
		return fail()
	}
	defer f.Close()
	// Scratch root, archive and unpacked root; scanArchive requires each nested
	// parent to be declared, so every remaining directory/file has a tar entry.
	demand := PreparationScratchDemand{Bytes: uint64(p.bundle.asset.Size), Entries: 3}
	err = walkArchive(ctx, f, func(h *tar.Header, _ io.Reader) error {
		if _, err := archiveName(h); err != nil {
			return ErrPreparationConfig
		}
		demand.Bytes += uint64(h.Size)
		demand.Entries++
		if demand.Bytes > MaxBundleBytes+MaxExpandedBytes || demand.Entries > MaxEntries+3 {
			return ErrPreparationConfig
		}
		return nil
	})
	if err != nil || p.Verify(ctx, expected, check) != nil {
		return fail()
	}
	return demand, nil
}
