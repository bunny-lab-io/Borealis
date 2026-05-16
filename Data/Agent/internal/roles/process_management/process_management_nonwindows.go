//go:build !windows

package processmanagement

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ssPIDPattern           = regexp.MustCompile(`pid=([0-9]+)`)
	ssBytesAckedPattern    = regexp.MustCompile(`bytes_acked:([0-9]+)`)
	ssBytesReceivedPattern = regexp.MustCompile(`bytes_received:([0-9]+)`)
	ssBytesSentPattern     = regexp.MustCompile(`bytes_sent:([0-9]+)`)
)

func collectPlatformNetworkRates(ctx context.Context, previous map[string]rateCounter) (map[int]float64, map[string]rateCounter) {
	output, err := runCommand(ctx, 3*time.Second, "ss", "-tinpH")
	if err != nil {
		return map[int]float64{}, map[string]rateCounter{}
	}
	now := time.Now()
	totals := parseSSNetworkTotals(output)
	next := make(map[string]rateCounter, len(totals))
	rates := map[int]float64{}
	for pid, total := range totals {
		key := "tcp:" + strconv.Itoa(pid)
		current := rateCounter{At: now, Total: total}
		next[key] = current
		if prev, ok := previous[key]; ok {
			rates[pid] = bytesPerSecond(prev, current)
		}
	}
	return rates, next
}

func parseSSNetworkTotals(output string) map[int]int64 {
	totals := map[int]int64{}
	activePIDs := []int{}
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		pids := ssPIDs(line)
		if len(pids) > 0 {
			activePIDs = pids
		}
		total := ssByteTotal(line)
		if total <= 0 || len(activePIDs) == 0 {
			continue
		}
		for _, pid := range activePIDs {
			totals[pid] += total
		}
		activePIDs = nil
	}
	return totals
}

func ssPIDs(line string) []int {
	matches := ssPIDPattern.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[int]bool{}
	out := []int{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		pid := asInt(match[1])
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
	}
	return out
}

func ssByteTotal(line string) int64 {
	acked := firstRegexInt64(ssBytesAckedPattern, line)
	received := firstRegexInt64(ssBytesReceivedPattern, line)
	if acked == 0 {
		acked = firstRegexInt64(ssBytesSentPattern, line)
	}
	return acked + received
}

func firstRegexInt64(pattern *regexp.Regexp, value string) int64 {
	match := pattern.FindStringSubmatch(value)
	if len(match) < 2 {
		return 0
	}
	parsed, _ := strconv.ParseInt(match[1], 10, 64)
	return parsed
}
