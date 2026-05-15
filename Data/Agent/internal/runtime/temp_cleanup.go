package agentruntime

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func cleanupStartupTemp(configPath string, logger *log.Logger) error {
	root := filepath.Dir(strings.TrimSpace(configPath))
	if root == "" || root == "." {
		return fmt.Errorf("agent root cannot be resolved from config path")
	}
	tempDir := filepath.Join(root, "Temp")
	if filepath.Base(tempDir) != "Temp" {
		return fmt.Errorf("refusing to clean unexpected temp path: %s", tempDir)
	}
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.RemoveAll(tempDir); err != nil {
		return err
	}
	if logger != nil {
		logger.Printf("startup temp cleanup removed %s", tempDir)
	}
	return nil
}
