package agentruntime

import "math"

func uint32PIDToInt(pid uint32) (int, bool) {
	if pid > math.MaxInt32 {
		return 0, false
	}
	return int(pid), true
}

func uint32PIDMatchesInt(pid uint32, other int) bool {
	return other > 0 && uint64(other) == uint64(pid)
}
