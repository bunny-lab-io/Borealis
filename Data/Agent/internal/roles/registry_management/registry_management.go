package registrymanagement

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxRegistryBinaryBytes = 256 * 1024

type Manager struct {
	hostname       string
	serviceMode    string
	mu             sync.Mutex
	listenerReady  bool
	lastError      string
	lastMutationAt int64
}

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

type registryError struct {
	Code    string
	Message string
}

type registryPath struct {
	Hive    string
	SubPath string
	Path    string
	Parts   []string
}

type registryValueInput struct {
	Name string
	Type string
	Data any
}

func (e registryError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

func New(hostname string, serviceMode string) *Manager {
	return &Manager{
		hostname:      strings.TrimSpace(hostname),
		serviceMode:   normalizeServiceMode(serviceMode),
		listenerReady: true,
	}
}

func (m *Manager) HandleRequest(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Registry request payload must be an object."), nil
	}
	if !m.matchesTarget(body) {
		return errorResponse("not_for_host", "The registry request targeted another device."), nil
	}
	action := strings.ToLower(cleanText(body["action"]))
	response, err := m.handleAction(ctx, action, body)
	if err != nil {
		rerr := normalizeError(err)
		m.setLastError(rerr.Message)
		return errorResponse(rerr.Code, rerr.Message), nil
	}
	m.setLastError("")
	return response, nil
}

func (m *Manager) Health() RoleHealth {
	details := map[string]any{
		"running_status": "Ready",
		"listener_state": "Registered",
		"runtime":        "go",
		"platform":       runtime.GOOS,
		"service_mode":   m.serviceMode,
	}
	if runtime.GOOS != "windows" {
		details["running_status"] = "Unsupported"
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     "Registry Management is supported on Windows agents only.",
			Details:    details,
		}
	}
	m.mu.Lock()
	lastError := m.lastError
	lastMutationAt := m.lastMutationAt
	ready := m.listenerReady
	m.mu.Unlock()
	details["last_mutation_at"] = strconv.FormatInt(lastMutationAt, 10)
	if !ready {
		return RoleHealth{
			Status:     "unhealthy",
			StatusCode: "unhealthy",
			Detail:     "Registry-management listener is not registered.",
			Details:    details,
		}
	}
	if strings.TrimSpace(lastError) != "" {
		details["last_error"] = lastError
		return RoleHealth{
			Status:     "recovering",
			StatusCode: "recovering",
			Detail:     lastError,
			Details:    details,
		}
	}
	return RoleHealth{
		Status:     "healthy",
		StatusCode: "healthy",
		Detail:     "Registry-management listener is ready.",
		Details:    details,
	}
}

func (m *Manager) handleAction(ctx context.Context, action string, payload map[string]any) (any, error) {
	switch action {
	case "roots":
		return registryRoots(ctx)
	case "children":
		pathValue, err := parseRegistryPath(payload["path"])
		if err != nil {
			return nil, err
		}
		return registryChildren(ctx, pathValue)
	case "key_create":
		parentPath, err := parseRegistryPath(payload["parent_path"])
		if err != nil {
			return nil, err
		}
		name, err := validateRegistryChildName(payload["name"])
		if err != nil {
			return nil, err
		}
		result, err := registryCreateKey(ctx, parentPath, name)
		m.recordMutation(err)
		return result, err
	case "key_rename":
		pathValue, err := parseRegistryPath(payload["path"])
		if err != nil {
			return nil, err
		}
		name, err := validateRegistryChildName(payload["new_name"])
		if err != nil {
			return nil, err
		}
		result, err := registryRenameKey(ctx, pathValue, name)
		m.recordMutation(err)
		return result, err
	case "key_delete":
		pathValue, err := parseRegistryPath(payload["path"])
		if err != nil {
			return nil, err
		}
		confirmPath := normalizeRegistryPathText(payload["confirm_path"])
		if confirmPath == "" || !strings.EqualFold(confirmPath, pathValue.Path) {
			return nil, newError("confirmation_required", "Registry key deletion requires matching confirm_path.")
		}
		result, err := registryDeleteKey(ctx, pathValue, boolFromAny(payload["recursive"]))
		m.recordMutation(err)
		return result, err
	case "value_create", "value_update":
		pathValue, err := parseRegistryPath(payload["path"])
		if err != nil {
			return nil, err
		}
		input, err := normalizeRegistryValueInput(payload)
		if err != nil {
			return nil, err
		}
		result, err := registrySetValue(ctx, pathValue, input, action == "value_create")
		m.recordMutation(err)
		return result, err
	case "value_delete":
		pathValue, err := parseRegistryPath(payload["path"])
		if err != nil {
			return nil, err
		}
		name, err := validateRegistryValueName(payload["name"])
		if err != nil {
			return nil, err
		}
		result, err := registryDeleteValue(ctx, pathValue, name)
		m.recordMutation(err)
		return result, err
	default:
		return nil, newError("invalid_request", fmt.Sprintf("Unsupported registry-management action '%s'.", action))
	}
}

func (m *Manager) matchesTarget(payload map[string]any) bool {
	targetHostname := strings.ToLower(cleanText(payload["hostname"]))
	if targetHostname == "" {
		targetHostname = strings.ToLower(cleanText(payload["target_hostname"]))
	}
	if targetHostname != "" && targetHostname != strings.ToLower(strings.TrimSpace(m.hostname)) {
		return false
	}
	return true
}

func (m *Manager) setLastError(value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = strings.TrimSpace(value)
}

func (m *Manager) recordMutation(err error) {
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastMutationAt = time.Now().Unix()
}

func parseRegistryPath(value any) (registryPath, error) {
	text := normalizeRegistryPathText(value)
	if text == "" {
		return registryPath{}, newError("path_required", "Registry path is required.")
	}
	parts := splitRegistryPath(text)
	if len(parts) == 0 {
		return registryPath{}, newError("invalid_path", "Registry path is invalid.")
	}
	hive, ok := normalizeHiveAlias(parts[0])
	if !ok {
		return registryPath{}, newError("invalid_hive", "Registry path must start with HKCR, HKCU, HKLM, HKU, or HKCC.")
	}
	for _, part := range parts[1:] {
		if _, err := validateRegistryChildName(part); err != nil {
			return registryPath{}, err
		}
	}
	subParts := append([]string{}, parts[1:]...)
	normalizedParts := append([]string{hive}, subParts...)
	return registryPath{
		Hive:    hive,
		SubPath: strings.Join(subParts, `\`),
		Path:    strings.Join(normalizedParts, `\`),
		Parts:   normalizedParts,
	}, nil
}

func normalizeRegistryPathText(value any) string {
	text := cleanText(value)
	text = strings.ReplaceAll(text, "/", `\`)
	text = strings.Trim(text, `\`)
	parts := splitRegistryPath(text)
	if len(parts) == 0 {
		return ""
	}
	if hive, ok := normalizeHiveAlias(parts[0]); ok {
		parts[0] = hive
	}
	return strings.Join(parts, `\`)
}

func splitRegistryPath(value string) []string {
	rows := strings.Split(strings.TrimSpace(value), `\`)
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		part := strings.TrimSpace(row)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func normalizeHiveAlias(value string) (string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	switch normalized {
	case "HKCR", "HKEYCLASSESROOT":
		return "HKCR", true
	case "HKCU", "HKEYCURRENTUSER":
		return "HKCU", true
	case "HKLM", "HKEYLOCALMACHINE":
		return "HKLM", true
	case "HKU", "HKEYUSERS":
		return "HKU", true
	case "HKCC", "HKEYCURRENTCONFIG":
		return "HKCC", true
	default:
		return "", false
	}
}

func hiveDisplayName(alias string) string {
	switch strings.ToUpper(strings.TrimSpace(alias)) {
	case "HKCR":
		return "HKEY_CLASSES_ROOT"
	case "HKCU":
		return "HKEY_CURRENT_USER"
	case "HKLM":
		return "HKEY_LOCAL_MACHINE"
	case "HKU":
		return "HKEY_USERS"
	case "HKCC":
		return "HKEY_CURRENT_CONFIG"
	default:
		return alias
	}
}

func validateRegistryChildName(value any) (string, error) {
	name := cleanText(value)
	if name == "" {
		return "", newError("name_required", "Registry key name is required.")
	}
	if strings.ContainsAny(name, `\/`) || strings.ContainsRune(name, 0) {
		return "", newError("invalid_name", "Registry key names cannot contain slash, backslash, or null characters.")
	}
	if name == "." || name == ".." {
		return "", newError("invalid_name", "Registry key name is invalid.")
	}
	if len([]rune(name)) > 255 {
		return "", newError("invalid_name", "Registry key names cannot exceed 255 characters.")
	}
	return name, nil
}

func validateRegistryValueName(value any) (string, error) {
	name := cleanText(value)
	if strings.ContainsRune(name, 0) || strings.ContainsAny(name, `\/`) {
		return "", newError("invalid_name", "Registry value names cannot contain slash, backslash, or null characters.")
	}
	return name, nil
}

func normalizeRegistryValueInput(payload map[string]any) (registryValueInput, error) {
	name, err := validateRegistryValueName(payload["name"])
	if err != nil {
		return registryValueInput{}, err
	}
	valueType := normalizeRegistryValueType(payload["type"])
	if valueType == "" {
		return registryValueInput{}, newError("type_required", "Registry value type is required.")
	}
	return registryValueInput{Name: name, Type: valueType, Data: payload["data"]}, nil
}

func normalizeRegistryValueType(value any) string {
	text := strings.ToUpper(strings.TrimSpace(fmt.Sprint(value)))
	text = strings.ReplaceAll(text, "-", "_")
	text = strings.ReplaceAll(text, " ", "_")
	if text == "" {
		return ""
	}
	if !strings.HasPrefix(text, "REG_") {
		text = "REG_" + text
	}
	switch text {
	case "REG_SZ", "REG_EXPAND_SZ", "REG_MULTI_SZ", "REG_DWORD", "REG_QWORD", "REG_BINARY":
		return text
	default:
		return ""
	}
}

func normalizeStringList(value any) []string {
	if rows, ok := value.([]string); ok {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row)
		}
		return out
	}
	if rows, ok := value.([]any); ok {
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, fmt.Sprint(row))
		}
		return out
	}
	text := fmt.Sprint(value)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}

func parseUnsignedRegistryInteger(value any, bitSize int) (uint64, error) {
	switch typed := value.(type) {
	case float64:
		if typed < 0 {
			return 0, newError("invalid_value", "Registry integer values must be non-negative.")
		}
		return uint64(typed), nil
	case int:
		if typed < 0 {
			return 0, newError("invalid_value", "Registry integer values must be non-negative.")
		}
		return uint64(typed), nil
	case int64:
		if typed < 0 {
			return 0, newError("invalid_value", "Registry integer values must be non-negative.")
		}
		return uint64(typed), nil
	case uint64:
		return typed, nil
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return 0, nil
	}
	base := 10
	if strings.HasPrefix(strings.ToLower(text), "0x") {
		base = 0
	}
	parsed, err := strconv.ParseUint(text, base, bitSize)
	if err != nil {
		return 0, newError("invalid_value", "Registry integer value is invalid.")
	}
	return parsed, nil
}

func parseBinaryData(value any) ([]byte, error) {
	if raw, ok := value.([]byte); ok {
		if len(raw) > maxRegistryBinaryBytes {
			return nil, newError("value_too_large", "Registry binary value is too large.")
		}
		return raw, nil
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return []byte{}, nil
	}
	if strings.HasPrefix(strings.ToLower(text), "base64:") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text[len("base64:"):]))
		if err != nil {
			return nil, newError("invalid_value", "Registry binary base64 data is invalid.")
		}
		if len(decoded) > maxRegistryBinaryBytes {
			return nil, newError("value_too_large", "Registry binary value is too large.")
		}
		return decoded, nil
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "", ",", "", "0x", "", "0X", "", "-", "").Replace(text)
	if len(compact)%2 != 0 {
		return nil, newError("invalid_value", "Registry binary hex data must contain complete byte pairs.")
	}
	decoded, err := hex.DecodeString(compact)
	if err != nil {
		return nil, newError("invalid_value", "Registry binary hex data is invalid.")
	}
	if len(decoded) > maxRegistryBinaryBytes {
		return nil, newError("value_too_large", "Registry binary value is too large.")
	}
	return decoded, nil
}

func formatBinaryData(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	encoded := strings.ToUpper(hex.EncodeToString(value))
	pairs := make([]string, 0, len(encoded)/2)
	for index := 0; index < len(encoded); index += 2 {
		pairs = append(pairs, encoded[index:index+2])
	}
	return strings.Join(pairs, " ")
}

func registryValueDisplayName(name string) string {
	if name == "" {
		return "(Default)"
	}
	return name
}

func sortKeyEntries(entries []map[string]any) []map[string]any {
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(cleanText(entries[i]["name"])) < strings.ToLower(cleanText(entries[j]["name"]))
	})
	return entries
}

func sortRegistryValues(values []map[string]any) []map[string]any {
	sort.Slice(values, func(i, j int) bool {
		left := cleanText(values[i]["name"])
		right := cleanText(values[j]["name"])
		if left == "" && right != "" {
			return true
		}
		if left != "" && right == "" {
			return false
		}
		return strings.ToLower(left) < strings.ToLower(right)
	})
	return values
}

func unsupportedRegistryPlatform() (map[string]any, error) {
	return nil, newError("unsupported_platform", "Registry Management is supported on Windows agents only.")
}

func normalizeError(err error) registryError {
	if err == nil {
		return registryError{}
	}
	if rerr, ok := err.(registryError); ok {
		return rerr
	}
	return registryError{Code: "registry_error", Message: err.Error()}
}

func newError(code string, message string) registryError {
	return registryError{Code: strings.TrimSpace(code), Message: strings.TrimSpace(message)}
}

func errorResponse(code string, message string) map[string]any {
	return map[string]any{"ok": false, "error": code, "message": message}
}

func cleanText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return normalized == "1" || normalized == "true" || normalized == "yes" || normalized == "y"
	case float64:
		return typed != 0
	case int:
		return typed != 0
	default:
		return false
	}
}

func normalizeServiceMode(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return "system"
	}
	return normalized
}
