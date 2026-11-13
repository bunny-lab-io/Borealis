package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"
)

// HostPackageSet owns inert release bytes. It supplies no target compatibility,
// installation capability or complete storage budget.
type HostPackageSet struct {
	mu        sync.Mutex
	path      string
	root      *os.Root
	file      *os.File
	inventory *HostPackageInventory
	failed    bool
}

func (*HostPackageSet) String() string               { return "staged host packages [private]" }
func (*HostPackageSet) GoString() string             { return "staged host packages [private]" }
func (*HostPackageSet) MarshalJSON() ([]byte, error) { return nil, ErrImageArchive }

// Reuse existing scratch reservation and immutable-file checks. The caller owns
// source authority and cancels/joins transport; response closure is part of the
// acquisition result, including when opening returns a body with an error.
func StageHostPackages(ctx context.Context, parent string, expected Expected, inventory *HostPackageInventory,
	open func(context.Context) (io.ReadCloser, error), check func(context.Context) error) (_ *HostPackageSet, result error) {
	if expected.Validate() != nil || inventory == nil || len(inventory.raw) == 0 || open == nil ||
		inventory.wire.Repository != expected.Repository || inventory.wire.Release != expected.Release || inventory.wire.SourceSHA != expected.SourceSHA ||
		imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	asset := inventory.Archive()
	if asset.Name != HostPackageArchiveName || asset.Size <= 0 || asset.Size > MaxImageArchiveBytes {
		return nil, ErrImageArchive
	}
	directory, err := os.MkdirTemp(parent, "borealis-node-host-packages-")
	if err != nil {
		return nil, ErrImageArchive
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.Join(ErrImageArchive, os.Remove(directory))
	}
	set := &HostPackageSet{path: directory, root: root, inventory: inventory}
	defer func() {
		if result != nil {
			result = errors.Join(result, set.Close())
		}
	}()
	files, err := reserveStagedImageArchives(ctx, root, map[string]int64{asset.Name: asset.Size}, check, reserveImageFile)
	if err != nil {
		return nil, err
	}
	defer closeStagedImageArchives(files)
	file := files[asset.Name]
	input, openErr := open(ctx)
	if input == nil {
		return nil, ErrImageArchive
	}
	if openErr != nil {
		return nil, errors.Join(ErrImageArchive, input.Close())
	}
	n, copyErr := io.Copy(file, io.LimitReader(contextReader{ctx, input}, asset.Size+1))
	closeErr := input.Close()
	if copyErr != nil || closeErr != nil || n != asset.Size || imageBoundary(ctx, check) != nil ||
		!stagedImageFileMatches(root, asset.Name, file, asset.Size) || inventory.VerifyArchive(ctx, file, n) != nil || file.Sync() != nil {
		return nil, ErrImageArchive
	}
	if imageBoundary(ctx, check) != nil {
		return nil, ErrSessionAuthority
	}
	// Keep the authenticated descriptor. Later reads never reopen a path that
	// could have become a link, FIFO or replacement inode.
	set.file = file
	delete(files, asset.Name)
	return set, nil
}

// Verify before exposing any bytes, then reprove after the synchronous writer
// joins. A failed read latches failure; previously staged bytes cannot renew
// authority. Transport must cancel blocked output before releasing this owner.
func (s *HostPackageSet) WriteArchive(ctx context.Context, output io.Writer, check func(context.Context) error) (result error) {
	if s == nil || output == nil {
		return ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.failed = s.failed || result != nil }()
	if s.failed || s.root == nil || s.file == nil || s.inventory == nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	asset := s.inventory.Archive()
	file := s.file
	if !stagedImageFileMatches(s.root, asset.Name, file, asset.Size) || s.inventory.VerifyArchive(ctx, file, asset.Size) != nil || imageBoundary(ctx, check) != nil {
		return ErrImageArchive
	}
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(output, digest), contextReader{ctx, io.NewSectionReader(file, 0, asset.Size)})
	if err != nil || n != asset.Size || hex.EncodeToString(digest.Sum(nil)) != asset.SHA256 || !stagedImageFileMatches(s.root, asset.Name, file, asset.Size) ||
		s.inventory.VerifyArchive(ctx, file, asset.Size) != nil || imageBoundary(ctx, check) != nil {
		return ErrImageArchive
	}
	return nil
}

func (s *HostPackageSet) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = true
	if s.root == nil {
		return nil
	}
	var fileErr error
	if s.file != nil {
		fileErr = s.file.Close()
		s.file = nil
	}
	// Remove only the fixed archive through the retained directory descriptor.
	// A renamed scratch directory cannot redirect this removal into a new one.
	removeErr := s.root.Remove(HostPackageArchiveName)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	// Path identity is only an observation, not an atomic removal fence. Never
	// recurse through this path: even replacement after Lstat must preserve its
	// contents. Unexpected children or changed identity leave scratch retained.
	held, heldErr := s.root.Stat(".")
	current, currentErr := os.Lstat(s.path)
	if heldErr != nil || currentErr != nil || !current.IsDir() || !os.SameFile(held, current) {
		removeErr = errors.Join(removeErr, ErrImageArchive)
	} else {
		removeErr = errors.Join(removeErr, os.Remove(s.path))
	}
	err := errors.Join(fileErr, removeErr, s.root.Close())
	s.path, s.root, s.inventory = "", nil, nil
	return err
}
