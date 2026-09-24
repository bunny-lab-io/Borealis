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

// ImageSet owns a complete, privately staged application image generation.
// It exposes no filesystem paths and never imports images or extracts layers.
// Staging/transfer success grants no admission or durable preparation authority.
type ImageSet struct {
	mu        sync.Mutex
	path      string
	root      *os.Root
	inventory *ImageInventory
}

func (*ImageSet) String() string               { return "staged application images [private]" }
func (*ImageSet) GoString() string             { return "staged application images [private]" }
func (*ImageSet) MarshalJSON() ([]byte, error) { return nil, ErrImageArchive }

// StageImages downloads one archive at a time into new mode0700 scratch. The
// opener must bind its IO to ctx; the caller maintains joined lease heartbeats
// during long IO. check verifies current source/cohort/claim at each boundary.
// No partial set escapes; any failure removes only this call's scratch.
func StageImages(ctx context.Context, parent string, inventory *ImageInventory,
	open func(context.Context, ImageArchiveProof) (io.ReadCloser, error),
	check func(context.Context) error) (_ *ImageSet, result error) {
	if inventory == nil || len(inventory.raw) == 0 || open == nil || check == nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	directory, err := os.MkdirTemp(parent, "borealis-node-images-")
	if err != nil {
		return nil, ErrImageArchive
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, ErrImageArchive
	}
	set := &ImageSet{path: directory, root: root, inventory: inventory}
	defer func() {
		if result != nil {
			_ = set.Close()
		}
	}()
	for _, proof := range inventory.Images() {
		if imageBoundary(ctx, check) != nil {
			return nil, ErrSessionAuthority
		}
		if err := set.receive(ctx, proof, open); err != nil {
			return nil, err
		}
		if imageBoundary(ctx, check) != nil {
			return nil, ErrSessionAuthority
		}
	}
	return set, nil
}

func imageBoundary(ctx context.Context, check func(context.Context) error) error {
	if check == nil || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return ErrSessionAuthority
	}
	return nil
}

func (s *ImageSet) receive(ctx context.Context, proof ImageArchiveProof, open func(context.Context, ImageArchiveProof) (io.ReadCloser, error)) error {
	input, err := open(ctx, proof)
	if err != nil || input == nil {
		if input != nil {
			_ = input.Close()
		}
		return ErrImageArchive
	}
	defer input.Close()
	f, err := s.root.OpenFile(ImageAssetName(proof.Role), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return ErrImageArchive
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(contextReader{ctx, input}, proof.ArchiveBytes+1))
	if err != nil || n != proof.ArchiveBytes || hex.EncodeToString(h.Sum(nil)) != proof.ArchiveSHA256 {
		return ErrImageArchive
	}
	actual, err := InspectImageArchive(ctx, f, n, proof.Role, s.inventory.wire.SourceSHA)
	if err != nil || s.inventory.MatchesArchive(actual) != nil || f.Sync() != nil || ctx.Err() != nil {
		return ErrImageArchive
	}
	return nil
}

// WriteArchive streams only a known role's verified bytes to a synchronous
// destination. The receiver must keep bytes inert until the whole transfer,
// independent verification and final authority check succeed. A failed stream
// may have emitted a prefix; it is never an import receipt. Cancellation of a
// blocked writer is the transport owner's responsibility.
func (s *ImageSet) WriteArchive(ctx context.Context, role string, output io.Writer, check func(context.Context) error) error {
	if s == nil || output == nil {
		return ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil || s.inventory == nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	var proof ImageArchiveProof
	for _, value := range s.inventory.Images() {
		if value.Role == role {
			proof = value
			break
		}
	}
	if proof.Role == "" {
		return ErrImageArchive
	}
	name := ImageAssetName(role)
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

func (s *ImageSet) Close() error {
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
	s.root, s.inventory, s.path = nil, nil, ""
	return err
}
