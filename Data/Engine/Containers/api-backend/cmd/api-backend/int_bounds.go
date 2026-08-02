package main

import (
	"math"
	"strconv"
)

func int64FitsInt(value int64) bool {
	if strconv.IntSize == 32 {
		return value >= math.MinInt32 && value <= math.MaxInt32
	}
	return true
}

func int64ToIntDefault(value int64, fallback int) int {
	if !int64FitsInt(value) {
		return fallback
	}
	return int(value)
}
