package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapHostFileContinuation(t *testing.T) {
	expected := bootstrapHostFilesFixture()
	files, _ := bootstrapPrejoinHostFiles(expected)
	for count := 0; count <= len(files); count++ {
		for _, scratch := range []bool{false, true} {
			if scratch && count == len(files) {
				continue
			}
			t.Run(fmt.Sprintf("prefix%d/scratch%v", count, scratch), func(t *testing.T) {
				root := bootstrapHostCustodyFixture(t)
				before := map[string]os.FileInfo{}
				for index, file := range files {
					if index >= count && !(index == count && scratch) {
						break
					}
					if bootstrapHostParents(context.Background(), root, filepath.Dir(file.path), func() error { return nil }, true) != nil {
						t.Fatal("parents")
					}
					path := file.path
					if index == count {
						path = filepath.Join(filepath.Dir(file.path), file.pending)
					}
					if err := root.WriteFile(path, file.data, file.mode); err != nil {
						t.Fatal(err)
					}
					info, err := root.Lstat(path)
					if err != nil {
						t.Fatal(err)
					}
					before[file.path] = info
				}
				check := func(context.Context) error {
					return verifyBootstrapHostPaths(root, expected, "complete_prejoin_host", false)
				}
				if err := completeBootstrapHostFilesWith(context.Background(), root, expected, check); err != nil {
					t.Fatal(err)
				}
				for path, info := range before {
					after, err := root.Lstat(path)
					if err != nil || !os.SameFile(info, after) {
						t.Fatal("retained file rewritten", path, err)
					}
				}
				if proveBootstrapHostFilePrefix(root, expected, true) != nil {
					t.Fatal("missing suffix")
				}
			})
		}
	}
}

func TestBootstrapHostFileContinuationRefusesUnknown(t *testing.T) {
	for _, mode := range []string{"partial scratch", "foreign final", "hole", "double scratch", "late authority", "replaced prefix"} {
		t.Run(mode, func(t *testing.T) {
			root := bootstrapHostCustodyFixture(t)
			expected := bootstrapHostFilesFixture()
			files, _ := bootstrapPrejoinHostFiles(expected)
			for _, file := range files {
				if bootstrapHostParents(context.Background(), root, filepath.Dir(file.path), func() error { return nil }, true) != nil {
					t.Fatal("parents")
				}
			}
			first := files[0]
			path := filepath.Join(filepath.Dir(first.path), first.pending)
			data := []byte("preserve unknown scratch")
			switch mode {
			case "foreign final":
				path = first.path
			case "hole":
				path = files[2].path
				data = files[2].data
			case "double scratch":
				data = first.data
				f := files[1]
				if err := root.WriteFile(filepath.Join(filepath.Dir(f.path), f.pending), f.data, f.mode); err != nil {
					t.Fatal(err)
				}
			case "late authority", "replaced prefix":
				path = first.path
				data = first.data
			}
			if err := root.WriteFile(path, data, first.mode); err != nil {
				t.Fatal(err)
			}
			checks := 0
			replaced := false
			check := func(context.Context) error {
				checks++
				if mode == "late authority" && checks == 2 {
					return errBootstrapSource
				}
				_, pendingErr := root.Lstat(filepath.Join(filepath.Dir(files[1].path), files[1].pending))
				if mode == "replaced prefix" && pendingErr == nil && !replaced {
					replaced = true
					if err := root.Rename(first.path, first.path+".retained"); err != nil {
						t.Fatal(err)
					}
					if err := root.WriteFile(first.path, first.data, first.mode); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			if err := completeBootstrapHostFilesWith(context.Background(), root, expected, check); err == nil {
				t.Fatal("unknown state accepted", checks)
			}
			if mode != "replaced prefix" {
				got, err := root.ReadFile(path)
				if err != nil || string(got) != string(data) {
					t.Fatal("unknown evidence changed", err)
				}
			}
		})
	}
}

func TestBootstrapHostRetainedScratchLateCollision(t *testing.T) {
	for _, mode := range []string{"collision", "authority", "parent replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := bootstrapHostCustodyFixture(t)
			files, _ := bootstrapPrejoinHostFiles(bootstrapHostFilesFixture())
			file := files[3]
			parent := filepath.Dir(file.path)
			if err := bootstrapHostParents(context.Background(), root, parent, func() error { return nil }, true); err != nil {
				t.Fatal(err)
			}
			pending := filepath.Join(parent, file.pending)
			if err := root.WriteFile(pending, file.data, file.mode); err != nil {
				t.Fatal(err)
			}
			before, err := root.Lstat(pending)
			if err != nil {
				t.Fatal(err)
			}
			checks := 0
			check := func() error {
				checks++
				if checks == 2 {
					switch mode {
					case "collision":
						if err := root.WriteFile(file.path, []byte("foreign final"), file.mode); err != nil {
							t.Fatal(err)
						}
					case "authority":
						return errBootstrapSource
					case "parent replacement":
						if err := root.Rename(parent, parent+".retained"); err != nil {
							t.Fatal(err)
						}
						if err := root.Mkdir(parent, 0755); err != nil {
							t.Fatal(err)
						}
						if err := root.WriteFile(pending, file.data, file.mode); err != nil {
							t.Fatal(err)
						}
					}
				}
				return nil
			}
			if err := promoteBootstrapRetainedHostFile(root, file, check); err == nil {
				t.Fatal("late drift accepted")
			}
			retained := pending
			if mode == "parent replacement" {
				retained = filepath.Join(parent+".retained", file.pending)
			}
			after, err := root.Lstat(retained)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("retained scratch lost", err)
			}
			if mode == "collision" {
				raw, err := root.ReadFile(file.path)
				if err != nil || string(raw) != "foreign final" {
					t.Fatal("foreign destination overwritten", err)
				}
			}
		})
	}
}
