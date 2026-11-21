//go:build !linux

package clusterbootstrap

import "os"

func readScratchFilesystemCapacity(*os.File) (scratchFilesystemCapacity, error) {
	return scratchFilesystemCapacity{}, errScratchCapacity
}
