package clusterbootstrap

import "testing"

func TestScratchCapacityCalculatedFit(t *testing.T) {
	for _, name := range []string{"fit", "empty files", "byte boundary", "inode boundary", "no inodes", "unavailable inodes", "invalid allocation", "zero allocation", "excess bytes", "excess inodes", "allocation overflow", "sum overflow", "no entries"} {
		t.Run(name, func(t *testing.T) {
			fs := scratchFilesystemCapacity{TotalBytes: 1 << 30, AvailableBytes: 1 << 29, AllocationUnit: 4096, TotalInodes: 1000, AvailableInodes: 100}
			demand := PreparationScratchDemand{Bytes: 100000, Entries: 10}
			want := false
			switch name {
			case "fit":
				want = true
			case "empty files":
				demand.Bytes = 0
				want = true
			case "byte boundary":
				fs.AvailableBytes = demand.Bytes + demand.Entries*fs.AllocationUnit
			case "inode boundary":
				fs.AvailableInodes = demand.Entries
			case "no inodes":
				fs.TotalInodes = 0
			case "unavailable inodes":
				fs.AvailableInodes = 0
			case "invalid allocation":
				fs.AllocationUnit = 1000
			case "zero allocation":
				fs.AllocationUnit = 0
			case "excess bytes":
				fs.AvailableBytes = fs.TotalBytes + 1
			case "excess inodes":
				fs.AvailableInodes = fs.TotalInodes + 1
			case "allocation overflow":
				fs.TotalInodes = ^uint64(0)
				fs.AvailableInodes = fs.TotalInodes
				demand.Entries = fs.AvailableInodes - 1
			case "sum overflow":
				demand.Bytes = ^uint64(0) - 1
			case "no entries":
				demand.Entries = 0
			}
			if scratchCapacityFits(fs, demand) != want {
				t.Fatal("incorrect measured scratch fit", name)
			}
		})
	}
}
