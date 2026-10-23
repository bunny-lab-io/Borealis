package clusterbootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScratchCapacityUsesOwnedFilesystem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Rename the directory; held-root observation and subsequent allocation must
	// still describe the same inode, without resolving the old pathname again.
	if err := os.Rename(path, path+".moved"); err != nil {
		t.Fatal(err)
	}
	f, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	fs, err := readScratchFilesystemCapacity(f)
	if err != nil || fs.TotalBytes == 0 || fs.TotalInodes == 0 {
		t.Fatal("native scratch observation failed", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if checkScratchCapacity(f, PreparationScratchDemand{Bytes: 1, Entries: 1}) == nil {
		t.Fatal("closed descriptor accepted")
	}
	if checkRootScratchCapacity(root, PreparationScratchDemand{Bytes: 4096, Entries: 1}) != nil {
		t.Fatal("held scratch root lost")
	}
	if checkRootScratchCapacity(root, PreparationScratchDemand{Bytes: fs.TotalBytes, Entries: 1}) == nil {
		t.Fatal("filesystem exhaustion accepted")
	}
	entries, err := os.ReadDir(path + ".moved")
	if err != nil || len(entries) != 0 {
		t.Fatal("capacity observation created files")
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if checkRootScratchCapacity(root, PreparationScratchDemand{Bytes: 1, Entries: 1}) == nil {
		t.Fatal("closed root accepted")
	}
}
