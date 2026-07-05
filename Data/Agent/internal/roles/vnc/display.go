package vnc

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func displayVirtualBounds(topology []map[string]any) map[string]any {
	if len(topology) == 0 {
		return map[string]any{}
	}
	var left, top, right, bottom int
	seen := false
	for _, item := range topology {
		if item == nil {
			continue
		}
		itemLeft := displayInt(item["left"], 0)
		itemTop := displayInt(item["top"], 0)
		itemWidth := displayInt(item["width"], 0)
		itemHeight := displayInt(item["height"], 0)
		itemRight := displayInt(item["right"], itemLeft+itemWidth)
		itemBottom := displayInt(item["bottom"], itemTop+itemHeight)
		if !seen {
			left = itemLeft
			top = itemTop
			right = itemRight
			bottom = itemBottom
			seen = true
			continue
		}
		if itemLeft < left {
			left = itemLeft
		}
		if itemTop < top {
			top = itemTop
		}
		if itemRight > right {
			right = itemRight
		}
		if itemBottom > bottom {
			bottom = itemBottom
		}
	}
	if !seen {
		return map[string]any{}
	}
	return map[string]any{
		"left":   left,
		"top":    top,
		"right":  right,
		"bottom": bottom,
		"width":  maxInt(0, right-left),
		"height": maxInt(0, bottom-top),
	}
}

func sortDisplayTopology(topology []map[string]any) []map[string]any {
	sort.SliceStable(topology, func(i, j int) bool {
		left := topology[i]
		right := topology[j]
		leftIndex := displayInt(left["display_index"], 0)
		rightIndex := displayInt(right["display_index"], 0)
		if leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		leftPrimary := displayBool(left["primary"])
		rightPrimary := displayBool(right["primary"])
		if leftPrimary != rightPrimary {
			return leftPrimary
		}
		leftTop := displayInt(left["top"], 0)
		rightTop := displayInt(right["top"], 0)
		if leftTop != rightTop {
			return leftTop < rightTop
		}
		return displayInt(left["left"], 0) < displayInt(right["left"], 0)
	})
	return topology
}

func selectDisplayTopology(primary []map[string]any, fallback []map[string]any) []map[string]any {
	if len(primary) == 0 {
		return sortDisplayTopology(fallback)
	}
	if len(fallback) == 0 {
		return sortDisplayTopology(primary)
	}
	if len(fallback) > len(primary) {
		return sortDisplayTopology(fallback)
	}
	primaryBounds := displayVirtualBounds(primary)
	fallbackBounds := displayVirtualBounds(fallback)
	primaryArea := displayInt(primaryBounds["width"], 0) * displayInt(primaryBounds["height"], 0)
	fallbackArea := displayInt(fallbackBounds["width"], 0) * displayInt(fallbackBounds["height"], 0)
	if len(fallback) >= len(primary) && fallbackArea >= primaryArea {
		return sortDisplayTopology(fallback)
	}
	return sortDisplayTopology(primary)
}

func displayIndexFromDeviceName(deviceName string, fallback int) int {
	upper := strings.ToUpper(strings.TrimSpace(deviceName))
	index := strings.LastIndex(upper, "DISPLAY")
	if index >= 0 {
		digits := upper[index+len("DISPLAY"):]
		end := 0
		for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
			end++
		}
		if end > 0 {
			if parsed, err := strconv.Atoi(digits[:end]); err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	if fallback < 1 {
		return 1
	}
	return fallback
}

func displayInt(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case uint32:
		return int(typed)
	case uint64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func displayBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return normalized == "true" || normalized == "1" || normalized == "yes"
	default:
		return fmt.Sprint(value) == "1"
	}
}

func jsonText(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
