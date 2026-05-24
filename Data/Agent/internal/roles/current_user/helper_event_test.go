package currentuser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendHelperEventWritesSessionAndMessage(t *testing.T) {
	dir := t.TempDir()
	appendHelperEvent(dir, 42, "duplicate helper launch prevented")

	data, err := os.ReadFile(filepath.Join(dir, helperEventLogFile))
	if err != nil {
		t.Fatalf("read helper event log: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "session=42") {
		t.Fatalf("helper event did not include session: %q", text)
	}
	if !strings.Contains(text, "duplicate helper launch prevented") {
		t.Fatalf("helper event did not include message: %q", text)
	}
}
