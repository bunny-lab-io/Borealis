package agentruntime

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

func OpenLogger(configPath string, verbose bool) (*log.Logger, func()) {
	logPath := logPathForConfig(configPath)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return log.New(os.Stdout, "", log.LstdFlags), func() {}
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return log.New(os.Stdout, "", log.LstdFlags), func() {}
	}
	var writer io.Writer = file
	if verbose {
		writer = io.MultiWriter(os.Stdout, file)
	}
	return log.New(writer, "", log.LstdFlags), func() { _ = file.Close() }
}
