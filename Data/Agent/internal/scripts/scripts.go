package scripts

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var base64Hints = map[string]bool{
	"base64":  true,
	"b64":     true,
	"base-64": true,
}

type Result struct {
	ReturnCode int
	Stdout     string
	Stderr     string
}

func DecodeScriptBytes(content any, encodingHint string) ([]byte, bool) {
	switch value := content.(type) {
	case nil:
		return []byte{}, true
	case []byte:
		return value, true
	case string:
		encoding := strings.ToLower(strings.TrimSpace(encodingHint))
		if base64Hints[encoding] {
			decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(value), ""))
			if err != nil {
				return nil, false
			}
			return decoded, true
		}
		if decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(value), "")); err == nil {
			return decoded, true
		}
		return []byte(value), true
	default:
		return nil, false
	}
}

func VerifySignature(scriptBytes []byte, signatureB64 string, publicKeyB64 string) bool {
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signatureB64))
	if err != nil {
		return false
	}
	keyDER, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyB64))
	if err != nil {
		return false
	}
	parsed, err := x509.ParsePKIXPublicKey(keyDER)
	if err != nil {
		return false
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return false
	}
	return ed25519.Verify(publicKey, scriptBytes, signature)
}

func CanonicalEnvKey(name string) string {
	cleaned := regexp.MustCompile(`[^A-Za-z0-9_]`).ReplaceAllString(strings.TrimSpace(name), "_")
	return strings.ToUpper(cleaned)
}

func BuildEnvMap(rawEnv map[string]any, variables []map[string]any) map[string]string {
	envMap := map[string]string{}
	for key, value := range rawEnv {
		envKey := CanonicalEnvKey(key)
		if envKey == "" {
			continue
		}
		envMap[envKey] = stringify(value)
	}
	for _, variable := range variables {
		name := strings.TrimSpace(stringify(variable["name"]))
		if name == "" {
			continue
		}
		key := CanonicalEnvKey(name)
		if key == "" {
			continue
		}
		if _, ok := envMap[key]; !ok {
			envMap[key] = stringify(variable["default"])
		}
	}
	for _, variable := range variables {
		name := strings.TrimSpace(stringify(variable["name"]))
		if name == "" {
			continue
		}
		key := CanonicalEnvKey(name)
		value, ok := envMap[key]
		if !ok {
			continue
		}
		alias := regexp.MustCompile(`[^A-Za-z0-9_]`).ReplaceAllString(name, "_")
		if alias != "" {
			if _, exists := envMap[alias]; !exists {
				envMap[alias] = value
			}
		}
		if alias != name && regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
			if _, exists := envMap[name]; !exists {
				envMap[name] = value
			}
		}
	}
	return envMap
}

func Run(ctx context.Context, scriptType string, content []byte, envMap map[string]string, timeoutSeconds int) Result {
	normalized := strings.ToLower(strings.TrimSpace(scriptType))
	switch normalized {
	case "powershell":
		return runPowerShell(ctx, string(content), envMap, timeoutSeconds)
	case "batch":
		return runBatch(ctx, string(content), envMap, timeoutSeconds)
	case "bash":
		return runBash(ctx, string(content), envMap, timeoutSeconds)
	default:
		return Result{ReturnCode: -1, Stderr: "Unsupported type: " + normalized}
	}
}

func runPowerShell(ctx context.Context, content string, envMap map[string]string, timeoutSeconds int) Result {
	binary := "pwsh"
	if runtime.GOOS == "windows" {
		binary = filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if _, err := os.Stat(binary); err != nil {
			binary = "powershell.exe"
		}
	}
	wrapped := BuildPowerShellScript(content, envMap)
	path, cleanup, err := writeTempScript("borealis-*.ps1", wrapped, 0o600)
	if err != nil {
		return Result{ReturnCode: -1, Stderr: err.Error()}
	}
	defer cleanup()
	return CleanPowerShellResult(runCommand(ctx, timeoutSeconds, envMap, binary, "-ExecutionPolicy", "Bypass", "-NoProfile", "-File", path))
}

func runBatch(ctx context.Context, content string, envMap map[string]string, timeoutSeconds int) Result {
	if runtime.GOOS != "windows" {
		return Result{ReturnCode: -1, Stderr: "Batch scripts are only supported on Windows agents."}
	}
	path, cleanup, err := writeTempScript("borealis-*.bat", strings.ReplaceAll(content, "\n", "\r\n"), 0o600)
	if err != nil {
		return Result{ReturnCode: -1, Stderr: err.Error()}
	}
	defer cleanup()
	return runCommand(ctx, timeoutSeconds, envMap, "cmd.exe", "/D", "/C", path)
}

func runBash(ctx context.Context, content string, envMap map[string]string, timeoutSeconds int) Result {
	binary := firstExistingExecutable(os.Getenv("BOREALIS_BASH_BIN"), "bash", "/bin/bash", "/usr/bin/bash", "sh", "/bin/sh", "/usr/bin/sh")
	if binary == "" {
		return Result{ReturnCode: -1, Stderr: "Bash is not available on this agent."}
	}
	path, cleanup, err := writeTempScript("borealis-*.sh", normalizeNewlines(content), 0o700)
	if err != nil {
		return Result{ReturnCode: -1, Stderr: err.Error()}
	}
	defer cleanup()
	return runCommand(ctx, timeoutSeconds, envMap, binary, path)
}

func BuildPowerShellScript(content string, envMap map[string]string) string {
	lines := PowerShellPreludeLines()
	for key, value := range envMap {
		if key == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("try { [System.Environment]::SetEnvironmentVariable(%s, %s, 'Process') } catch {}", PowerShellLiteral(key), PowerShellLiteral(value)))
		lines = append(lines, fmt.Sprintf("try { Set-Item -LiteralPath ([string]::Format('Env:{0}', %s)) -Value %s -ErrorAction Stop } catch {}", PowerShellLiteral(key), PowerShellLiteral(value)))
	}
	lines = append(lines, "$__BorealisScript = {\n"+content+"\n}")
	lines = append(lines, "& $__BorealisScript")
	return strings.Join(lines, "\n") + "\n"
}

func PowerShellPreludeLines() []string {
	return []string{
		"$ProgressPreference = 'SilentlyContinue'",
		"$InformationPreference = 'SilentlyContinue'",
		"$VerbosePreference = 'SilentlyContinue'",
		"$DebugPreference = 'SilentlyContinue'",
	}
}

func PowerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func CleanPowerShellResult(result Result) Result {
	result.Stdout = CleanPowerShellStream(result.Stdout)
	result.Stderr = CleanPowerShellStream(result.Stderr)
	return result
}

func CleanPowerShellStream(text string) string {
	trimmed := strings.TrimLeft(text, "\ufeff\r\n\t ")
	if !strings.HasPrefix(trimmed, "#< CLIXML") {
		return text
	}
	xmlStart := strings.Index(trimmed, "<Objs")
	if xmlStart < 0 {
		return ""
	}
	decoder := xml.NewDecoder(strings.NewReader(trimmed[xmlStart:]))
	var streamTexts []string
	inString := false
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch typed := token.(type) {
		case xml.StartElement:
			inString = typed.Name.Local == "S"
		case xml.EndElement:
			if typed.Name.Local == "S" {
				inString = false
			}
		case xml.CharData:
			if inString {
				decoded := decodeCliXMLString(string(typed))
				if strings.TrimSpace(decoded) != "" {
					streamTexts = append(streamTexts, decoded)
				}
			}
		}
	}
	if len(streamTexts) == 0 {
		return ""
	}
	return strings.TrimRight(strings.Join(streamTexts, ""), "\r\n") + "\n"
}

func decodeCliXMLString(text string) string {
	return regexp.MustCompile(`_x([0-9A-Fa-f]{4})_`).ReplaceAllStringFunc(text, func(match string) string {
		parts := regexp.MustCompile(`^_x([0-9A-Fa-f]{4})_$`).FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		value, err := strconv.ParseInt(parts[1], 16, 32)
		if err != nil {
			return match
		}
		return string(rune(value))
	})
}

func runCommand(ctx context.Context, timeoutSeconds int, envMap map[string]string, name string, args ...string) Result {
	if timeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	for key, value := range envMap {
		if key != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return Result{
			ReturnCode: -1,
			Stdout:     stdout.String(),
			Stderr:     joinStderr(stderr.String(), fmt.Sprintf("Script timed out after %d seconds", timeoutSeconds)),
		}
	}
	rc := 0
	if err != nil {
		rc = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		} else if stderr.Len() == 0 {
			stderr.WriteString(err.Error())
		}
	}
	return Result{ReturnCode: rc, Stdout: stdout.String(), Stderr: stderr.String()}
}

func writeTempScript(pattern string, content string, mode os.FileMode) (string, func(), error) {
	dir := filepath.Join(os.TempDir(), "Borealis", "quick_jobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", func() {}, err
	}
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, mode)
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func firstExistingExecutable(candidates ...string) string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return candidate
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func joinStderr(existing string, addition string) string {
	existing = strings.TrimRight(existing, "\r\n")
	if existing == "" {
		return addition
	}
	return existing + "\n" + addition
}

func stringify(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case bool:
		if v {
			return "True"
		}
		return "False"
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}
