#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/Python_API_Endpoints/macro_engines.py
import os
import platform

if platform.system().lower().startswith("win"):
    try:
        from ahk import AHK
    except Exception:
        AHK = None
        ahk = None
    else:
        _script_dir = os.path.dirname(os.path.abspath(__file__))
        _ahk_exe = os.path.join(_script_dir, "..", "AutoHotKey", "AutoHotkey64.exe")
        ahk = AHK(executable_path=_ahk_exe) if os.path.isfile(_ahk_exe) else None
        if ahk is None:
            print(f"[macro_engines] AutoHotKey binary not found at: {_ahk_exe}")
else:
    AHK = None
    ahk = None

def list_windows():
    """List all visible windows with titles."""
    if ahk is None:
        return []
    windows = []
    try:
        for win in ahk.windows():
            title = getattr(win, "title", "")
            handle = getattr(win, "id", None)
            if title and str(title).strip():
                windows.append({"title": title, "handle": int(handle)})
    except Exception:
        pass
    return windows

def _get_window(handle):
    if ahk is None:
        raise RuntimeError("Macro engine not supported on this OS")
    try:
        return ahk.win_get(id=int(handle))
    except Exception:
        return None

def send_keypress_to_window(handle, key):
    """Send a single keypress to the specified window handle."""
    win = _get_window(handle)
    if win is None:
        return False, "Window not found"
    try:
        win.activate()
        win.send(key)
        return True
    except Exception as e:
        return False, str(e)

def type_text_to_window(handle, text):
    """Type a string into the window."""
    win = _get_window(handle)
    if win is None:
        return False, "Window not found"
    try:
        win.activate()
        win.send(text)
        return True
    except Exception as e:
        return False, str(e)
