package engineidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var identityPaths = []string{EngineSecretPath, AgentJWTPath, ScriptKeyPath, ScriptPublicPath}

type fileRecord struct {
	Present bool      `json:"present"`
	UID     int       `json:"uid"`
	GID     int       `json:"gid"`
	Mode    uint32    `json:"mode"`
	Mtime   time.Time `json:"mtime"`
	Digest  string    `json:"digest"`
}

type installationJournal struct {
	Version  int                   `json:"version"`
	Binding  Binding               `json:"binding"`
	Target   TargetBinding         `json:"target"`
	Incoming string                `json:"incoming_digest"`
	State    string                `json:"state"`
	Files    map[string]fileRecord `json:"files"`
	Created  []string              `json:"created_directories"`
}

// Installation carries independently verified operation fences. Check must
// verify target quiescence and unchanged authority at each boundary. Callers
// hold a host-wide lock and stop all consumers/writers for the whole call.
type Installation struct {
	Root    string
	Journal string
	Binding Binding
	Target  TargetBinding
	UID     int
	GID     int
	Check   func() error
	// Quiesced verifies target identity and stopped consumers even when the broader
	// operation/authority check fails. Rollback cannot write through a lost fence.
	Quiesced func() error
}

// openIdentityRoot rejects symlinks in the supplied root path. os.Root keeps
// subsequent path resolution confined to the opened directory.
func openIdentityRoot(path string) (*os.Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("identity directory invalid")
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil || real != abs {
		return nil, errors.New("identity directory must exist without symlinks")
	}
	return os.OpenRoot(abs)
}

func checkIdentityPath(root *os.Root, name string) error {
	parts := strings.Split(name, string(filepath.Separator))
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) {
			return errors.New("identity path contains symlink or invalid directory")
		}
	}
	return nil
}

func readIdentityFile(root *os.Root, name string) ([]byte, fileRecord, error) {
	if err := checkIdentityPath(root, name); err != nil {
		return nil, fileRecord{}, err
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fileRecord{}, nil
	}
	if err != nil {
		return nil, fileRecord{}, errors.New("cannot open identity file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > MaxEnvelopeBytes/4 {
		return nil, fileRecord{}, errors.New("identity file must be bounded regular file with mode0600")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return nil, fileRecord{}, errors.New("identity hard links are forbidden")
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxEnvelopeBytes/4+1))
	if err != nil || len(raw) > MaxEnvelopeBytes/4 {
		return nil, fileRecord{}, errors.New("identity file read failed or oversized")
	}
	digest := sha256.Sum256(raw)
	return raw, fileRecord{Present: true, UID: int(stat.Uid), GID: int(stat.Gid),
		Mode: uint32(info.Mode().Perm()), Mtime: info.ModTime(), Digest: hex.EncodeToString(digest[:])}, nil
}

// Load reads only the four fixed files, without generating missing keys.
func Load(path string) (*Material, error) {
	root, err := openIdentityRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := map[string][]byte{}
	for _, name := range identityPaths {
		raw, _, err := readIdentityFile(root, name)
		if err != nil {
			return nil, err
		}
		files[name] = raw
	}
	return ParseFiles(files)
}

func syncDirectory(root *os.Root, name string) error {
	directory, err := root.Open(name)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func atomicPrivateFile(root *os.Root, name string, raw []byte, record fileRecord) error {
	if err := checkIdentityPath(root, name); err != nil {
		return err
	}
	temporary := name + ".borealis-identity-tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("identity temporary file already exists or cannot be created")
	}
	defer root.Remove(temporary)
	defer file.Close()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Chown(record.UID, record.GID); err != nil {
		return err
	}
	if err := file.Chmod(os.FileMode(record.Mode)); err != nil {
		return err
	}
	if !record.Mtime.IsZero() {
		if err := root.Chtimes(temporary, record.Mtime, record.Mtime); err != nil {
			return err
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	return syncDirectory(root, filepath.Dir(name))
}

func writeJournal(root *os.Root, journal installationJournal) error {
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	return atomicPrivateFile(root, "journal.json", raw, fileRecord{UID: os.Geteuid(), GID: os.Getegid(), Mode: 0o600})
}

func backupName(index int) string { return fmt.Sprintf("previous-%d.bin", index) }

func restoreIdentity(root, backup *os.Root, journal installationJournal, quiesced func() error) error {
	allowedDirectories := map[string]bool{"Auth_Tokens": true, "Certificates": true, "Certificates/Code-Signing": true}
	for _, directory := range journal.Created {
		if !allowedDirectories[directory] {
			return errors.New("identity rollback directory inventory invalid")
		}
		delete(allowedDirectories, directory)
	}
	// Verify the complete backup before touching any destination.
	files := map[string][]byte{}
	for index, name := range identityPaths {
		record, exists := journal.Files[name]
		if !exists {
			return errors.New("identity rollback inventory incomplete")
		}
		if !record.Present {
			continue
		}
		raw, observed, err := readIdentityFile(backup, backupName(index))
		if err != nil || !observed.Present || observed.Digest != record.Digest || record.Mode != 0o600 {
			return errors.New("identity rollback backup is missing or changed")
		}
		files[name] = raw
	}
	for _, name := range identityPaths {
		if err := quiesced(); err != nil {
			return errors.New("identity rollback requires stopped target consumers; retain private journal")
		}
		if err := checkIdentityPath(root, name); err != nil {
			return err
		}
		// Prepared journal owns these fixed temporary names. They were proved
		// absent before preparation and can survive a killed writing process.
		if err := removePrivateTemporary(root, name); err != nil {
			return err
		}
		if journal.Files[name].Present {
			if err := atomicPrivateFile(root, name, files[name], journal.Files[name]); err != nil {
				return err
			}
		} else if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for i := len(journal.Created) - 1; i >= 0; i-- {
		if err := root.Remove(journal.Created[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDirectory(root, ".")
}

func removePrivateTemporary(root *os.Root, name string) error {
	temporary := name + ".borealis-identity-tmp"
	_, record, err := readIdentityFile(root, temporary)
	if err != nil || !record.Present {
		return err
	}
	return root.Remove(temporary)
}

// Install performs a private journaled fixed-file transaction. A recovered
// interrupted transaction returns rolled_back without starting another write.
// Live authority selection, immutable release proof and maintenance belong in
// the calling workflow and must be supplied through Check, never inferred here.
func (install Installation) Install(material *Material) (state string, resultErr error) {
	if !material.valid() || !validBinding(install.Binding) || !install.Target.valid(install.Binding) ||
		install.UID < 0 || install.GID < 0 || install.Check == nil || install.Quiesced == nil {
		return "", errors.New("identity installation contract invalid")
	}
	if err := install.Check(); err != nil {
		return "", err
	}
	root, err := openIdentityRoot(install.Root)
	if err != nil {
		return "", err
	}
	defer root.Close()
	rootPath, _ := filepath.Abs(install.Root)
	journalPath, err := filepath.Abs(install.Journal)
	if err != nil || journalPath == rootPath || strings.HasPrefix(journalPath, rootPath+string(filepath.Separator)) {
		return "", errors.New("identity journal must be outside identity directory")
	}
	if err := os.Mkdir(install.Journal, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", errors.New("cannot create private identity journal")
	}
	backup, err := openIdentityRoot(install.Journal)
	if err != nil {
		return "", err
	}
	defer backup.Close()
	info, err := backup.Stat(".")
	if err != nil || info.Mode().Perm() != 0o700 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return "", errors.New("identity journal must be owned by current user with mode0700")
	}
	parent, err := openIdentityRoot(filepath.Dir(journalPath))
	if err != nil {
		return "", err
	}
	err = syncDirectory(parent, ".")
	parent.Close()
	if err != nil {
		return "", err
	}
	journal := installationJournal{Version: 2, Binding: install.Binding, Target: install.Target,
		Incoming: material.Digest(), State: "prepared", Files: map[string]fileRecord{}}
	previous, _, err := readIdentityFile(backup, "journal.json")
	if err != nil {
		return "", err
	}
	if len(previous) > 0 {
		// Decode into zero state. In particular an omitted fresh-node UID must
		// not inherit the joined UID from the current request during unmarshal.
		var retained installationJournal
		if json.Unmarshal(previous, &retained) != nil || retained.Version != 2 || retained.Binding != install.Binding ||
			retained.Target != install.Target || retained.Incoming != material.Digest() || len(retained.Files) != 4 {
			return "", errors.New("identity journal belongs to a different request or is invalid")
		}
		journal = retained
		switch journal.State {
		case "committed":
			current, err := Load(install.Root)
			if err != nil || !material.Equal(current) {
				return "", errors.New("committed identity files changed; explicit recovery required")
			}
			return "already_committed", nil
		case "prepared":
			if err := restoreIdentity(root, backup, journal, install.Quiesced); err != nil {
				return "", err
			}
			if err := removePrivateTemporary(backup, "journal.json"); err != nil {
				return "", err
			}
			journal.State = "rolled_back"
			return "rolled_back", writeJournal(backup, journal)
		default:
			return "", errors.New("retained rollback journal requires a new reviewed attempt directory")
		}
	}
	for index, name := range identityPaths {
		if _, existing, err := readIdentityFile(root, name+".borealis-identity-tmp"); err != nil || existing.Present {
			return "", errors.New("identity temporary file predates transaction; inspect before retry")
		}
		raw, record, err := readIdentityFile(root, name)
		if err != nil {
			return "", err
		}
		journal.Files[name] = record
		if record.Present {
			if err := atomicPrivateFile(backup, backupName(index), raw,
				fileRecord{UID: os.Geteuid(), GID: os.Getegid(), Mode: 0o600}); err != nil {
				return "", err
			}
		}
	}
	for _, directory := range []string{"Auth_Tokens", "Certificates", "Certificates/Code-Signing"} {
		if _, err := root.Lstat(directory); errors.Is(err, os.ErrNotExist) {
			journal.Created = append(journal.Created, directory)
		} else if err != nil {
			return "", err
		}
	}
	if err := writeJournal(backup, journal); err != nil {
		return "", err
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if err := restoreIdentity(root, backup, journal, install.Quiesced); err != nil {
			resultErr = errors.Join(resultErr, errors.New("identity rollback failed; retain private journal and keep target stopped"), err)
			return
		}
		journal.State = "rolled_back"
		resultErr = errors.Join(resultErr, writeJournal(backup, journal))
	}()
	if err := install.Check(); err != nil {
		return "", err
	}
	for _, directory := range journal.Created {
		if err := root.Mkdir(directory, 0o700); err != nil {
			return "", err
		}
		if err := root.Chown(directory, install.UID, install.GID); err != nil {
			return "", err
		}
		if err := syncDirectory(root, filepath.Dir(directory)); err != nil {
			return "", err
		}
	}
	files := material.Files()
	for _, name := range identityPaths {
		if err := install.Quiesced(); err != nil {
			return "", errors.New("identity installation requires stopped target consumers")
		}
		if err := atomicPrivateFile(root, name, files[name], fileRecord{UID: install.UID, GID: install.GID, Mode: 0o600}); err != nil {
			return "", err
		}
		if err := install.Check(); err != nil {
			return "", err
		}
	}
	journal.State = "committed"
	if err := writeJournal(backup, journal); err != nil {
		return "", err
	}
	return "committed", nil
}
