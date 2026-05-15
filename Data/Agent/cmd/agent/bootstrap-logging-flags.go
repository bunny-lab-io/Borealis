package main

import "os"

func bootstrapLogOpenFlags(truncate bool) int {
	flags := os.O_CREATE | os.O_WRONLY
	if truncate {
		return flags | os.O_TRUNC
	}
	return flags | os.O_APPEND
}
