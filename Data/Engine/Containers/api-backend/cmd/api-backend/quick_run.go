package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const quickRunDefaultScriptPath = "Scripts/Internal/Inline.ps1"

var quickRunEnvVarPattern = regexp.MustCompile(`(?i)\$env:(\{)?([A-Za-z0-9_\-]+)(\})?`)

type quickRunStore interface {
	assemblyStore
	deviceProcessStore
	insertQuickRunActivity(ctx context.Context, hostname string, scriptPath string, scriptName string, scriptType string, status string, metadata map[string]any) (int64, error)
	markQuickRunActivityFailed(ctx context.Context, activityID int64, failureText string) error
}

type quickRunTarget struct {
	Hostname string
	Context  deviceProcessContext
}

func registerQuickRunRoutes(mux *http.ServeMux, auth *authService, realtime *operatorRealtimeHub) {
	mux.HandleFunc("POST /api/scripts/quick_run", quickRunHandler(auth, realtime))
}

func quickRunHandler(auth *authService, realtime *operatorRealtimeHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		store, ok := auth.store.(quickRunStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "quick_run_unavailable"})
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		hostnames := quickRunNormalizeHostnames(body["hostnames"])
		if len(hostnames) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing hostnames[]"})
			return
		}
		assemblyGUID := assemblyCoerceGUID(body["assembly_guid"])
		relPath := quickRunNormalizeScriptRelPath(body["script_path"])
		if relPath == "" && assemblyGUID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Missing script_path or assembly_guid"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		targets, inaccessible, status, err := quickRunResolveTargets(ctx, store, profile, hostnames)
		if err != nil {
			if len(inaccessible) > 0 {
				writeJSON(w, http.StatusForbidden, map[string]any{
					"error":     "out_of_scope_hostnames",
					"message":   "One or more selected devices is outside your assigned sites.",
					"hostnames": inaccessible,
				})
				return
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		item, resolvedPath, resolvedGUID, status, err := quickRunResolveAssembly(ctx, store, assemblyGUID, relPath)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		payloadDoc := quickRunPayloadMap(item)
		if payloadDoc == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "Script not found"})
			return
		}
		doc := quickRunLoadAssemblyDocument(resolvedPath, "powershell", payloadDoc)
		if cleanText(doc["name"]) == "" {
			doc["name"] = firstNonEmptyAny(item["display_name"], item["name"], "Script")
		}
		scriptType := quickRunNormalizeScriptType(doc["type"])
		if !quickRunSupportedScriptType(scriptType) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": fmt.Sprintf("Unsupported script type '%s'. Agent quick jobs currently support PowerShell, Batch, and Bash.", scriptType),
			})
			return
		}
		overrides := quickRunVariableOverrides(body["variable_values"])
		runMode := quickRunNormalizeRunMode(body["run_mode"])
		result, code, response := dispatchQuickRun(r.Context(), auth, realtime, store, targets, doc, resolvedPath, firstText(cleanText(profile.Username), "unknown"), overrides, map[string]any{
			"run_mode":          runMode,
			"session_target":    body["session_target"],
			"target_session_id": body["target_session_id"],
			"admin_user":        cleanText(body["admin_user"]),
			"admin_pass":        cleanText(body["admin_pass"]),
			"assembly_source":   "runtime",
			"assembly_guid":     resolvedGUID,
		})
		if code != 0 {
			writeJSON(w, code, response)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": result})
	}
}

func quickRunResolveTargets(ctx context.Context, store quickRunStore, profile operatorProfile, hostnames []string) ([]quickRunTarget, []string, int, error) {
	targets := make([]quickRunTarget, 0, len(hostnames))
	inaccessible := []string{}
	for _, hostname := range hostnames {
		snapshot, status, err := store.loadDeviceProcessContext(ctx, profile, hostname)
		if err != nil {
			if status == http.StatusNotFound {
				inaccessible = append(inaccessible, hostname)
				continue
			}
			return nil, inaccessible, status, err
		}
		targets = append(targets, quickRunTarget{Hostname: firstText(snapshot.Hostname, hostname), Context: snapshot})
	}
	if len(inaccessible) > 0 {
		return nil, inaccessible, http.StatusForbidden, errors.New("out_of_scope_hostnames")
	}
	return targets, nil, http.StatusOK, nil
}

func quickRunResolveAssembly(ctx context.Context, store quickRunStore, assemblyGUID string, relPath string) (map[string]any, string, string, int, error) {
	if assemblyGUID != "" {
		item, found, err := store.getAssembly(ctx, assemblyGUID, true)
		if err != nil {
			return nil, "", "", http.StatusInternalServerError, err
		}
		if !found {
			return nil, "", "", http.StatusNotFound, errors.New("Script not found")
		}
		resolvedPath := firstText(relPath, quickRunItemPath(item), quickRunDefaultScriptPath)
		return item, resolvedPath, assemblyGUID, http.StatusOK, nil
	}
	items, _, err := store.listAssemblies(ctx, assemblyListFilter{})
	if err != nil {
		return nil, "", "", http.StatusInternalServerError, err
	}
	target := quickRunNormalizeScriptRelPath(relPath)
	for _, item := range items {
		itemPath := quickRunItemPath(item)
		if itemPath == "" {
			continue
		}
		if strings.EqualFold(quickRunNormalizeScriptRelPath(itemPath), target) {
			guid := assemblyCoerceGUID(firstNonEmptyAny(item["assembly_guid"], item["assembly_id"]))
			if guid == "" {
				break
			}
			fullItem, found, err := store.getAssembly(ctx, guid, true)
			if err != nil {
				return nil, "", "", http.StatusInternalServerError, err
			}
			if !found {
				break
			}
			return fullItem, target, guid, http.StatusOK, nil
		}
	}
	return nil, "", "", http.StatusNotFound, errors.New("Script not found")
}

func quickRunItemPath(item map[string]any) string {
	return firstText(
		quickRunNormalizeScriptRelPath(item["source_path"]),
		quickRunNormalizeScriptRelPath(item["virtual_path"]),
		quickRunNormalizeScriptRelPath(item["path"]),
	)
}

func dispatchQuickRun(ctx context.Context, auth *authService, realtime *operatorRealtimeHub, store quickRunStore, targets []quickRunTarget, doc map[string]any, scriptPath string, requestedBy string, overrides map[string]any, options map[string]any) ([]map[string]any, int, map[string]any) {
	scriptType := quickRunNormalizeScriptType(doc["type"])
	envMap, variables, literalLookup := quickRunPrepareVariableContext(quickRunVariables(doc["variables"]), overrides)
	normalizedScript := quickRunRewriteScriptForDispatch(cleanText(doc["script"]), scriptType, literalLookup)
	scriptBytes := []byte(normalizedScript)
	encodedContent := base64.StdEncoding.EncodeToString(scriptBytes)
	signer, err := loadOrCreateScriptSigner()
	if err != nil || signer == nil || len(signer.privateKey) == 0 {
		message := "script signer unavailable"
		if err != nil {
			message = err.Error()
		}
		return nil, http.StatusInternalServerError, map[string]any{"error": "dispatch_failed", "message": message}
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(signer.privateKey, scriptBytes))
	signingKey := scriptSigningKeyB64(signer)
	timeoutSeconds := maxInt64(0, coerceInt64(firstNonEmpty(doc["timeout_seconds"], 0)))
	friendlyName := cleanText(doc["name"])
	if friendlyName == "" {
		friendlyName = filepath.Base(scriptPath)
	}
	if strings.TrimSpace(scriptPath) == "" {
		scriptPath = quickRunDefaultScriptPath
	}
	runMode := quickRunNormalizeRunMode(options["run_mode"])
	activityMetadata := map[string]any{
		"assembly_source": cleanText(options["assembly_source"]),
		"requested_by":    requestedBy,
	}
	if guid := assemblyCoerceGUID(options["assembly_guid"]); guid != "" {
		activityMetadata["assembly_guid"] = guid
	}
	results := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		insertCtx, insertCancel := requestTimeout(ctx, auth)
		activityID, err := store.insertQuickRunActivity(insertCtx, target.Hostname, scriptPath, friendlyName, scriptType, "Running", activityMetadata)
		insertCancel()
		if err != nil {
			return nil, http.StatusInternalServerError, map[string]any{"error": "dispatch_failed", "message": err.Error()}
		}
		payload := map[string]any{
			"job_id":          activityID,
			"target_hostname": target.Hostname,
			"script_type":     scriptType,
			"script_name":     friendlyName,
			"script_path":     scriptPath,
			"script_content":  encodedContent,
			"script_encoding": "base64",
			"environment":     envMap,
			"variables":       variables,
			"timeout_seconds": timeoutSeconds,
			"files":           quickRunFiles(doc["files"]),
			"run_mode":        runMode,
			"admin_user":      cleanText(options["admin_user"]),
			"admin_pass":      cleanText(options["admin_pass"]),
			"signature":       signature,
			"sig_alg":         "ed25519",
			"signing_key":     signingKey,
			"context": map[string]any{
				"assembly_source":   cleanText(options["assembly_source"]),
				"activity_metadata": activityMetadata,
			},
		}
		for key, value := range quickRunCurrentUserDispatchFields(runMode, options["session_target"], options["target_session_id"]) {
			payload[key] = value
		}
		if guid := assemblyCoerceGUID(options["assembly_guid"]); guid != "" {
			payload["context"].(map[string]any)["assembly_guid"] = guid
		}
		result, _, workerErr := emitWorkerHostServiceEvent(ctx, auth, target.Context.Route, map[string]any{
			"hostname":     target.Hostname,
			"service_mode": runMode,
			"event_name":   "quick_job_run",
			"payload":      payload,
		}, 6*time.Second)
		if workerErr != nil || (!boolFromAny(result["emitted"]) && !boolFromAny(result["queued"])) {
			failure := fmt.Sprintf("No %s agent socket is registered for host %s; unable to dispatch quick job.", runMode, target.Hostname)
			if workerErr != nil {
				failure = firstText(cleanText(workerErr["message"]), cleanText(workerErr["error"]), failure)
			}
			_ = store.markQuickRunActivityFailed(ctx, activityID, failure)
			quickRunEmitActivityChanged(realtime, target.Hostname, activityID, "updated")
			results = append(results, map[string]any{"hostname": target.Hostname, "job_id": activityID, "status": "Failed", "error": failure})
			continue
		}
		quickRunEmitActivityChanged(realtime, target.Hostname, activityID, "created")
		results = append(results, map[string]any{"hostname": target.Hostname, "job_id": activityID, "status": "Running"})
	}
	return results, 0, nil
}

func quickRunEmitActivityChanged(realtime *operatorRealtimeHub, hostname string, activityID int64, change string) {
	if realtime == nil {
		return
	}
	_ = realtime.emit("device_activity_changed", map[string]any{
		"hostname":    hostname,
		"activity_id": activityID,
		"change":      change,
		"source":      "quick_job",
	})
}

func quickRunPayloadMap(item map[string]any) map[string]any {
	if payload, ok := item["payload_json"].(map[string]any); ok {
		return copyMap(payload)
	}
	switch raw := item["payload"].(type) {
	case map[string]any:
		return copyMap(raw)
	case string:
		var decoded map[string]any
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil && decoded != nil {
			return decoded
		}
	}
	return nil
}

func quickRunLoadAssemblyDocument(sourceIdentifier string, defaultType string, payload map[string]any) map[string]any {
	baseName := strings.TrimSuffix(filepath.Base(strings.ReplaceAll(sourceIdentifier, "\\", "/")), filepath.Ext(sourceIdentifier))
	doc := map[string]any{
		"name":            baseName,
		"description":     "",
		"category":        "script",
		"type":            defaultType,
		"script":          "",
		"variables":       []map[string]any{},
		"files":           []map[string]any{},
		"timeout_seconds": int64(3600),
	}
	if strings.EqualFold(defaultType, "ansible") {
		doc["category"] = "application"
	}
	if payload == nil {
		return doc
	}
	doc["name"] = firstText(cleanText(payload["name"]), cleanText(payload["display_name"]), cleanText(payload["tab_name"]), cleanText(doc["name"]))
	doc["description"] = cleanText(firstNonEmptyAny(payload["description"], payload["summary"]))
	if category := strings.ToLower(cleanText(payload["category"])); category == "application" || category == "script" {
		doc["category"] = category
	}
	if typ := strings.ToLower(cleanText(firstNonEmptyAny(payload["type"], payload["script_type"], defaultType))); typ == "powershell" || typ == "batch" || typ == "bash" || typ == "ansible" {
		doc["type"] = typ
	}
	script := ""
	if lines, ok := payload["script_lines"].([]any); ok {
		values := make([]string, 0, len(lines))
		for _, line := range lines {
			values = append(values, fmt.Sprint(line))
		}
		script = strings.Join(values, "\n")
	} else {
		script = cleanText(firstNonEmptyAny(payload["script"], payload["content"]))
	}
	encodingHint := strings.ToLower(cleanText(firstNonEmptyAny(payload["script_encoding"], payload["scriptEncoding"])))
	doc["script"] = quickRunDecodeScriptContent(script, encodingHint)
	if encodingHint == "base64" || encodingHint == "b64" || encodingHint == "base-64" {
		doc["script_encoding"] = "base64"
	} else if decoded := quickRunDecodeBase64Text(script); decoded != nil {
		doc["script_encoding"] = "base64"
		doc["script"] = strings.ReplaceAll(*decoded, "\r\n", "\n")
	} else {
		doc["script_encoding"] = "plain"
	}
	timeout := coerceInt64(firstNonEmpty(payload["timeout_seconds"], payload["timeout"], 3600))
	if timeout < 0 {
		timeout = 0
	}
	doc["timeout_seconds"] = timeout
	doc["variables"] = quickRunNormalizeVariables(payload["variables"])
	doc["files"] = quickRunNormalizeFiles(payload["files"])
	return doc
}

func quickRunDecodeBase64Text(value any) *string {
	text := cleanText(value)
	if text == "" {
		empty := ""
		return &empty
	}
	cleaned := strings.Join(strings.Fields(text), "")
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil
	}
	result := string(decoded)
	return &result
}

func quickRunDecodeScriptContent(value any, encodingHint string) string {
	text := cleanText(value)
	lower := strings.ToLower(strings.TrimSpace(encodingHint))
	if lower == "base64" || lower == "b64" || lower == "base-64" {
		if decoded := quickRunDecodeBase64Text(text); decoded != nil {
			return strings.ReplaceAll(*decoded, "\r\n", "\n")
		}
	}
	if decoded := quickRunDecodeBase64Text(text); decoded != nil {
		return strings.ReplaceAll(*decoded, "\r\n", "\n")
	}
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func quickRunNormalizeVariables(value any) []map[string]any {
	raw, _ := value.([]any)
	result := []map[string]any{}
	for _, item := range raw {
		varMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := cleanText(firstNonEmptyAny(varMap["name"], varMap["key"]))
		if name == "" {
			continue
		}
		vtype := strings.ToLower(firstText(cleanText(varMap["type"]), "string"))
		if vtype != "string" && vtype != "number" && vtype != "boolean" && vtype != "credential" {
			vtype = "string"
		}
		result = append(result, map[string]any{
			"name":        name,
			"label":       cleanText(varMap["label"]),
			"type":        vtype,
			"default":     firstNonEmptyAny(varMap["default"], varMap["default_value"]),
			"required":    boolFromAny(varMap["required"]),
			"description": cleanText(varMap["description"]),
		})
	}
	return result
}

func quickRunNormalizeFiles(value any) []map[string]any {
	raw, _ := value.([]any)
	result := []map[string]any{}
	for _, item := range raw {
		fileMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := cleanText(firstNonEmptyAny(fileMap["file_name"], fileMap["name"]))
		data := cleanText(fileMap["data"])
		if name == "" || data == "" {
			continue
		}
		result = append(result, map[string]any{
			"file_name": name,
			"size":      coerceInt64(fileMap["size"]),
			"mime_type": cleanText(firstNonEmptyAny(fileMap["mime_type"], fileMap["mimeType"])),
			"data":      data,
		})
	}
	return result
}

func quickRunPrepareVariableContext(docVariables []map[string]any, overrides map[string]any) (map[string]string, []map[string]any, map[string]string) {
	envMap := map[string]string{}
	variables := []map[string]any{}
	literalLookup := map[string]string{}
	docNames := map[string]bool{}
	for _, variable := range docVariables {
		name := cleanText(variable["name"])
		if name == "" {
			continue
		}
		docNames[name] = true
		canonical := quickRunCanonicalEnvKey(name)
		vtype := strings.ToLower(firstText(cleanText(variable["type"]), "string"))
		finalValue := firstNonEmptyAny(variable["value"], variable["default"], variable["defaultValue"], variable["default_value"])
		if override, ok := overrides[name]; ok {
			finalValue = override
		}
		if canonical != "" {
			envMap[canonical] = quickRunEnvString(finalValue)
			literalLookup[canonical] = quickRunPowerShellLiteral(finalValue, vtype)
		}
		next := copyMap(variable)
		if _, ok := overrides[name]; ok {
			next["value"] = overrides[name]
		}
		variables = append(variables, next)
	}
	for name, value := range overrides {
		if docNames[name] {
			continue
		}
		canonical := quickRunCanonicalEnvKey(name)
		if canonical != "" {
			envMap[canonical] = quickRunEnvString(value)
			literalLookup[canonical] = quickRunPowerShellLiteral(value, "string")
		}
		variables = append(variables, map[string]any{"name": name, "value": value, "type": "string"})
	}
	envMap = quickRunExpandEnvAliases(envMap, variables)
	return envMap, variables, literalLookup
}

func quickRunRewriteScriptForDispatch(content string, scriptType string, literalLookup map[string]string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if scriptType != "powershell" || len(literalLookup) == 0 {
		return normalized
	}
	return quickRunEnvVarPattern.ReplaceAllStringFunc(normalized, func(match string) string {
		parts := quickRunEnvVarPattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		canonical := quickRunCanonicalEnvKey(parts[2])
		if literal, ok := literalLookup[canonical]; ok {
			return literal
		}
		return match
	})
}

func quickRunCanonicalEnvKey(value any) string {
	text := cleanText(value)
	if text == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range text {
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' {
			builder.WriteRune(ch)
		} else {
			builder.WriteByte('_')
		}
	}
	return strings.ToUpper(builder.String())
}

func quickRunEnvString(value any) string {
	if typed, ok := value.(bool); ok {
		if typed {
			return "True"
		}
		return "False"
	}
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func quickRunPowerShellLiteral(value any, varType string) string {
	switch strings.ToLower(cleanText(varType)) {
	case "boolean":
		truthy := boolFromAny(value)
		text := strings.ToLower(cleanText(value))
		if text == "false" || text == "0" || text == "no" || text == "n" || text == "off" || text == "" {
			truthy = false
		}
		if truthy {
			return "$true"
		}
		return "$false"
	case "number":
		if cleanText(value) == "" {
			return "0"
		}
		return cleanText(value)
	default:
		return "'" + strings.ReplaceAll(fmt.Sprint(firstNonEmptyAny(value, "")), "'", "''") + "'"
	}
}

func quickRunExpandEnvAliases(envMap map[string]string, variables []map[string]any) map[string]string {
	expanded := map[string]string{}
	for key, value := range envMap {
		expanded[key] = value
	}
	validName := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	for _, variable := range variables {
		name := cleanText(variable["name"])
		if name == "" {
			continue
		}
		canonical := quickRunCanonicalEnvKey(name)
		value, ok := expanded[canonical]
		if !ok {
			continue
		}
		alias := regexp.MustCompile(`[^A-Za-z0-9_]`).ReplaceAllString(name, "_")
		if alias != "" {
			if _, exists := expanded[alias]; !exists {
				expanded[alias] = value
			}
		}
		if alias != name && validName.MatchString(name) {
			if _, exists := expanded[name]; !exists {
				expanded[name] = value
			}
		}
	}
	return expanded
}

func quickRunCurrentUserDispatchFields(runMode string, sessionTarget any, targetSessionID any) map[string]any {
	normalized := quickRunNormalizeRunMode(runMode)
	payload := map[string]any{"target_context": normalized}
	if normalized != "currentuser" {
		return payload
	}
	target := strings.ToLower(strings.ReplaceAll(cleanText(sessionTarget), "-", "_"))
	switch target {
	case "specific", "specific_session", "single", "session":
		payload["session_target"] = "specific_session"
	default:
		payload["session_target"] = "all_active_sessions"
	}
	sessionID := coerceInt64(targetSessionID)
	if payload["session_target"] == "specific_session" && sessionID > 0 {
		payload["target_session_id"] = sessionID
	}
	return payload
}

func quickRunNormalizeHostnames(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	result := []string{}
	for _, item := range raw {
		host := cleanText(item)
		if host == "" {
			continue
		}
		key := strings.ToLower(host)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, host)
	}
	return result
}

func quickRunNormalizeScriptRelPath(value any) string {
	text := strings.ReplaceAll(cleanText(value), "\\", "/")
	if text == "" {
		return ""
	}
	segments := []string{}
	for _, part := range strings.Split(text, "/") {
		candidate := strings.TrimSpace(part)
		if candidate == "" || candidate == "." {
			continue
		}
		if candidate == ".." {
			return ""
		}
		segments = append(segments, candidate)
	}
	if len(segments) == 0 {
		return ""
	}
	if !strings.EqualFold(segments[0], "Scripts") {
		segments = append([]string{"Scripts"}, segments...)
	} else {
		segments[0] = "Scripts"
	}
	return strings.Join(segments, "/")
}

func quickRunNormalizeScriptType(value any) string {
	text := strings.ToLower(firstText(cleanText(value), "powershell"))
	if text == "" {
		return "powershell"
	}
	return text
}

func quickRunSupportedScriptType(scriptType string) bool {
	switch scriptType {
	case "powershell", "batch", "bash":
		return true
	default:
		return false
	}
}

func quickRunNormalizeRunMode(value any) string {
	mode := strings.ToLower(cleanText(value))
	if mode == "current_user" {
		mode = "currentuser"
	}
	if mode == "currentuser" {
		return "currentuser"
	}
	return "system"
}

func quickRunVariableOverrides(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{}
	for key, val := range raw {
		name := cleanText(key)
		if name != "" {
			out[name] = val
		}
	}
	return out
}

func quickRunVariables(value any) []map[string]any {
	if typed, ok := value.([]map[string]any); ok {
		return typed
	}
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := []map[string]any{}
	for _, item := range raw {
		if itemMap, ok := item.(map[string]any); ok {
			out = append(out, itemMap)
		}
	}
	return out
}

func quickRunFiles(value any) []map[string]any {
	if typed, ok := value.([]map[string]any); ok {
		return typed
	}
	raw, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, item := range raw {
		if itemMap, ok := item.(map[string]any); ok {
			out = append(out, itemMap)
		}
	}
	return out
}

func (s *postgresOperatorStore) insertQuickRunActivity(ctx context.Context, hostname string, scriptPath string, scriptName string, scriptType string, status string, metadata map[string]any) (int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	var startedAt any
	if strings.EqualFold(status, "running") {
		startedAt = now
	}
	var activityID int64
	err = conn.QueryRowContext(ctx, `
		INSERT INTO engine.activity_history(
			hostname, script_path, script_name, script_type, ran_at, status,
			stdout, stderr, queue_lane, activity_kind, metadata_json,
			started_at, updated_at, finished_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, cleanText(hostname), cleanText(scriptPath), cleanText(scriptName), cleanText(scriptType), now, firstText(cleanText(status), "Running"), "", "", "", "", string(metadataJSON), startedAt, now, nil).Scan(&activityID)
	return activityID, err
}

func (s *postgresOperatorStore) markQuickRunActivityFailed(ctx context.Context, activityID int64, failureText string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	now := time.Now().Unix()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.activity_history
		   SET status=$1,
		       stderr=$2,
		       updated_at=$3,
		       finished_at=$4
		 WHERE id=$5
	`, "Failed", failureText, now, now, activityID)
	return err
}
