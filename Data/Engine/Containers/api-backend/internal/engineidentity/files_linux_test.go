package engineidentity

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func installationFixture(t *testing.T, fresh bool) (Installation, *Material, *Material) {
	t.Helper()
	before, binding := fixture(t)
	after, _ := fixture(t)
	base := t.TempDir()
	root := filepath.Join(base, "identity")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if !fresh {
		for name, raw := range before.Files() {
			path := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			mtime := time.Unix(1700000000, 123456789)
			if err := os.Chtimes(path, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
	}
	fingerprint := sha256.Sum256([]byte("test-only SSH host identity"))
	target := TargetBinding{ProvisioningID: "50000000-0000-4000-8000-000000000005",
		SSHFingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(fingerprint[:])}
	if !fresh {
		target.NodeUID = "40000000-0000-4000-8000-000000000004"
	}
	return Installation{Root: root, Journal: filepath.Join(base, "journal"), Binding: binding,
		Target: target, UID: os.Geteuid(), GID: os.Getegid(),
		Check: func() error { return nil }, Quiesced: func() error { return nil }}, before, after
}

func fileInventory(t *testing.T, path string) map[string]fileRecord {
	t.Helper()
	root, err := openIdentityRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	result := map[string]fileRecord{}
	for _, name := range identityPaths {
		_, record, err := readIdentityFile(root, name)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = record
	}
	return result
}

func requireInventory(t *testing.T, path string, expected map[string]fileRecord) {
	t.Helper()
	actual := fileInventory(t, path)
	for name, want := range expected {
		got := actual[name]
		if got.Present != want.Present || got.UID != want.UID || got.GID != want.GID ||
			got.Mode != want.Mode || got.Digest != want.Digest || !got.Mtime.Equal(want.Mtime) {
			t.Fatalf("bytes or metadata changed for fixed path %s", name)
		}
	}
}

func TestInstallCommitsAndRetainsPrivateBackup(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "fresh"}[fresh], func(t *testing.T) {
			install, _, after := installationFixture(t, fresh)
			state, err := install.Install(after)
			if err != nil || state != "committed" {
				t.Fatalf("commit failed: state=%s err=%v", state, err)
			}
			loaded, err := Load(install.Root)
			if err != nil || !loaded.Equal(after) {
				t.Fatalf("installed identity differs: %v", err)
			}
			beforeRepeat := fileInventory(t, install.Root)
			if state, err := install.Install(after); state != "already_committed" || err != nil {
				t.Fatalf("repeat changed committed request: state=%s err=%v", state, err)
			}
			requireInventory(t, install.Root, beforeRepeat)
			if err := filepath.Walk(install.Journal, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				want := os.FileMode(0o600)
				if info.IsDir() {
					want = 0o700
				}
				if info.Mode().Perm() != want {
					t.Error("private journal permissions changed")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallBindsFreshSSHHostBeforeKubernetesUIDExists(t *testing.T) {
	install, _, material := installationFixture(t, true)
	if install.Target.NodeUID != "" {
		t.Fatal("fresh fixture must not invent a Kubernetes UID")
	}
	if state, err := install.Install(material); err != nil || state != "committed" {
		t.Fatalf("fresh pre-staging failed: %s %v", state, err)
	}
	inventory := fileInventory(t, install.Root)
	original := install.Target
	for name, mutate := range map[string]func(*TargetBinding){
		"different provisioning target": func(target *TargetBinding) { target.ProvisioningID = "60000000-0000-4000-8000-000000000006" },
		"different SSH host": func(target *TargetBinding) {
			digest := sha256.Sum256([]byte("different test SSH identity"))
			target.SSHFingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
		},
		"later joined UID": func(target *TargetBinding) { target.NodeUID = "40000000-0000-4000-8000-000000000004" },
	} {
		t.Run(name, func(t *testing.T) {
			install.Target = original
			mutate(&install.Target)
			if _, err := install.Install(material); err == nil {
				t.Fatal("private journal rebound to another target or lifecycle phase")
			}
			requireInventory(t, install.Root, inventory)
		})
	}
}

func TestInstallRejectsInvalidSSHBindingBeforeFilesystemWork(t *testing.T) {
	for name, mutate := range map[string]func(*Installation){
		"missing provisioning ID": func(i *Installation) { i.Target.ProvisioningID = "" },
		"zero provisioning ID":    func(i *Installation) { i.Target.ProvisioningID = "00000000-0000-0000-0000-000000000000" },
		"missing host key":        func(i *Installation) { i.Target.SSHFingerprint = "" },
		"wrong hash kind":         func(i *Installation) { i.Target.SSHFingerprint = "MD5:invalid" },
		"zero node UID":           func(i *Installation) { i.Target.NodeUID = "00000000-0000-0000-0000-000000000000" },
		"source node UID":         func(i *Installation) { i.Target.NodeUID = i.Binding.SourceUID },
		"padded fingerprint":      func(i *Installation) { i.Target.SSHFingerprint += "=" },
	} {
		t.Run(name, func(t *testing.T) {
			install, _, material := installationFixture(t, false)
			inventory := fileInventory(t, install.Root)
			mutate(&install)
			checked := false
			install.Check = func() error { checked = true; return nil }
			if _, err := install.Install(material); err == nil || checked {
				t.Fatal("invalid target reached operation or filesystem work")
			}
			if _, err := os.Stat(install.Journal); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid target created a journal")
			}
			requireInventory(t, install.Root, inventory)
		})
	}
}

func TestInstallFailureRestoresBytesAndMetadataAtEveryBoundary(t *testing.T) {
	for failAt := 2; failAt <= 6; failAt++ {
		install, _, after := installationFixture(t, false)
		before := fileInventory(t, install.Root)
		calls := 0
		install.Check = func() error {
			calls++
			if calls == failAt {
				return errors.New("injected authority change")
			}
			return nil
		}
		if _, err := install.Install(after); err == nil {
			t.Fatalf("boundary %d accepted failed guard", failAt)
		}
		requireInventory(t, install.Root, before)
	}
}

func TestFreshInstallFailureRemovesOnlyCreatedFilesAndDirectories(t *testing.T) {
	install, _, after := installationFixture(t, true)
	calls := 0
	install.Check = func() error {
		calls++
		if calls == 4 {
			return errors.New("injected partial installation failure")
		}
		return nil
	}
	if _, err := install.Install(after); err == nil {
		t.Fatal("expected injected failure")
	}
	entries, err := os.ReadDir(install.Root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("new identity files/directories survived rollback: err=%v", err)
	}
}

func TestLostConsumerFenceRetainsJournalUntilSafeRecovery(t *testing.T) {
	install, before, after := installationFixture(t, false)
	inventory := fileInventory(t, install.Root)
	stopped := true
	install.Quiesced = func() error {
		if !stopped {
			return errors.New("consumer restarted")
		}
		return nil
	}
	calls := 0
	install.Check = func() error {
		calls++
		if calls == 3 {
			stopped = false
			return errors.New("lost consumer fence")
		}
		return nil
	}
	if _, err := install.Install(after); err == nil {
		t.Fatal("lost fence accepted")
	}
	partial := fileInventory(t, install.Root)
	if partial[EngineSecretPath].Digest == inventory[EngineSecretPath].Digest ||
		partial[AgentJWTPath].Digest != inventory[AgentJWTPath].Digest {
		t.Fatal("rollback wrote through lost consumer fence")
	}
	stopped = true
	install.Check = func() error { return nil }
	// A killed process may leave its fixed temporary file behind.
	if err := os.WriteFile(filepath.Join(install.Root, AgentJWTPath)+".borealis-identity-tmp", []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if state, err := install.Install(after); err != nil || state != "rolled_back" {
		t.Fatalf("interrupted recovery failed: state=%s err=%v", state, err)
	}
	requireInventory(t, install.Root, inventory)
	loaded, err := Load(install.Root)
	if err != nil || !loaded.Equal(before) {
		t.Fatalf("rollback trust changed: %v", err)
	}
}

func TestInstallRejectsUnsafeFilesystemBeforeIdentityWrites(t *testing.T) {
	for name, alter := range map[string]func(Installation) error{
		"leaf symlink": func(i Installation) error {
			path := filepath.Join(i.Root, EngineSecretPath)
			if err := os.Rename(path, path+".original"); err != nil {
				return err
			}
			return os.Symlink(path+".original", path)
		},
		"parent symlink": func(i Installation) error {
			path := filepath.Join(i.Root, "Auth_Tokens")
			if err := os.Rename(path, path+".original"); err != nil {
				return err
			}
			return os.Symlink(path+".original", path)
		},
		"hard link": func(i Installation) error {
			return os.Link(filepath.Join(i.Root, EngineSecretPath), filepath.Join(i.Root, "alias"))
		},
		"FIFO": func(i Installation) error {
			path := filepath.Join(i.Root, AgentJWTPath)
			if err := os.Remove(path); err != nil {
				return err
			}
			return syscall.Mkfifo(path, 0o600)
		},
		"readable key": func(i Installation) error { return os.Chmod(filepath.Join(i.Root, AgentJWTPath), 0o644) },
		"preexisting temporary": func(i Installation) error {
			return os.WriteFile(filepath.Join(i.Root, AgentJWTPath)+".borealis-identity-tmp", []byte("unrelated"), 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			install, _, after := installationFixture(t, false)
			before, err := os.ReadFile(filepath.Join(install.Root, ScriptKeyPath))
			if err != nil {
				t.Fatal(err)
			}
			if err := alter(install); err != nil {
				t.Fatal(err)
			}
			if _, err := install.Install(after); err == nil {
				t.Fatal("unsafe filesystem accepted")
			}
			current, err := os.ReadFile(filepath.Join(install.Root, ScriptKeyPath))
			if err != nil || string(current) != string(before) {
				t.Fatal("unsafe preflight changed identity")
			}
		})
	}
}

func TestInterruptedRecoveryRejectsDamagedBackupBeforeAnyRestore(t *testing.T) {
	install, _, after := installationFixture(t, false)
	if _, err := install.Install(after); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(install.Journal, "journal.json")
	raw, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var journal installationJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		t.Fatal(err)
	}
	journal.State = "prepared"
	raw, err = json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install.Journal, backupName(3)), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory := fileInventory(t, install.Root)
	if _, err := install.Install(after); err == nil {
		t.Fatal("damaged backup accepted")
	}
	requireInventory(t, install.Root, inventory)
}
