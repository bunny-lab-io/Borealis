package clusterbootstrap

import (
	"os"
	"syscall"
)

func readScratchFilesystemCapacity(file *os.File) (scratchFilesystemCapacity, error) {
	var stat syscall.Statfs_t
	if file == nil || syscall.Fstatfs(int(file.Fd()), &stat) != nil || stat.Flags&1 != 0 || stat.Bsize <= 0 {
		return scratchFilesystemCapacity{}, errScratchCapacity
	}
	fragment := stat.Frsize
	if fragment == 0 {
		fragment = stat.Bsize
	}
	if fragment < 1 || stat.Blocks > ^uint64(0)/uint64(fragment) || stat.Bavail > stat.Blocks {
		return scratchFilesystemCapacity{}, errScratchCapacity
	}
	return scratchFilesystemCapacity{
		TotalBytes: stat.Blocks * uint64(fragment), AvailableBytes: stat.Bavail * uint64(fragment), AllocationUnit: uint64(stat.Bsize),
		TotalInodes: stat.Files, AvailableInodes: stat.Ffree,
	}, nil
}
