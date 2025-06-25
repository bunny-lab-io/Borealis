# ---------------------- Information Gathering ----------------------
import os
from ahk import AHK

# Get the directory containing this script (cross-platform)
script_dir = os.path.dirname(os.path.abspath(__file__))

# Build the path to the AutoHotkey binary (adjust filename if needed)
ahk_bin_path = os.path.join(script_dir, 'AutoHotKey', 'AutoHotkey64.exe')

# ---------------------- Information Analysis ----------------------
# Confirm that the AHK binary exists at the given path
if not os.path.isfile(ahk_bin_path):
    raise FileNotFoundError(f"AutoHotkey binary not found at: {ahk_bin_path}")

# ---------------------- Information Processing ----------------------
# Initialize AHK instance with explicit executable_path
ahk = AHK(executable_path=ahk_bin_path)

window_title = '*TargetWindow - Notepad'  # Change this to your target window

# Find the window by its title
target_window = ahk.find_window(title=window_title)

if target_window is None:
    print(f"Window with title '{window_title}' not found.")
else:
    # Bring the target window to the foreground
    target_window.activate()

    # Wait briefly to ensure the window is focused
    import time
    time.sleep(1)

    # Send keystrokes/text to the window
    text = "Hello from Python and AutoHotkey!"
    for c in text:
        ahk.send(c)
        import time
        time.sleep(0.05)  # slow down for debugging
    ahk.send('{ENTER}')

    print("Sent keystrokes to the window.")
