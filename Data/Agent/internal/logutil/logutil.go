package logutil

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

const dayLayout = "2006-01-02"

func RetentionDaysFromConfig(configPath string) int {
	cfg, err := agentconfig.Load(configPath)
	if err != nil {
		return agentconfig.DefaultLogRetentionDays
	}
	if cfg.Agent.LogRetentionDays <= 0 {
		return agentconfig.DefaultLogRetentionDays
	}
	return cfg.Agent.LogRetentionDays
}

func OpenLogger(path string, verbose bool, retentionDays int) (*log.Logger, func()) {
	writer, err := NewRotatingWriter(path, retentionDays)
	if err != nil {
		return log.New(os.Stdout, "", log.LstdFlags), func() {}
	}
	var output io.Writer = writer
	if verbose {
		output = io.MultiWriter(os.Stdout, writer)
	}
	return log.New(output, "", log.LstdFlags), func() { _ = writer.Close() }
}

type RotatingWriter struct {
	mu            sync.Mutex
	path          string
	retentionDays int
	day           string
	file          *os.File
}

func NewRotatingWriter(path string, retentionDays int) (*RotatingWriter, error) {
	if err := RotateAndPrune(path, retentionDays, time.Now()); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &RotatingWriter{
		path:          path,
		retentionDays: retentionDays,
		day:           time.Now().Format(dayLayout),
		file:          file,
	}, nil
}

func (w *RotatingWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotateIfNeeded(time.Now()); err != nil {
		return 0, err
	}
	return w.file.Write(payload)
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingWriter) rotateIfNeeded(now time.Time) error {
	if w.file == nil {
		return os.ErrClosed
	}
	day := now.Format(dayLayout)
	if day == w.day {
		return nil
	}
	_ = w.file.Close()
	w.file = nil
	if err := RotateAndPrune(w.path, w.retentionDays, now); err != nil {
		return err
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.file = file
	w.day = day
	return nil
}

func Append(path string, retentionDays int, format string, args ...any) {
	if strings.TrimSpace(path) == "" {
		return
	}
	_ = RotateAndPrune(path, retentionDays, time.Now())
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	line := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, _ = file.WriteString(line)
}

func RotateAndPrune(path string, retentionDays int, now time.Time) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if retentionDays <= 0 {
		retentionDays = agentconfig.DefaultLogRetentionDays
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rotateActive(path, now)
	pruneRotated(path, retentionDays, now)
	return nil
}

func rotateActive(path string, now time.Time) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return
	}
	if sameDay(info.ModTime(), now) {
		return
	}
	rotated := path + "." + info.ModTime().Format(dayLayout)
	if _, err := os.Stat(rotated); err == nil {
		rotated = rotated + "." + fmt.Sprintf("%d", info.ModTime().Unix())
	}
	_ = os.Rename(path, rotated)
}

func pruneRotated(path string, retentionDays int, now time.Time) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := startOfDay(now).AddDate(0, 0, -retentionDays+1)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, base+".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func sameDay(left time.Time, right time.Time) bool {
	ly, lm, ld := left.Local().Date()
	ry, rm, rd := right.Local().Date()
	return ly == ry && lm == rm && ld == rd
}

func startOfDay(value time.Time) time.Time {
	local := value.Local()
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, local.Location())
}
