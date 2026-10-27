package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapDockerScratchFreshBoundary(t *testing.T) {
	for _, mode := range []string{"absent", "ordinary key", "key scratch", "source scratch", "linked directory", "unsafe directory", "too many", "authority", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root, _ := bootstrapDockerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pending := ""
			switch mode {
			case "ordinary key":
				bootstrapDockerWrite(t, root, "etc/apt/keyrings/ordinary.asc", "existing package key")
			case "key scratch":
				pending = "etc/apt/keyrings/.borealis-docker-retained"
			case "source scratch":
				pending = "etc/apt/sources.list.d/.borealis-docker-retained"
			case "linked directory":
				if err := root.Symlink(t.TempDir(), "etc/apt/keyrings"); err != nil {
					t.Fatal(err)
				}
			case "unsafe directory":
				bootstrapDockerWrite(t, root, "etc/apt/keyrings/ordinary.asc", "key")
				if err := root.Chmod("etc/apt/keyrings", 0777); err != nil {
					t.Fatal(err)
				}
			case "too many":
				for i := 0; i < 129; i++ {
					bootstrapDockerWrite(t, root, fmt.Sprintf("etc/apt/keyrings/key%d", i), "key")
				}
			case "cancel":
				cancel()
			}
			if pending != "" {
				bootstrapDockerWrite(t, root, pending, "retain partial bytes")
			}
			check := func(context.Context) error {
				if mode == "authority" {
					return errBootstrapSource
				}
				return nil
			}
			err := proveBootstrapDockerScratchAbsent(ctx, root, check)
			if (err == nil) != (mode == "absent" || mode == "ordinary key") {
				t.Fatal("unsafe fresh boundary", err)
			}
			if pending != "" {
				raw, err := os.ReadFile(filepath.Join(root.Name(), pending))
				if err != nil || string(raw) != "retain partial bytes" {
					t.Fatal("scratch removed or changed", err)
				}
			}
		})
	}
}
