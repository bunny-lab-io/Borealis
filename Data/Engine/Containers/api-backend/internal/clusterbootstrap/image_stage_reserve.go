package clusterbootstrap

import (
	"context"
	"os"
	"path/filepath"
	"sort"
)

// Reserve the eventual archive files, not a sparse promise beside them. Keep
// descriptors until receive completes so reopening cannot substitute another
// inode. Each set's complete measured demand is owned before its first fetch.
func reserveStagedImageArchives(ctx context.Context, root *os.Root, sizes map[string]int64, check func(context.Context) error, allocate func(*os.File, int64) error) (files map[string]*os.File, result error) {
	if root == nil || allocate == nil || len(sizes) == 0 || len(sizes) > len(ImageRoles())+len(ExternalImagePins()) || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	files = map[string]*os.File{}
	defer func() {
		if result != nil {
			closeStagedImageArchives(files)
		}
	}()
	names := make([]string, 0, len(sizes))
	for name, size := range sizes {
		if name == "." || filepath.Base(name) != name || size < 1 || size > MaxImageArchiveBytes {
			return files, ErrImageArchive
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if imageBoundary(ctx, check) != nil {
			return files, ErrSessionAuthority
		}
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return files, ErrImageArchive
		}
		files[name] = f
		if allocate(f, sizes[name]) != nil || !stagedImageFileMatches(root, name, f, sizes[name]) {
			return files, ErrImageArchive
		}
		if imageBoundary(ctx, check) != nil {
			return files, ErrSessionAuthority
		}
	}
	return files, nil
}
func closeStagedImageArchives(files map[string]*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
func stagedImageFileMatches(root *os.Root, name string, f *os.File, size int64) bool {
	if root == nil || f == nil {
		return false
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 || opened.Size() != size {
		return false
	}
	named, err := root.Lstat(name)
	return err == nil && named.Mode().IsRegular() && named.Mode().Perm() == 0600 && named.Size() == size && os.SameFile(opened, named)
}
