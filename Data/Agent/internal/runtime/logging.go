package agentruntime

import (
	"log"
	"os"

	"github.com/bunny-lab-io/borealis/go-agent/internal/logutil"
)

func OpenLogger(configPath string, verbose bool) (*log.Logger, func()) {
	logPath := logPathForConfig(configPath)
	if logPath == "" {
		return log.New(os.Stdout, "", log.LstdFlags), func() {}
	}
	return logutil.OpenLogger(logPath, verbose, logutil.RetentionDaysFromConfig(configPath))
}
