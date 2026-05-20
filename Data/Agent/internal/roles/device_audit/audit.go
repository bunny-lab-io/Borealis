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

type platformMetadata struct {
	OperatingSystem string
	LastReboot      string
	LastUser        string
	Domain          string
	Manufacturer    string
	SystemModelRaw  string
	SystemModel     string
	SystemSerial    string
	BoardSerial     string
}

func NewAuditor() *Auditor {
	return &Auditor{}
}

func collectPlatformMetadata(ctx context.Context) platformMetadata {
	switch runtime.GOOS {
	case "windows":
		return collectWindowsPlatformMetadata(ctx)
	case "linux":
		return collectLinuxPlatformMetadata()
	default:
		return platformMetadata{}
	}
}

func collectWindowsPlatformMetadata(ctx context.Context) platformMetadata {
	const script = `$ErrorActionPreference='SilentlyContinue'; $os=Get-CimInstance Win32_OperatingSystem; $cs=Get-CimInstance Win32_ComputerSystem; $bios=Get-CimInstance Win32_BIOS; $baseboard=Get-CimInstance Win32_BaseBoard; $cv=Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'; $logon=Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\LogonUI'; $lastBoot=''; if($os -and $os.LastBootUpTime){ $lastBoot=$os.LastBootUpTime.ToString('MM/dd/yyyy @ hh:mmtt') }; [pscustomobject]@{ Caption=$os.Caption; DisplayVersion=$cv.DisplayVersion; ReleaseId=$cv.ReleaseId; BuildNumber=$os.BuildNumber; UBR=$cv.UBR; Version=$os.Version; LastBootUpTime=$lastBoot; UserName=$cs.UserName; LastLoggedOnSAMUser=$logon.LastLoggedOnSAMUser; LastLoggedOnUser=$logon.LastLoggedOnUser; ComputerName=$env:COMPUTERNAME; Domain=$cs.Domain; Workgroup=$cs.Workgroup; PartOfDomain=[bool]$cs.PartOfDomain; Manufacturer=$cs.Manufacturer; Model=$cs.Model; SerialNumber=$bios.SerialNumber; BaseBoardSerialNumber=$baseboard.SerialNumber } | ConvertTo-Json -Compress -Depth 4`
	out, err := commandOutput(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		return platformMetadata{}
	}
	items := jsonObjects(out)
	if len(items) == 0 {
		return platformMetadata{}
	}
	item := items[0]
	manufacturer := normalizeHardwareString(asString(item["Manufacturer"]))
	modelRaw := normalizeHardwareString(asString(item["Model"]))
	systemSerial := normalizeHardwareString(asString(item["SerialNumber"]))
	boardSerial := normalizeHardwareString(asString(item["BaseBoardSerialNumber"]))
	return platformMetadata{
		OperatingSystem: formatWindowsOperatingSystem(
			asString(item["Caption"]),
			asString(item["DisplayVersion"]),
			asString(item["ReleaseId"]),
			asString(item["BuildNumber"]),
			asInt(item["UBR"]),
			asString(item["Version"]),
		),
		LastReboot:     strings.TrimSpace(asString(item["LastBootUpTime"])),
		LastUser:       normalizeWindowsLastUser(item),
		Domain:         normalizeWindowsDomainValue(item),
		Manufacturer:   manufacturer,
		SystemModelRaw: modelRaw,
		SystemModel:    combineManufacturerModel(manufacturer, modelRaw),
		SystemSerial:   firstNonEmpty(systemSerial, boardSerial),
		BoardSerial:    boardSerial,
	}
}

func collectLinuxPlatformMetadata() platformMetadata {
	osRelease := parseOSRelease(readFile("/etc/os-release"))
	manufacturer := normalizeHardwareString(firstNonEmpty(
		readFile("/sys/class/dmi/id/sys_vendor"),
		readFile("/sys/devices/virtual/dmi/id/sys_vendor"),
	))
	modelRaw := normalizeHardwareString(firstNonEmpty(
		readFile("/sys/class/dmi/id/product_name"),
		readFile("/sys/devices/virtual/dmi/id/product_name"),
	))
	serial := normalizeHardwareString(firstNonEmpty(
		readFile("/sys/class/dmi/id/product_serial"),
		readFile("/sys/devices/virtual/dmi/id/product_serial"),
	))
	boardSerial := normalizeHardwareString(firstNonEmpty(
		readFile("/sys/class/dmi/id/board_serial"),
		readFile("/sys/devices/virtual/dmi/id/board_serial"),
	))
	return platformMetadata{
		OperatingSystem: firstNonEmpty(osRelease["PRETTY_NAME"], osRelease["NAME"]),
		LastUser:        currentUserName(),
		Manufacturer:    manufacturer,
		SystemModelRaw:  modelRaw,
		SystemModel:     combineManufacturerModel(manufacturer, modelRaw),
		SystemSerial:    firstNonEmpty(serial, boardSerial),
		BoardSerial:     boardSerial,
	}
}

func (a *Auditor) Collect(ctx context.Context) Snapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	platform := collectPlatformMetadata(ctx)
	networkAdapters := collectNetwork(ctx)
	internalIP := firstInternalIP(networkAdapters)
	memoryEntries, memoryMetrics, memoryErr := collectMemory(ctx)
	storageEntries, storageErr := collectStorage(ctx)
	cpuPayload, cpuErr := collectCPU(ctx)
	cpuPayload = addPlatformHardwareIdentity(cpuPayload, platform)
	metrics := map[string]any{}
	if platform.LastUser != "" {
		metrics["last_user"] = platform.LastUser
	} else if userName := currentUserName(); userName != "" {
		metrics["last_user"] = userName
	}
	if platform.Domain != "" {
		metrics["domain"] = platform.Domain
	}
	if platform.OperatingSystem != "" {
		metrics["operating_system"] = platform.OperatingSystem
	}
	if platform.LastReboot != "" {
		metrics["last_reboot"] = platform.LastReboot
	}
	for key, value := range memoryMetrics {
		metrics[key] = value
	}
	if uptime := collectUptime(ctx); uptime > 0 {
		metrics["uptime"] = uptime
		if _, ok := metrics["last_reboot"]; !ok {
			metrics["last_reboot"] = formatLastRebootFromUptime(uptime)
		}
	}
	deviceType := detectDeviceType(platform.OperatingSystem)
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
		const script = `
$ErrorActionPreference='SilentlyContinue'
$rows=@()
Get-CimInstance Win32_LogicalDisk | Where-Object { $_.DriveType -in 2,3,5 } | ForEach-Object {
  $logical=$_
  $letter=($logical.DeviceID -replace ':','')
  $mediaType=''
  $busType=''
  $diskName=''
  if($logical.DriveType -in 2,3 -and -not [string]::IsNullOrWhiteSpace($letter)){
    $partition=Get-Partition -DriveLetter $letter -ErrorAction SilentlyContinue | Select-Object -First 1
    if($partition){
      $disk=Get-Disk -Number $partition.DiskNumber -ErrorAction SilentlyContinue
      if($disk){
        $mediaType=[string]$disk.MediaType
        $busType=[string]$disk.BusType
        $diskName=[string]$disk.FriendlyName
        if([string]::IsNullOrWhiteSpace($mediaType) -or $mediaType -eq 'Unspecified'){
          $physical=Get-PhysicalDisk -ErrorAction SilentlyContinue | Where-Object { $_.DeviceId -eq $disk.Number -or $_.FriendlyName -eq $disk.FriendlyName } | Select-Object -First 1
          if($physical){ $mediaType=[string]$physical.MediaType }
        }
      }
    }
  }
  $rows += [pscustomobject]@{ DeviceID=$logical.DeviceID; VolumeName=$logical.VolumeName; DriveType=$logical.DriveType; Size=$logical.Size; FreeSpace=$logical.FreeSpace; MediaType=$mediaType; BusType=$busType; DiskName=$diskName }
}
$rows | ConvertTo-Json -Compress -Depth 4`
		out, err := commandOutput(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
		if err != nil {
			return nil, err
		}
		disks := []map[string]any{}
		for _, item := range jsonObjects(out) {
			driveType := asInt(item["DriveType"])
			total := asInt64(item["Size"])
			free := asInt64(item["FreeSpace"])
			if total <= 0 && driveType == 3 {
				continue
			}
			mediaType := normalizeWindowsMediaType(asString(item["MediaType"]))
			diskType := windowsStorageDiskType(driveType, mediaType)
			entry := map[string]any{
				"drive":      fallbackString(asString(item["DeviceID"]), "disk"),
				"disk_type":  diskType,
				"drive_type": windowsDriveTypeName(driveType),
			}
			if mediaType != "" {
				entry["media_type"] = mediaType
			}
			if busType := strings.TrimSpace(asString(item["BusType"])); busType != "" && !strings.EqualFold(busType, "Unknown") {
				entry["bus_type"] = busType
			}
			if diskName := strings.TrimSpace(asString(item["DiskName"])); diskName != "" {
				entry["disk_name"] = diskName
			}
			if volumeName := strings.TrimSpace(asString(item["VolumeName"])); volumeName != "" {
				entry["volume_name"] = volumeName
			}
			if total > 0 {
				used := total - free
				entry["usage"] = roundPercent(float64(used) / float64(total) * 100)
				entry["total"] = total
				entry["free"] = free
				entry["used"] = used
			}
			disks = append(disks, cleanMap(entry))
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

func collectNetwork(ctx context.Context) []map[string]any {
	if runtime.GOOS == "windows" {
		if adapters := collectWindowsNetwork(ctx); len(adapters) > 0 {
			return adapters
		}
	}
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
		linkSpeed := ""
		if runtime.GOOS == "linux" {
			linkSpeed = linuxInterfaceLinkSpeed(iface.Name)
		}
		adapters = append(adapters, map[string]any{
			"adapter":    iface.Name,
			"ips":        ips,
			"mac":        fallbackString(iface.HardwareAddr.String(), "unknown"),
			"link_speed": fallbackString(linkSpeed, "unknown"),
		})
	}
	return adapters
}

func collectWindowsNetwork(ctx context.Context) []map[string]any {
	const script = `$ErrorActionPreference='SilentlyContinue'; $configs=Get-CimInstance Win32_NetworkAdapterConfiguration -Filter "IPEnabled=True"; $rows=@(); foreach($cfg in $configs){ $adapter=Get-CimInstance Win32_NetworkAdapter -Filter "Index=$($cfg.Index)" -ErrorAction SilentlyContinue; $ips=@($cfg.IPAddress | Where-Object { $_ -match '^\d{1,3}(\.\d{1,3}){3}$' }); if($ips.Count -eq 0){ continue }; $name=$adapter.NetConnectionID; if([string]::IsNullOrWhiteSpace($name)){ $name=$adapter.Name }; if([string]::IsNullOrWhiteSpace($name)){ $name=$cfg.Description }; $rows += [pscustomobject]@{ Adapter=$name; Description=$cfg.Description; MACAddress=$cfg.MACAddress; IPAddress=$ips; Speed=$adapter.Speed } }; $rows | ConvertTo-Json -Compress -Depth 4`
	out, err := commandOutput(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		return nil
	}
	adapters := []map[string]any{}
	for _, item := range jsonObjects(out) {
		ips := asStringSlice(item["IPAddress"])
		if len(ips) == 0 {
			continue
		}
		adapters = append(adapters, cleanMap(map[string]any{
			"adapter":     fallbackString(asString(item["Adapter"]), fallbackString(asString(item["Description"]), "Adapter")),
			"description": strings.TrimSpace(asString(item["Description"])),
			"ips":         ips,
			"mac":         fallbackString(asString(item["MACAddress"]), "unknown"),
			"link_speed":  fallbackString(formatBitsPerSecond(asInt64(item["Speed"])), "unknown"),
		}))
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

func detectDeviceType(osName string) string {
	if runtime.GOOS == "windows" {
		if strings.Contains(strings.ToLower(osName), "server") {
			return "Server"
		}
		return "Workstation"
	}
	if runtime.GOOS == "linux" {
		return "Server"
	}
	return strings.Title(runtime.GOOS)
}

func addPlatformHardwareIdentity(cpu map[string]any, platform platformMetadata) map[string]any {
	if cpu == nil {
		cpu = map[string]any{}
	}
	if platform.Manufacturer != "" {
		cpu["system_manufacturer"] = platform.Manufacturer
	}
	if platform.SystemModelRaw != "" {
		cpu["system_model_raw"] = platform.SystemModelRaw
	}
	if platform.SystemModel != "" {
		cpu["system_model"] = platform.SystemModel
	}
	if serial := firstNonEmpty(platform.SystemSerial, platform.BoardSerial); serial != "" {
		cpu["system_serial_number"] = serial
		cpu["serial_number"] = serial
	}
	if platform.BoardSerial != "" {
		cpu["baseboard_serial_number"] = platform.BoardSerial
		cpu["motherboard_serial_number"] = platform.BoardSerial
	}
	return cpu
}

func formatWindowsOperatingSystem(caption string, displayVersion string, releaseID string, buildNumber string, ubr int, version string) string {
	caption = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(caption), "Microsoft "))
	displayVersion = firstNonEmpty(displayVersion, releaseID)
	buildNumber = strings.TrimSpace(buildNumber)
	if buildNumber == "" {
		parts := strings.Split(strings.TrimSpace(version), ".")
		if len(parts) >= 3 {
			buildNumber = parts[2]
		}
	}
	buildText := buildNumber
	if buildText != "" && ubr > 0 {
		buildText = fmt.Sprintf("%s.%d", buildText, ubr)
	}
	parts := []string{}
	if caption != "" {
		parts = append(parts, caption)
	}
	if displayVersion != "" && !strings.Contains(strings.ToLower(caption), strings.ToLower(displayVersion)) {
		parts = append(parts, displayVersion)
	}
	if buildText != "" {
		parts = append(parts, "Build "+buildText)
	}
	return strings.Join(parts, " ")
}

func normalizeWindowsLastUser(item map[string]any) string {
	computerName := strings.TrimSpace(asString(item["ComputerName"]))
	domainName := strings.TrimSpace(asString(item["Domain"]))
	partOfDomain := asBool(item["PartOfDomain"])
	currentRaw := asString(item["UserName"])
	current := normalizeWindowsUserValue(currentRaw, computerName, domainName, partOfDomain)
	if current != "" && (strings.Contains(currentRaw, `\`) || strings.Contains(currentRaw, "@")) {
		return current
	}
	if sam := normalizeWindowsUserValue(asString(item["LastLoggedOnSAMUser"]), computerName, domainName, partOfDomain); sam != "" {
		return sam
	}
	if current != "" {
		return current
	}
	if last := normalizeWindowsUserValue(asString(item["LastLoggedOnUser"]), computerName, domainName, partOfDomain); last != "" {
		return last
	}
	return ""
}

func normalizeWindowsDomainValue(item map[string]any) string {
	computerName := strings.TrimSpace(asString(item["ComputerName"]))
	domainName := strings.TrimSpace(asString(item["Domain"]))
	workgroupName := strings.TrimSpace(asString(item["Workgroup"]))
	partOfDomain := asBool(item["PartOfDomain"])
	if partOfDomain {
		if domainName != "" && !strings.EqualFold(domainName, computerName) {
			return domainName
		}
		for _, key := range []string{"LastLoggedOnSAMUser", "LastLoggedOnUser", "UserName"} {
			if prefix := windowsUserDomainPrefix(asString(item[key]), computerName); prefix != "" {
				return prefix
			}
		}
		return ""
	}
	if workgroupName != "" && !strings.EqualFold(workgroupName, computerName) {
		return workgroupName
	}
	if domainName != "" && !strings.EqualFold(domainName, computerName) {
		return domainName
	}
	return ""
}

func windowsUserDomainPrefix(value string, computerName string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, `\`) {
		parts := strings.SplitN(value, `\`, 2)
		prefix := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		if prefix == "" || prefix == "." || strings.EqualFold(prefix, computerName) || isIgnoredWindowsUser(name) {
			return ""
		}
		return prefix
	}
	if strings.Contains(value, "@") {
		parts := strings.SplitN(value, "@", 2)
		if isIgnoredWindowsUser(parts[0]) {
			return ""
		}
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func normalizeWindowsUserValue(value string, computerName string, domainName string, partOfDomain bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, `.\`) {
		name := strings.TrimSpace(strings.TrimPrefix(value, `.\`))
		if isIgnoredWindowsUser(name) {
			return ""
		}
		if computerName == "" {
			return name
		}
		return computerName + `\` + name
	}
	if strings.Contains(value, `\`) {
		parts := strings.SplitN(value, `\`, 2)
		prefix := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		if isIgnoredWindowsUser(name) {
			return ""
		}
		if prefix == "" || prefix == "." {
			prefix = computerName
		}
		if prefix == "" {
			prefix = preferredWindowsUserPrefix(computerName, domainName, partOfDomain)
		}
		if prefix == "" {
			return name
		}
		return prefix + `\` + name
	}
	if strings.Contains(value, "@") {
		if isIgnoredWindowsUser(strings.SplitN(value, "@", 2)[0]) {
			return ""
		}
		return value
	}
	if isIgnoredWindowsUser(value) {
		return ""
	}
	prefix := preferredWindowsUserPrefix(computerName, domainName, partOfDomain)
	if prefix == "" {
		return value
	}
	return prefix + `\` + value
}

func preferredWindowsUserPrefix(computerName string, domainName string, partOfDomain bool) string {
	computerName = strings.TrimSpace(computerName)
	domainName = strings.TrimSpace(domainName)
	if partOfDomain && domainName != "" && !strings.EqualFold(domainName, computerName) {
		return domainName
	}
	return computerName
}

func isIgnoredWindowsUser(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return true
	}
	upper := strings.ToUpper(trimmed)
	return upper == "SYSTEM" || upper == "LOCAL SYSTEM" || upper == "NETWORK SERVICE" || upper == "LOCAL SERVICE" || strings.HasSuffix(upper, "$")
}

func normalizeHardwareString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	placeholders := []string{
		"none",
		"null",
		"unknown",
		"system product name",
		"system serial number",
		"base board serial number",
		"serial number",
		"to be filled by o.e.m.",
		"to be filled by oem",
		"default string",
		"not specified",
		"not available",
		"not applicable",
	}
	for _, placeholder := range placeholders {
		if lower == placeholder {
			return ""
		}
	}
	return trimmed
}

func combineManufacturerModel(manufacturer string, model string) string {
	manufacturer = strings.TrimSpace(manufacturer)
	model = strings.TrimSpace(model)
	if manufacturer == "" {
		return model
	}
	if model == "" {
		return manufacturer
	}
	if strings.Contains(strings.ToLower(model), strings.ToLower(manufacturer)) {
		return model
	}
	return manufacturer + " " + model
}

func formatLastRebootFromUptime(uptimeSeconds int64) string {
	if uptimeSeconds <= 0 {
		return ""
	}
	return time.Now().Add(-time.Duration(uptimeSeconds) * time.Second).Format("01/02/2006 @ 03:04PM")
}

func windowsDriveTypeName(driveType int) string {
	switch driveType {
	case 2:
		return "Removable Disk"
	case 3:
		return "Fixed Disk"
	case 4:
		return "Network Drive"
	case 5:
		return "CD-ROM"
	default:
		return "Disk"
	}
}

func windowsStorageDiskType(driveType int, mediaType string) string {
	mediaType = normalizeWindowsMediaType(mediaType)
	if driveType == 5 {
		return "CD-ROM"
	}
	if mediaType != "" {
		return mediaType
	}
	return windowsDriveTypeName(driveType)
}

func normalizeWindowsMediaType(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	switch upper {
	case "0", "UNSPECIFIED", "UNKNOWN":
		return ""
	case "3", "HDD":
		return "HDD"
	case "4", "SSD":
		return "SSD"
	case "5", "SCM":
		return "SCM"
	}
	if strings.Contains(upper, "SSD") || strings.Contains(upper, "SOLID STATE") {
		return "SSD"
	}
	if strings.Contains(upper, "HDD") || strings.Contains(upper, "ROTATIONAL") || strings.Contains(upper, "HARD DISK") {
		return "HDD"
	}
	return trimmed
}

func linuxStorageDiskType(source string, fstype string) string {
	switch strings.ToLower(strings.TrimSpace(fstype)) {
	case "iso9660", "udf":
		return "CD-ROM"
	}
	root := linuxRootBlockDevice(source)
	if root == "" {
		return "Fixed Disk"
	}
	if rotational := strings.TrimSpace(readFile("/sys/block/" + root + "/queue/rotational")); rotational != "" {
		if rotational == "0" {
			return "SSD"
		}
		if rotational == "1" {
			return "HDD"
		}
	}
	if removable := strings.TrimSpace(readFile("/sys/block/" + root + "/removable")); removable == "1" {
		return "Removable Disk"
	}
	return "Fixed Disk"
}

func linuxRootBlockDevice(source string) string {
	name := strings.TrimSpace(source)
	if strings.HasPrefix(name, "/dev/") {
		name = strings.TrimPrefix(name, "/dev/")
	}
	if name == "" || strings.Contains(name, "/") {
		return ""
	}
	candidates := linuxBlockDeviceCandidates(name)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat("/sys/block/" + candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func linuxBlockDeviceCandidates(name string) []string {
	candidates := []string{name}
	if strings.HasPrefix(name, "nvme") && strings.Contains(name, "p") {
		if index := strings.LastIndex(name, "p"); index > 0 {
			candidates = append(candidates, name[:index])
		}
	}
	if strings.HasPrefix(name, "mmcblk") && strings.Contains(name, "p") {
		if index := strings.LastIndex(name, "p"); index > 0 {
			candidates = append(candidates, name[:index])
		}
	}
	for len(name) > 0 {
		last := name[len(name)-1]
		if last < '0' || last > '9' {
			break
		}
		name = name[:len(name)-1]
	}
	if name != "" {
		candidates = append(candidates, name)
	}
	return candidates
}

func linuxInterfaceLinkSpeed(name string) string {
	value := strings.TrimSpace(readFile("/sys/class/net/" + name + "/speed"))
	if value == "" || strings.HasPrefix(value, "-") {
		return ""
	}
	mbps, err := strconv.ParseInt(value, 10, 64)
	if err != nil || mbps <= 0 {
		return ""
	}
	return formatBitsPerSecond(mbps * 1000 * 1000)
}

func formatBitsPerSecond(bitsPerSecond int64) string {
	if bitsPerSecond <= 0 {
		return ""
	}
	units := []struct {
		label string
		value float64
	}{
		{"Tbps", 1000 * 1000 * 1000 * 1000},
		{"Gbps", 1000 * 1000 * 1000},
		{"Mbps", 1000 * 1000},
		{"Kbps", 1000},
	}
	value := float64(bitsPerSecond)
	for _, unit := range units {
		if value >= unit.value {
			rate := value / unit.value
			if math.Abs(rate-math.Round(rate)) < 0.01 {
				return fmt.Sprintf("%.0f %s", rate, unit.label)
			}
			return fmt.Sprintf("%.1f %s", rate, unit.label)
		}
	}
	return fmt.Sprintf("%d bps", bitsPerSecond)
}

func parseOSRelease(text string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
			"drive":          mount,
			"storage_source": fields[0],
			"disk_type":      linuxStorageDiskType(fields[0], fstype),
			"usage":          roundPercent(float64(used) / float64(total) * 100),
			"total":          total,
			"free":           free,
			"used":           used,
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

func asStringSlice(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		out := []string{}
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := []string{}
		for _, item := range typed {
			if trimmed := strings.TrimSpace(asString(item)); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case string:
		if trimmed := strings.TrimSpace(typed); trimmed != "" {
			return []string{trimmed}
		}
		return nil
	default:
		if trimmed := strings.TrimSpace(fmt.Sprint(typed)); trimmed != "" {
			return []string{trimmed}
		}
		return nil
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

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed
	default:
		return false
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
