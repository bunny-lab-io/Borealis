package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const k3sImageDirectory = "/var/lib/rancher/k3s/agent/images"
const nodeDeployDirectory = "/opt/Borealis/Engine/Deploy"

// PublishK3sArchives installs an already received complete image generation for
// K3s's native importer. It never builds images, starts K3s or joins membership.
// The journal must already record source, shared identity and host preparation.
// Its authority check must include current capacity and paired staging approval.
// Current bootstrap-session does not dispatch this function.
func (s *ImageSet) PublishK3sArchives(ctx context.Context, journal *MutationJournal) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrMutationJournal
	}
	return s.publishK3sArchives(ctx, journal, k3sImageDirectory, nodeDeployDirectory)
}

// Only tests can replace fixed installed paths. Existing directories must be
// private-owner controlled; preparation owns their creation and qualification.
func imageInstallRoot(directory string) (*os.Root, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil || absolute != filepath.Clean(directory) {
		return nil, ErrImageArchive
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || resolved != absolute {
		return nil, ErrImageArchive
	}
	for p := absolute; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			return nil, ErrImageArchive
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		// Shared sticky /tmp is permitted only as an ancestor of private test
		// directories, never either fixed production destination.
		if !ok || (int(stat.Uid) != os.Geteuid() && stat.Uid != 0) || info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return nil, ErrImageArchive
		}
		if p == "/" {
			break
		}
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, ErrImageArchive
	}
	info, err := root.Stat(".")
	if err != nil || info.Mode().Perm()&0o022 != 0 {
		root.Close()
		return nil, ErrImageArchive
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		root.Close()
		return nil, ErrImageArchive
	}
	return root, nil
}

func (s *ImageSet) imageInstallReady(j *MutationJournal) bool {
	if s.inventory == nil || s.root == nil || j == nil || j.state.Source.Repository != s.inventory.wire.Repository ||
		j.state.Source.Release != s.inventory.wire.Release || j.state.Source.SourceSHA != s.inventory.wire.SourceSHA {
		return false
	}
	for _, step := range []string{"stage_source", "install_identity", "prepare_host"} {
		if j.state.Steps[step].State != "applied" {
			return false
		}
	}
	_, joined := j.state.Steps["join_cluster"]
	return !joined
}

func imagePinnedArchive(proof ImageArchiveProof) string {
	repository, _, _ := strings.Cut(proof.Image, ":")
	h := sha256.Sum256([]byte(repository + "@" + proof.ManifestDigest))
	return "borealis-" + proof.Role + "-" + hex.EncodeToString(h[:])[:16] + ".tar"
}

func (s *ImageSet) publishK3sArchives(ctx context.Context, j *MutationJournal, imagesPath, deployPath string) (string, error) {
	if s == nil || j == nil {
		return "", ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j.mu.Lock()
	ready := s.imageInstallReady(j) && j.authority(ctx) == nil
	j.mu.Unlock()
	if !ready {
		return "", ErrSessionAuthority
	}
	images, err := imageInstallRoot(imagesPath)
	if err != nil {
		return "", err
	}
	defer images.Close()
	deploy, err := imageInstallRoot(deployPath)
	if err != nil {
		return "", err
	}
	defer deploy.Close()
	manifest, err := s.runtimeManifest()
	if err != nil {
		return "", err
	}
	input := sha256.Sum256(s.inventory.raw)
	result := sha256.Sum256(manifest)
	return j.Apply(ctx, "stage_images", hex.EncodeToString(input[:]), func(ctx context.Context, _ *os.File) (string, error) {
		if !s.imageInstallReady(j) {
			return "", ErrSessionAuthority
		}
		for _, proof := range s.inventory.Images() {
			if err := publishImageFile(ctx, images, imagePinnedArchive(proof), func(output io.Writer) error {
				return s.writeArchive(ctx, proof.Role, output, j.authority)
			}, j.authority); err != nil {
				return "", err
			}
		}
		if err := publishImageFile(ctx, deploy, "image-manifest.json", func(output io.Writer) error {
			_, err := io.Copy(output, bytes.NewReader(manifest))
			return err
		}, j.authority); err != nil {
			return "", err
		}
		return hex.EncodeToString(result[:]), nil
	})
}

// Write, sync and publish without replacing any existing name. Failure after
// the journal intent leaves outcome_unknown; finalized files stay for read-only
// recovery. Temporary files never have a recognized K3s import extension.
func publishImageFile(ctx context.Context, root *os.Root, name string, write func(io.Writer) error, check func(context.Context) error) error {
	if imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrImageArchive
	}
	pending := name + ".pending-" + hex.EncodeToString(random[:])
	f, err := root.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return ErrImageArchive
	}
	defer f.Close()
	defer root.Remove(pending)
	if write(f) != nil || f.Sync() != nil || imageBoundary(ctx, check) != nil {
		return ErrImageArchive
	}
	if root.Link(pending, name) != nil {
		return ErrImageArchive
	}
	if root.Remove(pending) != nil {
		return ErrImageArchive
	}
	directory, err := root.Open(".")
	if err != nil {
		return ErrImageArchive
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

// ReconcileK3sArchives proves file publication only. K3s image readiness and
// membership/admission require separate native evidence. No absent-state
// shortcut is provided: partial copies stay outcome_unknown for recovery.
func (s *ImageSet) ReconcileK3sArchives(ctx context.Context, j *MutationJournal) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrMutationJournal
	}
	return s.reconcileK3sArchives(ctx, j, k3sImageDirectory, nodeDeployDirectory)
}

func (s *ImageSet) reconcileK3sArchives(ctx context.Context, j *MutationJournal, imagesPath, deployPath string) (string, error) {
	if s == nil || j == nil {
		return "", ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j.mu.Lock()
	ready := s.imageInstallReady(j) && j.authority(ctx) == nil
	j.mu.Unlock()
	if !ready {
		return "", ErrSessionAuthority
	}
	images, err := imageInstallRoot(imagesPath)
	if err != nil {
		return "", err
	}
	defer images.Close()
	deploy, err := imageInstallRoot(deployPath)
	if err != nil {
		return "", err
	}
	defer deploy.Close()
	manifest, err := s.runtimeManifest()
	if err != nil {
		return "", err
	}
	input, result := sha256.Sum256(s.inventory.raw), sha256.Sum256(manifest)
	return j.Reconcile(ctx, "stage_images", hex.EncodeToString(input[:]), func(ctx context.Context) (string, error) {
		for _, proof := range s.inventory.Images() {
			if verifyPublishedImageFile(ctx, images, imagePinnedArchive(proof), proof.ArchiveBytes, proof.ArchiveSHA256) != nil {
				return "", ErrImageArchive
			}
		}
		if verifyPublishedImageFile(ctx, deploy, "image-manifest.json", int64(len(manifest)), hex.EncodeToString(result[:])) != nil {
			return "", ErrImageArchive
		}
		return hex.EncodeToString(result[:]), nil
	})
}

func verifyPublishedImageFile(ctx context.Context, root *os.Root, name string, size int64, digest string) error {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ErrImageArchive
	}
	defer f.Close()
	if !mutationFileValid(f, size) {
		return ErrImageArchive
	}
	before, err := f.Stat()
	if err != nil {
		return ErrImageArchive
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, f}, size+1))
	if err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != digest || ctx.Err() != nil {
		return ErrImageArchive
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || !mutationFileValid(f, size) {
		return ErrImageArchive
	}
	return nil
}
