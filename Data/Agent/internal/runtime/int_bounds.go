package agentruntime

const maxAgentInt = uint64(^uint(0) >> 1)

func uint32PIDToInt(pid uint32) (int, bool) {
	if uint64(pid) > maxAgentInt {
		return 0, false
	}
	return int(pid), true
}

func uint32PIDMatchesInt(pid uint32, other int) bool {
	return other > 0 && uint64(other) == uint64(pid)
}
