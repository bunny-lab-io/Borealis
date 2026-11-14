package engineidentity

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func prejoinCommit(transaction func() (string, error)) error {
	_, err := transaction()
	return err
}

func TestPreparedPrejoinIdentityConsumesOnlyExactFreshFence(t *testing.T) {
	for _, mode := range []string{"valid", "missing marker", "changed marker", "changed source", "joined", "no journal", "existing key", "retained journal", "authority", "quiescence"} {
		t.Run(mode, func(t *testing.T) {
			install, _, material := installationFixture(t, true)
			marker, err := install.PrejoinPreparationMarker(material)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "missing marker" {
				if mode == "changed marker" {
					marker = append(marker, '\n')
				}
				if err := os.WriteFile(filepath.Join(install.Root, recoveryMarker), marker, 0600); err != nil {
					t.Fatal(err)
				}
				requireStartupBlocked(t, install.Root)
			}
			commit := prejoinCommit
			switch mode {
			case "changed source":
				install.Binding.SourceID = "50000000-0000-4000-8000-000000000005"
			case "joined":
				install.Target.NodeUID = "40000000-0000-4000-8000-000000000004"
			case "no journal":
				commit = nil
			case "existing key":
				if err := os.WriteFile(filepath.Join(install.Root, EngineSecretPath), material.Files()[EngineSecretPath], 0600); err != nil {
					t.Fatal(err)
				}
			case "retained journal":
				if err := os.Mkdir(install.Journal, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(install.Journal, "journal.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "authority":
				install.Check = func() error { return errors.New("lost authority") }
			case "quiescence":
				install.Quiesced = func() error { return errors.New("consumer present") }
			}
			before := fileInventory(t, install.Root)
			state, err := install.InstallPreparedPrejoinFencedJournaledRetained(material, commit)
			if mode != "valid" {
				if err == nil {
					t.Fatal("invalid prepared identity accepted", state)
				}
				requireInventory(t, install.Root, before)
				return
			}
			if err != nil || state != "committed" || install.ProvePrejoinFencedCommitted(material) != nil {
				t.Fatal("prepared commit failed", state, err)
			}
			requireStartupBlocked(t, install.Root)
			if _, err := install.InstallPreparedPrejoinFencedJournaledRetained(material, commit); err == nil {
				t.Fatal("prepared transaction replayed")
			}
			if _, err := install.InstallPrejoinFencedJournaledRetained(material, commit); err == nil {
				t.Fatal("ordinary pre-join accepted retained marker")
			}
			if state, err := install.ReleasePrejoinFencedCommitted(material); err != nil || state != "released" {
				t.Fatal("release", state, err)
			}
		})
	}
}

func TestPrejoinIdentityRetainsFenceWithoutInventingNodeUID(t *testing.T) {
	install, _, material := installationFixture(t, true)
	if _, err := install.InstallFencedJournaledRetained(material, prejoinCommit); err == nil {
		t.Fatal("joined contract accepted missing Node UID")
	}
	if state, err := install.InstallPrejoinFencedJournaledRetained(material, prejoinCommit); err != nil || state != "committed" {
		t.Fatal("pre-join commit", state, err)
	}
	requireStartupBlocked(t, install.Root)
	if err := install.ProvePrejoinFencedCommitted(material); err != nil {
		t.Fatal(err)
	}
	if err := install.ProveFencedCommitted(material); err == nil {
		t.Fatal("pre-join proof became joined proof")
	}
	if _, err := install.ReleaseFencedCommitted(material); err == nil {
		t.Fatal("joined release accepted pre-join transaction")
	}
	if _, err := install.InstallPrejoinFencedJournaledRetained(material, prejoinCommit); err == nil {
		t.Fatal("retained attempt replayed")
	}
	changed := install
	changed.Target.NodeUID = "40000000-0000-4000-8000-000000000004"
	if changed.ProvePrejoinFencedCommitted(material) == nil || changed.ProveFencedCommitted(material) == nil {
		t.Fatal("later Node UID rebound completed journal")
	}
	if _, err := changed.ReleasePrejoinFencedCommitted(material); err == nil {
		t.Fatal("joined target accepted by pre-join release")
	}
	requireStartupBlocked(t, install.Root)
	before, err := os.ReadFile(filepath.Join(install.Journal, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"released", "already_released"} {
		if state, err := install.ReleasePrejoinFencedCommitted(material); err != nil || state != want {
			t.Fatal(state, err)
		}
	}
	consumer, err := AcquireConsumer(install.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if got, err := consumer.Load(); err != nil || !got.Equal(material) {
		t.Fatal("incomplete serving identity", err)
	}
	after, err := os.ReadFile(filepath.Join(install.Journal, "journal.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("release rewrote retained transaction")
	}
}

func TestPrejoinIdentityRejectsExistingFilesAndOwners(t *testing.T) {
	for _, mode := range append([]string{"joined", "consumer", "marker", "no journal", "authority", "quiescence"}, identityPaths...) {
		t.Run(mode, func(t *testing.T) {
			install, _, material := installationFixture(t, true)
			commit := prejoinCommit
			switch mode {
			case "joined":
				install.Target.NodeUID = "40000000-0000-4000-8000-000000000004"
			case "consumer":
				consumer, err := AcquireConsumer(install.Root)
				if err != nil {
					t.Fatal(err)
				}
				defer consumer.Close()
			case "marker":
				if err := os.WriteFile(filepath.Join(install.Root, recoveryMarker), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "no journal":
				commit = nil
			case "authority":
				install.Check = func() error { return errors.New("lost source") }
			case "quiescence":
				install.Quiesced = func() error { return errors.New("consumer present") }
			default:
				path := filepath.Join(install.Root, mode)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, material.Files()[mode], 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := fileInventory(t, install.Root)
			if _, err := install.InstallPrejoinFencedJournaledRetained(material, commit); err == nil {
				t.Fatal("unsafe pre-join transaction accepted")
			}
			requireInventory(t, install.Root, before)
		})
	}
}

func TestPrejoinIdentityUncertainCommitAndFailedRelease(t *testing.T) {
	install, _, material := installationFixture(t, true)
	if _, err := install.InstallPrejoinFencedJournaledRetained(material, func(transaction func() (string, error)) error {
		if _, err := transaction(); err != nil {
			t.Fatal(err)
		}
		return errors.New("outer journal acknowledgement lost")
	}); err == nil {
		t.Fatal("uncertain outer commit acknowledged")
	}
	requireStartupBlocked(t, install.Root)
	if install.ProvePrejoinFencedCommitted(material) != nil {
		t.Fatal("committed effect cannot be reconciled")
	}
	install.Check = func() error {
		if _, err := os.Stat(filepath.Join(install.Root, recoveryMarker)); errors.Is(err, os.ErrNotExist) {
			return errors.New("authority lost after unlink")
		}
		return nil
	}
	if _, err := install.ReleasePrejoinFencedCommitted(material); err == nil {
		t.Fatal("lost authority released startup")
	}
	requireStartupBlocked(t, install.Root)
	install.Check = func() error { return nil }
	if state, err := install.ReleasePrejoinFencedCommitted(material); err != nil || state != "released" {
		t.Fatal(state, err)
	}
}

func TestPrejoinIdentityRollbackAndOriginalInventoryProof(t *testing.T) {
	for _, mode := range []string{"rollback", "changed inventory"} {
		t.Run(mode, func(t *testing.T) {
			install, _, material := installationFixture(t, true)
			before := fileInventory(t, install.Root)
			if mode == "rollback" {
				install.Check = func() error {
					if _, err := os.Stat(filepath.Join(install.Root, EngineSecretPath)); err == nil {
						return errors.New("authority lost after write")
					}
					return nil
				}
			}
			_, err := install.InstallPrejoinFencedJournaledRetained(material, prejoinCommit)
			if mode == "rollback" {
				if err == nil {
					t.Fatal("partial write acknowledged")
				}
				requireInventory(t, install.Root, before)
				install.Check = func() error { return nil }
			} else {
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(install.Journal, "journal.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var journal installationJournal
				if json.Unmarshal(raw, &journal) != nil {
					t.Fatal("journal fixture")
				}
				journal.Files[EngineSecretPath] = fileRecord{Present: true}
				raw, err = json.Marshal(journal)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if install.ProvePrejoinFencedCommitted(material) == nil {
				t.Fatal("non-fresh or rolled back proof accepted")
			}
			if _, err := install.ReleasePrejoinFencedCommitted(material); err == nil {
				t.Fatal("invalid proof released startup")
			}
			requireStartupBlocked(t, install.Root)
		})
	}
}
