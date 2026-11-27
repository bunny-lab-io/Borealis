package main

import (
	"context"
	"io"
	"os"

	"borealis/api-backend/internal/clusterbootstrap"
	"golang.org/x/sys/unix"
)

// Open only the pipe made by the root wrapper for this invocation. Waiting
// for the first byte is cancellable even if the second SSH channel never opens.
func openBootstrapInput(ctx context.Context, nonce string) (io.ReadCloser, error) {
	if _, err := clusterbootstrap.SessionPreamble(nonce); err != nil {
		return nil, err
	}
	fd, err := unix.Open("/run/borealis-ssh-input-"+nonce, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errBootstrapSource
	}
	file := os.NewFile(uintptr(fd), "preparation-input")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFIFO && st.Mode&07777 == 0600 && st.Uid == 0 && st.Nlink == 1 {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for ctx.Err() == nil {
			n, err := unix.Poll(poll, 200)
			if err == unix.EINTR {
				continue
			}
			if err != nil {
				break
			}
			if n == 0 {
				continue
			}
			if poll[0].Revents&unix.POLLIN != 0 {
				return file, nil
			}
			break
		}
	}
	file.Close()
	return nil, errBootstrapSource
}
