package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type sourceExecutableReader func([]byte) (int, error)

func (read sourceExecutableReader) Read(p []byte) (int, error) { return read(p) }

func TestSourceKubeletExecutableBytesAndBounds(t *testing.T) {
	data := bytes.Repeat([]byte("reviewed server"), 10000)
	digest := sha256.Sum256(data)
	for _, mode := range []string{"valid", "wrong bytes", "short", "long", "error", "no progress", "canceled", "canceled during read", "wrong role", "bad digest", "oversize pin", "empty pin", "launcher pin"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pin := clusterbootstrap.K3sAssetPin{Name: "bin/k3s", Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
			var source io.Reader = bytes.NewReader(data)
			switch mode {
			case "wrong bytes":
				changed := bytes.Clone(data)
				changed[len(changed)/2]++
				source = bytes.NewReader(changed)
			case "short":
				source = bytes.NewReader(data[:len(data)-1])
			case "long":
				source = io.MultiReader(bytes.NewReader(data), strings.NewReader("extra"))
			case "error":
				source = sourceExecutableReader(func([]byte) (int, error) { return 0, errors.New("private path") })
			case "no progress":
				source = sourceExecutableReader(func([]byte) (int, error) { return 0, nil })
			case "canceled":
				cancel()
			case "canceled during read":
				base := source
				source = sourceExecutableReader(func(p []byte) (int, error) { cancel(); return base.Read(p) })
			case "wrong role":
				pin.Name = "bin/kubectl"
			case "bad digest":
				pin.SHA256 = strings.ToUpper(pin.SHA256)
			case "oversize pin":
				pin.Size = clusterbootstrap.K3sPins().Payload.FileBytes + 1
			case "empty pin":
				pin.Size = 0
			case "launcher pin":
				pin = clusterbootstrap.K3sPins().Binary
			}
			readBytes := 0
			read := sourceExecutableReader(func(p []byte) (int, error) {
				n, err := source.Read(p)
				readBytes += n
				return n, err
			})
			err := hashSourceKubeletExecutable(ctx, read, pin)
			if mode == "valid" {
				if err != nil || readBytes != len(data) {
					t.Fatal("valid executable rejected", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("unsafe executable accepted or private error escaped", err)
			}
			if readBytes > len(data)+1 {
				t.Fatal("read exceeded pinned size plus one")
			}
		})
	}
}

func TestSourceKubeletExecutableRejectsUnreviewedProductionDescriptor(t *testing.T) {
	file, err := os.Open("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := checkSourceKubeletExecutable(context.Background(), file); err != clusterbootstrap.ErrPreparationConfig {
		t.Fatal("production observer accepted unreviewed executable")
	}
}
