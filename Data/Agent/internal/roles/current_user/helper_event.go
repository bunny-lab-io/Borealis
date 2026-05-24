package currentuser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
)

const helperEventLogFile = "helper-events.log"

func appendHelperEvent(stateDir string, sessionID int, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	dir := localui.StateDir(stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	line := fmt.Sprintf("%s session=%d pid=%d %s\n", time.Now().Format(time.RFC3339), sessionID, os.Getpid(), message)
	file, err := os.OpenFile(filepath.Join(dir, helperEventLogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line)
}
