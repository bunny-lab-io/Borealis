package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var bootstrapDockerSuite = regexp.MustCompile(`^[a-z][a-z0-9]{0,31}$`)
var bootstrapDockerVersion = regexp.MustCompile(`^[0-9]{2}\.(04|10)$`)
var bootstrapDockerPackage = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,199}$`)

const bootstrapDockerInventoryLimit = 2 << 20
const bootstrapDockerAbsentProperties = "LoadState,ActiveState,SubState,NeedDaemonReload,Job"

type bootstrapDockerFileState struct {
	device, inode uint64
	mode          os.FileMode
	modified      int64
	digest        [32]byte
}

type bootstrapDockerHost struct {
	suite, osLink string
	os            bootstrapDockerFileState
	sources       map[string]bootstrapDockerFileState
	probes        map[string]string
	parents       map[string]bootstrapHostDirectoryIdentity
}

// Clean means no existing Docker/containerd installation or retained state.
// It does not authorize takeover, package removal or interrupted-write replay.
// Existing source/identity/K3s/consumer custody is still the caller's contract.
func observeBootstrapDockerHost(ctx context.Context, root *os.Root, installed []bootstrapHostFile,
	check func(context.Context) error, read func(context.Context, string) ([]byte, error)) (bootstrapDockerHost, error) {
	return observeBootstrapDockerHostPrefix(ctx, root, installed, nil, check, read)
}

// Pending files are accepted only by the independently classified repository
// continuation. Ordinary fresh publication retains its original absence rules.
func observeBootstrapDockerHostPrefix(ctx context.Context, root *os.Root, installed, pending []bootstrapHostFile,
	check func(context.Context) error, read func(context.Context, string) ([]byte, error)) (bootstrapDockerHost, error) {
	invalid := bootstrapDockerHost{}
	if root == nil || check == nil || read == nil || len(installed) > 2 || len(pending) > 1 {
		return invalid, errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	var before bootstrapDockerHost
	for pass := 0; pass < 2; pass++ {
		if boundary() != nil {
			return invalid, errBootstrapSource
		}
		current, err := observeBootstrapDockerPlatform(root)
		if err != nil {
			return invalid, errBootstrapSource
		}
		current.probes = map[string]string{}
		for _, path := range []string{"etc/docker", "etc/containerd", "var/lib/docker", "var/lib/containerd", "run/docker", "run/docker.sock", "run/containerd",
			"usr/bin/docker", "usr/bin/dockerd", "usr/bin/containerd", "usr/bin/runc", "usr/local/bin/docker", "usr/local/bin/dockerd", "usr/local/bin/containerd", "usr/local/bin/runc",
			"etc/apt/keyrings/docker.gpg", "etc/apt/trusted.gpg.d/docker.asc", "etc/apt/trusted.gpg.d/docker.gpg"} {
			if !bootstrapDockerPathAbsent(root, path) || boundary() != nil {
				return invalid, errBootstrapSource
			}
		}
		for i, path := range []string{"etc/apt/keyrings/docker.asc", "etc/apt/sources.list.d/docker.sources"} {
			if i < len(installed) {
				if installed[i].path != path || proveBootstrapHostFile(root, installed[i]) != nil {
					return invalid, errBootstrapSource
				}
			} else if !bootstrapDockerPathAbsent(root, path) {
				return invalid, errBootstrapSource
			}
		}
		for _, file := range pending {
			if proveBootstrapHostFile(root, file) != nil {
				return invalid, errBootstrapSource
			}
		}
		current.sources, err = bootstrapDockerSourcesWithPending(root, len(installed) == 2, pending)
		if err != nil {
			return invalid, errBootstrapSource
		}
		current.parents, err = bootstrapDockerParents(root, current.osLink != "")
		if err != nil {
			return invalid, errBootstrapSource
		}
		for _, probe := range []string{"architecture", "packages", "docker.service", "docker.socket", "containerd.service"} {
			if boundary() != nil {
				return invalid, errBootstrapSource
			}
			raw, err := read(ctx, probe)
			if err != nil || boundary() != nil || validateBootstrapDockerProbe(probe, raw) != nil {
				return invalid, errBootstrapSource
			}
			current.probes[probe] = string(raw)
		}
		if pass == 0 {
			before = current
		} else if !reflect.DeepEqual(before, current) {
			return invalid, errBootstrapSource
		}
	}
	if boundary() != nil {
		return invalid, errBootstrapSource
	}
	return before, nil
}

func bootstrapDockerPathAbsent(root *os.Root, path string) bool {
	absent, err := bootstrapSourcePath(root, path)
	return err == nil && absent
}

func bootstrapDockerRelease(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > 16384 || raw[len(raw)-1] != '\n' {
		return "", errBootstrapSource
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if _, duplicate := values[key]; !ok || key == "" || duplicate {
			return "", errBootstrapSource
		}
		if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) || strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
			if len(value) < 2 {
				return "", errBootstrapSource
			}
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	version := values["VERSION_ID"]
	major, minor, ok := strings.Cut(version, ".")
	year, yearErr := strconv.Atoi(major)
	month, monthErr := strconv.Atoi(minor)
	suite := values["VERSION_CODENAME"]
	if values["ID"] != "ubuntu" || !ok || !bootstrapDockerVersion.MatchString(version) || yearErr != nil || monthErr != nil ||
		(year < 24 || year == 24 && month < 4) || month != 4 && month != 10 || !bootstrapDockerSuite.MatchString(suite) ||
		(values["UBUNTU_CODENAME"] != "" && values["UBUNTU_CODENAME"] != suite) {
		return "", errBootstrapSource
	}
	return suite, nil
}

func bootstrapDockerParents(root *os.Root, osLink bool) (map[string]bootstrapHostDirectoryIdentity, error) {
	paths := []string{"etc", "etc/apt", "etc/apt/keyrings", "etc/apt/sources.list.d"}
	if osLink {
		paths = append(paths, "usr", "usr/lib")
	}
	result := map[string]bootstrapHostDirectoryIdentity{}
	for _, path := range paths {
		info, err := root.Lstat(path)
		if errors.Is(err, os.ErrNotExist) && (path == "etc/apt/keyrings" || path == "etc/apt/sources.list.d") {
			continue
		}
		if err != nil {
			return nil, errBootstrapSource
		}
		// Reuse the canonical ancestor policy; append a synthetic leaf solely
		// to request validation of the directory itself, without opening a file.
		parents, err := bootstrapHostFileAncestors(root, []bootstrapHostFile{{path: filepath.Join(path, "observation")}})
		if err != nil || !info.IsDir() {
			return nil, errBootstrapSource
		}
		result[path] = parents[path]
	}
	return result, nil
}

func readBootstrapDockerFile(root *os.Root, path string, limit int64) ([]byte, bootstrapDockerFileState, error) {
	invalid := bootstrapDockerFileState{}
	if bootstrapHostParents(context.Background(), root, filepath.Dir(path), func() error { return nil }, false) != nil {
		return nil, invalid, errBootstrapSource
	}
	before, err := root.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || before.Mode()&^os.FileMode(0777) != 0 || before.Size() > limit {
		return nil, invalid, errBootstrapSource
	}
	owner, ok := before.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() || int(owner.Gid) != os.Getegid() || owner.Nlink != 1 {
		return nil, invalid, errBootstrapSource
	}
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, invalid, errBootstrapSource
	}
	held, statErr := file.Stat()
	if statErr != nil || !os.SameFile(before, held) || !held.Mode().IsRegular() {
		file.Close()
		return nil, invalid, errBootstrapSource
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(raw)) > limit || proveBootstrapHostFile(root, bootstrapHostFile{path: path, mode: before.Mode(), data: raw}) != nil {
		return nil, invalid, errBootstrapSource
	}
	after, err := root.Lstat(path)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		return nil, invalid, errBootstrapSource
	}
	return raw, bootstrapDockerFileState{uint64(owner.Dev), uint64(owner.Ino), before.Mode(), before.ModTime().UnixNano(), sha256.Sum256(raw)}, nil
}

func bootstrapDockerSources(root *os.Root, installed bool) (map[string]bootstrapDockerFileState, error) {
	return bootstrapDockerSourcesWithPending(root, installed, nil)
}

func bootstrapDockerSourcesWithPending(root *os.Root, installed bool, pending []bootstrapHostFile) (map[string]bootstrapDockerFileState, error) {
	if _, err := bootstrapHostFileAncestors(root, []bootstrapHostFile{{path: "etc/apt/sources.list"}}); err != nil {
		return nil, errBootstrapSource
	}
	paths := []string{}
	if !bootstrapDockerPathAbsent(root, "etc/apt/sources.list") {
		paths = append(paths, "etc/apt/sources.list")
	}
	dirPath := "etc/apt/sources.list.d"
	if !bootstrapDockerPathAbsent(root, dirPath) {
		if bootstrapHostParents(context.Background(), root, dirPath, func() error { return nil }, false) != nil {
			return nil, errBootstrapSource
		}
		dir, err := root.OpenFile(dirPath, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return nil, errBootstrapSource
		}
		entries, readErr := dir.ReadDir(129)
		closeErr := dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) > 128 {
			return nil, errBootstrapSource
		}
		for _, entry := range entries {
			if installed && entry.Name() == "docker.sources" {
				continue // Caller independently proves this call's exact new file.
			}
			skip := false
			for _, file := range pending {
				if file.path == filepath.Join(dirPath, entry.Name()) && proveBootstrapHostFile(root, file) == nil {
					skip = true
				}
			}
			if !skip {
				paths = append(paths, filepath.Join(dirPath, entry.Name()))
			}
		}
	}
	result := map[string]bootstrapDockerFileState{}
	active, total := false, 0
	for _, path := range paths {
		raw, state, err := readBootstrapDockerFile(root, path, 64<<10)
		total += len(raw)
		lower := bytes.ToLower(raw)
		if err != nil || total > bootstrapDockerInventoryLimit || bytes.Contains(lower, []byte("download.docker.com")) ||
			bytes.Contains(lower, []byte("docker.asc")) || bytes.Contains(lower, []byte("docker.gpg")) {
			return nil, errBootstrapSource
		}
		if len(bytes.TrimSpace(raw)) > 0 && (strings.HasSuffix(path, ".list") || strings.HasSuffix(path, ".sources")) {
			active = true
		}
		result[path] = state
	}
	if !active {
		return nil, errBootstrapSource
	}
	return result, nil
}

func validateBootstrapDockerProbe(probe string, raw []byte) error {
	if len(raw) == 0 || len(raw) > bootstrapDockerInventoryLimit || raw[len(raw)-1] != '\n' {
		return errBootstrapSource
	}
	if probe == "architecture" {
		if string(raw) != "amd64\n" {
			return errBootstrapSource
		}
		return nil
	}
	if probe == "packages" {
		seen := map[string]bool{}
		required := map[string]bool{"apt": false, "dpkg": false, "base-files": false}
		for _, line := range strings.Split(string(raw[:len(raw)-1]), "\n") {
			f := strings.Split(line, "\t")
			if len(f) != 4 || !bootstrapDockerPackage.MatchString(f[0]) || !bootstrapDockerPackage.MatchString(f[1]) ||
				seen[f[0]+":"+f[1]] || !bootstrapHostPackageVersion.MatchString(f[3]) ||
				(f[2] != "install ok installed" && f[2] != "deinstall ok config-files") {
				return errBootstrapSource
			}
			seen[f[0]+":"+f[1]] = true
			if strings.HasPrefix(f[0], "docker") || strings.HasPrefix(f[0], "containerd") || f[0] == "runc" || f[0] == "podman-docker" {
				return errBootstrapSource
			}
			if _, ok := required[f[0]]; ok {
				if f[2] != "install ok installed" || f[1] != "amd64" && f[1] != "all" {
					return errBootstrapSource
				}
				required[f[0]] = true
			}
		}
		for _, present := range required {
			if !present {
				return errBootstrapSource
			}
		}
		return nil
	}
	if probe != "docker.service" && probe != "docker.socket" && probe != "containerd.service" {
		return errBootstrapSource
	}
	want := map[string]string{"LoadState": "not-found", "ActiveState": "inactive", "SubState": "dead", "NeedDaemonReload": "no", "Job": ""}
	for _, line := range strings.Split(string(raw[:len(raw)-1]), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if expected, exists := want[key]; !ok || !exists || value != expected {
			return errBootstrapSource
		}
		delete(want, key)
	}
	if len(want) != 0 {
		return errBootstrapSource
	}
	return nil
}

func readBootstrapDockerProbe(parent context.Context, probe string) ([]byte, error) {
	program, args := "", []string{}
	switch probe {
	case "architecture":
		program, args = "/usr/bin/dpkg", []string{"--print-architecture"}
	case "packages":
		program, args = "/usr/bin/dpkg-query", []string{"--show", "--showformat=${Package}\t${Architecture}\t${Status}\t${Version}\n"}
	case "docker.service", "docker.socket", "containerd.service":
		program, args = "/usr/bin/systemctl", []string{"show", "--no-pager", "--property=" + bootstrapDockerAbsentProperties, "--", probe}
	default:
		return nil, errBootstrapSource
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Env, command.Dir = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}, "/"
	command.WaitDelay = 250 * time.Millisecond
	output := &bootstrapDockerOutput{}
	command.Stdout, command.Stderr = output, io.Discard
	if command.Run() != nil || ctx.Err() != nil {
		return nil, errBootstrapSource
	}
	return output.Bytes(), nil
}

type bootstrapDockerOutput struct{ bytes.Buffer }

func (b *bootstrapDockerOutput) Write(raw []byte) (int, error) {
	if len(raw) > bootstrapDockerInventoryLimit-b.Len() {
		return 0, errBootstrapSource
	}
	return b.Buffer.Write(raw)
}
