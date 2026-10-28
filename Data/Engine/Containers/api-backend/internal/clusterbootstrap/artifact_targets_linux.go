package clusterbootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
)

type prejoinArtifactTargets struct{ system, source *os.Root }

func (t prejoinArtifactTargets) destination(file prejoinArtifactFile) (*os.Root, string) {
	if strings.HasPrefix(file.path, "opt/Borealis/") {
		return t.source, strings.TrimPrefix(file.path, "opt/Borealis/")
	}
	return t.system, file.path
}

func artifactPathAbsent(root *os.Root, path string) bool {
	_, err := root.Lstat(path)
	return os.IsNotExist(err)
}

// No prior K3s tree, binary or either generated source file can enter a fresh
// publication. Existing private/source/system parents must already be safe.
func (t prejoinArtifactTargets) fresh(ctx context.Context, plan prejoinArtifactPlan, input string, check func(context.Context) error) error {
	if t.system == nil || t.source == nil || len(plan.files) != 37 || !digestPattern.MatchString(plan.digest) || !digestPattern.MatchString(input) || imageBoundary(ctx, check) != nil {
		return ErrImageArchive
	}
	for _, path := range []string{"var/lib/observation", "usr/local/bin/observation"} {
		if _, err := prejoinArtifactParents(t.system, path); err != nil {
			return ErrImageArchive
		}
	}
	if !artifactPathAbsent(t.system, "var/lib/rancher") {
		if _, err := prejoinArtifactParents(t.system, "var/lib/rancher/observation"); err != nil {
			return ErrImageArchive
		}
	}
	if _, err := prejoinArtifactParents(t.source, "Engine/Deploy/observation"); err != nil {
		return ErrImageArchive
	}
	if !artifactPathAbsent(t.system, "var/lib/rancher/k3s") {
		return ErrImageArchive
	}
	for _, file := range plan.files {
		root, path := t.destination(file)
		if !artifactPathAbsent(root, path) || !artifactPathAbsent(root, filepath.Join(filepath.Dir(path), prejoinArtifactPending(input, file.path))) {
			return ErrImageArchive
		}
	}
	if artifactScratchAbsent(t.system, "usr/local/bin") != nil || artifactScratchAbsent(t.source, "Engine/Deploy") != nil {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

func artifactScratchAbsent(root *os.Root, path string) error {
	if _, err := prejoinArtifactParents(root, filepath.Join(path, "observation")); err != nil {
		return ErrImageArchive
	}
	dir, err := root.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return ErrImageArchive
	}
	entries, readErr := dir.ReadDir(4097)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) > 4096 {
		return ErrImageArchive
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".borealis-artifact-") {
			return ErrImageArchive
		}
	}
	return nil
}

// Every created directory belongs to this fresh, journaled publication. Existing
// /var/lib/rancher metadata is preserved; no permission repair or runtime starts.
func (t prejoinArtifactTargets) directories(ctx context.Context, check func(context.Context) error) error {
	base, err := prejoinArtifactParents(t.system, "var/lib/observation")
	if err != nil {
		return ErrImageArchive
	}
	for _, path := range []string{"var/lib/rancher", "var/lib/rancher/k3s", "var/lib/rancher/k3s/agent", "var/lib/rancher/k3s/agent/images"} {
		if imageBoundary(ctx, check) != nil {
			return ErrSessionAuthority
		}
		current, err := prejoinArtifactParents(t.system, filepath.Join(filepath.Dir(path), "observation"))
		if err != nil {
			return ErrImageArchive
		}
		for name, identity := range base {
			if current[name] != identity {
				return ErrImageArchive
			}
		}
		if path == "var/lib/rancher" && !artifactPathAbsent(t.system, path) {
			base, err = prejoinArtifactParents(t.system, filepath.Join(path, "observation"))
			if err != nil {
				return ErrImageArchive
			}
			continue
		}
		if t.system.Mkdir(path, 0700) != nil || t.system.Chmod(path, 0700) != nil {
			return ErrImageArchive
		}
		parent, err := t.system.OpenFile(filepath.Dir(path), os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return ErrImageArchive
		}
		syncErr, closeErr := parent.Sync(), parent.Close()
		if syncErr != nil || closeErr != nil {
			return ErrImageArchive
		}
		base, err = prejoinArtifactParents(t.system, filepath.Join(path, "observation"))
		if err != nil || imageBoundary(ctx, check) != nil {
			return ErrImageArchive
		}
	}
	return nil
}

func (t prejoinArtifactTargets) publish(ctx context.Context, plan prejoinArtifactPlan, input string, check func(context.Context) error) error {
	return t.publishWithSource(ctx, plan, input, check, check)
}

func (t prejoinArtifactTargets) publishWithSource(ctx context.Context, plan prejoinArtifactPlan, input string, check, sourceProof func(context.Context) error) error {
	if imageBoundary(ctx, sourceProof) != nil || t.fresh(ctx, plan, input, check) != nil || t.directories(ctx, check) != nil {
		return ErrImageArchive
	}
	for _, file := range plan.files {
		root, path := t.destination(file)
		if imageBoundary(ctx, check) != nil || publishPrejoinArtifactFileGuarded(ctx, root, file, path, input, check, sourceProof) != nil {
			return ErrImageArchive
		}
	}
	return t.prove(ctx, plan, input, check)
}

func (t prejoinArtifactTargets) prove(ctx context.Context, plan prejoinArtifactPlan, input string, check func(context.Context) error) error {
	return t.provePhase(ctx, plan, input, check, false)
}

// Running K3s owns its datastore/payload/agent tree. All staged files and the
// archive-only images directory remain exact; native running proof owns the
// service/process/CA/Node/member boundary. Earlier callers never opt into this.
func (t prejoinArtifactTargets) proveRunning(ctx context.Context, plan prejoinArtifactPlan, input string, check func(context.Context) error) error {
	return t.provePhase(ctx, plan, input, check, true)
}

func (t prejoinArtifactTargets) provePhase(ctx context.Context, plan prejoinArtifactPlan, input string, check func(context.Context) error, running bool) error {
	if t.system == nil || t.source == nil || len(plan.files) != 37 || !digestPattern.MatchString(plan.digest) || !digestPattern.MatchString(input) || imageBoundary(ctx, check) != nil {
		return ErrImageArchive
	}
	// Exact pre-start K3s tree: no token, datastore, extra archive or subdirectory.
	want := map[string]map[string]bool{"var/lib/rancher/k3s": {"agent": true}, "var/lib/rancher/k3s/agent": {"images": true}, "var/lib/rancher/k3s/agent/images": {}}
	if running {
		delete(want, "var/lib/rancher/k3s")
		delete(want, "var/lib/rancher/k3s/agent")
	}
	for _, file := range plan.files {
		if filepath.Dir(file.path) == "var/lib/rancher/k3s/agent/images" {
			want[filepath.Dir(file.path)][filepath.Base(file.path)] = false
		}
	}
	for path, names := range want {
		before, err := prejoinArtifactParents(t.system, filepath.Join(path, "observation"))
		if err != nil {
			return ErrImageArchive
		}
		info, err := t.system.Lstat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			return ErrImageArchive
		}
		dir, err := t.system.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return ErrImageArchive
		}
		entries, readErr := dir.ReadDir(38)
		closeErr := dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) != len(names) {
			return ErrImageArchive
		}
		for _, entry := range entries {
			directory, exists := names[entry.Name()]
			if !exists || entry.IsDir() != directory || entry.Type()&os.ModeSymlink != 0 {
				return ErrImageArchive
			}
		}
		after, err := prejoinArtifactParents(t.system, filepath.Join(path, "observation"))
		if err != nil || !reflect.DeepEqual(before, after) || imageBoundary(ctx, check) != nil {
			return ErrImageArchive
		}
	}
	for _, file := range plan.files {
		root, path := t.destination(file)
		if imageBoundary(ctx, check) != nil || provePrejoinArtifactFile(ctx, root, path, file) != nil || !artifactPathAbsent(root, filepath.Join(filepath.Dir(path), prejoinArtifactPending(input, file.path))) {
			return ErrImageArchive
		}
	}
	if artifactScratchAbsent(t.system, "usr/local/bin") != nil || artifactScratchAbsent(t.source, "Engine/Deploy") != nil {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}
