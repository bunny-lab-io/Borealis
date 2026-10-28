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

// K3sSet owns inert binary, installer and image bytes. No executable path or
// installation capability escapes; callers retain joined lease/cleanup custody.
type K3sSet struct {
	mu        sync.Mutex
	path      string
	root      *os.Root
	inventory *K3sInventory
	assets    map[string]K3sAssetPin
}

func (*K3sSet) String() string               { return "staged K3s inputs [private]" }
func (*K3sSet) GoString() string             { return "staged K3s inputs [private]" }
func (*K3sSet) MarshalJSON() ([]byte, error) { return nil, ErrImageArchive }

// StageK3sInputs reserves all three final scratch files before fetching any.
// Source pins authenticate binary/installer; archive structure and expansion
// are independently measured. Neither binary nor installer is ever executed.
func StageK3sInputs(ctx context.Context, parent string, inventory *K3sInventory,
	open func(context.Context, string) (io.ReadCloser, error), check func(context.Context) error) (*K3sSet, error) {
	return stageK3sInputs(ctx, parent, inventory, open, check, K3sPins(), reserveImageFile)
}

// Pin/allocation seams are private; callers cannot replace reviewed inputs.
func stageK3sInputs(ctx context.Context, parent string, inventory *K3sInventory,
	open func(context.Context, string) (io.ReadCloser, error), check func(context.Context) error,
	pins K3sInputPins, allocate func(*os.File, int64) error) (_ *K3sSet, result error) {
	if inventory == nil || len(inventory.raw) == 0 || open == nil || allocate == nil || imageBoundary(ctx, check) != nil ||
		inventory.wire.K3sVersion != pins.Version || inventory.wire.Binary != pins.Binary || inventory.wire.Installer != pins.Installer ||
		inventory.wire.Payload != pins.Payload || inventory.wire.Archive.ArchiveSHA256 != pins.Archive.SHA256 || inventory.wire.Archive.ArchiveBytes != pins.Archive.Size {
		return nil, ErrImageArchive
	}
	assets := map[string]K3sAssetPin{K3sBinaryName: pins.Binary, K3sInstallerName: pins.Installer, K3sArchiveName: pins.Archive}
	sizes := map[string]int64{}
	for name, pin := range assets {
		if pin.Size < 1 || pin.Size > MaxImageArchiveBytes || !digestPattern.MatchString(pin.SHA256) {
			return nil, ErrImageArchive
		}
		sizes[name] = pin.Size
	}
	directory, err := os.MkdirTemp(parent, "borealis-node-k3s-inputs-")
	if err != nil {
		return nil, ErrImageArchive
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, ErrImageArchive
	}
	set := &K3sSet{path: directory, root: root, inventory: inventory, assets: assets}
	defer func() {
		if result != nil {
			_ = set.Close()
		}
	}()
	files, err := reserveStagedImageArchives(ctx, root, sizes, check, allocate)
	if err != nil {
		return nil, err
	}
	defer closeStagedImageArchives(files)
	for _, name := range []string{K3sBinaryName, K3sInstallerName, K3sArchiveName} {
		if imageBoundary(ctx, check) != nil {
			return nil, ErrSessionAuthority
		}
		f := files[name]
		if err := set.receive(ctx, name, f, open, pins); err != nil {
			return nil, err
		}
		closeErr := f.Close()
		delete(files, name)
		if closeErr != nil {
			return nil, ErrImageArchive
		}
		if imageBoundary(ctx, check) != nil {
			return nil, ErrSessionAuthority
		}
	}
	return set, nil
}

func (s *K3sSet) receive(ctx context.Context, name string, f *os.File,
	open func(context.Context, string) (io.ReadCloser, error), pins K3sInputPins) error {
	pin := s.assets[name]
	if !stagedImageFileMatches(s.root, name, f, pin.Size) {
		return ErrImageArchive
	}
	input, err := open(ctx, name)
	if err != nil || input == nil {
		if input != nil {
			_ = input.Close()
		}
		return ErrImageArchive
	}
	defer input.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(contextReader{ctx, input}, pin.Size+1))
	if err != nil || n != pin.Size || hex.EncodeToString(h.Sum(nil)) != pin.SHA256 {
		return ErrImageArchive
	}
	if name == K3sArchiveName {
		proof, err := inspectK3sArchive(ctx, f, n, pins)
		if err != nil || s.inventory.MatchesArchive(proof) != nil {
			return ErrImageArchive
		}
	}
	if f.Sync() != nil || !stagedImageFileMatches(s.root, name, f, pin.Size) || ctx.Err() != nil {
		return ErrImageArchive
	}
	return nil
}

// WriteAsset authenticates staged bytes before output and again while streaming.
// Output is inert until its receiver independently verifies the complete set.
// Transport owns cancellation of blocked IO and must join its producer.
func (s *K3sSet) WriteAsset(ctx context.Context, name string, output io.Writer, check func(context.Context) error) error {
	if s == nil || output == nil {
		return ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeAsset(ctx, name, output, check)
}

func (s *K3sSet) writeAsset(ctx context.Context, name string, output io.Writer, check func(context.Context) error) error {
	if s.root == nil || s.inventory == nil || imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	pin, exists := s.assets[name]
	if !exists {
		return ErrImageArchive
	}
	before, err := s.root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() != pin.Size {
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
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, f}, pin.Size+1))
	if err != nil || n != pin.Size || hex.EncodeToString(h.Sum(nil)) != pin.SHA256 {
		return ErrImageArchive
	}
	if imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ErrImageArchive
	}
	h.Reset()
	n, err = io.Copy(io.MultiWriter(output, h), io.LimitReader(contextReader{ctx, f}, pin.Size+1))
	if err != nil || n != pin.Size || hex.EncodeToString(h.Sum(nil)) != pin.SHA256 {
		return ErrImageArchive
	}
	after, err := s.root.Lstat(name)
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() || after.ModTime() != opened.ModTime() || after.Mode() != opened.Mode() {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

func (s *K3sSet) Close() error {
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
	s.root, s.inventory, s.assets, s.path = nil, nil, nil, ""
	return err
}
