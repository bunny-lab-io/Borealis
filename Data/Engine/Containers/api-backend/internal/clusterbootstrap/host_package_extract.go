package clusterbootstrap

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// WithArchives lends exact inert .deb files to one synchronous consumer. The
// consumer must join its commands before returning and must not retain the
// path. Neither this scope nor its public pins grants installation authority.
// Caller-created cache metadata remains caller-owned; unexpected files prevent
// removing the private directory rather than enabling recursive cleanup.
//
// Materialization adds the sum of package archive sizes and one inode per
// package plus its private directory, alongside the retained source tar. This
// is content scratch demand, not a complete installation or runtime budget.
func (s *HostPackageSet) WithArchives(ctx context.Context, check func(context.Context) error,
	consume func(context.Context, string, []HostPackagePin) error) (result error) {
	if s == nil || consume == nil {
		return ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	completed := false
	defer func() { s.failed = s.failed || !completed || result != nil }()
	if s.failed || s.root == nil || s.file == nil || s.inventory == nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	asset := s.inventory.Archive()
	prove := func() error {
		if imageBoundary(ctx, check) != nil || !stagedImageFileMatches(s.root, asset.Name, s.file, asset.Size) ||
			s.inventory.VerifyArchive(ctx, s.file, asset.Size) != nil || imageBoundary(ctx, check) != nil {
			return ErrImageArchive
		}
		return nil
	}
	if prove() != nil {
		return ErrImageArchive
	}
	pins := s.inventory.Packages()
	demand := PreparationScratchDemand{Entries: uint64(len(pins)) + 1}
	for _, pin := range pins {
		demand.Bytes += uint64(pin.Size)
	}
	if checkRootScratchCapacity(s.root, demand) != nil || imageBoundary(ctx, check) != nil {
		return errScratchCapacity
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrImageArchive
	}
	name := "archives-" + hex.EncodeToString(nonce[:])
	if s.root.Mkdir(name, 0700) != nil {
		return ErrImageArchive
	}
	root, err := s.root.OpenRoot(name)
	if err != nil {
		return errors.Join(ErrImageArchive, s.root.Remove(name))
	}
	files := map[string]*os.File{}
	defer func() {
		result = errors.Join(result, closeHostPackageExtraction(s.root, name, root, files))
		if imageBoundary(ctx, check) != nil {
			result = errors.Join(result, ErrSessionAuthority)
		}
	}()
	path, err := hostPackageExtractionPath(s, name, root)
	if err != nil {
		return err
	}
	// Reserve actual final files. The existing set reservation helper is capped
	// at the image-role count; this fixed package set uses the same allocation,
	// capacity and inode checks directly without widening that unrelated bound.
	for _, pin := range pins {
		if imageBoundary(ctx, check) != nil {
			return ErrSessionAuthority
		}
		file, err := root.OpenFile(pin.File, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return ErrImageArchive
		}
		files[pin.File] = file
		if reserveImageFile(file, pin.Size) != nil || !stagedImageFileMatches(root, pin.File, file, pin.Size) || imageBoundary(ctx, check) != nil {
			return ErrImageArchive
		}
	}
	archive := tar.NewReader(contextReader{ctx, io.NewSectionReader(s.file, 0, asset.Size)})
	for _, pin := range pins {
		if imageBoundary(ctx, check) != nil {
			return ErrSessionAuthority
		}
		header, err := archive.Next()
		if err != nil || header.Name != pin.File || header.Size != pin.Size || header.Typeflag != tar.TypeReg || header.Linkname != "" || len(header.PAXRecords) != 0 {
			return ErrImageArchive
		}
		digest := sha256.New()
		file := files[pin.File]
		n, err := io.Copy(io.MultiWriter(file, digest), archive)
		if err != nil || n != pin.Size || hex.EncodeToString(digest.Sum(nil)) != pin.SHA256 || file.Sync() != nil ||
			!stagedImageFileMatches(root, pin.File, file, pin.Size) || imageBoundary(ctx, check) != nil {
			return ErrImageArchive
		}
	}
	if _, err := archive.Next(); err != io.EOF {
		return ErrImageArchive
	}
	if current, err := hostPackageExtractionPath(s, name, root); err != nil || current != path || prove() != nil || syncHostPackageExtraction(root) != nil {
		return ErrImageArchive
	}
	if err := consume(ctx, path, slices.Clone(pins)); err != nil {
		return err
	}
	if imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	// The consumer cannot replace or rewrite a package and then retain a
	// successful scope. Hash the retained descriptors and recheck every name.
	for _, pin := range pins {
		file := files[pin.File]
		digest := sha256.New()
		if !stagedImageFileMatches(root, pin.File, file, pin.Size) {
			return ErrImageArchive
		}
		n, err := io.Copy(digest, contextReader{ctx, io.NewSectionReader(file, 0, pin.Size)})
		if err != nil || n != pin.Size || hex.EncodeToString(digest.Sum(nil)) != pin.SHA256 || imageBoundary(ctx, check) != nil {
			return ErrImageArchive
		}
	}
	if current, err := hostPackageExtractionPath(s, name, root); err != nil || current != path || prove() != nil {
		return ErrImageArchive
	}
	completed = true
	return nil
}

func syncHostPackageExtraction(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func hostPackageExtractionPath(s *HostPackageSet, name string, root *os.Root) (string, error) {
	path, err := filepath.Abs(filepath.Join(s.path, name))
	if err != nil {
		return "", ErrImageArchive
	}
	for _, item := range []struct {
		held *os.Root
		path string
	}{{s.root, filepath.Dir(path)}, {root, path}} {
		held, heldErr := item.held.Stat(".")
		current, currentErr := os.Lstat(item.path)
		if heldErr != nil || currentErr != nil || !current.IsDir() || current.Mode().Perm() != 0700 || !os.SameFile(held, current) {
			return "", ErrImageArchive
		}
	}
	return path, nil
}

func closeHostPackageExtraction(parent *os.Root, name string, root *os.Root, files map[string]*os.File) error {
	var result error
	for name, file := range files {
		held, heldErr := file.Stat()
		named, namedErr := root.Lstat(name)
		if heldErr != nil || namedErr != nil || !named.Mode().IsRegular() || !os.SameFile(held, named) {
			result = errors.Join(result, ErrImageArchive)
		} else {
			result = errors.Join(result, root.Remove(name))
		}
		result = errors.Join(result, file.Close())
	}
	held, heldErr := root.Stat(".")
	named, namedErr := parent.Lstat(name)
	if heldErr != nil || namedErr != nil || !named.IsDir() || !os.SameFile(held, named) {
		result = errors.Join(result, ErrImageArchive)
	} else {
		// Empty-only removal preserves unknown children and replacement paths.
		result = errors.Join(result, parent.Remove(name))
	}
	return errors.Join(result, root.Close())
}
