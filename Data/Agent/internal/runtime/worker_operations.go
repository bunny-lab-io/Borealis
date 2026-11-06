package agentruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/transport"
)

var workerOperationTokenPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

var errWorkerOperation = errors.New("tracked worker operation unavailable or inconsistent")

type workerOperationEmitter interface {
	Emit(string, any) error
	EmitWithAck(context.Context, string, any, time.Duration) ([]any, error)
}

type workerOperationTerminal struct {
	key     agentconfig.WorkerOperationKey
	receipt []byte
}

// A failed disk write never emits success. Retain the terminal bytes in memory
// for a later validated connection; the durable granted intent still fences
// reexecution if the process dies before storage recovers.
type workerOperationOutbox struct {
	mu       sync.Mutex
	delivery sync.Mutex
	pending  map[string]workerOperationTerminal
	logger   *log.Logger
}

type workerOperationConnection struct {
	journal    *agentconfig.WorkerOperations
	emitter    workerOperationEmitter
	outbox     *workerOperationOutbox
	mu         sync.Mutex
	active     map[string]agentconfig.WorkerOperationKey
	closed     bool
	stopReplay func()
}

func (a *Agent) workerOperationConnection(emitter workerOperationEmitter) *workerOperationConnection {
	a.workerMu.Lock()
	if a.workerOutbox == nil {
		logger := a.logger
		if logger == nil {
			logger = log.Default()
		}
		a.workerOutbox = &workerOperationOutbox{pending: map[string]workerOperationTerminal{}, logger: logger}
	}
	outbox := a.workerOutbox
	a.workerMu.Unlock()
	connection := &workerOperationConnection{emitter: emitter, outbox: outbox, active: map[string]agentconfig.WorkerOperationKey{}}
	cfg := a.authClient.Config()
	journal, err := agentconfig.OpenWorkerOperations(a.configPath, a.authClient.AgentID(), cfg.ServerURL)
	if err == nil {
		err = journal.PruneCommitted()
	}
	if err != nil {
		outbox.logger.Printf("tracked worker operations disabled: %v", err)
		return connection
	}
	connection.journal = journal
	return connection
}

func workerOperationInteger(value any) (int64, bool) {
	var number float64
	switch v := value.(type) {
	case float64:
		number = v
	case int:
		number = float64(v)
	case int64:
		if v <= 0 || v > 1<<53-1 {
			return 0, false
		}
		return v, true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil && n > 0 && n <= 1<<53-1
	default:
		return 0, false
	}
	return int64(number), !math.IsNaN(number) && !math.IsInf(number, 0) && number > 0 && number <= 1<<53-1 && math.Trunc(number) == number
}

func (c *workerOperationConnection) addRegistrationCapabilities(payload map[string]any) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed || c.journal == nil {
		return
	}
	if _, err := c.journal.Pending(); err != nil {
		return
	}
	payload["worker_operation_version"] = 1
	payload["worker_operation_file_transfer_version"] = 1
}

func workerOperationBinding(payload any) (map[string]any, string, int64, bool, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return nil, "", 0, false, nil
	}
	contextValue, exists := body["context"]
	if !exists || contextValue == nil {
		return body, "", 0, false, nil
	}
	operationContext, ok := contextValue.(map[string]any)
	if !ok {
		return body, "", 0, false, errWorkerOperation
	}
	value, tracked := operationContext["worker_operation"]
	if !tracked {
		return body, "", 0, false, nil
	}
	correlation, ok := value.(map[string]any)
	version, versionOK := workerOperationInteger(correlation["version"])
	token, tokenOK := correlation["token"].(string)
	job, jobOK := workerOperationInteger(body["job_id"])
	encodedContext, err := json.Marshal(operationContext)
	if !ok || len(correlation) != 2 || !versionOK || version != 1 || !tokenOK || !workerOperationTokenPattern.MatchString(token) || !jobOK || err != nil || len(encodedContext) > 64<<10 {
		return body, "", 0, true, errWorkerOperation
	}
	return body, token, job, true, nil
}

func workerOperationCorrelation(token string) map[string]any {
	return map[string]any{"version": 1, "token": token}
}

func workerOperationProof(ack []any, key agentconfig.WorkerOperationKey, nonce string) (string, error) {
	if len(ack) != 1 {
		return "", errWorkerOperation
	}
	body, ok := ack[0].(map[string]any)
	job, jobOK := workerOperationInteger(body["job_id"])
	correlation, correlationOK := body["worker_operation"].(map[string]any)
	version, versionOK := workerOperationInteger(correlation["version"])
	status, statusOK := body["status"].(string)
	wantFields := 3
	if nonce != "" {
		wantFields++
		preflight, ok := body["worker_operation_preflight"].(map[string]any)
		version, valid := workerOperationInteger(preflight["version"])
		if !ok || len(preflight) != 2 || !valid || version != 1 || preflight["nonce"] != nonce || (status != "execute" && status != "deny") {
			return "", errWorkerOperation
		}
	} else if status != "committed" {
		return "", errWorkerOperation
	}
	if !ok || len(body) != wantFields || !jobOK || job != key.JobID || !correlationOK || len(correlation) != 2 || !versionOK || version != 1 || correlation["token"] != key.Token || !statusOK {
		return "", errWorkerOperation
	}
	return status, nil
}

func (c *workerOperationConnection) begin(ctx context.Context, event string, payload any) (any, agentconfig.WorkerOperationKey, bool, bool, error) {
	body, token, job, tracked, err := workerOperationBinding(payload)
	if err != nil || !tracked {
		return payload, agentconfig.WorkerOperationKey{}, tracked, false, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if c.journal == nil || closed {
		return nil, agentconfig.WorkerOperationKey{}, true, false, errWorkerOperation
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, agentconfig.WorkerOperationKey{}, true, false, err
	}
	var owned map[string]any
	if err := json.Unmarshal(raw, &owned); err != nil {
		return nil, agentconfig.WorkerOperationKey{}, true, false, err
	}
	digest := sha256.Sum256(append(append([]byte(event), 0), raw...))
	key := agentconfig.WorkerOperationKey{Token: token, JobID: job, Event: event, RequestSHA256: hex.EncodeToString(digest[:])}
	record, fresh, err := c.journal.Begin(key)
	if err != nil || !fresh {
		if err == nil && record.State == "terminal" {
			c.outbox.delivery.Lock()
			err = c.emitTerminal(ctx, record)
			c.outbox.delivery.Unlock()
		}
		return owned, key, true, false, err
	}
	// Only the process that durably created this intent may obtain a fresh grant.
	// Every non-successful transport/proof path leaves uncertainty fenced.
	defer c.journal.AbandonPreflight(key)
	ack, err := c.emitter.EmitWithAck(ctx, "quick_job_progress", map[string]any{
		"job_id": job, "status": "Running", "context": map[string]any{"worker_operation": workerOperationCorrelation(token)},
		"worker_operation_preflight": map[string]any{"version": 1, "nonce": record.PreflightNonce},
	}, 10*time.Second)
	if err != nil {
		return owned, key, true, false, err
	}
	decision, err := workerOperationProof(ack, key, record.PreflightNonce)
	if err != nil {
		return owned, key, true, false, err
	}
	if decision == "deny" {
		if err := c.journal.DiscardDenied(key, record.PreflightNonce); err != nil {
			return owned, key, true, false, err
		}
		return owned, key, true, false, errWorkerOperation
	}
	if err := c.journal.Grant(key, record.PreflightNonce); err != nil {
		return owned, key, true, false, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return owned, key, true, false, errWorkerOperation
	}
	c.active[token] = key
	c.mu.Unlock()
	return owned, key, true, true, nil
}

func workerOperationAck(err error) (any, error) {
	if err != nil {
		return map[string]any{"ok": false, "status": "rejected", "error": "worker_operation_unavailable"}, nil
	}
	return map[string]any{"ok": true, "status": "pending"}, nil
}

func (c *workerOperationConnection) async(event string, handler func(context.Context, any, func(error)) (any, error)) transport.Handler {
	return func(ctx context.Context, payload any) (any, error) {
		owned, key, tracked, execute, err := c.begin(ctx, event, payload)
		if err != nil || tracked && !execute {
			return workerOperationAck(err)
		}
		if !tracked {
			return handler(ctx, payload, nil)
		}
		complete := func(err error) { c.complete(key, owned, "Completed", "", "", err) }
		response, err := handler(ctx, owned, complete)
		body, _ := response.(map[string]any)
		// Callback handlers return explicit rejection only before work.
		// Unexpected errors/ACKs do not prove termination.
		_, hasStatus := body["status"]
		if err == nil && body["ok"] == false && !hasStatus {
			complete(errWorkerOperation)
		}
		return response, err
	}
}

func (c *workerOperationConnection) synchronous(event string, handler transport.Handler, supported func(any) bool) transport.Handler {
	return func(ctx context.Context, payload any) (any, error) {
		owned, key, tracked, execute, err := c.begin(ctx, event, payload)
		if err != nil || tracked && !execute {
			return workerOperationAck(err)
		}
		if !tracked {
			return handler(ctx, payload)
		}
		if supported != nil && !supported(owned) {
			c.complete(key, owned, "Failed", "", "Tracked operation lacks terminal completion support.", nil)
			return workerOperationAck(errWorkerOperation)
		}
		response, err := handler(ctx, owned)
		body, _ := response.(map[string]any)
		// An accepted/unknown response or transport error proves no termination.
		// Keep the granted claim and server charge in that case.
		_, hasStatus := body["status"]
		if err != nil || hasStatus {
			return response, err
		}
		if ok, exact := body["ok"].(bool); exact {
			var terminalErr error
			if !ok {
				terminalErr = errWorkerOperation
			}
			c.complete(key, owned, "Completed", "", "", terminalErr)
		}
		return response, err
	}
}

func (c *workerOperationConnection) scripts(handler transport.Handler, hostname string) transport.Handler {
	return func(ctx context.Context, payload any) (any, error) {
		owned, key, tracked, execute, err := c.begin(ctx, "quick_job_run", payload)
		if err != nil || tracked && !execute {
			return workerOperationAck(err)
		}
		if !tracked {
			return handler(ctx, payload)
		}
		body := owned.(map[string]any)
		target, _ := body["target_hostname"].(string)
		mode, _ := body["run_mode"].(string)
		validMode := false
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "", "system", "currentuser":
			validMode = true
		}
		if !validMode || strings.TrimSpace(target) != "" && !strings.EqualFold(strings.TrimSpace(target), strings.TrimSpace(hostname)) {
			c.complete(key, owned, "Failed", "", "Script target or run mode rejected before execution.", nil)
			return workerOperationAck(errWorkerOperation)
		}
		// Role emits actual terminal evidence; missing evidence stays unresolved.
		return handler(ctx, owned)
	}
}

// Each Role keeps this connection's emitter, never a mutable global socket.
func (c *workerOperationConnection) Emit(event string, payload any) error {
	if event != "quick_job_result" {
		return c.emitter.Emit(event, payload)
	}
	body, token, job, tracked, err := workerOperationBinding(payload)
	if err != nil || !tracked {
		if err != nil {
			return err
		}
		return c.emitter.Emit(event, payload)
	}
	c.mu.Lock()
	key, exists := c.active[token]
	c.mu.Unlock()
	if !exists || key.JobID != job {
		return errWorkerOperation
	}
	status, _ := body["status"].(string)
	if status == "Success" {
		status = "Completed"
	}
	stdout, _ := body["stdout"].(string)
	stderr, _ := body["stderr"].(string)
	return c.complete(key, payload, status, stdout, stderr, nil)
}

func workerOperationOutput(text string, limit int) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	if len(text) <= limit {
		return text
	}
	const suffix = "\n[truncated]"
	end := limit - len(suffix)
	if end < 0 {
		return ""
	}
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + suffix
}

func (c *workerOperationConnection) complete(key agentconfig.WorkerOperationKey, payload any, status, stdout, stderr string, operationErr error) error {
	if operationErr != nil {
		status, stderr = "Failed", operationErr.Error()
	}
	if status != "Completed" && status != "Failed" && status != "Skipped" {
		return errWorkerOperation
	}
	body, _, _, _, _ := workerOperationBinding(payload)
	contextValue := body["context"]
	stdout, stderr = workerOperationOutput(stdout, 256<<10), workerOperationOutput(stderr, 256<<10)
	var raw []byte
	for {
		var err error
		raw, err = json.Marshal(map[string]any{"job_id": key.JobID, "status": status, "stdout": stdout, "stderr": stderr, "context": contextValue})
		if err != nil {
			return err
		}
		if len(raw) <= agentconfig.WorkerOperationReceiptLimit {
			break
		}
		if stdout == "" && stderr == "" {
			return errWorkerOperation
		}
		stdout, stderr = workerOperationOutput(stdout, len(stdout)/2), workerOperationOutput(stderr, len(stderr)/2)
	}
	c.outbox.delivery.Lock()
	defer c.outbox.delivery.Unlock()
	c.mu.Lock()
	if active, ok := c.active[key.Token]; !ok || active != key {
		c.mu.Unlock()
		return errWorkerOperation
	}
	delete(c.active, key.Token)
	c.mu.Unlock()
	c.outbox.mu.Lock()
	if existing, ok := c.outbox.pending[key.Token]; ok && (existing.key != key || !bytes.Equal(existing.receipt, raw)) {
		c.outbox.mu.Unlock()
		return errWorkerOperation
	}
	c.outbox.pending[key.Token] = workerOperationTerminal{key, raw}
	c.outbox.mu.Unlock()
	if err := c.persistTerminal(key, raw); err != nil {
		c.outbox.logger.Printf("tracked terminal retained for persistence retry; server charge unresolved: %v", err)
		return err
	}
	return c.emitTerminal(context.Background(), agentconfig.WorkerOperationRecord{Key: key, State: "terminal", Receipt: raw})
}

func (c *workerOperationConnection) persistTerminal(key agentconfig.WorkerOperationKey, raw []byte) error {
	if err := c.journal.Complete(key, raw); err != nil {
		return err
	}
	c.outbox.mu.Lock()
	delete(c.outbox.pending, key.Token)
	c.outbox.mu.Unlock()
	return nil
}

func (c *workerOperationConnection) emitTerminal(ctx context.Context, record agentconfig.WorkerOperationRecord) error {
	pending, err := c.journal.Pending()
	if err != nil {
		return err
	}
	found := false
	for _, current := range pending {
		if current.Key == record.Key {
			if !bytes.Equal(current.Receipt, record.Receipt) {
				return errWorkerOperation
			}
			found = true
			break
		}
	}
	if !found {
		return nil
	} // Another serialized delivery already committed it.
	ack, err := c.emitter.EmitWithAck(ctx, "quick_job_result", record.Receipt, 10*time.Second)
	if err != nil {
		return err
	}
	if _, err := workerOperationProof(ack, record.Key, ""); err != nil {
		return err
	}
	if err := c.journal.Commit(record.Key, record.Receipt); err != nil {
		return err
	}
	return c.journal.PruneCommitted()
}

func (c *workerOperationConnection) replay(ctx context.Context) {
	c.outbox.delivery.Lock()
	defer c.outbox.delivery.Unlock()
	if c.journal == nil {
		return
	}
	c.outbox.mu.Lock()
	var retry []workerOperationTerminal
	for _, terminal := range c.outbox.pending {
		retry = append(retry, terminal)
	}
	c.outbox.mu.Unlock()
	for _, terminal := range retry {
		if ctx.Err() != nil {
			return
		}
		if err := c.persistTerminal(terminal.key, terminal.receipt); err != nil {
			c.outbox.logger.Printf("tracked terminal persistence still unavailable: %v", err)
			return
		}
	}
	retained, err := c.journal.Pending()
	if err != nil {
		c.outbox.logger.Printf("tracked terminal replay unavailable: %v", err)
		return
	}
	for _, record := range retained {
		if ctx.Err() != nil {
			return
		}
		if err := c.emitTerminal(ctx, record); err != nil {
			c.outbox.logger.Printf("tracked terminal commit acknowledgement pending: %v", err)
			return
		}
	}
}

func (c *workerOperationConnection) startReplay(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.stopReplay != nil || c.journal == nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			c.replay(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	c.stopReplay = func() { cancel(); <-done }
}

func (c *workerOperationConnection) close() {
	c.mu.Lock()
	c.closed = true
	stop := c.stopReplay
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func workerOperationSynchronousFile(payload any) bool {
	body, _ := payload.(map[string]any)
	action, _ := body["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "roots", "children", "upload_conflicts", "read_text", "write_text", "mkdir", "rename", "move", "paste", "delete":
		return true
	}
	return false
}

func workerOperationUnsupported(any) bool { return false }

func workerOperationSynchronousProcess(payload any) bool {
	body, _ := payload.(map[string]any)
	action, _ := body["action"].(string)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", "list", "snapshot", "terminate", "kill", "end_task":
		return true
	}
	return false
}

func workerOperationSynchronousRegistry(payload any) bool {
	body, _ := payload.(map[string]any)
	action, _ := body["action"].(string)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "roots", "children", "key_create", "key_rename", "key_delete", "value_create", "value_update", "value_delete":
		return true
	}
	return false
}

func (c *workerOperationConnection) bindShell(ctx context.Context, payload map[string]any) (func(error), error) {
	if len(payload) != 4 || payload["type"] != "worker_operation_bind" {
		return nil, errWorkerOperation
	}
	operationContext, ok := payload["context"].(map[string]any)
	if !ok || len(operationContext) != 1 {
		return nil, errWorkerOperation
	}
	owned, key, tracked, execute, err := c.begin(ctx, "vpn_shell_open", payload)
	if err != nil || !tracked || !execute {
		return nil, errWorkerOperation
	}
	return func(err error) { c.complete(key, owned, "Completed", "", "", err) }, nil
}
