#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/Python_API_Endpoints/macro_engines.py
import os
import platform

print("[macro_engines] Initializing macro engine...")

if platform.system().lower().startswith("win"):
    print("[macro_engines] Detected Windows OS")
    try:
        from ahk import AHK
        print("[macro_engines] Imported ahk module")
    except Exception as e:
        AHK = None
        ahk = None
        print(f"[macro_engines] Failed to import ahk: {e}")
    else:
        _script_dir = os.path.dirname(os.path.abspath(__file__))
        _ahk_exe = os.path.join(_script_dir, "..", "AutoHotKey", "AutoHotkey64.exe")
        print(f"[macro_engines] Looking for AutoHotKey binary at: {_ahk_exe}")
        ahk = AHK(executable_path=_ahk_exe) if os.path.isfile(_ahk_exe) else None
        if ahk is None:
            print(f"[macro_engines] AutoHotKey binary not found at: {_ahk_exe}")
        else:
            print("[macro_engines] AutoHotKey initialized")
else:
    print("[macro_engines] Non-Windows OS detected - macro engine disabled")
    AHK = None
    ahk = None

def list_windows():
    """List all visible windows with titles."""
    if ahk is None:
        print("[macro_engines] list_windows called but ahk is None")
        return []
    windows = []
    try:
        for win in ahk.windows():
            title = getattr(win, "title", "")
            handle = getattr(win, "id", None)
            if title and str(title).strip():
                windows.append({"title": title, "handle": int(handle)})
                print(f"[macro_engines] Found window: {title} ({handle})")
    except Exception as e:
        print(f"[macro_engines] Error listing windows: {e}")
    return windows

def _get_window(handle):
    if ahk is None:
        raise RuntimeError("Macro engine not supported on this OS")
    try:
        print(f"[macro_engines] Retrieving window id {handle}")
        return ahk.win_get(id=int(handle))
    except Exception as e:
        print(f"[macro_engines] Failed to retrieve window {handle}: {e}")
        return None

def send_keypress_to_window(handle, key):
    """Send a single keypress to the specified window handle."""
    win = _get_window(handle)
    if win is None:
        return False, "Window not found"
    try:
        print(f"[macro_engines] Sending key '{key}' to window {handle}")
        win.activate()
        win.send(key)
        return True
    except Exception as e:
        print(f"[macro_engines] Failed to send keypress: {e}")
        return False, str(e)

def type_text_to_window(handle, text):
    """Type a string into the window."""
    win = _get_window(handle)
    if win is None:
        return False, "Window not found"
    try:
        print(f"[macro_engines] Typing text to window {handle}: {text}")
        win.activate()
        win.send(text)
        return True
    except Exception as e:
        print(f"[macro_engines] Failed to send typed text: {e}")
        return False, str(e)
