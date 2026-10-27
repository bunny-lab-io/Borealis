package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapHostKernelProof(t *testing.T) {
	for _, mode := range []string{"valid", "no maps", "module missing", "module unloading", "module symlink", "multipath", "unknown map", "uuid control", "uuid oversized", "escaping map", "late drift", "authority", "cancel", "nil check", "nil root"} {
		t.Run(mode, func(t *testing.T) {
			path := t.TempDir()
			for _, dir := range []string{"module/iscsi_tcp", "class/block", "devices/virtual/block/dm-0/dm"} {
				if err := os.MkdirAll(filepath.Join(path, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(name, raw string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(path, name), []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			module, uuid := "live\n", "LVM-fixture\n"
			switch mode {
			case "module unloading":
				module = "going\n"
			case "multipath":
				uuid = "mpath-fixture\n"
			case "uuid control":
				uuid = "LVM\x00fixture\n"
			case "uuid oversized":
				uuid = strings.Repeat("x", 4096) + "\n"
			}
			if mode != "module missing" && mode != "module symlink" {
				write("module/iscsi_tcp/initstate", module)
			}
			if mode == "module symlink" {
				write("module/iscsi_tcp/other", module)
				if err := os.Symlink("other", filepath.Join(path, "module/iscsi_tcp/initstate")); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "unknown map" {
				write("devices/virtual/block/dm-0/dm/uuid", uuid)
			}
			if mode != "no maps" {
				target := "../../devices/virtual/block/dm-0"
				if mode == "escaping map" {
					target = t.TempDir()
				}
				if err := os.Symlink(target, filepath.Join(path, "class/block/dm-0")); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checks := 0
			check := func(context.Context) error {
				checks++
				if checks == 5 && mode == "late drift" {
					write("devices/virtual/block/dm-0/dm/uuid", "LVM-changed\n")
				}
				if mode == "authority" && checks == 8 {
					return errors.New("private failure")
				}
				if mode == "cancel" {
					cancel()
				}
				return nil
			}
			if mode == "nil check" {
				check = nil
			}
			if mode == "nil root" {
				root = nil
			}
			err = proveBootstrapPrejoinKernelWith(ctx, root, check)
			if (err == nil) != (mode == "valid" || mode == "no maps") || err != nil && err != errBootstrapSource {
				t.Fatal("kernel proof accepted invalid state or exposed diagnostics", err)
			}
		})
	}
}
