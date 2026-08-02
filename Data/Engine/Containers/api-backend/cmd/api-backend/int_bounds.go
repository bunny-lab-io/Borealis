package main

import "math"

func int64FitsInt(value int64) bool {
	return value >= math.MinInt32 && value <= math.MaxInt32
}

func int64ToIntDefault(value int64, fallback int) int {
	if !int64FitsInt(value) {
		return fallback
	}
	return int(value)
}
