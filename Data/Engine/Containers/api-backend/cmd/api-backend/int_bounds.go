package main

const (
	maxBorealisInt = int64(^uint(0) >> 1)
	minBorealisInt = -maxBorealisInt - 1
)

func int64FitsInt(value int64) bool {
	return value >= minBorealisInt && value <= maxBorealisInt
}

func int64ToIntDefault(value int64, fallback int) int {
	if !int64FitsInt(value) {
		return fallback
	}
	return int(value)
}
