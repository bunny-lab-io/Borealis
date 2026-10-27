package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bootstrapDockerKeyFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/docker-archive-key.asc")
	if err != nil || !validBootstrapDockerKey(raw) {
		t.Fatal("reviewed Docker public-key fixture missing or changed", err)
	}
	return raw
}

func bootstrapDockerFixture(t *testing.T) (*os.Root, func(context.Context, string) ([]byte, error)) {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	bootstrapDockerWrite(t, root, "etc/os-release", "ID=ubuntu\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\nUBUNTU_CODENAME=noble\n")
	bootstrapDockerWrite(t, root, "etc/apt/sources.list.d/ubuntu.sources", "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu\nSuites: noble noble-updates\nComponents: main\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n")
	return root, func(_ context.Context, probe string) ([]byte, error) {
		switch probe {
		case "architecture":
			return []byte("amd64\n"), nil
		case "packages":
			return []byte("apt\tamd64\tinstall ok installed\t2.7.14\nbase-files\tamd64\tinstall ok installed\t13ubuntu10\ndpkg\tamd64\tinstall ok installed\t1.22.6\n"), nil
		case "docker.service", "docker.socket", "containerd.service":
			return []byte("LoadState=not-found\nActiveState=inactive\nSubState=dead\nNeedDaemonReload=no\nJob=\n"), nil
		default:
			return nil, errors.New("unexpected probe")
		}
	}
}

func bootstrapDockerWrite(t *testing.T, root *os.Root, path, text string) {
	t.Helper()
	path = filepath.Join(root.Name(), path)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapDockerRepositoryPublication(t *testing.T) {
	root, read := bootstrapDockerFixture(t)
	expected, key := bootstrapHostFilesFixture(), bootstrapDockerKeyFixture(t)
	check := func(context.Context) error { return nil }
	before, err := observeBootstrapDockerHost(context.Background(), root, nil, check, read)
	if err != nil {
		t.Fatal(err)
	}
	downloads := 0
	download := func(context.Context) ([]byte, error) { downloads++; return key, nil }
	if err := installBootstrapDockerRepositoryWith(context.Background(), root, expected, check, read, download); err != nil {
		t.Fatal(err)
	}
	files, err := bootstrapDockerRepositoryFiles(expected, "noble", key)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if proveBootstrapHostFile(root, file) != nil {
			t.Fatal("published bytes/metadata differ", file.path)
		}
		if !bootstrapDockerPathAbsent(root, filepath.Join(filepath.Dir(file.path), file.pending)) {
			t.Fatal("owned scratch remains")
		}
	}
	after, err := observeBootstrapDockerHost(context.Background(), root, files, check, read)
	if err != nil || before.suite != after.suite || before.probes["packages"] != after.probes["packages"] {
		t.Fatal("independent proof failed", err)
	}
	if installBootstrapDockerRepositoryWith(context.Background(), root, expected, check, read, download) == nil || downloads != 1 {
		t.Fatal("fresh publication replayed or adopted existing files")
	}
	// The two-file contract matches the existing installer's heredoc. Never
	// execute installer startup, APT or its curl/systemctl commands in this test.
	installer, err := os.ReadFile("../../../../../../Engine.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, body, found := strings.Cut(string(installer), "  cat > \"${temp_file}\" <<EOF\nTypes: deb\n")
	body, _, end := strings.Cut(body, "\nEOF")
	want := strings.NewReplacer("${repo_os}", "ubuntu", "${codename}", "noble", "${arch}", "amd64").Replace("Types: deb\n" + body + "\n")
	if !found || !end || string(files[1].data) != want {
		t.Fatal("repository contract diverged from installer")
	}
	first := bytes.Clone(files[0].data)
	key[0] ^= 1
	if !bytes.Equal(files[0].data, first) {
		t.Fatal("key aliases download buffer")
	}
	expected.Target.Generation++
	expected.Target.OperationAttempt++
	takeover, err := bootstrapDockerRepositoryFiles(expected, "noble", first)
	if err != nil || takeover[0].pending != files[0].pending {
		t.Fatal("takeover lost stable scratch identity")
	}
	expected.Target.TargetID = "99999999-9999-4999-8999-999999999999"
	foreign, err := bootstrapDockerRepositoryFiles(expected, "noble", first)
	if err != nil || foreign[0].pending == files[0].pending {
		t.Fatal("scratch ignored target identity")
	}
}

func TestBootstrapDockerRepositoryRejectsBeforeWrites(t *testing.T) {
	for _, mode := range []string{"docker state", "containerd config", "socket", "binary", "existing key", "legacy key", "existing source", "duplicate source", "linked source", "hardlinked source", "unsafe source", "unsafe parent", "scratch collision", "installed Docker", "residual Docker", "pending package", "foreign arch", "loaded unit", "probe error", "invalid key", "download error", "source drift during download", "package drift", "cancel", "authority", "missing sources", "bad OS", "nil download"} {
		t.Run(mode, func(t *testing.T) {
			root, originalRead := bootstrapDockerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected, key := bootstrapHostFilesFixture(), bootstrapDockerKeyFixture(t)
			files, _ := bootstrapDockerRepositoryFiles(expected, "noble", key)
			check := func(context.Context) error { return nil }
			read := originalRead
			download := func(context.Context) ([]byte, error) { return key, nil }
			switch mode {
			case "docker state":
				bootstrapDockerWrite(t, root, "var/lib/docker/retained", "preserve")
			case "containerd config":
				bootstrapDockerWrite(t, root, "etc/containerd/config.toml", "preserve")
			case "socket":
				bootstrapDockerWrite(t, root, "run/docker.sock", "preserve")
			case "binary":
				bootstrapDockerWrite(t, root, "usr/local/bin/docker", "preserve")
			case "existing key":
				bootstrapDockerWrite(t, root, files[0].path, string(key))
			case "legacy key":
				bootstrapDockerWrite(t, root, "etc/apt/keyrings/docker.gpg", "preserve")
			case "existing source":
				bootstrapDockerWrite(t, root, files[1].path, string(files[1].data))
			case "duplicate source":
				bootstrapDockerWrite(t, root, "etc/apt/sources.list.d/other.list", "deb https://download.docker.com/linux/ubuntu noble stable\n")
			case "linked source", "hardlinked source":
				path := filepath.Join(root.Name(), "etc/apt/sources.list.d/alias.list")
				var err error
				if mode == "linked source" {
					err = os.Symlink("ubuntu.sources", path)
				} else {
					err = os.Link(filepath.Join(root.Name(), "etc/apt/sources.list.d/ubuntu.sources"), path)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "unsafe source", "unsafe parent":
				path := "etc/apt/sources.list.d/ubuntu.sources"
				if mode == "unsafe parent" {
					path = "etc/apt"
				}
				if err := root.Chmod(path, 0777); err != nil {
					t.Fatal(err)
				}
			case "scratch collision":
				bootstrapDockerWrite(t, root, filepath.Join(filepath.Dir(files[1].path), files[1].pending), "preserve")
			case "installed Docker", "residual Docker", "pending package", "foreign arch", "loaded unit", "probe error", "package drift":
				calls := 0
				read = func(ctx context.Context, probe string) ([]byte, error) {
					raw, err := originalRead(ctx, probe)
					if probe == "packages" {
						calls++
						switch mode {
						case "installed Docker":
							raw = append(raw, []byte("docker-ce\tamd64\tinstall ok installed\t5:29.0\n")...)
						case "residual Docker":
							raw = append(raw, []byte("docker.io\tamd64\tdeinstall ok config-files\t29.0\n")...)
						case "pending package":
							raw = bytes.ReplaceAll(raw, []byte("install ok installed"), []byte("install ok unpacked"))
						case "probe error":
							return nil, errors.New("private diagnostic")
						case "package drift":
							if calls == 2 {
								raw = bytes.ReplaceAll(raw, []byte("2.7.14"), []byte("2.7.15"))
							}
						}
					}
					if mode == "foreign arch" && probe == "architecture" {
						raw = []byte("arm64\n")
					}
					if mode == "loaded unit" && probe == "docker.socket" {
						raw = bytes.ReplaceAll(raw, []byte("not-found"), []byte("loaded"))
					}
					return raw, err
				}
			case "invalid key":
				key[0] ^= 1
			case "download error":
				download = func(context.Context) ([]byte, error) { return nil, errors.New("private diagnostic") }
			case "source drift during download":
				download = func(context.Context) ([]byte, error) {
					bootstrapDockerWrite(t, root, "etc/apt/sources.list.d/changed.list", "deb http://example.invalid noble main\n")
					return key, nil
				}
			case "cancel":
				cancel()
			case "authority":
				check = func(context.Context) error { return errors.New("private diagnostic") }
			case "missing sources":
				if err := root.Remove("etc/apt/sources.list.d/ubuntu.sources"); err != nil {
					t.Fatal(err)
				}
			case "bad OS":
				bootstrapDockerWrite(t, root, "etc/os-release", "ID=debian\nVERSION_ID=13\nVERSION_CODENAME=trixie\n")
			case "nil download":
				download = nil
			}
			err := installBootstrapDockerRepositoryWith(ctx, root, expected, check, read, download)
			if err != errBootstrapSource {
				t.Fatal("unsafe write accepted or diagnostic leaked", err)
			}
			if mode != "existing key" && !bootstrapDockerPathAbsent(root, files[0].path) && mode != "unsafe parent" {
				t.Fatal("key written before failed preflight")
			}
			if mode != "existing source" && !bootstrapDockerPathAbsent(root, files[1].path) && mode != "unsafe parent" {
				t.Fatal("source written before failed preflight")
			}
		})
	}
}

func TestBootstrapDockerRepositoryRetainsPartialEffects(t *testing.T) {
	for _, mode := range []string{"authority after key", "source collision", "package change after key", "authority after source"} {
		t.Run(mode, func(t *testing.T) {
			root, originalRead := bootstrapDockerFixture(t)
			key := bootstrapDockerKeyFixture(t)
			files, _ := bootstrapDockerRepositoryFiles(bootstrapHostFilesFixture(), "noble", key)
			injected := false
			check := func(context.Context) error {
				_, err := root.Lstat(files[0].path)
				if err == nil {
					if mode == "authority after key" {
						return errors.New("revoked")
					}
					if mode == "source collision" && !injected {
						injected = true
						bootstrapDockerWrite(t, root, files[1].path, "foreign source")
					}
				}
				if mode == "authority after source" {
					if _, err := root.Lstat(files[1].path); err == nil {
						return errors.New("revoked")
					}
				}
				return nil
			}
			read := func(ctx context.Context, probe string) ([]byte, error) {
				raw, err := originalRead(ctx, probe)
				if mode == "package change after key" && probe == "packages" {
					if _, e := root.Lstat(files[0].path); e == nil {
						raw = bytes.ReplaceAll(raw, []byte("2.7.14"), []byte("2.7.15"))
					}
				}
				return raw, err
			}
			if installBootstrapDockerRepositoryWith(context.Background(), root, bootstrapHostFilesFixture(), check, read, func(context.Context) ([]byte, error) { return key, nil }) == nil {
				t.Fatal("uncertain partial state acknowledged")
			}
			if proveBootstrapHostFile(root, files[0]) != nil {
				t.Fatal("partial key removed or changed")
			}
			switch mode {
			case "source collision":
				raw, err := root.ReadFile(files[1].path)
				if err != nil || string(raw) != "foreign source" {
					t.Fatal("foreign source overwritten", err)
				}
			case "authority after source":
				if proveBootstrapHostFile(root, files[1]) != nil {
					t.Fatal("published source rolled back")
				}
			default:
				if !bootstrapDockerPathAbsent(root, files[1].path) {
					t.Fatal("continued after failed key boundary")
				}
			}
		})
	}
}

type bootstrapDockerResponseBody struct {
	io.Reader
	closed bool
}

func (b *bootstrapDockerResponseBody) Close() error { b.closed = true; return nil }

func TestBootstrapDockerKeyDownload(t *testing.T) {
	for _, mode := range []string{"valid", "redirect", "not found", "encoding", "oversize header", "oversize body", "wrong key", "truncated", "cancel", "transport error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			key := bootstrapDockerKeyFixture(t)
			body := &bootstrapDockerResponseBody{Reader: bytes.NewReader(key)}
			do := func(request *http.Request) (*http.Response, error) {
				deadline, ok := request.Context().Deadline()
				if !ok || time.Until(deadline) > 15*time.Second || request.URL.String() != bootstrapDockerKeyURL || request.Method != http.MethodGet || request.Header.Get("Accept-Encoding") != "identity" || request.Header.Get("Authorization") != "" {
					t.Fatal("unbounded or variable key request")
				}
				response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body, ContentLength: int64(len(key))}
				switch mode {
				case "redirect":
					response.StatusCode = http.StatusFound
				case "not found":
					response.StatusCode = http.StatusNotFound
				case "encoding":
					response.Header.Set("Content-Encoding", "gzip")
				case "oversize header":
					response.ContentLength = bootstrapDockerKeyLimit + 1
				case "oversize body":
					response.ContentLength = -1
					body.Reader = strings.NewReader(strings.Repeat("x", bootstrapDockerKeyLimit+1))
				case "wrong key":
					key[0] ^= 1
				case "truncated":
					body.Reader = bytes.NewReader(key[:len(key)-1])
				case "cancel":
					cancel()
				case "transport error":
					return nil, errors.New("private diagnostic")
				}
				return response, nil
			}
			raw, err := downloadBootstrapDockerKeyWith(ctx, do)
			if (err == nil) != (mode == "valid") || err != nil && (err != errBootstrapSource || raw != nil) {
				t.Fatal("unverified key or private error escaped", err)
			}
			if mode != "transport error" && !body.closed {
				t.Fatal("response leaked")
			}
		})
	}
}

func TestBootstrapDockerKeyTransportPolicy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	client := newBootstrapDockerKeyClient()
	defer client.CloseIdleConnections()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify ||
		transport.TLSClientConfig.MinVersion < tls.VersionTLS12 || transport.MaxResponseHeaderBytes != 16<<10 ||
		!transport.DisableCompression || !transport.DisableKeepAlives || client.Timeout != 15*time.Second || client.Jar != nil {
		t.Fatal("key download accepts ambient proxy/credentials or unbounded transport")
	}
	if client.CheckRedirect == nil || client.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse {
		t.Fatal("key download follows redirects")
	}
}

func TestBootstrapDockerRepositoryRejectsReplacedSourceDirectory(t *testing.T) {
	root, read := bootstrapDockerFixture(t)
	key := bootstrapDockerKeyFixture(t)
	download := func(context.Context) ([]byte, error) {
		// Preserve the source-file inode while replacing its parent directory.
		// A content-only/file-only snapshot must not accept this transition.
		old := filepath.Join(root.Name(), "etc/apt/sources.list.d")
		if err := os.Rename(old, old+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(old, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(old+".old", "ubuntu.sources"), filepath.Join(old, "ubuntu.sources")); err != nil {
			t.Fatal(err)
		}
		return key, nil
	}
	if installBootstrapDockerRepositoryWith(context.Background(), root, bootstrapHostFilesFixture(), func(context.Context) error { return nil }, read, download) != errBootstrapSource {
		t.Fatal("replaced source parent adopted")
	}
	if !bootstrapDockerPathAbsent(root, "etc/apt/keyrings/docker.asc") {
		t.Fatal("key published after parent drift")
	}
}

func TestBootstrapDockerRepositoryMissingSourceDirectory(t *testing.T) {
	root, read := bootstrapDockerFixture(t)
	raw, err := root.ReadFile("etc/apt/sources.list.d/ubuntu.sources")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapDockerWrite(t, root, "etc/apt/sources.list", string(raw))
	if root.Remove("etc/apt/sources.list.d/ubuntu.sources") != nil || root.Remove("etc/apt/sources.list.d") != nil {
		t.Fatal("fixture cleanup failed")
	}
	if err := installBootstrapDockerRepositoryWith(context.Background(), root, bootstrapHostFilesFixture(), func(context.Context) error { return nil }, read,
		func(context.Context) ([]byte, error) { return bootstrapDockerKeyFixture(t), nil }); err != nil {
		t.Fatal("safe missing parents rejected", err)
	}
}

func TestBootstrapDockerReleaseAndProbeRejections(t *testing.T) {
	valid := "ID=ubuntu\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\n"
	for _, raw := range []string{strings.Replace(valid, "24.04", "22.04", 1), strings.Replace(valid, "24.04", "24.00", 1), strings.Replace(valid, "24.04", "24.4", 1), strings.Replace(valid, "24.04", "24.+4", 1), strings.Replace(valid, "noble", "noble stable", 1), strings.Replace(valid, "noble", "$(id)", 1), valid + "ID=ubuntu\n", valid + "UBUNTU_CODENAME=other\n", strings.TrimSuffix(valid, "\n")} {
		if _, err := bootstrapDockerRelease([]byte(raw)); err == nil {
			t.Fatal("ambiguous OS/suite accepted", raw)
		}
	}
	root, read := bootstrapDockerFixture(t)
	for _, packageName := range []string{"docker.io", "docker-ce-rootless-extras", "docker-compose-v2", "docker-buildx", "podman-docker", "containerd", "containerd.io", "runc"} {
		raw, _ := read(context.Background(), "packages")
		raw = append(raw, []byte(packageName+"\tamd64\tinstall ok installed\t1.0\n")...)
		if validateBootstrapDockerProbe("packages", raw) == nil {
			t.Fatal("conflicting package accepted", packageName)
		}
	}
	raw, _ := read(context.Background(), "packages")
	for _, broken := range [][]byte{nil, []byte("\n"), append(bytes.Clone(raw), raw...), bytes.ReplaceAll(raw, []byte("install ok installed"), []byte("hold ok installed")), bytes.ReplaceAll(raw, []byte("dpkg"), []byte("other")), bytes.Repeat([]byte("x"), bootstrapDockerInventoryLimit+1)} {
		if validateBootstrapDockerProbe("packages", broken) == nil {
			t.Fatal("incomplete package inventory accepted")
		}
	}
	if err := root.Remove("etc/os-release"); err != nil {
		t.Fatal(err)
	}
	bootstrapDockerWrite(t, root, "usr/lib/os-release", valid)
	if err := root.Symlink("../usr/lib/os-release", "etc/os-release"); err != nil {
		t.Fatal(err)
	}
	if _, err := observeBootstrapDockerHost(context.Background(), root, nil, func(context.Context) error { return nil }, read); err != nil {
		t.Fatal("canonical Ubuntu os-release symlink rejected", err)
	}
}
