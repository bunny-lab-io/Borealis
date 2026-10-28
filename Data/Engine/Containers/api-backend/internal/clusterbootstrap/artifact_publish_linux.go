package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"golang.org/x/sys/unix"
)

// Only the journaled artifact transaction may call this after its fresh target
// preflight under both fences. Parent directories must already exist. The
// authority callback accepts the current writer's inert partial archive only;
// partial bytes never satisfy fresh admission or complete reconciliation.
func publishPrejoinArtifactFile(ctx context.Context, root *os.Root, file prejoinArtifactFile, input string, check func(context.Context) error) (result error) {
	return publishPrejoinArtifactFileAt(ctx, root, file, file.path, input, check)
}

func publishPrejoinArtifactFileAt(ctx context.Context, root *os.Root, file prejoinArtifactFile, destination, input string, check func(context.Context) error) (result error) {
	return publishPrejoinArtifactFileGuarded(ctx, root, file, destination, input, check, check)
}

// Full source-content proof belongs at publication boundaries. Per-chunk checks
// retain current authority/custody and pinned parents without rehashing the
// entire source archive/tree for every block of a multi-gigabyte artifact.
func publishPrejoinArtifactFileGuarded(ctx context.Context, root *os.Root, file prejoinArtifactFile, destination, input string, check, sourceProof func(context.Context) error) (result error) {
	if sourceProof == nil {
		return ErrImageArchive
	}
	if !digestPattern.MatchString(input) || file.write == nil || file.size < 1 || file.size > MaxImageArchiveBytes || !digestPattern.MatchString(file.digest) || file.mode != 0600 && file.mode != 0755 || imageBoundary(ctx, check) != nil {
		return ErrImageArchive
	}
	parents, err := prejoinArtifactParents(root, destination)
	if err != nil {
		return ErrImageArchive
	}
	boundary := func(context.Context) error {
		if imageBoundary(ctx, check) != nil {
			return ErrSessionAuthority
		}
		current, err := prejoinArtifactParents(root, destination)
		if err != nil || !reflect.DeepEqual(parents, current) {
			return ErrImageArchive
		}
		return nil
	}
	write := file.write
	// Source custody admits only complete installer/manifest scratch. These
	// two bounded files authenticate into memory before creating visible scratch,
	// so guarded streaming never needs to tolerate partial source-tree contents.
	if file.path == prejoinK3sInstaller || file.path == prejoinImageManifest {
		limit := int64(MaxImageInventoryBytes)
		if file.path == prejoinK3sInstaller {
			limit = 128 << 10
		}
		if file.size > limit {
			return ErrImageArchive
		}
		var buffer bytes.Buffer
		bounded := &artifactBoundedWriter{output: &buffer, remaining: file.size}
		if file.write(ctx, bounded, boundary) != nil || bounded.remaining != 0 {
			return ErrImageArchive
		}
		digest := sha256.Sum256(buffer.Bytes())
		if hex.EncodeToString(digest[:]) != file.digest {
			return ErrImageArchive
		}
		write = func(_ context.Context, out io.Writer, _ func(context.Context) error) error {
			_, err := io.Copy(out, bytes.NewReader(buffer.Bytes()))
			return err
		}
	}
	parentPath := filepath.Dir(destination)
	pending := prejoinArtifactPending(input, file.path)
	for _, path := range []string{destination, filepath.Join(parentPath, pending)} {
		if _, err := root.Lstat(path); !os.IsNotExist(err) {
			return ErrImageArchive
		}
	}
	parent, err := root.OpenRoot(parentPath)
	if err != nil {
		return ErrImageArchive
	}
	defer func() {
		if parent.Close() != nil {
			result = ErrImageArchive
		}
	}()
	directory, err := parent.OpenFile(".", os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return ErrImageArchive
	}
	defer func() {
		if directory.Close() != nil {
			result = ErrImageArchive
		}
	}()
	if boundary(ctx) != nil || sourceProof(ctx) != nil || boundary(ctx) != nil {
		return ErrImageArchive
	}
	f, err := parent.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrImageArchive
	}
	defer func() {
		if f.Close() != nil {
			result = ErrImageArchive
		}
	}()
	// Retain partial evidence on failure. No cleanup can erase an uncertain
	// durable intent or a complete retained file belonging to another producer.
	if reserveImageFile(f, file.size) != nil {
		return ErrImageArchive
	}
	h := sha256.New()
	bounded := &artifactBoundedWriter{output: io.MultiWriter(f, h), remaining: file.size}
	if write(ctx, bounded, boundary) != nil || bounded.remaining != 0 || hex.EncodeToString(h.Sum(nil)) != file.digest ||
		f.Chmod(file.mode) != nil || f.Sync() != nil || boundary(ctx) != nil {
		return ErrImageArchive
	}
	staged, err := f.Stat()
	named, namedErr := parent.Lstat(pending)
	if err != nil || namedErr != nil || !os.SameFile(staged, named) || provePrejoinArtifactFile(ctx, root, filepath.Join(parentPath, pending), file) != nil {
		return ErrImageArchive
	}
	if sourceProof(ctx) != nil || boundary(ctx) != nil {
		return ErrImageArchive
	}
	if unix.Renameat2(int(directory.Fd()), pending, int(directory.Fd()), filepath.Base(destination), unix.RENAME_NOREPLACE) != nil || directory.Sync() != nil || boundary(ctx) != nil || provePrejoinArtifactFile(ctx, root, destination, file) != nil || sourceProof(ctx) != nil || boundary(ctx) != nil {
		return ErrImageArchive
	}
	return nil
}

type artifactBoundedWriter struct {
	output    io.Writer
	remaining int64
}

func (w *artifactBoundedWriter) Write(raw []byte) (int, error) {
	if int64(len(raw)) > w.remaining {
		return 0, ErrImageArchive
	}
	n, err := w.output.Write(raw)
	w.remaining -= int64(n)
	return n, err
}
