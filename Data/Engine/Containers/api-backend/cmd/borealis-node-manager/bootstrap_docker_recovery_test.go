package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootstrapDockerRecoveryFixture(t *testing.T, phase int) (*os.Root, func(context.Context, string) ([]byte, error), []bootstrapHostFile) {
	t.Helper()
	root, original := bootstrapDockerFixture(t)
	read := func(ctx context.Context, probe string) ([]byte, error) {
		raw, err := original(ctx, probe)
		if probe == "packages" {
			for _, name := range bootstrapHostPackages[:7] {
				raw = append(raw, []byte(name+"\tamd64\tinstall ok installed\t1.0\n")...)
			}
		}
		return raw, err
	}
	files, err := bootstrapDockerRepositoryFiles(bootstrapHostFilesFixture(), "noble", bootstrapDockerKeyFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for i, file := range files {
		if err := root.MkdirAll(filepath.Dir(file.path), 0755); err != nil {
			t.Fatal(err)
		}
		if phase >= (i+1)*2 {
			bootstrapDockerWrite(t, root, file.path, string(file.data))
		} else if phase == i*2+1 {
			bootstrapDockerWrite(t, root, filepath.Join(filepath.Dir(file.path), file.pending), string(file.data))
		}
	}
	return root, read, files
}

func TestBootstrapDockerRepositoryContinuation(t *testing.T) {
	for phase := 1; phase <= 4; phase++ {
		t.Run(string(rune('0'+phase)), func(t *testing.T) {
			root, read, files := bootstrapDockerRecoveryFixture(t, phase)
			ctx, expected, check := context.Background(), bootstrapHostFilesFixture(), func(context.Context) error { return nil }
			retained := map[string]os.FileInfo{}
			for _, file := range files {
				info, err := root.Lstat(file.path)
				if os.IsNotExist(err) {
					info, err = root.Lstat(filepath.Join(filepath.Dir(file.path), file.pending))
				}
				if err == nil {
					retained[file.path] = info
				}
			}
			if _, err := observeBootstrapEarlyHost(ctx, root, expected, check, read, func(context.Context, string) error { return nil }, true); err != nil {
				t.Fatal("early repository proof", err)
			}
			if completeBootstrapDockerRepositoryWith(ctx, root, expected, check, read) != nil {
				t.Fatal("repository continuation failed")
			}
			for _, file := range files {
				if proveBootstrapHostFile(root, file) != nil || !bootstrapDockerPathAbsent(root, filepath.Join(filepath.Dir(file.path), file.pending)) {
					t.Fatal("incomplete repository")
				}
				info, err := root.Lstat(file.path)
				if err != nil || retained[file.path] != nil && !os.SameFile(retained[file.path], info) {
					t.Fatal("retained inode replaced")
				}
			}
			if completeBootstrapDockerRepositoryWith(ctx, root, expected, check, read) != nil {
				t.Fatal("complete repository must preserve files")
			}
		})
	}
}

func TestBootstrapDockerRepositoryContinuationRejects(t *testing.T) {
	for _, mode := range []string{"absent", "partial key", "foreign scratch", "extra scratch", "out of order", "both names", "partial source", "foreign source", "unsafe key", "linked key", "docker state", "docker package", "missing base", "unpacked base", "package drift", "source drift", "authority", "cancel", "generated host file", "firewall unit"} {
		t.Run(mode, func(t *testing.T) {
			root, original, files := bootstrapDockerRecoveryFixture(t, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			check := func(context.Context) error { return nil }
			read := original
			switch mode {
			case "absent":
				root.Remove(files[0].path)
			case "partial key":
				bootstrapDockerWrite(t, root, files[0].path, "partial")
			case "foreign scratch":
				root.Rename(files[0].path, filepath.Join(filepath.Dir(files[0].path), ".borealis-docker-foreign"))
			case "extra scratch":
				bootstrapDockerWrite(t, root, "etc/apt/sources.list.d/.borealis-docker-foreign", "retained")
			case "out of order":
				root.Remove(files[0].path)
				bootstrapDockerWrite(t, root, files[1].path, string(files[1].data))
			case "both names":
				bootstrapDockerWrite(t, root, filepath.Join(filepath.Dir(files[0].path), files[0].pending), string(files[0].data))
			case "partial source":
				bootstrapDockerWrite(t, root, filepath.Join(filepath.Dir(files[1].path), files[1].pending), "partial")
			case "foreign source":
				bootstrapDockerWrite(t, root, files[1].path, "foreign")
			case "unsafe key":
				root.Chmod(files[0].path, 0666)
			case "linked key":
				root.Rename(files[0].path, files[0].path+".real")
				root.Symlink("docker.asc.real", files[0].path)
			case "docker state":
				bootstrapDockerWrite(t, root, "var/lib/docker/retained", "preserve")
			case "authority":
				check = func(context.Context) error { return errBootstrapSource }
			case "cancel":
				cancel()
			case "generated host file":
				bootstrapDockerWrite(t, root, "usr/local/lib/borealis/k3s-api-firewall.sh", "retain")
			}
			calls := 0
			read = func(ctx context.Context, probe string) ([]byte, error) {
				raw, err := original(ctx, probe)
				if probe == "packages" {
					calls++
					switch mode {
					case "docker package":
						raw = append(raw, []byte("docker-ce\tamd64\tinstall ok installed\t1.0\n")...)
					case "missing base":
						raw = []byte(strings.ReplaceAll(string(raw), "python3\tamd64\tinstall ok installed\t1.0\n", ""))
					case "unpacked base":
						raw = []byte(strings.ReplaceAll(string(raw), "python3\tamd64\tinstall ok installed", "python3\tamd64\tinstall ok unpacked"))
					case "package drift":
						if calls > 1 {
							raw = []byte(strings.ReplaceAll(string(raw), "1.0", "2.0"))
						}
					case "source drift":
						if calls == 1 {
							bootstrapDockerWrite(t, root, "etc/apt/sources.list.d/ubuntu.sources", "Types: deb\nURIs: http://different.invalid/\n")
						}
					}
				}
				return raw, err
			}
			absent := func(context.Context, string) error {
				if mode == "firewall unit" {
					return errBootstrapSource
				}
				return nil
			}
			if _, err := observeBootstrapEarlyHost(ctx, root, bootstrapHostFilesFixture(), check, read, absent, true); err == nil {
				t.Fatal("unsafe repository accepted")
			}
			if !bootstrapDockerPathAbsent(root, files[1].path) && mode != "out of order" && mode != "foreign source" {
				t.Fatal("read-only proof wrote source")
			}
		})
	}
}

func TestBootstrapDockerRepositoryContinuationLateDrift(t *testing.T) {
	for _, mode := range []string{"key replacement", "parent replacement", "authority", "cancel", "collision"} {
		t.Run(mode, func(t *testing.T) {
			root, original, files := bootstrapDockerRecoveryFixture(t, 3)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pending := filepath.Join(filepath.Dir(files[1].path), files[1].pending)
			retained, err := root.Lstat(pending)
			if err != nil {
				t.Fatal(err)
			}
			lost, calls := false, 0
			check := func(context.Context) error {
				if lost {
					return errBootstrapSource
				}
				return nil
			}
			read := func(ctx context.Context, probe string) ([]byte, error) {
				raw, err := original(ctx, probe)
				if probe == "packages" {
					calls++
					// First observation completed and final key pinned. Introduce
					// late drift during the repeated pre-write native observation.
					if calls == 3 {
						switch mode {
						case "key replacement":
							if err := root.Rename(files[0].path, files[0].path+".retained"); err != nil {
								t.Fatal(err)
							}
							bootstrapDockerWrite(t, root, files[0].path, string(files[0].data))
						case "parent replacement":
							parent := filepath.Dir(files[1].path)
							if err := root.Rename(parent, parent+".retained"); err != nil {
								t.Fatal(err)
							}
							if err := root.Mkdir(parent, 0755); err != nil {
								t.Fatal(err)
							}
							bootstrapDockerWrite(t, root, pending, string(files[1].data))
						case "authority":
							lost = true
						case "cancel":
							cancel()
						case "collision":
							bootstrapDockerWrite(t, root, files[1].path, "foreign final")
						}
					}
				}
				return raw, err
			}
			if completeBootstrapDockerRepositoryWith(ctx, root, bootstrapHostFilesFixture(), check, read) == nil || calls < 3 {
				t.Fatal("late drift accepted or injection missed")
			}
			path := pending
			if mode == "parent replacement" {
				path = filepath.Join(filepath.Dir(files[1].path)+".retained", files[1].pending)
			}
			after, err := root.Lstat(path)
			if err != nil || !os.SameFile(retained, after) {
				t.Fatal("retained scratch lost", err)
			}
			if mode == "collision" {
				raw, err := root.ReadFile(files[1].path)
				if err != nil || string(raw) != "foreign final" {
					t.Fatal("foreign final overwritten")
				}
			} else if !bootstrapDockerPathAbsent(root, files[1].path) {
				t.Fatal("source published after drift")
			}
		})
	}
}
