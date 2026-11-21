package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// This verifier consumes an already opened procfs executable descriptor. The
// trust anchor is the embedded server, not the self-extracting K3s launcher.
func checkSourceKubeletExecutable(ctx context.Context, file *os.File) error {
	pin := clusterbootstrap.K3sPins().ServerExecutable
	info, err := file.Stat()
	if err != nil || info.Size() != pin.Size {
		return clusterbootstrap.ErrPreparationConfig
	}
	return hashSourceKubeletExecutable(ctx, file, pin)
}

// One synchronous native observation owns the descriptor. Holding it prevents
// inode reuse. Every subsequent procfs open must identify the same unchanged
// executable, including ownership, mode, size, mtime and ctime.
type sourceKubeletExecutableProof struct {
	held    *os.File
	initial os.FileInfo
}

func (p *sourceKubeletExecutableProof) verify(ctx context.Context, file *os.File, hash func(context.Context, *os.File) error) error {
	before, err := file.Stat()
	if err != nil || ctx.Err() != nil || !sourceKubeletExecutableUnchanged(before, before) {
		return clusterbootstrap.ErrPreparationConfig
	}
	if p.held != nil {
		held, err := p.held.Stat()
		if err != nil || !sourceKubeletExecutableUnchanged(p.initial, held) || !sourceKubeletExecutableUnchanged(held, before) {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	if hash(ctx, file) != nil || ctx.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	p.held = os.NewFile(uintptr(fd), "observed-k3s-executable")
	after, err := p.held.Stat()
	if err != nil || !sourceKubeletExecutableUnchanged(before, after) {
		p.close()
		return clusterbootstrap.ErrPreparationConfig
	}
	p.initial = before
	return nil
}

func (p *sourceKubeletExecutableProof) close() {
	if p.held != nil {
		p.held.Close()
		p.held = nil
	}
}

func hashSourceKubeletExecutable(ctx context.Context, source io.Reader, pin clusterbootstrap.K3sAssetPin) error {
	fail := clusterbootstrap.ErrPreparationConfig
	digest, err := hex.DecodeString(pin.SHA256)
	if source == nil || ctx.Err() != nil || pin.Name != "bin/k3s" || pin.Size < 1 || pin.Size > clusterbootstrap.K3sPins().Payload.FileBytes ||
		err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != pin.SHA256 {
		return fail
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	remaining := pin.Size + 1
	for remaining > 0 {
		if ctx.Err() != nil {
			return fail
		}
		n, readErr := source.Read(buffer[:min(int64(len(buffer)), remaining)])
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
			remaining -= int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil || n == 0 {
			return fail
		}
	}
	if remaining != 1 || ctx.Err() != nil || hex.EncodeToString(hash.Sum(nil)) != pin.SHA256 {
		return fail
	}
	return nil
}

func sourceKubeletExecutableUnchanged(before, after os.FileInfo) bool {
	if before == nil || after == nil || !before.Mode().IsRegular() || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return false
	}
	a, aOK := before.Sys().(*syscall.Stat_t)
	b, bOK := after.Sys().(*syscall.Stat_t)
	return aOK && bOK && a.Uid == 0 && b.Uid == 0 && a.Mode&0o022 == 0 && a.Mode&0o111 != 0 &&
		a.Mode == b.Mode && a.Gid == b.Gid && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}
