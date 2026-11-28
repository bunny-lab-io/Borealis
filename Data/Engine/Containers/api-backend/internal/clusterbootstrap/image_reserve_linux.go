package clusterbootstrap

import (
	"os"
	"syscall"
)

// Allocate actual blocks in the eventual destination, never a separate promise
// file or sparse truncate. Competing acquisitions cannot spend these blocks.
// Unsupported filesystems and quota/space failures stop before bulk downloads.
func reserveImageFile(f *os.File, size int64) error {
	if f == nil || size < 1 || size > MaxImageArchiveBytes {
		return ErrImageArchive
	}
	if syscall.Fallocate(int(f.Fd()), 0, 0, size) != nil {
		return ErrImageArchive
	}
	return nil
}
