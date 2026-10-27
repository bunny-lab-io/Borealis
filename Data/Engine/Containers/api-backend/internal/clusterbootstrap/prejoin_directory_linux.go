package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
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

const PreparationRuntimeID = 64646
const prejoinSecretsRelative = "Engine/Services/api-backend/secrets"

type prejoinDirectoryProof struct {
	uid, gid int
	marker   []byte
	files    map[string][]byte
}

// InstallPreparedPrejoinIdentity consumes the exact applied directory receipt
// and re-verifies actual source/configuration/empty-marker state before writing
// keys. The shared fence stays present throughout both journal commits.
func (p *PreparationInputs) InstallPreparedPrejoinIdentity(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.installPreparedPrejoinIdentity(ctx, j, install, material, "/opt")
}

func (p *PreparationInputs) installPreparedPrejoinIdentity(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent string) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	input, proof, err := p.prejoinDirectoryRequest(j, install, material, parent)
	if err != nil {
		return "", err
	}
	record := j.state.Steps["prepare_identity_root"]
	if record.State != "applied" || record.InputSHA256 != input || record.ResultSHA256 != input {
		return "", ErrMutationJournal
	}
	if prejoinDirectoryBoundary(ctx, j, install) != nil || p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil {
		return "", ErrSessionAuthority
	}
	archive, err := os.Open(p.bundle.ArchivePath())
	if err != nil {
		return "", ErrPreparationConfig
	}
	defer archive.Close()
	environment, err := p.configuration.Environment()
	if err != nil {
		return "", err
	}
	defer clear(environment)
	if verifySourceArchive(ctx, archive, p.bundle.asset) != nil || verifyPublishedSourceWithIdentityRoot(ctx, archive, filepath.Join(parent, "Borealis"), environment, &proof) != nil ||
		verifySourceArchive(ctx, archive, p.bundle.asset) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
		return "", ErrPreparationConfig
	}
	state, err := j.installIdentityLocked(ctx, install, material, true, true)
	if err != nil {
		return "", err
	}
	// The fixed-key transaction changes the permitted tree. Recheck the entire
	// installed source/configuration plus exact new keys before acknowledging it;
	// a committed journal alone cannot hide source drift during key publication.
	proof.files = material.Files()
	defer func() {
		for _, raw := range proof.files {
			clear(raw)
		}
	}()
	if len(proof.files) != 4 || verifySourceArchive(ctx, archive, p.bundle.asset) != nil ||
		verifyPublishedSourceWithIdentityRoot(ctx, archive, filepath.Join(parent, "Borealis"), environment, &proof) != nil ||
		verifySourceArchive(ctx, archive, p.bundle.asset) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
		return "", ErrMutationUnknown
	}
	return state, nil
}

// PreparePrejoinIdentityRoot atomically publishes only the fixed new identity
// directory, already containing its exact startup-blocking marker. Runtime
// account custody and legacy-consumer absence must be independently established
// through Check/Quiesced before this call. No user/group or service is created.
func (p *PreparationInputs) PreparePrejoinIdentityRoot(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.preparePrejoinIdentityRoot(ctx, j, install, material, "/opt")
}

// ReconcilePrejoinIdentityRoot proves only the exact marker-only directory and
// source/configuration tree through a distinct stopped-producer executor. It
// cannot interpret partially installed keys as a fresh directory or replay writes.
func (p *PreparationInputs) ReconcilePrejoinIdentityRoot(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.reconcilePrejoinIdentityRoot(ctx, j, install, material, "/opt")
}

// Caller holds journal mutex. Source receipt is checked against current actual
// archive/configuration inputs, never an arbitrary successful phase label.
func (p *PreparationInputs) prejoinDirectoryRequest(j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent string) (string, prejoinDirectoryProof, error) {
	return p.prejoinDirectoryRequestMode(j, install, material, parent, false)
}

func (p *PreparationInputs) prejoinDirectoryRequestMode(j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent string, installed bool) (string, prejoinDirectoryProof, error) {
	source, err := p.sourcePublicationInputForPrejoin(j, true, installed)
	if err != nil {
		return "", prejoinDirectoryProof{}, err
	}
	return prejoinDirectoryRequestFromSource(j, install, material, parent, source)
}

func prejoinDirectoryRequestFromSource(j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent, source string) (string, prejoinDirectoryProof, error) {
	invalid := prejoinDirectoryProof{}
	if j == nil || !digestPattern.MatchString(source) || install.Check == nil || install.Quiesced == nil || install.Binding.ClusterID != j.state.Owner.ClusterID ||
		install.Target.ProvisioningID != j.state.Owner.TargetID || install.Target.SSHFingerprint != j.state.Owner.HostKeyFingerprint ||
		install.Root != filepath.Join(parent, "Borealis", prejoinSecretsRelative) || install.Journal != filepath.Join(j.root.Name(), "identity-attempt") {
		return "", invalid, ErrMutationJournal
	}
	record := j.state.Steps["stage_source"]
	if record.State != "applied" || record.InputSHA256 != source || record.ResultSHA256 != source {
		return "", invalid, ErrMutationJournal
	}
	accountInput, err := runtimeIdentityInputFromSource(j, source, install.UID, install.GID)
	account := j.state.Steps["prepare_runtime_identity"]
	if err != nil || account.State != "applied" || account.InputSHA256 != accountInput || account.ResultSHA256 != accountInput {
		return "", invalid, ErrMutationJournal
	}
	marker, err := install.PrejoinPreparationMarker(material)
	if err != nil {
		return "", invalid, ErrMutationJournal
	}
	raw, err := json.Marshal(struct {
		Domain, Source, Root string
		UID, GID             int
		Marker               []byte
	}{"fresh-identity-directory-v1", source, install.Root, install.UID, install.GID, marker})
	if err != nil {
		return "", invalid, ErrMutationJournal
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), prejoinDirectoryProof{uid: install.UID, gid: install.GID, marker: marker}, nil
}

func prejoinDirectoryBoundary(ctx context.Context, j *MutationJournal, install engineidentity.Installation) error {
	if j.authority(ctx) != nil || install.Check() != nil || ctx.Err() != nil {
		return ErrSessionAuthority
	}
	if install.Quiesced() != nil || j.authority(ctx) != nil {
		return ErrSessionAuthority
	}
	return nil
}

func (p *PreparationInputs) preparePrejoinIdentityRoot(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent string) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	input, proof, err := p.prejoinDirectoryRequest(j, install, material, parent)
	if err != nil {
		return "", err
	}
	if prejoinDirectoryBoundary(ctx, j, install) != nil || p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil {
		return "", ErrSessionAuthority
	}
	archive, err := os.Open(p.bundle.ArchivePath())
	if err != nil {
		return "", ErrPreparationConfig
	}
	defer archive.Close()
	environment, err := p.configuration.Environment()
	if err != nil {
		return "", err
	}
	defer clear(environment)
	path := filepath.Join(parent, "Borealis")
	if verifySourceArchive(ctx, archive, p.bundle.asset) != nil || verifyPublishedSource(ctx, archive, path, environment) != nil {
		return "", ErrPreparationConfig
	}
	root, err := imageInstallRoot(filepath.Join(path, "Engine"))
	if err != nil {
		return "", err
	}
	defer root.Close()
	if _, err := root.Lstat("Services"); !errors.Is(err, os.ErrNotExist) {
		return "", ErrPreparationConfig
	}
	return j.applyLocked(ctx, "prepare_identity_root", input, func(ctx context.Context, _ *os.File) (string, error) {
		if prejoinDirectoryBoundary(ctx, j, install) != nil {
			return "", ErrSessionAuthority
		}
		directory, err := root.Open(".")
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer directory.Close()
		if checkScratchCapacity(directory, PreparationScratchDemand{Bytes: uint64(len(proof.marker)), Entries: 5}) != nil {
			return "", ErrPreparationConfig
		}
		pending, err := os.MkdirTemp(filepath.Join(path, "Engine"), ".borealis-identity-")
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer os.RemoveAll(pending)
		staging, err := os.OpenRoot(pending)
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer staging.Close()
		for _, name := range []string{"Services", "Services/api-backend", "Services/api-backend/secrets"} {
			if staging.Mkdir(name, 0700) != nil {
				return "", ErrPreparationConfig
			}
			mode := os.FileMode(0755)
			if name == "Services/api-backend/secrets" {
				mode = 0700
			}
			if staging.Chmod(name, mode) != nil {
				return "", ErrPreparationConfig
			}
		}
		marker, err := staging.OpenFile("Services/api-backend/secrets/"+engineidentity.RecoveryMarkerName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return "", ErrPreparationConfig
		}
		_, writeErr := io.Copy(marker, bytes.NewReader(proof.marker))
		ownerErr := marker.Chown(proof.uid, proof.gid)
		syncErr, closeErr := marker.Sync(), marker.Close()
		if errors.Join(writeErr, ownerErr, syncErr, closeErr) != nil || staging.Chown("Services/api-backend/secrets", proof.uid, proof.gid) != nil || syncSourceTree(ctx, pending) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
			return "", ErrPreparationConfig
		}
		if unix.Renameat2(int(directory.Fd()), filepath.Join(filepath.Base(pending), "Services"), int(directory.Fd()), "Services", unix.RENAME_NOREPLACE) != nil || directory.Sync() != nil {
			return "", ErrPreparationConfig
		}
		if os.RemoveAll(pending) != nil || directory.Sync() != nil || verifyPublishedSourceWithIdentityRoot(ctx, archive, path, environment, &proof) != nil || verifySourceArchive(ctx, archive, p.bundle.asset) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
			return "", ErrPreparationConfig
		}
		return input, nil
	})
}

func (p *PreparationInputs) reconcilePrejoinIdentityRoot(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent string) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	input, proof, err := p.prejoinDirectoryRequest(j, install, material, parent)
	if err != nil {
		return "", err
	}
	if prejoinDirectoryBoundary(ctx, j, install) != nil || p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil {
		return "", ErrSessionAuthority
	}
	return j.reconcileLocked(ctx, "prepare_identity_root", input, func(ctx context.Context) (string, error) {
		archive, err := os.Open(p.bundle.ArchivePath())
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer archive.Close()
		environment, err := p.configuration.Environment()
		if err != nil {
			return "", err
		}
		defer clear(environment)
		if verifySourceArchive(ctx, archive, p.bundle.asset) != nil || verifyPublishedSourceWithIdentityRoot(ctx, archive, filepath.Join(parent, "Borealis"), environment, &proof) != nil ||
			verifySourceArchive(ctx, archive, p.bundle.asset) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
			return "", ErrPreparationConfig
		}
		return input, nil
	}, "applied")
}

func verifyPrejoinDirectoryExtras(ctx context.Context, root *os.Root, actual map[string]fs.FileInfo, proof prejoinDirectoryProof) error {
	directories := map[string]os.FileMode{"Engine/Services": 0755, "Engine/Services/api-backend": 0755, prejoinSecretsRelative: 0700}
	if proof.files != nil {
		for _, relative := range []string{"Auth_Tokens", "Certificates", "Certificates/Code-Signing"} {
			directories[prejoinSecretsRelative+"/"+relative] = 0700
		}
	}
	for name, mode := range directories {
		info, ok := actual[name]
		if !ok || !info.IsDir() || info.Mode().Perm() != mode {
			return ErrPreparationConfig
		}
		if name == prejoinSecretsRelative || strings.HasPrefix(name, prejoinSecretsRelative+"/") {
			owner, ok := info.Sys().(*syscall.Stat_t)
			if !ok || int(owner.Uid) != proof.uid || int(owner.Gid) != proof.gid {
				return ErrPreparationConfig
			}
		}
		delete(actual, name)
	}
	name := prejoinSecretsRelative + "/" + engineidentity.RecoveryMarkerName
	if verifyPrejoinPrivateFile(ctx, root, actual, proof, name, proof.marker) != nil {
		return ErrPreparationConfig
	}
	for relative, raw := range proof.files {
		if verifyPrejoinPrivateFile(ctx, root, actual, proof, prejoinSecretsRelative+"/"+relative, raw) != nil {
			return ErrPreparationConfig
		}
	}
	return nil
}

func verifyPrejoinPrivateFile(ctx context.Context, root *os.Root, actual map[string]fs.FileInfo, proof prejoinDirectoryProof, name string, expected []byte) error {
	info, ok := actual[name]
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != int64(len(expected)) {
		return ErrPreparationConfig
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Nlink != 1 || int(owner.Uid) != proof.uid || int(owner.Gid) != proof.gid {
		return ErrPreparationConfig
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ErrPreparationConfig
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return ErrPreparationConfig
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
	defer clear(raw)
	if err != nil || !bytes.Equal(raw, expected) || ctx.Err() != nil {
		return ErrPreparationConfig
	}
	delete(actual, name)
	return nil
}
