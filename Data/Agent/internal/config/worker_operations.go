package config

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const (
	WorkerOperationReceiptLimit = 1 << 20
	workerOperationsFileLimit   = 72 << 20
	workerOperationsEntryLimit  = 4096
	workerOperationsPendingMax  = 64
)

var (
	ErrWorkerOperationJournal = errors.New("worker operation journal unavailable or inconsistent")
	workerOperationToken      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	workerOperationDigest     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type WorkerOperationKey struct {
	Token         string `json:"token"`
	JobID         int64  `json:"job_id"`
	Event         string `json:"event"`
	RequestSHA256 string `json:"request_sha256"`
}

type WorkerOperationRecord struct {
	Key            WorkerOperationKey `json:"key"`
	PreflightNonce string             `json:"preflight_nonce"`
	State          string             `json:"state"`
	Receipt        json.RawMessage    `json:"receipt,omitempty"`
	ReceiptSHA256  string             `json:"receipt_sha256,omitempty"`
}

type workerOperationsFile struct {
	Version   int                     `json:"version"`
	AgentID   string                  `json:"agent_id"`
	ServerURL string                  `json:"server_url"`
	Records   []WorkerOperationRecord `json:"records"`
}

type WorkerOperations struct {
	path, agentID, serverURL string
	poisoned                 bool
	syncDirectory            func(string) error
	freshClaims              map[string]string
}

// This private sidecar preserves uncertain intent through process replacement.
// Intent is never replay authorization. Every fresh claim requires an independent
// server preflight before effects; only committed tombstones may be pruned.
func OpenWorkerOperations(configPath, agentID, serverURL string) (*WorkerOperations, error) {
	if configPath == "" || agentID == "" || len(agentID) > 256 || serverURL == "" || len(serverURL) > 2048 {
		return nil, ErrWorkerOperationJournal
	}
	j := &WorkerOperations{path: filepath.Join(filepath.Dir(configPath), "worker-operations.json"), agentID: agentID, serverURL: serverURL, syncDirectory: syncWorkerOperationsDirectory, freshClaims: map[string]string{}}
	err := j.locked(func() error {
		if _, err := os.Lstat(j.path); errors.Is(err, os.ErrNotExist) {
			return j.save(workerOperationsFile{Version: 1, AgentID: agentID, ServerURL: serverURL, Records: []WorkerOperationRecord{}})
		}
		if _, err := j.load(); err != nil {
			return err
		}
		return j.syncDirectory(filepath.Dir(j.path))
	})
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (j *WorkerOperations) locked(fn func() error) error {
	if j == nil {
		return ErrWorkerOperationJournal
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	if j.poisoned {
		return ErrWorkerOperationJournal
	}
	return withProcessFileLock(j.path, fn)
}

func (j *WorkerOperations) Begin(key WorkerOperationKey) (record WorkerOperationRecord, fresh bool, err error) {
	if !validWorkerOperationKey(key) {
		return record, false, ErrWorkerOperationJournal
	}
	err = j.locked(func() error {
		file, err := j.load()
		if err != nil {
			return err
		}
		pending := 0
		for _, existing := range file.Records {
			if existing.Key.Token == key.Token || existing.Key.JobID == key.JobID {
				if existing.Key != key {
					return ErrWorkerOperationJournal
				}
				record = existing
				return nil
			}
			if existing.State != "committed" {
				pending++
			}
		}
		if len(file.Records) >= workerOperationsEntryLimit || pending >= workerOperationsPendingMax {
			return ErrWorkerOperationJournal
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		record = WorkerOperationRecord{Key: key, PreflightNonce: hex.EncodeToString(nonce[:]), State: "intent"}
		file.Records = append(file.Records, record)
		if err := j.save(file); err != nil {
			return err
		}
		fresh = true
		j.freshClaims[key.Token] = record.PreflightNonce
		return nil
	})
	return
}

// Grant persists acceptance before effects. Only this instance's new claim can
// consume its fresh nonce. A restarted or duplicate caller never gets a grant.
func (j *WorkerOperations) Grant(key WorkerOperationKey, nonce string) error {
	return j.resolvePreflight(key, nonce, true)
}

// DiscardDenied is only for an exact authenticated deny proof received by the
// still-new claim owner. It changes no server charge or operation terminal state.
func (j *WorkerOperations) DiscardDenied(key WorkerOperationKey, nonce string) error {
	return j.resolvePreflight(key, nonce, false)
}

func (j *WorkerOperations) resolvePreflight(key WorkerOperationKey, nonce string, execute bool) error {
	return j.locked(func() error {
		if !workerOperationToken.MatchString(nonce) || j.freshClaims[key.Token] != nonce {
			return ErrWorkerOperationJournal
		}
		delete(j.freshClaims, key.Token)
		file, err := j.load()
		if err != nil {
			return err
		}
		for i := range file.Records {
			record := &file.Records[i]
			if record.Key != key || record.PreflightNonce != nonce || record.State != "intent" {
				continue
			}
			if execute {
				record.State = "granted"
			} else {
				file.Records = append(file.Records[:i], file.Records[i+1:]...)
			}
			return j.save(file)
		}
		return ErrWorkerOperationJournal
	})
}

// Unknown transport outcomes permanently end this instance's preflight claim.
// The durable intent remains; a delayed grant/deny cannot revive or discard it.
func (j *WorkerOperations) AbandonPreflight(key WorkerOperationKey) {
	fileMu.Lock()
	defer fileMu.Unlock()
	if j != nil {
		delete(j.freshClaims, key.Token)
	}
}

func (j *WorkerOperations) Complete(key WorkerOperationKey, receipt []byte) error {
	if !validWorkerOperationReceipt(key, receipt) {
		return ErrWorkerOperationJournal
	}
	return j.transition(key, receipt, false)
}

func (j *WorkerOperations) Commit(key WorkerOperationKey, receipt []byte) error {
	if !validWorkerOperationReceipt(key, receipt) {
		return ErrWorkerOperationJournal
	}
	return j.transition(key, receipt, true)
}

func (j *WorkerOperations) transition(key WorkerOperationKey, receipt []byte, commit bool) error {
	digest := sha256.Sum256(receipt)
	hash := hex.EncodeToString(digest[:])
	return j.locked(func() error {
		file, err := j.load()
		if err != nil {
			return err
		}
		for i := range file.Records {
			record := &file.Records[i]
			if record.Key != key {
				continue
			}
			if record.State == "intent" || (record.State != "granted" && record.ReceiptSHA256 != hash) || (commit && record.State == "granted") {
				return ErrWorkerOperationJournal
			}
			if record.State == "committed" {
				return nil
			}
			record.ReceiptSHA256 = hash
			if commit {
				record.State, record.Receipt = "committed", nil
			} else {
				record.State, record.Receipt = "terminal", append(json.RawMessage(nil), receipt...)
			}
			return j.save(file)
		}
		return ErrWorkerOperationJournal
	})
}

func (j *WorkerOperations) Pending() (records []WorkerOperationRecord, err error) {
	err = j.locked(func() error {
		file, err := j.load()
		if err != nil {
			return err
		}
		for _, record := range file.Records {
			if record.State == "terminal" {
				records = append(records, record)
			}
		}
		return nil
	})
	return
}

// Call only in the tracked protocol whose fresh claims always obtain a new,
// nonce-bound server execute grant. The authoritative committed server record
// denies replay after this local tombstone is evicted; missing history denies too.
func (j *WorkerOperations) PruneCommitted() error {
	return j.locked(func() error {
		file, err := j.load()
		if err != nil {
			return err
		}
		retained := make([]WorkerOperationRecord, 0, len(file.Records))
		for _, record := range file.Records {
			if record.State != "committed" {
				retained = append(retained, record)
			}
		}
		if len(retained) == len(file.Records) {
			return nil
		}
		file.Records = retained
		return j.save(file)
	})
}

func validWorkerOperationKey(key WorkerOperationKey) bool {
	return workerOperationToken.MatchString(key.Token) && key.JobID > 0 && key.JobID <= 1<<53-1 && key.Event != "" && len(key.Event) <= 128 && workerOperationDigest.MatchString(key.RequestSHA256)
}

func validWorkerOperationReceipt(key WorkerOperationKey, raw []byte) bool {
	if !validWorkerOperationKey(key) || len(raw) == 0 || len(raw) > WorkerOperationReceiptLimit {
		return false
	}
	canonical, err := json.Marshal(json.RawMessage(raw))
	if err != nil || !bytes.Equal(canonical, raw) {
		return false
	}
	var receipt struct {
		JobID   int64                      `json:"job_id"`
		Status  string                     `json:"status"`
		Stdout  string                     `json:"stdout"`
		Stderr  string                     `json:"stderr"`
		Context map[string]json.RawMessage `json:"context"`
	}
	if strictWorkerOperationsJSON(raw, &receipt) != nil || receipt.JobID != key.JobID || (receipt.Status != "Completed" && receipt.Status != "Failed" && receipt.Status != "Skipped") {
		return false
	}
	var correlation struct {
		Version int    `json:"version"`
		Token   string `json:"token"`
	}
	return strictWorkerOperationsJSON(receipt.Context["worker_operation"], &correlation) == nil && correlation.Version == 1 && correlation.Token == key.Token
}

func (j *WorkerOperations) load() (file workerOperationsFile, err error) {
	info, err := os.Lstat(j.path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > workerOperationsFileLimit {
		return file, ErrWorkerOperationJournal
	}
	raw, err := readConfigFile(j.path)
	if err != nil || len(raw) > workerOperationsFileLimit || strictWorkerOperationsJSON(raw, &file) != nil || file.Version != 1 || file.AgentID != j.agentID || file.ServerURL != j.serverURL || file.Records == nil || len(file.Records) > workerOperationsEntryLimit {
		return file, ErrWorkerOperationJournal
	}
	tokens, jobs := map[string]bool{}, map[int64]bool{}
	pending := 0
	for _, record := range file.Records {
		if !validWorkerOperationKey(record.Key) || !workerOperationToken.MatchString(record.PreflightNonce) || tokens[record.Key.Token] || jobs[record.Key.JobID] {
			return file, ErrWorkerOperationJournal
		}
		tokens[record.Key.Token], jobs[record.Key.JobID] = true, true
		switch record.State {
		case "intent", "granted":
			if len(record.Receipt) != 0 || record.ReceiptSHA256 != "" {
				return file, ErrWorkerOperationJournal
			}
			pending++
		case "terminal":
			digest := sha256.Sum256(record.Receipt)
			if !validWorkerOperationReceipt(record.Key, record.Receipt) || record.ReceiptSHA256 != hex.EncodeToString(digest[:]) {
				return file, ErrWorkerOperationJournal
			}
			pending++
		case "committed":
			if len(record.Receipt) != 0 || !workerOperationDigest.MatchString(record.ReceiptSHA256) {
				return file, ErrWorkerOperationJournal
			}
		default:
			return file, ErrWorkerOperationJournal
		}
	}
	if pending > workerOperationsPendingMax {
		return file, ErrWorkerOperationJournal
	}
	return file, nil
}

func (j *WorkerOperations) save(file workerOperationsFile) error {
	parent := filepath.Dir(j.path)
	if err := RestrictParent(parent); err != nil {
		return err
	}
	raw, err := json.Marshal(file)
	if err != nil || len(raw) > workerOperationsFileLimit {
		return ErrWorkerOperationJournal
	}
	tmp, err := os.CreateTemp(parent, ".worker-operations-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(raw); err == nil {
		err = tmp.Chmod(0o600)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	// Replacement errors may arrive after the native rename took effect. No
	// further action on this instance may treat a reread as persistence proof.
	j.poisoned = true
	if err := replaceConfigFile(tmp.Name(), j.path); err != nil {
		return err
	}
	if err := RestrictFile(j.path); err != nil {
		return err
	}
	if err := j.syncDirectory(parent); err != nil {
		return err
	}
	j.poisoned = false
	return nil
}

// Reject duplicate keys before typed decoding: a replay journal cannot silently
// select one of two versions, identities, states or receipts.
func strictWorkerOperationsJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) error
	value = func(depth int) error {
		if depth > 32 {
			return ErrWorkerOperationJournal
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || keys[name] {
					return ErrWorkerOperationJournal
				}
				keys[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return ErrWorkerOperationJournal
			}
		case json.Delim('['):
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return ErrWorkerOperationJournal
			}
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrWorkerOperationJournal
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	canonical, err := json.Marshal(out)
	if err != nil {
		return err
	}
	var original, typed any
	if json.Unmarshal(raw, &original) != nil || json.Unmarshal(canonical, &typed) != nil || !workerOperationsExactKeys(original, typed) {
		return ErrWorkerOperationJournal
	}
	return nil
}

func workerOperationsExactKeys(original, typed any) bool {
	switch value := original.(type) {
	case map[string]any:
		other, ok := typed.(map[string]any)
		if !ok {
			return false
		}
		for key, item := range value {
			match, exists := other[key]
			if !exists || !workerOperationsExactKeys(item, match) {
				return false
			}
		}
	case []any:
		other, ok := typed.([]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for i := range value {
			if !workerOperationsExactKeys(value[i], other[i]) {
				return false
			}
		}
	}
	return true
}
