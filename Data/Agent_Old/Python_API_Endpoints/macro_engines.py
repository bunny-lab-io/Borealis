#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/Python_API_Endpoints/macro_engines.py
import platform

if platform.system().lower().startswith('win'):
    # pywinauto is only available/supported on Windows
    try:
        from pywinauto import Desktop, Application
    except ImportError:
        Desktop = None
        Application = None
        print("[macro_engines] pywinauto not installed!")
else:
    Desktop = None
    Application = None

def list_windows():
    """List all visible windows with titles (for dropdown in UI)."""
    if Desktop is None:
        return []
    windows = []
    for w in Desktop(backend="uia").windows():
        try:
            title = w.window_text()
            handle = w.handle
            if title.strip():
                windows.append({"title": title, "handle": handle})
        except Exception:
            continue
    return windows

def send_keypress_to_window(handle, key):
    """Send a single keypress to the specified window handle."""
    if Application is None:
        raise RuntimeError("Macro engine not supported on this OS")
    try:
        app = Application(backend="uia").connect(handle=handle)
        win = app.window(handle=handle)
        win.set_focus()  # pywinauto still needs focus for most key sends
        win.type_keys(key, with_spaces=True, set_foreground=True)
        return True
    except Exception as e:
        return False, str(e)

def type_text_to_window(handle, text):
    """Type a string into the window (as if pasted or typed)."""
    if Application is None:
        raise RuntimeError("Macro engine not supported on this OS")
    try:
        app = Application(backend="uia").connect(handle=handle)
        win = app.window(handle=handle)
        win.set_focus()
        win.type_keys(text, with_spaces=True, set_foreground=True)
        return True
    except Exception as e:
        return False, str(e)
