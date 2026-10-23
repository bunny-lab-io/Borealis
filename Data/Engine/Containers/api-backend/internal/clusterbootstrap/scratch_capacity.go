package clusterbootstrap

import (
	"errors"
	"os"
)

var errScratchCapacity = errors.New("node preparation scratch filesystem capacity unavailable")

type scratchFilesystemCapacity struct {
	TotalBytes, AvailableBytes, AllocationUnit uint64
	TotalInodes, AvailableInodes               uint64
}

// This is remaining demand at one acquisition boundary. Existing owned files
// already reduce filesystem availability and must not be charged a second time.
// Match target accounting: logical bytes plus one allocation unit per entry,
// with strictly spare bytes/inodes. This is not OS/runtime headroom or a quota
// guarantee; physical destination allocation remains mandatory afterward.
func scratchCapacityFits(fs scratchFilesystemCapacity, demand PreparationScratchDemand) bool {
	const max = ^uint64(0)
	unit := fs.AllocationUnit
	if fs.TotalBytes == 0 || fs.AvailableBytes > fs.TotalBytes || unit < 512 || unit > 1<<20 || unit&(unit-1) != 0 ||
		fs.TotalInodes == 0 || fs.AvailableInodes > fs.TotalInodes || demand.Entries == 0 || demand.Entries >= fs.AvailableInodes || demand.Entries > max/unit {
		return false
	}
	reserve := demand.Entries * unit
	return demand.Bytes <= max-reserve && demand.Bytes+reserve < fs.AvailableBytes
}

func checkScratchCapacity(file *os.File, demand PreparationScratchDemand) error {
	fs, err := readScratchFilesystemCapacity(file)
	if err != nil || !scratchCapacityFits(fs, demand) {
		return errScratchCapacity
	}
	return nil
}

func checkRootScratchCapacity(root *os.Root, demand PreparationScratchDemand) error {
	if root == nil {
		return errScratchCapacity
	}
	file, err := root.Open(".")
	if err != nil {
		return errScratchCapacity
	}
	defer file.Close()
	return checkScratchCapacity(file, demand)
}
