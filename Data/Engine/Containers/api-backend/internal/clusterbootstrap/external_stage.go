package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ExternalImageSet owns a complete, privately staged external image generation.
// It exposes no filesystem paths and never imports images or extracts layers.
// Staging/transfer success grants no admission or durable preparation authority.
type ExternalImageSet struct {
	mu        sync.Mutex
	path      string
	root      *os.Root
	inventory *ExternalImageInventory
	names     map[string]string
}

func (*ExternalImageSet) String() string               { return "staged external images [private]" }
func (*ExternalImageSet) GoString() string             { return "staged external images [private]" }
func (*ExternalImageSet) MarshalJSON() ([]byte, error) { return nil, ErrImageArchive }

// StageExternalImages downloads one archive at a time into new mode0700 scratch. The
// opener must bind its IO to ctx; the caller maintains joined lease heartbeats
// during long IO. check verifies current source/cohort/claim at each boundary.
// No partial set escapes; any failure removes only this call's scratch.
func StageExternalImages(ctx context.Context, parent string, inventory *ExternalImageInventory,
	open func(context.Context, ExternalImageProof) (io.ReadCloser, error),
	check func(context.Context) error) (*ExternalImageSet, error) {
	return stageExternalImages(ctx, parent, inventory, open, check, InspectExternalImageArchive)
}

// Keep the inspector seam private: production callers cannot substitute pins.
func stageExternalImages(ctx context.Context, parent string, inventory *ExternalImageInventory,
	open func(context.Context, ExternalImageProof) (io.ReadCloser, error),
	check func(context.Context) error,
	inspect func(context.Context, io.ReaderAt, int64, string) (ExternalImageProof, error)) (_ *ExternalImageSet, result error) {
	if inventory == nil || len(inventory.raw) == 0 || open == nil || inspect == nil || check == nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	directory, err := os.MkdirTemp(parent, "borealis-node-external-images-")
	if err != nil {
		return nil, ErrImageArchive
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, ErrImageArchive
	}
	names := map[string]string{}
	for _, proof := range inventory.Images() {
		names[proof.Reference] = ExternalImageAssetName(proof.Reference)
	}
	set := &ExternalImageSet{path: directory, root: root, inventory: inventory, names: names}
	defer func() {
		if result != nil {
			_ = set.Close()
		}
	}()
	for _, proof := range inventory.Images() {
		if imageBoundary(ctx, check) != nil {
			return nil, ErrSessionAuthority
		}
		if err := set.receive(ctx, proof, open, inspect); err != nil {
			return nil, err
		}
		if imageBoundary(ctx, check) != nil {
			return nil, ErrSessionAuthority
		}
	}
	return set, nil
}

func (s *ExternalImageSet) receive(ctx context.Context, proof ExternalImageProof, open func(context.Context, ExternalImageProof) (io.ReadCloser, error), inspect func(context.Context, io.ReaderAt, int64, string) (ExternalImageProof, error)) error {
	input, err := open(ctx, proof)
	if err != nil || input == nil {
		if input != nil {
			_ = input.Close()
		}
		return ErrImageArchive
	}
	defer input.Close()
	f, err := s.root.OpenFile(s.names[proof.Reference], os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return ErrImageArchive
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(contextReader{ctx, input}, proof.ArchiveBytes+1))
	if err != nil || n != proof.ArchiveBytes || hex.EncodeToString(h.Sum(nil)) != proof.ArchiveSHA256 {
		return ErrImageArchive
	}
	actual, err := inspect(ctx, f, n, proof.Reference)
	if err != nil || s.inventory.MatchesArchive(actual) != nil || f.Sync() != nil || ctx.Err() != nil {
		return ErrImageArchive
	}
	return nil
}

// WriteArchive streams only a known reference's verified bytes to a synchronous
// destination. The receiver must keep bytes inert until the whole transfer,
// independent verification and final authority check succeed. A failed stream
// may have emitted a prefix; it is never an import receipt. Cancellation of a
// blocked writer is the transport owner's responsibility.
func (s *ExternalImageSet) WriteArchive(ctx context.Context, reference string, output io.Writer, check func(context.Context) error) error {
	if s == nil || output == nil {
		return ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeArchive(ctx, reference, output, check)
}

func (s *ExternalImageSet) writeArchive(ctx context.Context, reference string, output io.Writer, check func(context.Context) error) error {
	if s.root == nil || s.inventory == nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	var proof ExternalImageProof
	for _, value := range s.inventory.Images() {
		if value.Reference == reference {
			proof = value
			break
		}
	}
	if proof.Reference == "" {
		return ErrImageArchive
	}
	name := s.names[reference]
	before, err := s.root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || before.Size() != proof.ArchiveBytes {
		return ErrImageArchive
	}
	f, err := s.root.Open(name)
	if err != nil {
		return ErrImageArchive
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return ErrImageArchive
	}
	// The initial scan established layer structure/counts. Rehash the complete
	// immutable archive before emitting bytes, then authenticate the stream too.
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, f}, proof.ArchiveBytes+1))
	if err != nil || n != proof.ArchiveBytes || hex.EncodeToString(h.Sum(nil)) != proof.ArchiveSHA256 {
		return ErrImageArchive
	}
	if imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ErrImageArchive
	}
	h.Reset()
	n, err = io.Copy(io.MultiWriter(output, h), io.LimitReader(contextReader{ctx, f}, proof.ArchiveBytes+1))
	if err != nil || n != proof.ArchiveBytes || hex.EncodeToString(h.Sum(nil)) != proof.ArchiveSHA256 {
		return ErrImageArchive
	}
	after, err := s.root.Lstat(name)
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() || after.ModTime() != opened.ModTime() {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

func (s *ExternalImageSet) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	if removeErr := os.RemoveAll(filepath.Clean(s.path)); removeErr != nil {
		err = removeErr
	}
	s.root, s.inventory, s.path, s.names = nil, nil, "", nil
	return err
}
