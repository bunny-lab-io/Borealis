package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"syscall"
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
