package clusterbootstrap

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// PreparationInputs owns verified central scratch and an immutable private
// configuration. It authorizes no host write: the receiver remains verify-only.
// Future dispatch must independently qualify network/storage/confirmation,
// establish its host journal and install shared identity before any consumers.
type PreparationInputs struct {
	bundle        *Bundle
	configuration *PreparationConfiguration
}

func (PreparationInputs) String() string               { return "node preparation inputs [redacted]" }
func (PreparationInputs) GoString() string             { return "node preparation inputs [redacted]" }
func (PreparationInputs) MarshalJSON() ([]byte, error) { return nil, ErrPreparationConfig }

// BindPreparationInputs rechecks actual scratch bytes, not just retained
// in-memory manifest metadata. Ownership transfers only on success; the caller
// still closes bundle after any error. check must recheck exact source,
// configuration, cohort and worker claim with no DB connection retained.
func BindPreparationInputs(ctx context.Context, bundle *Bundle, config *PreparationConfiguration, check func(context.Context) error) (*PreparationInputs, error) {
	if config == nil || len(config.raw) == 0 || bundle == nil || check == nil {
		return nil, ErrPreparationConfig
	}
	expected, _, _, err := bundle.ManagerProof()
	if err != nil || expected.Repository != config.wire.Expected.Source.Repository || expected.Release != config.wire.Expected.Source.Release || expected.SourceSHA != config.wire.Expected.Source.SourceSHA {
		return nil, ErrPreparationConfig
	}
	inputs := &PreparationInputs{bundle: bundle, configuration: config}
	if err := inputs.Verify(ctx, config.wire.Expected, check); err != nil {
		return nil, err
	}
	return inputs, nil
}

// Verify is a boundary check, never a lease renewal or mutation approval.
// A successful previous call cannot authorize a later transfer after state drift.
func (p *PreparationInputs) Verify(parent context.Context, expected PreparationExpected, check func(context.Context) error) error {
	if p == nil || p.bundle == nil || p.configuration == nil || check == nil || expected.Validate() != nil || !equalPreparationExpected(expected, p.configuration.wire.Expected) {
		return ErrPreparationConfig
	}
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return ErrSessionAuthority
	}
	if validatePreparationRuntime(p.configuration.wire.Runtime) != nil || p.bundle.verifyPreparationSource(ctx, expected.K3sVersion) != nil {
		return ErrPreparationConfig
	}
	if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return ErrSessionAuthority
	}
	return nil
}

// Configuration exports private bytes only at a freshly checked boundary.
// Caller must keep them in confidential stdin/private storage, never logs,
// events, URLs, argv or process environment. No transfer is enabled here.
func (p *PreparationInputs) Configuration(ctx context.Context, expected PreparationExpected, check func(context.Context) error) ([]byte, error) {
	if err := p.Verify(ctx, expected, check); err != nil {
		return nil, err
	}
	return p.configuration.Export()
}

// SourceBundle exposes the matching central archive/helper only after a fresh
// boundary check. The eventual SSH sender must also maintain its session lease
// and independently verify copied bytes. The bundle remains owned by inputs.
func (p *PreparationInputs) SourceBundle(ctx context.Context, expected PreparationExpected, check func(context.Context) error) (*Bundle, error) {
	if err := p.Verify(ctx, expected, check); err != nil {
		return nil, err
	}
	return p.bundle, nil
}

func (p *PreparationInputs) Close() error {
	if p == nil {
		return nil
	}
	var err error
	if p.bundle != nil {
		err = p.bundle.Close()
	}
	// Drop references; Go strings and allocator copies cannot promise zeroization.
	p.bundle, p.configuration = nil, nil
	return err
}

func (b *Bundle) verifyPreparationSource(ctx context.Context, k3sVersion string) error {
	if _, _, _, err := b.ManagerProof(); err != nil || b.asset.Size < 1 || b.asset.Size > MaxBundleBytes || !digestPattern.MatchString(b.asset.SHA256) {
		return ErrPreparationConfig
	}
	rootInfo, err := os.Lstat(b.root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0o700 {
		return ErrPreparationConfig
	}
	info, err := os.Lstat(b.ArchivePath())
	if err != nil || !info.Mode().IsRegular() || info.Size() != b.asset.Size || info.Mode().Perm() != 0o600 {
		return ErrPreparationConfig
	}
	f, err := os.Open(b.ArchivePath())
	if err != nil {
		return ErrPreparationConfig
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return ErrPreparationConfig
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, f}, b.asset.Size+1))
	if err != nil || n != b.asset.Size || hex.EncodeToString(h.Sum(nil)) != b.asset.SHA256 {
		return ErrPreparationConfig
	}
	m := &Manifest{identity: b.identity, asset: b.asset}
	files, err := scanArchive(ctx, f, m)
	if err != nil || verifyPreparationFiles(ctx, f, filepath.Join(b.root, "unpacked")) != nil || verifySource(ctx, b.SourcePath(), b.identity, files, "") != nil || verifyBinary(b.ManagerPath(), b.identity) != nil {
		return ErrPreparationConfig
	}
	// Compatibility must come from the verified source archive as well. The
	// existing controller still owns publication/ancestry and rolling policies.
	manifestFile, err := os.Open(filepath.Join(b.SourcePath(), "Data/Engine/release-manifest.json"))
	if err != nil {
		return ErrPreparationConfig
	}
	raw, err := io.ReadAll(io.LimitReader(manifestFile, MaxManifestBytes+1))
	_ = manifestFile.Close()
	if err != nil || len(raw) > MaxManifestBytes {
		return ErrPreparationConfig
	}
	var manifest struct {
		ClusterCompatible      bool     `json:"cluster_compatible"`
		RequiredK3sBaseline    string   `json:"required_k3s_baseline"`
		AllowedReleaseChannels []string `json:"allowed_release_channels"`
	}
	channel := "stable"
	if strings.Contains(b.identity.Release, "-rc.") {
		channel = "qualification"
	}
	if json.Unmarshal(raw, &manifest) != nil || !manifest.ClusterCompatible || manifest.RequiredK3sBaseline != k3sVersion || !slices.Contains(manifest.AllowedReleaseChannels, channel) {
		return ErrPreparationConfig
	}
	return nil
}

// Compare every extracted file, including Git configuration, before invoking
// trusted host Git again. An added hook/alternate or replaced source/binary must
// fail even when the archive and cached identity still have the original hash.
func verifyPreparationFiles(ctx context.Context, archive *os.File, directory string) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return ErrPreparationConfig
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return ErrPreparationConfig
	}
	defer root.Close()
	actual := map[string]fs.FileInfo{}
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return ErrPreparationConfig
		}
		if name == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil || len(actual) >= MaxEntries || (!info.IsDir() && !info.Mode().IsRegular()) || info.Mode().Perm()&0o022 != 0 {
			return ErrPreparationConfig
		}
		actual[name] = info
		return nil
	})
	if err != nil {
		return ErrPreparationConfig
	}
	err = walkArchive(ctx, archive, func(h *tar.Header, input io.Reader) error {
		name, err := archiveName(h)
		if err != nil {
			return ErrPreparationConfig
		}
		info, exists := actual[name]
		if !exists {
			return ErrPreparationConfig
		}
		delete(actual, name)
		if h.Typeflag == tar.TypeDir {
			if !info.IsDir() {
				return ErrPreparationConfig
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() != h.Size || int64(info.Mode().Perm()) != h.Mode {
			return ErrPreparationConfig
		}
		file, err := root.Open(name)
		if err != nil {
			return ErrPreparationConfig
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return ErrPreparationConfig
		}
		want, got := sha256.New(), sha256.New()
		if _, err := io.Copy(want, input); err != nil {
			return ErrPreparationConfig
		}
		n, err := io.Copy(got, io.LimitReader(contextReader{ctx, file}, h.Size+1))
		if err != nil || n != h.Size || !slices.Equal(want.Sum(nil), got.Sum(nil)) {
			return ErrPreparationConfig
		}
		return nil
	})
	if err != nil || len(actual) != 0 {
		return ErrPreparationConfig
	}
	return nil
}
