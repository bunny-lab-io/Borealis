package deviceaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const commandTimeout = 15 * time.Second

type Auditor struct{}

type Snapshot struct {
	Inventory  map[string]any
	Metrics    map[string]any
	InternalIP string
	DeviceType string
	Health     RoleHealth
}

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

func NewAuditor() *Auditor {
	return &Auditor{}
}

func (a *Auditor) Collect(ctx context.Context) Snapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	networkAdapters := collectNetwork()
	internalIP := firstInternalIP(networkAdapters)
	memoryEntries, memoryMetrics, memoryErr := collectMemory(ctx)
	storageEntries, storageErr := collectStorage(ctx)
	cpuPayload, cpuErr := collectCPU(ctx)
	metrics := map[string]any{
		"last_user": currentUserName(),
	}
	for key, value := range memoryMetrics {
		metrics[key] = value
	}
	if uptime := collectUptime(ctx); uptime > 0 {
		metrics["uptime"] = uptime
	}
	deviceType := detectDeviceType()
	errorsSeen := []string{}
	for _, err := range []error{memoryErr, storageErr, cpuErr} {
		if err != nil {
			errorsSeen = append(errorsSeen, err.Error())
		}
	}
	status := "healthy"
	detail := "Go device audit inventory is available."
	if len(errorsSeen) > 0 {
		status = "recovering"
		detail = strings.Join(errorsSeen, "; ")
	}
	if len(memoryEntries) == 0 && len(storageEntries) == 0 && len(networkAdapters) == 0 && len(cpuPayload) == 0 {
		status = "recovering"
		detail = "Device audit inventory has not produced a snapshot yet."
	}
	return Snapshot{
		Inventory: map[string]any{
			"memory":  memoryEntries,
			"storage": storageEntries,
			"network": networkAdapters,
			"cpu":     cpuPayload,
		},
		Metrics:    metrics,
		InternalIP: internalIP,
		DeviceType: deviceType,
		Health: RoleHealth{
			Status:     status,
			StatusCode: status,
			Detail:     detail,
			Details: map[string]any{
				"running_status": "Ready",
				"runtime":        "go",
				"memory_items":   len(memoryEntries),
				"storage_items":  len(storageEntries),
				"network_items":  len(networkAdapters),
				"cpu_present":    len(cpuPayload) > 0,
			},
		},
	}
}

func collectCPU(ctx context.Context) (map[string]any, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := commandOutput(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors,MaxClockSpeed | ConvertTo-Json -Compress")
		if err == nil && strings.TrimSpace(out) != "" {
			items := jsonObjects(out)
			name := ""
			physical := 0
			logical := 0
			mhz := 0
			for index, item := range items {
				if index == 0 {
					name = strings.TrimSpace(asString(item["Name"]))
					mhz = asInt(item["MaxClockSpeed"])
				}
				physical += asInt(item["NumberOfCores"])
				logical += asInt(item["NumberOfLogicalProcessors"])
			}
			return cleanMap(map[string]any{
				"name":           name,
				"physical_cores": positiveOrNil(physical),
				"logical_cores":  positiveOrNil(logical),
				"base_clock_ghz": mhzToGHz(mhz),
			}), nil
		}
	case "linux":
		cpu := parseProcCPUInfo(readFile("/proc/cpuinfo"))
		if len(cpu) > 0 {
			return cpu, nil
		}
	}
	return map[string]any{
		"name":          runtime.GOARCH,
		"logical_cores": runtime.NumCPU(),
	}, nil
}

func collectMemory(ctx context.Context) ([]map[string]any, map[string]any, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := commandOutput(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "Get-CimInstance Win32_PhysicalMemory | Select-Object BankLabel,Speed,SerialNumber,Capacity | ConvertTo-Json -Compress")
		if err != nil {
			return nil, nil, err
		}
		entries := []map[string]any{}
		for _, item := range jsonObjects(out) {
			capacity := asInt64(item["Capacity"])
			if capacity <= 0 {
				continue
			}
			entries = append(entries, map[string]any{
				"slot":     fallbackString(asString(item["BankLabel"]), "physical"),
				"speed":    fallbackString(asString(item["Speed"]), "unknown"),
				"serial":   fallbackString(asString(item["SerialNumber"]), "unknown"),
				"capacity": capacity,
			})
		}
		metrics := windowsMemoryMetrics(ctx)
		return entries, metrics, nil
	case "linux":
		total, available := parseProcMemInfo(readFile("/proc/meminfo"))
		if total <= 0 {
			return nil, nil, fmt.Errorf("unable to read /proc/meminfo")
		}
		metrics := map[string]any{}
		if available > 0 {
			metrics["memory_percent"] = roundPercent(float64(total-available) / float64(total) * 100)
		}
		return []map[string]any{{
			"slot":     "physical",
			"speed":    "unknown",
			"serial":   "unknown",
			"capacity": total,
		}}, metrics, nil
	default:
		return nil, nil, nil
	}
}

func collectStorage(ctx context.Context) ([]map[string]any, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := commandOutput(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "Get-CimInstance Win32_LogicalDisk -Filter \"DriveType=3\" | Select-Object DeviceID,Size,FreeSpace | ConvertTo-Json -Compress")
		if err != nil {
			return nil, err
		}
		disks := []map[string]any{}
		for _, item := range jsonObjects(out) {
			total := asInt64(item["Size"])
			free := asInt64(item["FreeSpace"])
			if total <= 0 {
				continue
			}
			used := total - free
			disks = append(disks, map[string]any{
				"drive":     fallbackString(asString(item["DeviceID"]), "disk"),
				"disk_type": "Fixed Disk",
				"usage":     roundPercent(float64(used) / float64(total) * 100),
				"total":     total,
				"free":      free,
				"used":      used,
			})
		}
		return disks, nil
	case "linux":
		out, err := commandOutput(ctx, "df", "-B1", "-P", "-T")
		if err != nil {
			return nil, err
		}
		return parseDFOutput(out), nil
	default:
		return nil, nil
	}
}

func collectNetwork() []map[string]any {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	adapters := []map[string]any{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		ips := []string{}
		for _, addr := range addrs {
			ip := ipFromAddr(addr)
			if ip == "" {
				continue
			}
			ips = append(ips, ip)
		}
		if len(ips) == 0 {
			continue
		}
		adapters = append(adapters, map[string]any{
			"adapter":    iface.Name,
			"ips":        ips,
			"mac":        fallbackString(iface.HardwareAddr.String(), "unknown"),
			"link_speed": "",
		})
	}
	return adapters
}

func collectUptime(ctx context.Context) int64 {
	if runtime.GOOS == "linux" {
		fields := strings.Fields(readFile("/proc/uptime"))
		if len(fields) > 0 {
			value, _ := strconv.ParseFloat(fields[0], 64)
			return int64(value)
		}
	}
	if runtime.GOOS == "windows" {
		out, err := commandOutput(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "$os=Get-CimInstance Win32_OperatingSystem; [int64]((Get-Date)-$os.LastBootUpTime).TotalSeconds")
		if err == nil {
			value, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
			return value
		}
	}
	return 0
}

func currentUserName() string {
	for _, key := range []string{"SUDO_USER", "USERNAME", "USER", "LOGNAME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func detectDeviceType() string {
	if runtime.GOOS == "windows" {
		return "Workstation"
	}
	if runtime.GOOS == "linux" {
		return "Server"
	}
	return strings.Title(runtime.GOOS)
}

func commandOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	output, err := cmd.Output()
	if cmdCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s timed out", name)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func parseProcCPUInfo(text string) map[string]any {
	name := ""
	logical := 0
	cores := 0
	mhz := 0.0
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "model name":
			if name == "" {
				name = value
			}
		case "processor":
			logical++
		case "cpu cores":
			if cores == 0 {
				cores = asInt(value)
			}
		case "cpu mhz":
			if mhz == 0 {
				mhz, _ = strconv.ParseFloat(value, 64)
			}
		}
	}
	return cleanMap(map[string]any{
		"name":           name,
		"physical_cores": positiveOrNil(cores),
		"logical_cores":  positiveOrNil(logical),
		"base_clock_ghz": mhzToGHzFloat(mhz),
	})
}

func parseProcMemInfo(text string) (total int64, available int64) {
	for _, raw := range strings.Split(text, "\n") {
		fields := strings.Fields(raw)
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseInt(fields[1], 10, 64)
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value * 1024
		case "MemAvailable":
			available = value * 1024
		}
	}
	return total, available
}

func parseDFOutput(text string) []map[string]any {
	skip := map[string]bool{
		"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true,
		"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
		"nsfs": true, "mqueue": true, "fusectl": true, "debugfs": true,
		"tracefs": true, "configfs": true, "securityfs": true, "pstore": true,
	}
	disks := []map[string]any{}
	for index, raw := range strings.Split(text, "\n") {
		if index == 0 {
			continue
		}
		fields := strings.Fields(raw)
		if len(fields) < 7 {
			continue
		}
		fstype := strings.ToLower(fields[1])
		mount := fields[6]
		if skip[fstype] || strings.HasPrefix(mount, "/snap/") {
			continue
		}
		total := parseInt64(fields[2])
		used := parseInt64(fields[3])
		free := parseInt64(fields[4])
		if total <= 0 {
			continue
		}
		disks = append(disks, map[string]any{
			"drive":     mount,
			"disk_type": "Fixed Disk",
			"usage":     roundPercent(float64(used) / float64(total) * 100),
			"total":     total,
			"free":      free,
			"used":      used,
		})
	}
	return disks
}

func windowsMemoryMetrics(ctx context.Context) map[string]any {
	out, err := commandOutput(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "$os=Get-CimInstance Win32_OperatingSystem; [pscustomobject]@{Total=$os.TotalVisibleMemorySize;Free=$os.FreePhysicalMemory} | ConvertTo-Json -Compress")
	if err != nil {
		return nil
	}
	items := jsonObjects(out)
	if len(items) == 0 {
		return nil
	}
	total := asInt64(items[0]["Total"])
	free := asInt64(items[0]["Free"])
	if total <= 0 {
		return nil
	}
	return map[string]any{"memory_percent": roundPercent(float64(total-free) / float64(total) * 100)}
}

func jsonObjects(text string) []map[string]any {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &list); err == nil {
		return list
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(trimmed), &item); err == nil {
		return []map[string]any{item}
	}
	return nil
}

func ipFromAddr(addr net.Addr) string {
	var ip net.IP
	switch typed := addr.(type) {
	case *net.IPNet:
		ip = typed.IP
	case *net.IPAddr:
		ip = typed.IP
	default:
		return ""
	}
	ip = ip.To4()
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return ""
	}
	return ip.String()
}

func firstInternalIP(adapters []map[string]any) string {
	for _, adapter := range adapters {
		ips, _ := adapter["ips"].([]string)
		for _, ip := range ips {
			if strings.TrimSpace(ip) != "" {
				return ip
			}
		}
	}
	return ""
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func asString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func asInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		value, _ := strconv.Atoi(strings.TrimSpace(typed))
		return value
	default:
		return 0
	}
}

func asInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func fallbackString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func positiveOrNil(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func mhzToGHz(value int) any {
	if value <= 0 {
		return nil
	}
	return math.Round((float64(value)/1000)*100) / 100
}

func mhzToGHzFloat(value float64) any {
	if value <= 0 {
		return nil
	}
	return math.Round((value/1000)*100) / 100
}

func roundPercent(value float64) float64 {
	return math.Round(value*100) / 100
}

func cleanMap(input map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		out[key] = value
	}
	return out
}
