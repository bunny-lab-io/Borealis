package main

import (
	"os"
	"testing"
)

func TestBootstrapLogOpenFlagsTruncatesPrimaryLog(t *testing.T) {
	flags := bootstrapLogOpenFlags(true)
	if flags&os.O_TRUNC == 0 {
		t.Fatalf("expected truncate flag for primary bootstrap log")
	}
	if flags&os.O_APPEND != 0 {
		t.Fatalf("primary bootstrap log should not append")
	}
}

func TestBootstrapLogOpenFlagsAppendsSecondaryLogs(t *testing.T) {
	flags := bootstrapLogOpenFlags(false)
	if flags&os.O_APPEND == 0 {
		t.Fatalf("expected append flag for secondary bootstrap logs")
	}
	if flags&os.O_TRUNC != 0 {
		t.Fatalf("secondary bootstrap logs should not truncate")
	}
}
