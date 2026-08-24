package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	publicJSONMapMaxBytes     int64 = 1 << 20
	publicAuthJSONMaxBytes    int64 = 64 << 10
	publicBackupJSONMaxBytes  int64 = 128 << 20
	maxInputPathTextLength          = 4096
	maxInputPlainTextLength         = 4096
	maxInputIdentifierLength        = 256
	maxNotificationTextLength       = 4096
)

var (
	errRequestBodyTooLarge = errors.New("request body too large")
	inputIdentifierRE      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/\-]*$`)
	inputSlugRE            = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	hostLabelRE            = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	portRE                 = regexp.MustCompile(`^\d{1,5}$`)
	unsafeMarkupRE         = regexp.MustCompile(`(?i)<\s*/?\s*(script|iframe|object|embed|img|svg|math|link|style|meta|base|form|input|button|textarea|select|option|video|audio|source|body|html)\b|on[a-z]+\s*=|javascript\s*:`)
)

type publicValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type inputFieldClass string

const (
	inputClassPlainSingleLine inputFieldClass = "plain_single_line"
	inputClassPlainMultiline  inputFieldClass = "plain_multiline"
	inputClassIdentifier      inputFieldClass = "identifier"
	inputClassSlug            inputFieldClass = "slug"
	inputClassHost            inputFieldClass = "host"
	inputClassURL             inputFieldClass = "url"
	inputClassPath            inputFieldClass = "path"
	inputClassRegistry        inputFieldClass = "registry"
	inputClassRegex           inputFieldClass = "regex"
	inputClassSecret          inputFieldClass = "secret"
	inputClassCode            inputFieldClass = "code"
)

func withPublicInputValidation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !publicInputValidationApplies(r) {
			next.ServeHTTP(w, r)
			return
		}
		if errs := validatePublicRequestTarget(r); len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func publicInputValidationApplies(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	if strings.HasPrefix(path, "/api/internal/") {
		return false
	}
	return path != "/api/server/k3s/operator"
}

func validatePublicRequestTarget(r *http.Request) []publicValidationError {
	errs := make([]publicValidationError, 0)
	if err := validateInputUTF8AndControls("path", r.URL.Path, maxInputPathTextLength, false); err != nil {
		errs = append(errs, publicValidationError{Field: "path", Message: err.Error()})
	} else if err := validateNoUnsafeMarkup("path", r.URL.Path); err != nil {
		errs = append(errs, publicValidationError{Field: "path", Message: err.Error()})
	}
	for key, values := range r.URL.Query() {
		field := "query." + key
		if err := validateInputUTF8AndControls(field, key, maxInputIdentifierLength, false); err != nil {
			errs = append(errs, publicValidationError{Field: field, Message: err.Error()})
			continue
		}
		class := classifyInputField(key)
		for _, value := range values {
			if err := validateInputValue(field, value, class); err != nil {
				errs = append(errs, publicValidationError{Field: field, Message: err.Error()})
			}
		}
	}
	return errs
}

func readJSONMapWithLimit(r *http.Request, limit int64) (map[string]any, error) {
	raw, err := readLimitedRequestBody(r, limit)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if body == nil {
		body = map[string]any{}
	}
	if errs := sanitizeJSONInputMap(body); len(errs) > 0 {
		return nil, publicValidationErrors(errs)
	}
	return body, nil
}

func readLimitedRequestBody(r *http.Request, limit int64) ([]byte, error) {
	if r == nil || r.Body == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = publicJSONMapMaxBytes
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errRequestBodyTooLarge
	}
	return raw, nil
}

func sanitizeJSONInputMap(body map[string]any) []publicValidationError {
	return sanitizeJSONInputValue("", body, inputClassPlainSingleLine)
}

func validateJSONTransportMap(body map[string]any) []publicValidationError {
	return validateJSONTransportValue("", body)
}

func validateJSONTransportValue(path string, value any) []publicValidationError {
	errs := make([]publicValidationError, 0)
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			fieldPath := key
			if path != "" {
				fieldPath = path + "." + key
			}
			errs = append(errs, validateJSONTransportValue(fieldPath, child)...)
		}
	case []any:
		for index, child := range typed {
			fieldPath := fmt.Sprintf("%s[%d]", path, index)
			errs = append(errs, validateJSONTransportValue(fieldPath, child)...)
		}
	case string:
		if err := validateInputUTF8AndControls(firstText(path, "body"), typed, 16<<20, true); err != nil {
			errs = append(errs, publicValidationError{Field: firstText(path, "body"), Message: err.Error()})
		}
	}
	return errs
}

func sanitizeJSONInputValue(path string, value any, parentClass inputFieldClass) []publicValidationError {
	errs := make([]publicValidationError, 0)
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			fieldPath := key
			if path != "" {
				fieldPath = path + "." + key
			}
			class := classifyInputField(key)
			if text, ok := child.(string); ok {
				sanitized, _ := sanitizeInputValueByClass(text, class)
				if err := validateInputValue(fieldPath, sanitized, class); err != nil {
					errs = append(errs, publicValidationError{Field: firstText(fieldPath, "body"), Message: err.Error()})
					continue
				}
				typed[key] = sanitized
				continue
			}
			childErrs := sanitizeJSONInputValue(fieldPath, child, class)
			if len(childErrs) > 0 {
				errs = append(errs, childErrs...)
			}
		}
	case []any:
		for index, child := range typed {
			fieldPath := fmt.Sprintf("%s[%d]", path, index)
			if text, ok := child.(string); ok {
				sanitized, _ := sanitizeInputValueByClass(text, parentClass)
				if err := validateInputValue(fieldPath, sanitized, parentClass); err != nil {
					errs = append(errs, publicValidationError{Field: firstText(fieldPath, "body"), Message: err.Error()})
					continue
				}
				typed[index] = sanitized
				continue
			}
			childErrs := sanitizeJSONInputValue(fieldPath, child, parentClass)
			if len(childErrs) > 0 {
				errs = append(errs, childErrs...)
			}
		}
	case string:
		sanitized, _ := sanitizeInputValueByClass(typed, parentClass)
		if err := validateInputValue(path, sanitized, parentClass); err != nil {
			errs = append(errs, publicValidationError{Field: firstText(path, "body"), Message: err.Error()})
		}
	}
	return errs
}

func sanitizeInputValueByClass(value string, class inputFieldClass) (string, bool) {
	switch class {
	case inputClassPlainSingleLine, inputClassIdentifier, inputClassSlug, inputClassHost, inputClassURL:
		sanitized := sanitizeSingleLineInput(value)
		return sanitized, sanitized != value
	case inputClassPlainMultiline:
		sanitized := sanitizeMultilineInput(value)
		return sanitized, sanitized != value
	default:
		return value, false
	}
}

func validateInputValue(field string, value string, class inputFieldClass) error {
	switch class {
	case inputClassSecret, inputClassCode:
		return validateInputUTF8AndControls(field, value, 16<<20, true)
	case inputClassPath:
		return validatePathInput(field, value)
	case inputClassRegistry:
		return validateRegistryInput(field, value)
	case inputClassRegex:
		return validateRegexInput(field, value)
	case inputClassURL:
		return validateURLInput(field, value)
	case inputClassHost:
		return validateHostInput(field, value)
	case inputClassSlug:
		return validateSlugInput(field, value)
	case inputClassIdentifier:
		return validateIdentifierInput(field, value)
	case inputClassPlainMultiline:
		if err := validateInputUTF8AndControls(field, value, maxInputPlainTextLength, true); err != nil {
			return err
		}
		return validateNoUnsafeMarkup(field, value)
	default:
		if err := validateInputUTF8AndControls(field, value, maxInputPlainTextLength, false); err != nil {
			return err
		}
		return validateNoUnsafeMarkup(field, value)
	}
}

func validateInputUTF8AndControls(field string, value string, maxLen int, allowNewlines bool) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", field)
	}
	if maxLen > 0 && len(value) > maxLen {
		return fmt.Errorf("%s exceeds %d bytes", field, maxLen)
	}
	for _, r := range value {
		if r == 0 || r == '\u007f' {
			return fmt.Errorf("%s cannot include control characters", field)
		}
		if unicode.IsControl(r) {
			if allowNewlines && (r == '\n' || r == '\r' || r == '\t') {
				continue
			}
			return fmt.Errorf("%s cannot include control characters", field)
		}
	}
	return nil
}

func validateNoUnsafeMarkup(field string, value string) error {
	if unsafeMarkupRE.MatchString(value) {
		return fmt.Errorf("%s cannot include executable markup", field)
	}
	return nil
}

func validateIdentifierInput(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if err := validateInputUTF8AndControls(field, value, maxInputIdentifierLength, false); err != nil {
		return err
	}
	if strings.ContainsAny(value, "<>\\") || !inputIdentifierRE.MatchString(value) {
		return fmt.Errorf("%s must be an identifier, enum, or safe ref", field)
	}
	return nil
}

func validateSlugInput(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if err := validateInputUTF8AndControls(field, value, 63, false); err != nil {
		return err
	}
	if !inputSlugRE.MatchString(value) {
		return fmt.Errorf("%s must be a lowercase slug", field)
	}
	return nil
}

func validateHostInput(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if err := validateInputUTF8AndControls(field, value, 253, false); err != nil {
		return err
	}
	if strings.ContainsAny(value, "<>\"'`\\ ") {
		return fmt.Errorf("%s must be a hostname, IP address, or CIDR", field)
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return nil
	}
	if _, err := netip.ParsePrefix(value); err == nil {
		return nil
	}
	if strings.ContainsAny(value, "/:") {
		return fmt.Errorf("%s must be a hostname, IP address, or CIDR", field)
	}
	for _, label := range strings.Split(value, ".") {
		if !hostLabelRE.MatchString(label) {
			return fmt.Errorf("%s must be a valid hostname", field)
		}
	}
	return nil
}

func validateURLInput(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if err := validateInputUTF8AndControls(field, value, 4096, false); err != nil {
		return err
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", field)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "ldap" && scheme != "ldaps" {
		return fmt.Errorf("%s uses an unsupported URL scheme", field)
	}
	if strings.ContainsAny(parsed.Host, "<>\"'` ") {
		return fmt.Errorf("%s has an invalid host", field)
	}
	return nil
}

func validatePathInput(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if err := validateInputUTF8AndControls(field, value, maxInputPathTextLength, false); err != nil {
		return err
	}
	if strings.Contains(value, "\x00") {
		return fmt.Errorf("%s cannot include NUL bytes", field)
	}
	return nil
}

func validateRemoteFileName(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if err := validateInputUTF8AndControls(field, value, 255, false); err != nil {
		return err
	}
	if value == "." || value == ".." || strings.Contains(value, "/") || strings.Contains(value, "\\") {
		return fmt.Errorf("%s cannot include path separators or traversal segments", field)
	}
	if strings.ContainsAny(value, `<>:"|?*`) {
		return fmt.Errorf("%s contains characters Windows file systems do not allow", field)
	}
	if strings.HasSuffix(value, ".") || strings.HasSuffix(value, " ") {
		return fmt.Errorf("%s cannot end with a space or period", field)
	}
	return nil
}

func validateRegistryInput(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if err := validateInputUTF8AndControls(field, value, maxInputPathTextLength, false); err != nil {
		return err
	}
	if strings.Contains(value, "/") || strings.ContainsAny(value, "<>\"|?*") {
		return fmt.Errorf("%s must be a Windows registry path or value name", field)
	}
	return nil
}

func validateRegexInput(field string, value string) error {
	if value == "" {
		return nil
	}
	if err := validateInputUTF8AndControls(field, value, maxInputPlainTextLength, true); err != nil {
		return err
	}
	if _, err := regexp.Compile(value); err != nil {
		return fmt.Errorf("%s must be a valid regular expression", field)
	}
	return nil
}

func sanitizeSingleLineInput(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == 0 || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func sanitizeMultilineInput(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, value)
	return strings.TrimSpace(value)
}

func sanitizeNotificationText(value string) string {
	value = sanitizeMultilineInput(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "<B>", "<b>")
	value = strings.ReplaceAll(value, "</B>", "</b>")
	value = strings.ReplaceAll(value, "<b>", "\x00BOLD_OPEN\x00")
	value = strings.ReplaceAll(value, "</b>", "\x00BOLD_CLOSE\x00")
	value = strings.ReplaceAll(value, "<", "")
	value = strings.ReplaceAll(value, ">", "")
	value = strings.ReplaceAll(value, "\x00BOLD_OPEN\x00", "<b>")
	value = strings.ReplaceAll(value, "\x00BOLD_CLOSE\x00", "</b>")
	if len(value) > maxNotificationTextLength {
		value = value[:maxNotificationTextLength]
	}
	return value
}

func classifyInputField(field string) inputFieldClass {
	key := strings.ToLower(strings.TrimSpace(field))
	key = strings.Trim(key, "[]")
	switch {
	case strings.Contains(key, "password"), strings.Contains(key, "cipher"), strings.Contains(key, "secret"),
		strings.Contains(key, "token"), strings.Contains(key, "pem"), strings.Contains(key, "private_key"),
		strings.Contains(key, "backup"), key == "invite_bundle":
		return inputClassSecret
	case strings.Contains(key, "script"), strings.Contains(key, "code"), strings.Contains(key, "content"),
		strings.Contains(key, "stdout"), strings.Contains(key, "stderr"), strings.Contains(key, "command"),
		strings.Contains(key, "argument"), strings.Contains(key, "json"), strings.Contains(key, "payload"),
		key == "data" || key == "value_data":
		return inputClassCode
	case strings.Contains(key, "regex"):
		return inputClassRegex
	case strings.Contains(key, "registry"):
		return inputClassRegistry
	case key == "path" || strings.HasSuffix(key, "_path") || strings.Contains(key, "directory") || strings.Contains(key, "folder"):
		return inputClassPath
	case strings.Contains(key, "url") || strings.Contains(key, "uri") || strings.Contains(key, "endpoint"):
		return inputClassURL
	case key == "hostname" || strings.Contains(key, "host_name") || strings.HasSuffix(key, "_host") || strings.Contains(key, "cidr") || strings.Contains(key, "ip_address"):
		return inputClassHost
	case strings.Contains(key, "slug"):
		return inputClassSlug
	case key == "id" || strings.HasSuffix(key, "_id") || strings.Contains(key, "guid") || strings.Contains(key, "uuid") ||
		key == "role" || key == "action" || strings.HasSuffix(key, "_action") || key == "platform" ||
		key == "artifact" || key == "branch" || key == "service_mode":
		return inputClassIdentifier
	case strings.Contains(key, "description") || strings.Contains(key, "notes") || strings.Contains(key, "message") || strings.Contains(key, "comment"):
		return inputClassPlainMultiline
	default:
		return inputClassPlainSingleLine
	}
}

type publicValidationErrors []publicValidationError

func (e publicValidationErrors) Error() string {
	return "validation_failed"
}

func asPublicValidationErrors(err error) ([]publicValidationError, bool) {
	var errs publicValidationErrors
	if errors.As(err, &errs) {
		return []publicValidationError(errs), true
	}
	return nil, false
}

func writePublicValidationErrors(w http.ResponseWriter, errs []publicValidationError) {
	if len(errs) == 0 {
		errs = []publicValidationError{{Field: "body", Message: "Request input is invalid."}}
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error":  "validation_failed",
		"errors": errs,
	})
}

func publicValidationErrorPayload(err error, fallbackError string) map[string]any {
	if errors.Is(err, errRequestBodyTooLarge) {
		return map[string]any{"error": "request_body_too_large"}
	}
	if errs, ok := asPublicValidationErrors(err); ok {
		if len(errs) == 0 {
			errs = []publicValidationError{{Field: "body", Message: "Request input is invalid."}}
		}
		return map[string]any{"error": "validation_failed", "errors": errs}
	}
	return map[string]any{"error": firstText(fallbackError, "invalid_json")}
}

func invalidJSONOrValidation(w http.ResponseWriter, err error) {
	if errors.Is(err, errRequestBodyTooLarge) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "request_body_too_large"})
		return
	}
	if errs, ok := asPublicValidationErrors(err); ok {
		writePublicValidationErrors(w, errs)
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
}

func requestHasJSONBody(r *http.Request) bool {
	if r == nil || r.Body == nil {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json")
	}
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func validHostPort(host string, port string) bool {
	if strings.TrimSpace(host) == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	return validateHostInput("host", host) == nil && (port == "" || portRE.MatchString(port))
}
