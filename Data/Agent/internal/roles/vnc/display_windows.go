//go:build windows

package vnc

import (
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	displayDeviceAttachedToDesktop = 0x00000001
	displayDevicePrimaryDevice     = 0x00000004
	displayDeviceMirroringDriver   = 0x00000008
	enumCurrentSettings            = 0xffffffff
	monitorInfoPrimary             = 0x00000001
)

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	procEnumDisplayDevicesW    = user32.NewProc("EnumDisplayDevicesW")
	procEnumDisplaySettingsExW = user32.NewProc("EnumDisplaySettingsExW")
	procEnumDisplayMonitors    = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW        = user32.NewProc("GetMonitorInfoW")
)

type pointL struct {
	X int32
	Y int32
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type displayDeviceW struct {
	Cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

type devModeW struct {
	DeviceName         [32]uint16
	SpecVersion        uint16
	DriverVersion      uint16
	Size               uint16
	DriverExtra        uint16
	Fields             uint32
	Position           pointL
	DisplayOrientation uint32
	DisplayFixedOutput uint32
	Color              int16
	Duplex             int16
	YResolution        int16
	TTOption           int16
	Collate            int16
	FormName           [32]uint16
	LogPixels          uint16
	BitsPerPel         uint32
	PelsWidth          uint32
	PelsHeight         uint32
	DisplayFlags       uint32
	DisplayFrequency   uint32
	ICMMethod          uint32
	ICMIntent          uint32
	MediaType          uint32
	DitherType         uint32
	Reserved1          uint32
	Reserved2          uint32
	PanningWidth       uint32
	PanningHeight      uint32
}

type monitorInfoExW struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	Flags     uint32
	Device    [32]uint16
}

func collectDisplayTopology() []map[string]any {
	return selectDisplayTopology(
		collectWindowsDisplayTopologyViaDisplaySettings(),
		collectWindowsDisplayTopologyViaMonitors(),
	)
}

func collectWindowsDisplayTopologyViaDisplaySettings() []map[string]any {
	topology := []map[string]any{}
	for index := uint32(0); ; index++ {
		adapter := displayDeviceW{Cb: uint32(unsafe.Sizeof(displayDeviceW{}))}
		ok, _, _ := procEnumDisplayDevicesW.Call(
			0,
			uintptr(index),
			uintptr(unsafe.Pointer(&adapter)),
			0,
		)
		if ok == 0 {
			break
		}
		stateFlags := adapter.StateFlags
		deviceName := windows.UTF16ToString(adapter.DeviceName[:])
		if deviceName == "" {
			continue
		}
		if stateFlags&displayDeviceMirroringDriver != 0 {
			continue
		}
		if stateFlags&displayDeviceAttachedToDesktop == 0 {
			continue
		}
		settings := devModeW{Size: uint16(unsafe.Sizeof(devModeW{}))}
		hasSettings, _, _ := procEnumDisplaySettingsExW.Call(
			uintptr(unsafe.Pointer(&adapter.DeviceName[0])),
			uintptr(enumCurrentSettings),
			uintptr(unsafe.Pointer(&settings)),
			0,
		)
		if hasSettings == 0 {
			continue
		}
		left := int(settings.Position.X)
		top := int(settings.Position.Y)
		width := maxInt(0, int(settings.PelsWidth))
		height := maxInt(0, int(settings.PelsHeight))
		if width <= 0 || height <= 0 {
			continue
		}
		right := left + width
		bottom := top + height
		displayIndex := displayIndexFromDeviceName(deviceName, len(topology)+1)
		topology = append(topology, map[string]any{
			"id":            strconv.Itoa(displayIndex),
			"display_index": displayIndex,
			"label":         strconv.Itoa(displayIndex),
			"device_name":   deviceName,
			"device_string": windows.UTF16ToString(adapter.DeviceString[:]),
			"left":          left,
			"top":           top,
			"right":         right,
			"bottom":        bottom,
			"width":         width,
			"height":        height,
			"work_left":     left,
			"work_top":      top,
			"work_right":    right,
			"work_bottom":   bottom,
			"work_width":    width,
			"work_height":   height,
			"primary":       stateFlags&displayDevicePrimaryDevice != 0,
			"source":        "display_settings",
		})
	}
	return sortDisplayTopology(topology)
}

func collectWindowsDisplayTopologyViaMonitors() []map[string]any {
	topology := []map[string]any{}
	callback := syscall.NewCallback(func(hMonitor uintptr, hdc uintptr, rectPtr uintptr, lparam uintptr) uintptr {
		info := monitorInfoExW{CbSize: uint32(unsafe.Sizeof(monitorInfoExW{}))}
		ok, _, _ := procGetMonitorInfoW.Call(
			hMonitor,
			uintptr(unsafe.Pointer(&info)),
		)
		if ok == 0 {
			return 1
		}
		deviceName := windows.UTF16ToString(info.Device[:])
		left := int(info.RcMonitor.Left)
		top := int(info.RcMonitor.Top)
		right := int(info.RcMonitor.Right)
		bottom := int(info.RcMonitor.Bottom)
		workLeft := int(info.RcWork.Left)
		workTop := int(info.RcWork.Top)
		workRight := int(info.RcWork.Right)
		workBottom := int(info.RcWork.Bottom)
		displayIndex := displayIndexFromDeviceName(deviceName, len(topology)+1)
		topology = append(topology, map[string]any{
			"id":            strconv.Itoa(displayIndex),
			"display_index": displayIndex,
			"label":         strconv.Itoa(displayIndex),
			"device_name":   deviceName,
			"left":          left,
			"top":           top,
			"right":         right,
			"bottom":        bottom,
			"width":         maxInt(0, right-left),
			"height":        maxInt(0, bottom-top),
			"work_left":     workLeft,
			"work_top":      workTop,
			"work_right":    workRight,
			"work_bottom":   workBottom,
			"work_width":    maxInt(0, workRight-workLeft),
			"work_height":   maxInt(0, workBottom-workTop),
			"primary":       info.Flags&monitorInfoPrimary != 0,
			"source":        "monitor_info",
		})
		return 1
	})
	_, _, _ = procEnumDisplayMonitors.Call(0, 0, callback, 0)
	return sortDisplayTopology(topology)
}
