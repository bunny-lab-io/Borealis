package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
)

func prejoinArtifactPending(input, path string) string {
	digest := sha256.Sum256([]byte("prejoin-artifact-file-v1\x00" + input + "\x00" + path))
	return ".borealis-artifact-" + hex.EncodeToString(digest[:])
}

// Exact mode/owner/link/content proof for one immutable publication. This does
// not infer executable readiness or authorize replacement of an existing path.
func provePrejoinArtifactFile(ctx context.Context, root *os.Root, path string, file prejoinArtifactFile) error {
	if root == nil || ctx.Err() != nil || filepath.IsAbs(path) || filepath.Clean(path) != path || file.size < 1 || file.size > MaxImageArchiveBytes || !digestPattern.MatchString(file.digest) || file.mode != 0600 && file.mode != 0755 {
		return ErrImageArchive
	}
	parents, err := prejoinArtifactParents(root, path)
	if err != nil {
		return ErrImageArchive
	}
	metadata := func(info os.FileInfo) bool {
		if info == nil || !info.Mode().IsRegular() || info.Mode() != file.mode || info.Size() != file.size {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		return ok && int(st.Uid) == os.Geteuid() && int(st.Gid) == os.Getegid() && st.Nlink == 1
	}
	before, err := root.Lstat(path)
	if err != nil || !metadata(before) {
		return ErrImageArchive
	}
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ErrImageArchive
	}
	defer f.Close()
	held, err := f.Stat()
	if err != nil || !metadata(held) || !os.SameFile(before, held) {
		return ErrImageArchive
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, f}, file.size+1))
	if err != nil || n != file.size || hex.EncodeToString(h.Sum(nil)) != file.digest || ctx.Err() != nil {
		return ErrImageArchive
	}
	after, err := root.Lstat(path)
	end, statErr := f.Stat()
	if err != nil || statErr != nil || !metadata(after) || !metadata(end) || !os.SameFile(held, after) || !held.ModTime().Equal(after.ModTime()) || !held.ModTime().Equal(end.ModTime()) {
		return ErrImageArchive
	}
	current, err := prejoinArtifactParents(root, path)
	if err != nil || !reflect.DeepEqual(parents, current) || ctx.Err() != nil {
		return ErrImageArchive
	}
	return nil
}

type prejoinArtifactDirectory struct {
	device, inode uint64
	mode          os.FileMode
	uid, gid      uint32
}

func prejoinArtifactParents(root *os.Root, path string) (map[string]prejoinArtifactDirectory, error) {
	if root == nil || path == "." || filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, "../") {
		return nil, ErrImageArchive
	}
	held, err := root.Stat(".")
	named, namedErr := os.Lstat(root.Name())
	if err != nil || namedErr != nil || !named.IsDir() || !os.SameFile(held, named) {
		return nil, ErrImageArchive
	}
	result := map[string]prejoinArtifactDirectory{}
	current := "."
	parts := append([]string{"."}, strings.Split(filepath.Dir(path), "/")...)
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return nil, ErrImageArchive
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(st.Uid) != os.Geteuid() || int(st.Gid) != os.Getegid() {
			return nil, ErrImageArchive
		}
		result[current] = prejoinArtifactDirectory{uint64(st.Dev), uint64(st.Ino), info.Mode(), st.Uid, st.Gid}
	}
	return result, nil
}
