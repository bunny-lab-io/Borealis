package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// PublishSource installs authenticated release source and inherited private
// environment together at the fixed fresh host path. It never executes source,
// starts consumers or joins membership. Caller owns current clean-host, capacity and
// preparation authority; a verified archive alone grants no write permission.
func (p *PreparationInputs) PublishSource(ctx context.Context, j *MutationJournal) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrMutationJournal
	}
	return p.publishSource(ctx, j, "/opt")
}

// ReconcileSource proves only exact source publication before subsequent host
// preparation. It cannot repair partial files, erase an intent or replay writes.
func (p *PreparationInputs) ReconcileSource(ctx context.Context, j *MutationJournal) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrMutationJournal
	}
	return p.reconcileSource(ctx, j, "/opt")
}

// Caller holds journal mutex. Stable input excludes only executor ownership;
// an expired claim may recover the same source/configuration, never substitute it.
func (p *PreparationInputs) sourcePublicationInput(j *MutationJournal) (string, error) {
	if p == nil || p.bundle == nil || p.configuration == nil || j == nil {
		return "", ErrPreparationConfig
	}
	e := p.configuration.wire.Expected
	if e.Target != j.state.Owner || e.Source != j.state.Source {
		return "", ErrSessionAuthority
	}
	for step := range j.state.Steps {
		if step != "stage_source" {
			return "", ErrMutationJournal
		}
	}
	wire := p.configuration.wire
	wire.Expected.Target = stableMutationBinding(wire.Expected.Target)
	raw, err := json.Marshal(struct {
		Domain  string                `json:"domain"`
		Archive string                `json:"archive"`
		Config  preparationConfigWire `json:"config"`
	}{"fresh-source-publication-v1", p.bundle.asset.SHA256, wire})
	if err != nil {
		return "", ErrPreparationConfig
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (p *PreparationInputs) publishSource(ctx context.Context, j *MutationJournal, parent string) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	input, err := p.sourcePublicationInput(j)
	if err != nil {
		return "", err
	}
	if err := p.Verify(ctx, p.configuration.wire.Expected, j.authority); err != nil {
		return "", err
	}
	root, err := imageInstallRoot(parent)
	if err != nil {
		return "", ErrPreparationConfig
	}
	defer root.Close()
	// Do not consume a new intent for an already occupied installation path.
	if _, err := root.Lstat("Borealis"); !errors.Is(err, os.ErrNotExist) {
		return "", ErrPreparationConfig
	}
	return j.applyLocked(ctx, "stage_source", input, func(ctx context.Context, _ *os.File) (string, error) {
		// Scratch is beside final path, so publication is atomic and cannot cross
		// filesystems. Only this call's unique scratch is removed on failure.
		pending, err := os.MkdirTemp(parent, ".borealis-source-")
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer os.RemoveAll(pending)
		archive, err := os.Open(p.bundle.ArchivePath())
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer archive.Close()
		if verifySourceArchive(ctx, archive, p.bundle.asset) != nil {
			return "", ErrPreparationConfig
		}
		manifest := &Manifest{identity: p.bundle.identity, asset: p.bundle.asset}
		_, demand, err := scanArchive(ctx, archive, manifest)
		if err != nil {
			return "", ErrPreparationConfig
		}
		environment, err := p.configuration.Environment()
		if err != nil || len(environment) > 2*MaxPreparationConfigBytes {
			return "", ErrPreparationConfig
		}
		demand.Bytes += uint64(2 * len(environment))
		demand.Entries += 4 // Engine, Deploy and two private environment files.
		directory, err := root.Open(".")
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer directory.Close()
		if checkScratchCapacity(directory, demand) != nil || j.authority(ctx) != nil {
			return "", ErrPreparationConfig
		}
		unpacked := filepath.Join(pending, "unpacked")
		if extractArchive(ctx, archive, unpacked, reserveImageFile) != nil ||
			verifyPreparationFiles(ctx, archive, unpacked) != nil ||
			seedPreparationEnvironment(ctx, filepath.Join(unpacked, "source"), environment, j.authority) != nil ||
			verifyPublishedSource(ctx, archive, filepath.Join(unpacked, "source"), environment) != nil || syncSourceTree(ctx, unpacked) != nil ||
			verifySourceArchive(ctx, archive, p.bundle.asset) != nil ||
			p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil {
			return "", ErrPreparationConfig
		}
		// RENAME_NOREPLACE also rejects an empty directory or symlink inserted
		// after the initial absence check. Never replace an existing checkout.
		from := filepath.Join(filepath.Base(pending), "unpacked", "source")
		if unix.Renameat2(int(directory.Fd()), from, int(directory.Fd()), "Borealis", unix.RENAME_NOREPLACE) != nil || directory.Sync() != nil {
			return "", ErrPreparationConfig
		}
		if verifyPublishedSource(ctx, archive, filepath.Join(parent, "Borealis"), environment) != nil || j.authority(ctx) != nil {
			return "", ErrPreparationConfig
		}
		if os.RemoveAll(pending) != nil || directory.Sync() != nil {
			return "", ErrPreparationConfig
		}
		return input, nil
	})
}

func (p *PreparationInputs) reconcileSource(ctx context.Context, j *MutationJournal, parent string) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	input, err := p.sourcePublicationInput(j)
	if err != nil {
		return "", err
	}
	if err := p.Verify(ctx, p.configuration.wire.Expected, j.authority); err != nil {
		return "", err
	}
	return j.reconcileLocked(ctx, "stage_source", input, func(ctx context.Context) (string, error) {
		root, err := imageInstallRoot(parent)
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer root.Close()
		archive, err := os.Open(p.bundle.ArchivePath())
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer archive.Close()
		environment, err := p.configuration.Environment()
		if err != nil {
			return "", ErrPreparationConfig
		}
		if verifySourceArchive(ctx, archive, p.bundle.asset) != nil ||
			verifyPublishedSource(ctx, archive, filepath.Join(parent, "Borealis"), environment) != nil ||
			verifySourceArchive(ctx, archive, p.bundle.asset) != nil ||
			p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil {
			return "", ErrPreparationConfig
		}
		return input, nil
	}, "applied")
}

func verifySourceArchive(ctx context.Context, file *os.File, asset assetIdentity) error {
	if asset.Size < 1 || asset.Size > MaxBundleBytes || !digestPattern.MatchString(asset.SHA256) ||
		!mutationFileValid(file, asset.Size) {
		return ErrPreparationConfig
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ErrPreparationConfig
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, file}, asset.Size+1))
	if err != nil || n != asset.Size || hex.EncodeToString(h.Sum(nil)) != asset.SHA256 {
		return ErrPreparationConfig
	}
	return nil
}

// Sync files before their containing directories, then the publication parent.
func syncSourceTree(ctx context.Context, path string) error {
	root, err := imageInstallRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	var directories []string
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return ErrPreparationConfig
		}
		if entry.IsDir() {
			directories = append(directories, name)
			return nil
		}
		file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return ErrPreparationConfig
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			file.Close()
			return ErrPreparationConfig
		}
		return errors.Join(file.Sync(), file.Close())
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		file, err := root.Open(directories[i])
		if err != nil {
			return ErrPreparationConfig
		}
		err = errors.Join(file.Sync(), file.Close())
		if err != nil || ctx.Err() != nil {
			return ErrPreparationConfig
		}
	}
	return nil
}

// Compare every source file, including inert Git metadata, with authenticated
// archive. No Git command executes from the installed checkout during proof.
func verifyPublishedSource(ctx context.Context, archive *os.File, path string, environment []byte) error {
	root, err := imageInstallRoot(path)
	if err != nil {
		return ErrPreparationConfig
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return ErrPreparationConfig
		}
		info, err := entry.Info()
		if err != nil {
			return ErrPreparationConfig
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() || (!info.IsDir() && (!info.Mode().IsRegular() || stat.Nlink != 1)) {
			return ErrPreparationConfig
		}
		return nil
	})
	if err != nil {
		return ErrPreparationConfig
	}
	return verifyArchiveFiles(ctx, archive, path, func(name string) (string, bool) {
		if name == "source" {
			return ".", true
		}
		return strings.TrimPrefix(name, "source/"), strings.HasPrefix(name, "source/")
	}, func(root *os.Root, actual map[string]fs.FileInfo) error {
		for name, mode := range map[string]os.FileMode{"Engine": 0755, "Engine/Deploy": 0700} {
			info, ok := actual[name]
			if !ok || !info.IsDir() || info.Mode().Perm() != mode {
				return ErrPreparationConfig
			}
			delete(actual, name)
		}
		digest := sha256.Sum256(environment)
		for _, name := range []string{"Engine/Deploy/compose.env", "Engine/Deploy/runtime.env"} {
			if _, ok := actual[name]; !ok || verifyPublishedImageFile(ctx, root, name, int64(len(environment)), hex.EncodeToString(digest[:])) != nil {
				return ErrPreparationConfig
			}
			delete(actual, name)
		}
		return nil
	})
}

// Write both parser-only environments before atomically publishing the source
// tree. No partial configuration is visible at /opt/Borealis. Existing Engine
// runtime state in an archive is rejected, not reused as an independent setup.
func seedPreparationEnvironment(ctx context.Context, path string, environment []byte, check func(context.Context) error) error {
	root, err := imageInstallRoot(path)
	if err != nil {
		return ErrPreparationConfig
	}
	defer root.Close()
	if imageBoundary(ctx, check) != nil || root.Mkdir("Engine", 0755) != nil || root.Mkdir("Engine/Deploy", 0700) != nil {
		return ErrPreparationConfig
	}
	if root.Chmod("Engine", 0755) != nil || root.Chmod("Engine/Deploy", 0700) != nil {
		return ErrPreparationConfig
	}
	for _, name := range []string{"Engine/Deploy/compose.env", "Engine/Deploy/runtime.env"} {
		if publishImageFile(ctx, root, name, func(out io.Writer) error {
			_, err := io.Copy(out, bytes.NewReader(environment))
			return err
		}, check) != nil {
			return ErrPreparationConfig
		}
	}
	return imageBoundary(ctx, check)
}
