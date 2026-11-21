package systemcontext

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
)

type Emitter interface {
	Emit(event string, payload any) error
}

type SigningKeyStore interface {
	LoadServerSigningKey() string
	StoreServerSigningKey(value string) error
}

type CurrentUserDispatcher interface {
	DispatchCurrentUserQuickJob(ctx context.Context, payload map[string]any) (scripts.Result, bool, string)
}

// Tracked current-user work requires explicit owned-process completion.
// A launched but unconfirmed process returns accepted without calling complete.
type CurrentUserCompletionDispatcher interface {
	DispatchCurrentUserQuickJobWithCompletion(context.Context, map[string]any, func(scripts.Result)) (bool, string)
}

type SoftwareRefreshRequester interface {
	RequestRefresh(reason string)
}

type Runner func(ctx context.Context, scriptType string, content []byte, envMap map[string]string, timeoutSeconds int) scripts.Result

type Role struct {
	Emitter               Emitter
	SigningKeys           SigningKeyStore
	CurrentUserDispatcher CurrentUserDispatcher
	SoftwareRefresh       SoftwareRefreshRequester
	Runner                Runner
	Hostname              string
	mu                    sync.Mutex
}

func New(emitter Emitter, signingKeys SigningKeyStore, dispatcher CurrentUserDispatcher) *Role {
	hostname, _ := os.Hostname()
	return &Role{
		Emitter:               emitter,
		SigningKeys:           signingKeys,
		CurrentUserDispatcher: dispatcher,
		Runner:                scripts.Run,
		Hostname:              hostname,
	}
}

func (r *Role) HandleQuickJob(ctx context.Context, payloadAny any) (any, error) {
	payload, ok := payloadAny.(map[string]any)
	if !ok {
		return nil, nil
	}
	target := strings.ToLower(strings.TrimSpace(asString(payload["target_hostname"])))
	if target != "" && target != strings.ToLower(strings.TrimSpace(r.Hostname)) {
		return nil, nil
	}
	jobID := payload["job_id"]
	runMode := normalizeRunMode(asString(payload["run_mode"]))
	if runMode == "currentuser" {
		return r.dispatchCurrentUser(ctx, payload)
	}
	if runMode != "system" {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return nil, r.executeSystem(ctx, payload, jobID)
}

func (r *Role) dispatchCurrentUser(ctx context.Context, payload map[string]any) (any, error) {
	contextPayload := contextBlock(payload)
	jobID := payload["job_id"]
	if r.CurrentUserDispatcher == nil {
		return nil, r.emitResult(jobID, "Failed", "", "Current-user dispatch broker is unavailable.", contextPayload)
	}
	if failure := r.validateSignedScriptPayload(payload); failure != "" {
		return nil, r.emitResult(jobID, "Failed", "", failure, contextPayload)
	}
	if _, tracked := contextPayload["worker_operation"]; tracked {
		return r.dispatchTrackedCurrentUser(ctx, payload, contextPayload)
	}
	if err := r.emitProgress(jobID, "Running", payload); err != nil {
		return nil, err
	}
	result, accepted, detail := r.CurrentUserDispatcher.DispatchCurrentUserQuickJob(ctx, payload)
	if !accepted {
		if detail == "" {
			detail = "Current-user dispatch failed."
		}
		return nil, r.emitResult(jobID, "Failed", "", detail, contextPayload)
	}
	status := "Failed"
	if result.ReturnCode == 0 {
		status = "Success"
	}
	err := r.emitResult(jobID, status, result.Stdout, result.Stderr, contextPayload)
	r.requestSoftwareRefresh(payload, status)
	return nil, err
}

func (r *Role) dispatchTrackedCurrentUser(ctx context.Context, payload map[string]any, operationContext map[string]any) (any, error) {
	// Freeze nested correlation and the original emitter before handing mutable
	// request data to the dispatcher. Callback cannot adopt a replacement role.
	raw, err := json.Marshal(operationContext)
	if err != nil {
		return nil, err
	}
	var retained map[string]any
	if err := json.Unmarshal(raw, &retained); err != nil {
		return nil, err
	}
	jobID, emitter := payload["job_id"], r.Emitter
	refresh, lane := r.SoftwareRefresh, queueLane(payload)
	if emitter == nil {
		return nil, fmt.Errorf("Current-user completion emitter is unavailable.")
	}
	var once sync.Once
	emitTerminal := func(result scripts.Result, refreshAfterCompletion bool) {
		once.Do(func() {
			status := "Failed"
			if result.ReturnCode == 0 {
				status = "Success"
			}
			_ = emitter.Emit("quick_job_result", map[string]any{"job_id": jobID, "status": status, "stdout": result.Stdout, "stderr": result.Stderr, "context": retained})
			if refreshAfterCompletion && refresh != nil && strings.EqualFold(lane, "software_management") {
				refresh.RequestRefresh("quick_job:" + strings.ToLower(status))
			}
		})
	}
	complete := func(result scripts.Result) { emitTerminal(result, true) }
	dispatcher, supported := r.CurrentUserDispatcher.(CurrentUserCompletionDispatcher)
	if !supported {
		emitTerminal(scripts.Result{ReturnCode: -1, Stderr: "Current-user terminal completion is unavailable."}, false)
		return nil, nil
	}
	if err := emitter.Emit("quick_job_progress", map[string]any{"job_id": jobID, "status": "Running", "context": retained}); err != nil {
		return nil, err
	}
	accepted, detail := dispatcher.DispatchCurrentUserQuickJobWithCompletion(ctx, payload, complete)
	if !accepted {
		if detail == "" {
			detail = "Current-user dispatch failed before launch."
		}
		emitTerminal(scripts.Result{ReturnCode: -1, Stderr: detail}, false)
	}
	return nil, nil
}

func (r *Role) executeSystem(ctx context.Context, payload map[string]any, jobID any) error {
	contextPayload := contextBlock(payload)
	scriptBytes, ok := scripts.DecodeScriptBytes(payload["script_content"], asString(payload["script_encoding"]))
	if !ok {
		return r.emitResult(jobID, "Failed", "", "Invalid script payload (unable to decode)", contextPayload)
	}
	if failure := r.validateSignatureForScript(payload, scriptBytes); failure != "" {
		return r.emitResult(jobID, "Failed", "", failure, contextPayload)
	}
	timeoutSeconds := asInt(payload["timeout_seconds"])
	envMap := scripts.BuildEnvMap(mapStringAny(payload["environment"]), listMapStringAny(payload["variables"]))
	if err := r.emitProgress(jobID, "Running", payload); err != nil {
		return err
	}
	runner := r.Runner
	if runner == nil {
		runner = scripts.Run
	}
	result := runner(ctx, strings.ToLower(strings.TrimSpace(asString(payload["script_type"]))), scriptBytes, envMap, timeoutSeconds)
	status := "Failed"
	if result.ReturnCode == 0 {
		status = "Success"
	}
	err := r.emitResult(jobID, status, result.Stdout, result.Stderr, contextPayload)
	r.requestSoftwareRefresh(payload, status)
	return err
}

func (r *Role) validateSignedScriptPayload(payload map[string]any) string {
	scriptBytes, ok := scripts.DecodeScriptBytes(payload["script_content"], asString(payload["script_encoding"]))
	if !ok {
		return "Invalid script payload (unable to decode)"
	}
	return r.validateSignatureForScript(payload, scriptBytes)
}

func (r *Role) validateSignatureForScript(payload map[string]any, scriptBytes []byte) string {
	sigAlg := strings.ToLower(strings.TrimSpace(asString(payload["sig_alg"])))
	if sigAlg == "" {
		sigAlg = "ed25519"
	}
	if sigAlg != "ed25519" && sigAlg != "eddsa" {
		return "Unsupported script signature algorithm: " + sigAlg
	}
	signatureB64 := strings.TrimSpace(asString(payload["signature"]))
	if signatureB64 == "" {
		return "Missing script signature; rejecting payload"
	}
	signingKey := strings.TrimSpace(asString(payload["signing_key"]))
	if !r.verifySignature(scriptBytes, signatureB64, signingKey) {
		return "Rejected script payload due to invalid signature"
	}
	return ""
}

func (r *Role) verifySignature(scriptBytes []byte, signatureB64 string, signingKeyHint string) bool {
	candidates := []string{}
	if signingKeyHint != "" {
		candidates = append(candidates, signingKeyHint)
	}
	if r.SigningKeys != nil {
		stored := strings.TrimSpace(r.SigningKeys.LoadServerSigningKey())
		if stored != "" && stored != signingKeyHint {
			candidates = append(candidates, stored)
		}
	}
	for _, candidate := range candidates {
		if scripts.VerifySignature(scriptBytes, signatureB64, candidate) {
			if r.SigningKeys != nil && candidate != "" {
				_ = r.SigningKeys.StoreServerSigningKey(candidate)
			}
			return true
		}
	}
	return false
}

func (r *Role) emitProgress(jobID any, status string, payload map[string]any) error {
	if r.Emitter == nil {
		return nil
	}
	progress := map[string]any{
		"job_id": jobID,
		"status": status,
	}
	if contextPayload := contextBlock(payload); contextPayload != nil {
		progress["context"] = contextPayload
	}
	if queueLane := queueLane(payload); queueLane != "" {
		progress["queue_lane"] = queueLane
	}
	return r.Emitter.Emit("quick_job_progress", progress)
}

func (r *Role) emitResult(jobID any, status string, stdout string, stderr string, contextPayload map[string]any) error {
	if r.Emitter == nil {
		return nil
	}
	result := map[string]any{
		"job_id": jobID,
		"status": status,
		"stdout": stdout,
		"stderr": stderr,
	}
	if contextPayload != nil {
		result["context"] = contextPayload
	}
	return r.Emitter.Emit("quick_job_result", result)
}

func (r *Role) requestSoftwareRefresh(payload map[string]any, status string) {
	if r.SoftwareRefresh == nil {
		return
	}
	if !strings.EqualFold(queueLane(payload), "software_management") {
		return
	}
	reason := "quick_job:" + strings.ToLower(strings.TrimSpace(status))
	r.SoftwareRefresh.RequestRefresh(reason)
}

func normalizeRunMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "system":
		return "system"
	case "currentuser", "current_user", "interactive", "user":
		return "currentuser"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func contextBlock(payload map[string]any) map[string]any {
	if value, ok := payload["context"].(map[string]any); ok {
		return value
	}
	return nil
}

func queueLane(payload map[string]any) string {
	contextPayload := contextBlock(payload)
	if contextPayload == nil {
		return ""
	}
	return strings.TrimSpace(asString(contextPayload["queue_lane"]))
}

func mapStringAny(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return nil
}

func listMapStringAny(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]map[string]any); ok {
			return typed
		}
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if mapped, ok := item.(map[string]any); ok {
			out = append(out, mapped)
		}
	}
	return out
}

func asString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func asInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		var parsed int
		_, _ = fmt.Sscanf(v, "%d", &parsed)
		return parsed
	default:
		return 0
	}
}

func EncodeScriptForTest(value []byte) string {
	return base64.StdEncoding.EncodeToString(value)
}
