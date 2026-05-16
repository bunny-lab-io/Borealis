//go:build windows

package currentuser

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
	"golang.org/x/sys/windows"
)

const (
	wmDestroy      = 0x0002
	wmRButtonUp    = 0x0205
	wmApp          = 0x8000
	wmTrayCallback = wmApp + 74

	nimAdd     = 0x00000000
	nimDelete  = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString       = 0x00000000
	mfGrayed       = 0x00000001
	mfDisabled     = 0x00000002
	mfSeparator    = 0x00000800
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	agentIconResourceID = 1
	idiApplication      = 32512

	menuRestartAgent = 1001
	menuUpdateCheck  = 1002
	menuRoleBase     = 2000
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	trayMu                  sync.Mutex
	activeTray              *trayApp
)

type trayOptions struct {
	StateDir  string
	SessionID int
	BuildID   string
}

type trayApp struct {
	options trayOptions
	hwnd    uintptr
	hicon   uintptr
}

type point struct {
	X int32
	Y int32
}

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

func runTray(ctx context.Context, options trayOptions) {
	app := &trayApp{options: options}
	trayMu.Lock()
	activeTray = app
	trayMu.Unlock()
	defer func() {
		trayMu.Lock()
		if activeTray == app {
			activeTray = nil
		}
		trayMu.Unlock()
	}()

	className, _ := windows.UTF16PtrFromString("BorealisAgentTray" + strconv.Itoa(os.Getpid()))
	wndProc := windows.NewCallback(trayWndProc)
	instance, _, _ := procGetModuleHandleW.Call(0)
	class := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   wndProc,
		HInstance:     instance,
		LpszClassName: className,
	}
	if atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return
	}
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0,
		0, 0, 0, 0,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return
	}
	app.hwnd = hwnd
	hicon := loadTrayIcon(instance)
	app.hicon = hicon
	app.addIcon()
	defer app.deleteIcon()

	go func() {
		<-ctx.Done()
		procDestroyWindow.Call(hwnd)
	}()

	var message msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func loadTrayIcon(instance uintptr) uintptr {
	if instance != 0 {
		hicon, _, _ := procLoadIconW.Call(instance, uintptr(agentIconResourceID))
		if hicon != 0 {
			return hicon
		}
	}
	hicon, _, _ := procLoadIconW.Call(0, idiApplication)
	return hicon
}

func trayWndProc(hwnd uintptr, message uint32, wParam uintptr, lParam uintptr) uintptr {
	trayMu.Lock()
	app := activeTray
	trayMu.Unlock()
	switch message {
	case wmTrayCallback:
		switch uint32(lParam) {
		case wmRButtonUp:
			if app != nil {
				app.showMenu()
			}
			return 0
		}
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func (a *trayApp) addIcon() {
	var data notifyIconData
	data.CbSize = uint32(unsafe.Sizeof(data))
	data.HWnd = a.hwnd
	data.UID = 1
	data.UFlags = nifMessage | nifIcon | nifTip
	data.UCallbackMessage = wmTrayCallback
	data.HIcon = a.hicon
	copyUTF16(data.SzTip[:], "Borealis Agent")
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
}

func (a *trayApp) deleteIcon() {
	var data notifyIconData
	data.CbSize = uint32(unsafe.Sizeof(data))
	data.HWnd = a.hwnd
	data.UID = 1
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
}

func (a *trayApp) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	appendRoleHealthItems(menu, a.statusSnapshot())
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuRestartAgent, "Restart Agent")
	appendMenu(menu, mfString, menuUpdateCheck, "Check For Updates")
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForegroundWindow.Call(a.hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton, uintptr(cursor.X), uintptr(cursor.Y), 0, a.hwnd, 0)
	a.handleMenuCommand(uint32(cmd))
}

func (a *trayApp) handleMenuCommand(command uint32) {
	switch command {
	case menuRestartAgent:
		a.sendTrayCommand(localui.CommandAgentRestart)
	case menuUpdateCheck:
		a.sendTrayCommand(localui.CommandAgentUpdate)
	}
}

func (a *trayApp) sendTrayCommand(command string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return
	default:
		_, _ = localui.WriteCommandRequest(a.options.StateDir, localui.CommandRequest{Command: command})
	}
}

func (a *trayApp) statusSnapshot() localui.StatusSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return localui.StatusSnapshot{}
	default:
	}
	snapshot, _ := localui.ReadStatusSnapshot(a.options.StateDir)
	return snapshot
}

func appendRoleHealthItems(menu uintptr, snapshot localui.StatusSnapshot) {
	roleFlags := uint32(mfString | mfDisabled | mfGrayed)
	if len(snapshot.Roles) == 0 {
		appendMenu(menu, roleFlags, menuRoleBase, "Role Health: Unhealthy")
		return
	}
	for index, role := range snapshot.Roles {
		label := strings.TrimSpace(role.RoleLabel)
		if label == "" {
			label = strings.TrimSpace(role.RoleName)
		}
		if label == "" {
			label = "Role"
		}
		appendMenu(menu, roleFlags, menuRoleBase+uint32(index), label+": "+simpleHealth(role))
	}
}

func simpleHealth(role localui.RoleHealth) string {
	status := strings.ToLower(strings.TrimSpace(role.StatusCode))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(role.Status))
	}
	switch status {
	case "healthy", "ok", "ready", "complete":
		return "Healthy"
	default:
		return "Unhealthy"
	}
}

func appendMenu(menu uintptr, flags uint32, id uint32, label string) {
	var labelPtr uintptr
	if label != "" {
		ptr, _ := windows.UTF16PtrFromString(label)
		labelPtr = uintptr(unsafe.Pointer(ptr))
	}
	procAppendMenuW.Call(menu, uintptr(flags), uintptr(id), labelPtr)
}

func copyUTF16(dst []uint16, value string) {
	encoded := windows.StringToUTF16(value)
	if len(encoded) > len(dst) {
		encoded = encoded[:len(dst)]
		encoded[len(encoded)-1] = 0
	}
	copy(dst, encoded)
}
