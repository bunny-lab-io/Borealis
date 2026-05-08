//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type BootstrapLogger struct {
	mu       sync.Mutex
	console  bool
	files    []*os.File
	redacts  []string
	lastPath string
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
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, nil, err
		}
		logger.files = append(logger.files, file)
		logger.lastPath = path
	}
	closeFn := func() {
		logger.mu.Lock()
		defer logger.mu.Unlock()
		for _, file := range logger.files {
			_ = file.Close()
		}
		logger.files = nil
	}
	return logger, closeFn, nil
}

func (l *BootstrapLogger) Println(message string) {
	l.writeLine("INFO", message)
}

func (l *BootstrapLogger) Infof(format string, args ...any) {
	l.writeLine("INFO", fmt.Sprintf(format, args...))
}

func (l *BootstrapLogger) Warnf(format string, args ...any) {
	l.writeLine("WARN", fmt.Sprintf(format, args...))
}

func (l *BootstrapLogger) Errorf(format string, args ...any) {
	l.writeLine("ERROR", fmt.Sprintf(format, args...))
}

func (l *BootstrapLogger) Marker(marker string) {
	l.writeRaw(marker)
}

func (l *BootstrapLogger) writeLine(level string, message string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	l.writeRaw(fmt.Sprintf("[%s] [%s] %s", timestamp, level, message))
}

func (l *BootstrapLogger) writeRaw(line string) {
	if l == nil {
		return
	}
	text := l.redact(line)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.console {
		fmt.Println(text)
	}
	for _, file := range l.files {
		_, _ = file.WriteString(text + "\r\n")
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
