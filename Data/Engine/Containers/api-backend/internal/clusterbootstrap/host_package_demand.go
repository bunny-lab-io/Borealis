package clusterbootstrap

import "context"

// StorageDemand lends the verified package materialization charge, not installed
// package content, generated files or service/runtime growth. The native host
// action retains one tar and extracts all pins in its private cache, including
// packages already installed on the target. The cache adds empty lock/partial
// entries; their filesystem allocation is charged by the existing capacity code.
func (s *HostPackageSet) StorageDemand(ctx context.Context, expected Expected, check func(context.Context) error) (demand PreparationScratchDemand, result error) {
	if s == nil {
		return PreparationScratchDemand{}, ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.failed = s.failed || result != nil }()
	if err := s.proveDemandOwner(ctx, expected, check); err != nil {
		return PreparationScratchDemand{}, err
	}
	wire := s.inventory.wire
	asset := s.inventory.Archive()
	// Stage directory + tar + extraction directory + APT lock + partial.
	demand = PreparationScratchDemand{Bytes: uint64(asset.Size), Entries: uint64(len(wire.Packages)) + 5}
	for _, pin := range wire.Packages {
		demand.Bytes += uint64(pin.Size)
	}
	if !stagedImageFileMatches(s.root, asset.Name, s.file, asset.Size) || imageBoundary(ctx, check) != nil {
		return PreparationScratchDemand{}, ErrImageArchive
	}
	return demand, nil
}

// Caller holds the owner's mutex across this proof and demand consumption.
func (s *HostPackageSet) proveDemandOwner(ctx context.Context, expected Expected, check func(context.Context) error) error {
	if s.failed || s.root == nil || s.file == nil || s.inventory == nil || expected.Validate() != nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	wire := s.inventory.wire
	if wire.Repository != expected.Repository || wire.Release != expected.Release || wire.SourceSHA != expected.SourceSHA {
		return ErrImageArchive
	}
	asset := s.inventory.Archive()
	if !stagedImageFileMatches(s.root, asset.Name, s.file, asset.Size) || s.inventory.VerifyArchive(ctx, s.file, asset.Size) != nil || imageBoundary(ctx, check) != nil {
		return ErrImageArchive
	}
	return nil
}
