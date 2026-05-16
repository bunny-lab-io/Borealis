//go:build windows

package currentuser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
	"golang.org/x/sys/windows"
)

const (
	wmDestroy       = 0x0002
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203
	wmApp           = 0x8000
	wmTrayCallback  = wmApp + 74

	nimAdd     = 0x00000000
	nimDelete  = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString       = 0x00000000
	mfSeparator    = 0x00000800
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	idiApplication = 32512
	cfUnicodeText  = 13
	gmemMoveable   = 0x0002

	menuOpenAgent       = 1001
	menuOpenEngine      = 1002
	menuRestartAgent    = 1003
	menuUpdateCheck     = 1004
	menuCopyDiagnostics = 1005
	menuExitTray        = 1006
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
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procEmptyClipboard      = user32.NewProc("EmptyClipboard")
	procSetClipboardData    = user32.NewProc("SetClipboardData")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc         = kernel32.NewProc("GlobalAlloc")
	procGlobalLock          = kernel32.NewProc("GlobalLock")
	procGlobalUnlock        = kernel32.NewProc("GlobalUnlock")
	procGlobalFree          = kernel32.NewProc("GlobalFree")
	trayMu                  sync.Mutex
	activeTray              *trayApp
)

type trayOptions struct {
	UIURL     string
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
	if strings.TrimSpace(options.UIURL) == "" {
		return
	}
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
	hicon, _, _ := procLoadIconW.Call(0, idiApplication)
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

func trayWndProc(hwnd uintptr, message uint32, wParam uintptr, lParam uintptr) uintptr {
	trayMu.Lock()
	app := activeTray
	trayMu.Unlock()
	switch message {
	case wmTrayCallback:
		switch uint32(lParam) {
		case wmLButtonDblClk:
			if app != nil {
				app.openAgentUI()
			}
			return 0
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
	appendMenu(menu, mfString, menuOpenAgent, "Open Borealis Agent")
	appendMenu(menu, mfString, menuOpenEngine, "Open Engine Web UI")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuRestartAgent, "Restart Agent")
	appendMenu(menu, mfString, menuUpdateCheck, "Check For Updates")
	appendMenu(menu, mfString, menuCopyDiagnostics, "Copy Diagnostics")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuExitTray, "Exit Tray UI")
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForegroundWindow.Call(a.hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton, uintptr(cursor.X), uintptr(cursor.Y), 0, a.hwnd, 0)
	a.handleMenuCommand(uint32(cmd))
}

func (a *trayApp) handleMenuCommand(command uint32) {
	switch command {
	case menuOpenAgent:
		a.openAgentUI()
	case menuOpenEngine:
		if url := a.engineURL(); url != "" {
			openURL(url)
		} else {
			a.openAgentUI()
		}
	case menuRestartAgent:
		a.sendBrokerCommand(localui.CommandAgentRestart)
	case menuUpdateCheck:
		a.sendBrokerCommand(localui.CommandAgentUpdate)
	case menuCopyDiagnostics:
		if text := a.diagnosticsText(); text != "" {
			_ = setClipboardText(text)
		}
	case menuExitTray:
		procDestroyWindow.Call(a.hwnd)
		go func() {
			time.Sleep(200 * time.Millisecond)
			os.Exit(0)
		}()
	}
}

func (a *trayApp) openAgentUI() {
	openURL(a.options.UIURL)
}

func (a *trayApp) sendBrokerCommand(command string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = localui.DoCommand(ctx, &http.Client{Timeout: 12 * time.Second}, a.options.StateDir, localui.CommandRequest{Command: command})
}

func (a *trayApp) engineURL() string {
	snapshot := a.statusSnapshot()
	return strings.TrimSpace(snapshot.ServerURL)
}

func (a *trayApp) diagnosticsText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	response, err := localui.DoCommand(ctx, &http.Client{Timeout: 12 * time.Second}, a.options.StateDir, localui.CommandRequest{Command: localui.CommandDiagnosticsCopy})
	if err != nil {
		return ""
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		encoded, _ := json.Marshal(response.Data)
		_ = json.Unmarshal(encoded, &data)
	}
	if data == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(data["diagnostics_text"]))
}

func (a *trayApp) statusSnapshot() localui.StatusSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := localui.DoCommand(ctx, &http.Client{Timeout: 8 * time.Second}, a.options.StateDir, localui.CommandRequest{Command: localui.CommandStatusGet})
	if err != nil {
		return localui.StatusSnapshot{}
	}
	var snapshot localui.StatusSnapshot
	encoded, err := json.Marshal(response.Data)
	if err != nil {
		return snapshot
	}
	_ = json.Unmarshal(encoded, &snapshot)
	return snapshot
}

func appendMenu(menu uintptr, flags uint32, id uint32, label string) {
	var labelPtr uintptr
	if label != "" {
		ptr, _ := windows.UTF16PtrFromString(label)
		labelPtr = uintptr(unsafe.Pointer(ptr))
	}
	procAppendMenuW.Call(menu, uintptr(flags), uintptr(id), labelPtr)
}

func openURL(url string) {
	url = strings.TrimSpace(url)
	if url == "" || url == "#" {
		return
	}
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
}

func copyUTF16(dst []uint16, value string) {
	encoded := windows.StringToUTF16(value)
	if len(encoded) > len(dst) {
		encoded = encoded[:len(dst)]
		encoded[len(encoded)-1] = 0
	}
	copy(dst, encoded)
}

func setClipboardText(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	utf16Text := utf16.Encode([]rune(value + "\x00"))
	size := uintptr(len(utf16Text) * 2)
	handle, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if handle == 0 {
		return err
	}
	locked, _, err := procGlobalLock.Call(handle)
	if locked == 0 {
		procGlobalFree.Call(handle)
		return err
	}
	copy((*[1 << 24]uint16)(unsafe.Pointer(locked))[:len(utf16Text)], utf16Text)
	procGlobalUnlock.Call(handle)
	if opened, _, err := procOpenClipboard.Call(0); opened == 0 {
		procGlobalFree.Call(handle)
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if result, _, err := procSetClipboardData.Call(cfUnicodeText, handle); result == 0 {
		procGlobalFree.Call(handle)
		return err
	}
	return nil
}
