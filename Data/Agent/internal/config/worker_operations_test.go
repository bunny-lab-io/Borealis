package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func workerJournalFixture(t *testing.T) (*WorkerOperations, WorkerOperationKey, []byte) {
	t.Helper()
	j, err := OpenWorkerOperations(filepath.Join(t.TempDir(), "agent.json"), "agent-identity", "https://engine.example")
	if err != nil {
		t.Fatal(err)
	}
	key := WorkerOperationKey{Token: strings.Repeat("a", 32), JobID: 17, Event: "service_control_action", RequestSHA256: strings.Repeat("b", 64)}
	receipt, err := json.Marshal(map[string]any{"job_id": key.JobID, "status": "Completed", "stdout": "done <safely>", "stderr": "", "context": map[string]any{"worker_operation": map[string]any{"version": 1, "token": key.Token}}})
	if err != nil {
		t.Fatal(err)
	}
	return j, key, receipt
}

func TestWorkerOperationsDurableLifecycle(t *testing.T) {
	j, key, receipt := workerJournalFixture(t)
	if record, fresh, err := j.Begin(key); err != nil || !fresh || record.State != "intent" {
		t.Fatal("intent not retained", record, fresh, err)
	}
	reopen := func() *WorkerOperations {
		t.Helper()
		next, err := OpenWorkerOperations(filepath.Join(filepath.Dir(j.path), "agent.json"), j.agentID, j.serverURL)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	if record, fresh, err := reopen().Begin(key); err != nil || fresh || record.State != "intent" {
		t.Fatal("uncertain intent became replay authorization", record, fresh, err)
	}
	if err := j.Commit(key, receipt); err == nil {
		t.Fatal("intent committed without terminal evidence")
	}
	record, _, err := j.Begin(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Grant(key, record.PreflightNonce); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(key, receipt); err != nil {
		t.Fatal(err)
	}
	pending, err := reopen().Pending()
	if err != nil || len(pending) != 1 || !bytes.Equal(pending[0].Receipt, receipt) {
		t.Fatal("terminal receipt lost on restart", pending, err)
	}
	pending[0].Receipt[0] = '!'
	if err := j.Complete(key, bytes.Replace(receipt, []byte("Completed"), []byte("Failed"), 1)); err == nil {
		t.Fatal("conflicting terminal receipt accepted")
	}
	if err := reopen().Commit(key, receipt); err != nil {
		t.Fatal(err)
	}
	if record, fresh, err := reopen().Begin(key); err != nil || fresh || record.State != "committed" || len(record.Receipt) != 0 || record.ReceiptSHA256 == "" {
		t.Fatal("committed tombstone did not fence duplicate", record, fresh, err)
	}
	if pending, err := j.Pending(); err != nil || len(pending) != 0 {
		t.Fatal("committed receipt remained pending", pending, err)
	}
}

func TestWorkerOperationsConcurrentIntentAndBinding(t *testing.T) {
	j, key, _ := workerJournalFixture(t)
	other, err := OpenWorkerOperations(filepath.Join(filepath.Dir(j.path), "agent.json"), j.agentID, j.serverURL)
	if err != nil {
		t.Fatal(err)
	}
	var freshCount atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 24; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			store := j
			if i%2 == 0 {
				store = other
			}
			_, fresh, err := store.Begin(key)
			if err != nil {
				t.Error(err)
			}
			if fresh {
				freshCount.Add(1)
			}
		}(i)
	}
	group.Wait()
	if freshCount.Load() != 1 {
		t.Fatal("duplicate effect authorization", freshCount.Load())
	}
	for _, change := range []func(*WorkerOperationKey){func(k *WorkerOperationKey) { k.Token = strings.Repeat("c", 32) }, func(k *WorkerOperationKey) { k.JobID++ }, func(k *WorkerOperationKey) { k.Event = "other" }, func(k *WorkerOperationKey) { k.RequestSHA256 = strings.Repeat("d", 64) }} {
		changed := key
		change(&changed)
		if _, fresh, err := j.Begin(changed); err == nil || fresh {
			t.Fatal("rebound operation accepted", changed)
		}
	}
	if _, err := OpenWorkerOperations(filepath.Join(filepath.Dir(j.path), "agent.json"), "new-agent", j.serverURL); err == nil {
		t.Fatal("journal crossed Agent identity")
	}
	if _, err := OpenWorkerOperations(filepath.Join(filepath.Dir(j.path), "agent.json"), j.agentID, "https://other.example"); err == nil {
		t.Fatal("journal crossed Engine identity")
	}
}

func TestWorkerOperationsRejectCorruptOrFullJournal(t *testing.T) {
	for _, mode := range []string{"truncated", "duplicate version", "alias version", "unknown field", "unknown state", "receipt drift", "missing file", "entry limit", "pending limit"} {
		t.Run(mode, func(t *testing.T) {
			j, key, receipt := workerJournalFixture(t)
			if _, _, err := j.Begin(key); err != nil {
				t.Fatal(err)
			}
			record, _, err := j.Begin(key)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.Grant(key, record.PreflightNonce); err != nil {
				t.Fatal(err)
			}
			if err := j.Complete(key, receipt); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(j.path)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "truncated":
				raw = raw[:len(raw)/2]
			case "duplicate version":
				raw = bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			case "alias version":
				raw = bytes.Replace(raw, []byte(`"version":1`), []byte(`"Version":1`), 1)
			case "unknown field":
				raw = append([]byte(`{"unknown":1,`), raw[1:]...)
			case "unknown state":
				raw = bytes.Replace(raw, []byte(`"state":"terminal"`), []byte(`"state":"expired"`), 1)
			case "receipt drift":
				raw = bytes.Replace(raw, []byte(`"status":"Completed"`), []byte(`"status":"Failed"`), 1)
			case "missing file":
				if err := os.Remove(j.path); err != nil {
					t.Fatal(err)
				}
			case "entry limit", "pending limit":
				file := workerOperationsFile{Version: 1, AgentID: j.agentID, ServerURL: j.serverURL, Records: []WorkerOperationRecord{}}
				count := workerOperationsEntryLimit
				if mode == "pending limit" {
					count = workerOperationsPendingMax
				}
				for i := 0; i < count; i++ {
					k := key
					k.Token, k.JobID = fmt.Sprintf("%032x", i+1), int64(i+1)
					r := WorkerOperationRecord{Key: k, PreflightNonce: strings.Repeat("f", 32), State: "committed", ReceiptSHA256: strings.Repeat("e", 64)}
					if mode == "pending limit" {
						r.State, r.ReceiptSHA256 = "intent", ""
					}
					file.Records = append(file.Records, r)
				}
				raw, err = json.Marshal(file)
				if err != nil {
					t.Fatal(err)
				}
				key.JobID = 99999
			}
			if mode != "missing file" {
				if err := os.WriteFile(j.path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, fresh, err := j.Begin(key); err == nil || fresh {
				t.Fatal("unavailable journal authorized effects")
			}
		})
	}
}

func TestWorkerOperationsPoisonUncertainPersistenceAndPruneOnlyCommitted(t *testing.T) {
	j, key, receipt := workerJournalFixture(t)
	j.syncDirectory = func(string) error { return errors.New("directory sync failed") }
	if _, fresh, err := j.Begin(key); err == nil || fresh {
		t.Fatal("uncertain persistence authorized execution")
	}
	j.syncDirectory = syncWorkerOperationsDirectory
	if _, _, err := j.Begin(key); err == nil {
		t.Fatal("reread revived poisoned journal instance")
	}
	j, err := OpenWorkerOperations(filepath.Join(filepath.Dir(j.path), "agent.json"), j.agentID, j.serverURL)
	if err != nil {
		t.Fatal(err)
	}
	if record, fresh, err := j.Begin(key); err != nil || fresh || record.State != "intent" {
		t.Fatal("reopened uncertain intent became execution grant", record, fresh, err)
	}
	if err := j.PruneCommitted(); err != nil {
		t.Fatal(err)
	}
	if record, fresh, err := j.Begin(key); err != nil || fresh || record.State != "intent" {
		t.Fatal("prune removed uncertain intent", record, fresh, err)
	}
	j, key, receipt = workerJournalFixture(t)
	record, _, err := j.Begin(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Grant(key, record.PreflightNonce); err != nil {
		t.Fatal(err)
	}
	if err := j.Complete(key, receipt); err != nil {
		t.Fatal(err)
	}
	if err := j.PruneCommitted(); err != nil {
		t.Fatal(err)
	}
	if pending, err := j.Pending(); err != nil || len(pending) != 1 {
		t.Fatal("prune removed unacknowledged terminal", pending, err)
	}
	if err := j.Commit(key, receipt); err != nil {
		t.Fatal(err)
	}
	if err := j.PruneCommitted(); err != nil {
		t.Fatal(err)
	}
	if _, fresh, err := j.Begin(key); err != nil || !fresh {
		t.Fatal("committed tombstone not pruned; fresh claim still requires server preflight", fresh, err)
	}
}

func TestWorkerOperationsFreshPreflightDenialAndDelayedProof(t *testing.T) {
	j, key, _ := workerJournalFixture(t)
	previous := ""
	for i := 0; i < workerOperationsPendingMax+5; i++ {
		record, fresh, err := j.Begin(key)
		if err != nil || !fresh || record.PreflightNonce == previous {
			t.Fatal("denied fresh claim consumed capacity or reused nonce", fresh, err)
		}
		if previous != "" {
			if err := j.DiscardDenied(key, previous); err == nil {
				t.Fatal("delayed previous denial cleared current claim")
			}
		}
		if err := j.DiscardDenied(key, record.PreflightNonce); err != nil {
			t.Fatal(err)
		}
		previous = record.PreflightNonce
	}
	record, fresh, err := j.Begin(key)
	if err != nil || !fresh {
		t.Fatal(err)
	}
	j.AbandonPreflight(key)
	if err := j.Grant(key, record.PreflightNonce); err == nil {
		t.Fatal("delayed grant revived uncertain preflight")
	}
	if err := j.DiscardDenied(key, record.PreflightNonce); err == nil {
		t.Fatal("delayed denial cleared uncertain preflight")
	}
	if _, fresh, err := j.Begin(key); err != nil || fresh {
		t.Fatal("uncertain claim became fresh", fresh, err)
	}
	j, key, _ = workerJournalFixture(t)
	record, _, err = j.Begin(key)
	if err != nil || j.Grant(key, record.PreflightNonce) != nil {
		t.Fatal("fresh grant failed", err)
	}
	if err := j.DiscardDenied(key, record.PreflightNonce); err == nil {
		t.Fatal("denial cleared granted/executing operation")
	}
}

func TestWorkerOperationsCrossProcessClaim(t *testing.T) {
	if path := os.Getenv("BOREALIS_WORKER_OPERATION_CHILD_PATH"); path != "" {
		j, err := OpenWorkerOperations(path, "agent-identity", "https://engine.example")
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(path + ".start"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("parent barrier missing")
			}
			time.Sleep(5 * time.Millisecond)
		}
		key := WorkerOperationKey{Token: strings.Repeat("a", 32), JobID: 17, Event: "service_control_action", RequestSHA256: strings.Repeat("b", 64)}
		_, fresh, err := j.Begin(key)
		if err != nil {
			t.Fatal(err)
		}
		if fresh {
			fmt.Println("BOREALIS_FRESH_CLAIM")
		}
		return
	}
	path := filepath.Join(t.TempDir(), "agent.json")
	type child struct {
		cmd    *exec.Cmd
		output bytes.Buffer
	}
	children := make([]child, 8)
	for i := range children {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerOperationsCrossProcessClaim$", "-test.count=1")
		cmd.Env = append(os.Environ(), "BOREALIS_WORKER_OPERATION_CHILD_PATH="+path)
		children[i].cmd = cmd
		cmd.Stdout = &children[i].output
		cmd.Stderr = &children[i].output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path+".start", []byte("start"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh := 0
	for i := range children {
		if err := children[i].cmd.Wait(); err != nil {
			t.Fatalf("child: %v %s", err, children[i].output.String())
		}
		fresh += strings.Count(children[i].output.String(), "BOREALIS_FRESH_CLAIM")
	}
	if fresh != 1 {
		t.Fatalf("cross-process fresh claims=%d", fresh)
	}
}
