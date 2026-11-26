package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const developmentBundleRef = "refs/heads/borealis-candidate"

// Admission to this path is through the existing authenticated node action.
// Read the administrator-retained generation. The build worker cannot change
// this directory. No network access, remote ref update or worktree checkout.
func (m *manager) verifyLocalDevelopmentRevision(ctx context.Context, release, sha string) error {
	parent := os.Getenv("BOREALIS_ENGINE_DEVELOPMENT_CANDIDATES")
	repository := envDefault("BOREALIS_ENGINE_GITHUB_REPOSITORY", "bunny-lab-io/Borealis")
	catalog, _, err := clusterbootstrap.ReadDevelopmentCatalog(parent, release, sha, repository)
	if err != nil {
		return err
	}
	upgrade, ok := catalog.Upgrade()
	if !ok {
		return errors.New("development snapshot lacks existing-member upgrade history")
	}
	head, err := runGit(ctx, m.repoRoot, "rev-parse", "HEAD")
	if err != nil || (strings.TrimSpace(head) != upgrade.BaseSHA && strings.TrimSpace(head) != sha) {
		return errors.New("development snapshot was prepared for a different baseline")
	}
	return m.importDevelopmentRevision(ctx, catalog, parent, upgrade)
}

func (m *manager) importDevelopmentRevision(ctx context.Context, catalog *clusterbootstrap.DevelopmentCatalog, parent string, upgrade clusterbootstrap.DevelopmentUpgrade) error {
	asset, ok := catalog.Asset(clusterbootstrap.DevelopmentHistoryName)
	if !ok {
		return errors.New("missing development history")
	}
	expected := catalog.Expected()
	source, err := catalog.OpenAsset(parent, clusterbootstrap.DevelopmentHistoryName)
	if err != nil {
		return err
	}
	defer source.Close()
	// Git needs a pathname. Copy the bounded, digest-verified file into
	// node-manager-owned private scratch, even when the catalog is remounted.
	private, err := os.MkdirTemp("", "borealis-development-history-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(private)
	bundle := filepath.Join(private, "history.bundle")
	dest, err := os.OpenFile(bundle, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dest, h), io.LimitReader(source, asset.Bytes+1))
	closeErr := dest.Close()
	if err != nil || closeErr != nil || n != asset.Bytes || hex.EncodeToString(h.Sum(nil)) != asset.SHA256 || ctx.Err() != nil {
		return errors.New("development history content changed")
	}
	heads, err := runGit(ctx, m.repoRoot, "bundle", "list-heads", bundle)
	if err != nil || strings.TrimSpace(heads) != expected.SourceSHA+" "+developmentBundleRef {
		return errors.New("development history references differ")
	}
	if _, err = runGit(ctx, m.repoRoot, "bundle", "verify", bundle); err != nil {
		return errors.New("local repository lacks required development history")
	}
	if _, err = runGit(ctx, m.repoRoot, "-c", "fetch.fsckObjects=true", "-c", "protocol.allow=never", "-c", "protocol.file.allow=always", "fetch", "--no-write-fetch-head", "--no-tags", "--no-recurse-submodules", bundle, developmentBundleRef); err != nil {
		return err
	}
	if _, err = runGit(ctx, m.repoRoot, "merge-base", "--is-ancestor", upgrade.BaseSHA, expected.SourceSHA); err != nil {
		return errors.New("development history is not a fast-forward")
	}
	tree, err := runGit(ctx, m.repoRoot, "rev-parse", expected.SourceSHA+"^{tree}")
	if err != nil || strings.TrimSpace(tree) != catalog.SourceTree() {
		return errors.New("development commit tree differs from bound source snapshot")
	}
	contents, err := runGit(ctx, m.repoRoot, "show", expected.SourceSHA+":Data/Engine/release-manifest.json")
	var artifact, sourceManifest map[string]any
	if err != nil || json.Unmarshal(upgrade.Compatibility, &artifact) != nil || json.Unmarshal([]byte(contents), &sourceManifest) != nil || !reflect.DeepEqual(artifact, sourceManifest) {
		return errors.New("development compatibility differs from the selected source")
	}
	return nil
}

func (m *manager) developmentMemberManifest(ctx context.Context, id, sha string, stage bool) (string, error) {
	parent := os.Getenv("BOREALIS_ENGINE_DEVELOPMENT_CANDIDATES")
	catalog, _, err := clusterbootstrap.ReadDevelopmentCatalog(parent, id, sha, envDefault("BOREALIS_ENGINE_GITHUB_REPOSITORY", "bunny-lab-io/Borealis"))
	if err != nil {
		return "", err
	}
	check := func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		head, err := runGit(ctx, m.repoRoot, "rev-parse", "HEAD")
		if err != nil || strings.TrimSpace(head) != sha {
			return errors.New("development staging source changed")
		}
		return nil
	}
	const config = "/etc/borealis"
	if stage {
		return catalog.StageMemberImages(ctx, parent, config, check)
	}
	if err := check(ctx); err != nil {
		return "", err
	}
	return catalog.MemberImageManifest(parent, config)
}
