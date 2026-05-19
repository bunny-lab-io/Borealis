//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/logutil"
)

type BootstrapLogger struct {
	mu            sync.Mutex
	console       bool
	primaryFile   *os.File
	operatorFiles []*os.File
	redacts       []string
	lastPath      string
}

func openBootstrapLogger(cfg BootstrapConfig, console bool) (*BootstrapLogger, func(), error) {
	paths := []string{filepath.Join(cfg.InstallDir, bootstrapLogRelativePath)}
	if strings.TrimSpace(cfg.StdoutPath) != "" && !samePath(cfg.StdoutPath, paths[0]) {
		paths = append(paths, cfg.StdoutPath)
	}
	if strings.TrimSpace(cfg.StderrPath) != "" && !samePath(cfg.StderrPath, paths[0]) && !samePath(cfg.StderrPath, cfg.StdoutPath) {
		paths = append(paths, cfg.StderrPath)
	}
	logger := &BootstrapLogger{
		console: console,
		redacts: []string{
			cfg.SiteEnrollmentCode,
			cfg.LegacyEnrollment,
			cfg.ServerURL,
		},
	}
	for index, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		retentionDays := logutil.RetentionDaysFromConfig(filepath.Join(cfg.InstallDir, agentconfig.FileName))
		_ = logutil.RotateAndPrune(path, retentionDays, time.Now())
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, nil, err
		}
		file, err := os.OpenFile(path, bootstrapLogOpenFlags(index == 0), 0644)
		if err != nil {
			return nil, nil, err
		}
		if index == 0 {
			logger.primaryFile = file
		} else {
			logger.operatorFiles = append(logger.operatorFiles, file)
		}
		logger.lastPath = path
	}
	closeFn := func() {
		logger.mu.Lock()
		defer logger.mu.Unlock()
		if logger.primaryFile != nil {
			_ = logger.primaryFile.Close()
		}
		for _, file := range logger.operatorFiles {
			_ = file.Close()
		}
		logger.primaryFile = nil
		logger.operatorFiles = nil
	}
	return logger, closeFn, nil
}

func (l *BootstrapLogger) Println(message string) {
	l.writeLine("INFO", message, true)
}

func (l *BootstrapLogger) Infof(format string, args ...any) {
	if l == nil {
		return
	}
	l.writeLine("INFO", fmt.Sprintf(format, args...), false)
}

func (l *BootstrapLogger) Stepf(format string, args ...any) {
	if l == nil {
		return
	}
	l.writeLine("INFO", fmt.Sprintf(format, args...), true)
}

func (l *BootstrapLogger) Tracef(format string, args ...any) {
	if l == nil {
		return
	}
	l.writeLine("TRACE", fmt.Sprintf(format, args...), false)
}

func (l *BootstrapLogger) Warnf(format string, args ...any) {
	l.writeLine("WARN", fmt.Sprintf(format, args...), true)
}

func (l *BootstrapLogger) Errorf(format string, args ...any) {
	l.writeLine("ERROR", fmt.Sprintf(format, args...), true)
}

func (l *BootstrapLogger) Marker(marker string) {
	l.writeRaw(marker, false)
}

func (l *BootstrapLogger) writeLine(level string, message string, operatorVisible bool) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	l.writeRaw(fmt.Sprintf("[%s] [%s] %s", timestamp, level, message), operatorVisible)
}

func (l *BootstrapLogger) writeRaw(line string, operatorVisible bool) {
	if l == nil {
		return
	}
	text := l.redact(line)
	l.mu.Lock()
	defer l.mu.Unlock()
	if operatorVisible && l.console {
		fmt.Println(text)
	}
	if l.primaryFile != nil {
		_, _ = l.primaryFile.WriteString(text + "\r\n")
	}
	if operatorVisible {
		for _, file := range l.operatorFiles {
			_, _ = file.WriteString(text + "\r\n")
		}
	}
}

func (l *BootstrapLogger) redact(value string) string {
	text := value
	for _, raw := range l.redacts {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		text = strings.ReplaceAll(text, item, "[redacted]")
	}
	return text
}

func logBootstrapConfigSummary(cfg BootstrapConfig, logger *BootstrapLogger) {
	if logger == nil {
		return
	}
	enrollmentHash := ""
	if strings.TrimSpace(cfg.SiteEnrollmentCode) != "" {
		enrollmentHash = hashText(cfg.SiteEnrollmentCode)
		if len(enrollmentHash) > 12 {
			enrollmentHash = enrollmentHash[:12]
		}
	}
	logger.Tracef(
		"Bootstrap config: install_dir=%s config_path=%s repo_url=%s repo_ref=%s payload_path=%s payload_exists=%t payload_sha256_present=%t state_path=%s events_path=%s stdout_path=%s stderr_path=%s job_id=%d run_id=%d target=%s service_name=%s interactive=%t noninteractive=%t server_url_present=%t site_enrollment_code_sha256_prefix=%s",
		cfg.InstallDir,
		cfg.ConfigPath,
		cfg.RepoURL,
		cfg.RepoRef,
		cfg.PayloadPath,
		fileExists(cfg.PayloadPath),
		strings.TrimSpace(cfg.PayloadSHA256) != "",
		cfg.StatePath,
		cfg.EventsPath,
		cfg.StdoutPath,
		cfg.StderrPath,
		cfg.JobID,
		cfg.RunID,
		cfg.Target,
		cfg.ServiceName,
		cfg.Interactive,
		cfg.NonInteractive,
		strings.TrimSpace(cfg.ServerURL) != "",
		enrollmentHash,
	)
}

func samePath(a string, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA == nil {
		a = aa
	}
	if errB == nil {
		b = bb
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
