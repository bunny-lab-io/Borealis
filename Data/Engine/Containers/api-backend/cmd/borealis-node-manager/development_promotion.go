package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Pin the actual deploy directory across the promotion. The top-level checkout
// can be owned by the lab operator, but Engine/Deploy and its manifest are
// managed by the service. Never follow a redirect or write through a link.
func openPromotionDirectory(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("invalid promotion directory")
	}
	const flags = unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_RDONLY | unix.O_CLOEXEC
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return nil, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := unix.Openat(fd, component, flags, 0)
		unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	defer unix.Close(fd)
	r, err := os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil {
		return nil, err
	}
	st, err := r.Stat(".")
	if err != nil || !ownedPromotionFile(st) {
		r.Close()
		return nil, errors.New("promotion directory ownership differs")
	}
	return r, nil
}

func ownedPromotionFile(st os.FileInfo) bool {
	id, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(id.Uid) == os.Geteuid() && st.Mode().Perm()&0022 == 0
}

func readPromotionFile(r *os.Root, name string) ([]byte, error) {
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const limit = 64 << 10
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !ownedPromotionFile(st) || st.Size() < 1 || st.Size() > limit {
		return nil, errors.New("invalid serving image manifest")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) != st.Size() {
		return nil, errors.New("serving manifest changed during read")
	}
	return b, nil
}

func syncPromotionRoot(r *os.Root) error {
	d, err := r.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// The prior generation is durably retained before workloads change. Switch the
// ordinary runtime manifest only after the reconciler reports success. Replays
// of the authenticated promotion can reuse either already-committed file.
func promoteDevelopmentGeneration(ctx context.Context, deployPath, verifiedManifest string, promote func() error) error {
	d, err := openPromotionDirectory(deployPath)
	if err != nil {
		return err
	}
	defer d.Close()
	cfg, err := openPromotionDirectory(filepath.Dir(verifiedManifest))
	if err != nil {
		return err
	}
	defer cfg.Close()
	lock, err := d.Open(".")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	const active = "image-manifest.json"
	old, err := readPromotionFile(d, active)
	if err != nil {
		return err
	}
	next, err := readPromotionFile(cfg, filepath.Base(verifiedManifest))
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !bytes.Equal(old, next) {
		backup := fmt.Sprintf("image-manifest-recovery-%x.json", sha256.Sum256(old))
		if err := publishPromotionFile(cfg, backup, old, false); err != nil {
			return err
		}
	}
	if err := promote(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	current, err := readPromotionFile(d, active)
	if err != nil || !bytes.Equal(old, current) {
		return errors.New("serving image manifest changed during promotion")
	}
	if bytes.Equal(current, next) {
		return nil
	}
	return publishPromotionFile(d, active, next, true)
}

func publishPromotionFile(r *os.Root, name string, b []byte, replace bool) error {
	if !replace {
		existing, err := readPromotionFile(r, name)
		if err == nil && bytes.Equal(existing, b) {
			return syncPromotionRoot(r)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("retained recovery manifest differs")
		}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	tmp := fmt.Sprintf(".image-manifest-%x", id)
	f, err := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if replace {
		err = r.Rename(tmp, name)
	} else {
		err = r.Link(tmp, name)
	}
	if err != nil {
		return err
	}
	return syncPromotionRoot(r)
}
