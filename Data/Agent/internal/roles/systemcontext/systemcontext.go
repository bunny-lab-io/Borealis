package systemcontext

import (
	"context"
	"encoding/base64"
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

type Runner func(ctx context.Context, scriptType string, content []byte, envMap map[string]string, timeoutSeconds int) scripts.Result

type Role struct {
	Emitter               Emitter
	SigningKeys           SigningKeyStore
	CurrentUserDispatcher CurrentUserDispatcher
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
	return nil, r.emitResult(jobID, status, result.Stdout, result.Stderr, contextPayload)
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
	return r.emitResult(jobID, status, result.Stdout, result.Stderr, contextPayload)
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
