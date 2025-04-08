import sys
import uuid
import time
import json
import base64
import threading
import requests
from io import BytesIO
import socket

from PyQt5 import QtCore, QtGui, QtWidgets
from PIL import ImageGrab

# ---------------- Configuration ----------------
SERVER_URL = "https://borealis.bunny-lab.io" # Production URL Example
#SERVER_URL = "http://localhost:5000" # Development URL Example
CHECKIN_ENDPOINT = f"{SERVER_URL}/api/agent/checkin"
CONFIG_ENDPOINT = f"{SERVER_URL}/api/agent/config"
DATA_POST_ENDPOINT = f"{SERVER_URL}/api/agent/data"
HEARTBEAT_ENDPOINT = f"{SERVER_URL}/api/agent/heartbeat"

HOSTNAME = socket.gethostname().lower()
RANDOM_SUFFIX = uuid.uuid4().hex[:8]
AGENT_ID = f"{HOSTNAME}-agent-{RANDOM_SUFFIX}"

# Default poll interval for config. Adjust as needed.
CONFIG_POLL_INTERVAL = 5

# ---------------- State ----------------
app_instance = None
region_widget = None
capture_thread_started = False
current_interval = 1000
config_ready = threading.Event()
overlay_visible = True
heartbeat_thread_started = False

# Track if we have a valid connection to Borealis
IS_CONNECTED = False
CONNECTION_LOST_REPORTED = False

# Keep a copy of the last config to avoid repeated provisioning
LAST_CONFIG = {}

# ---------------- Signal Bridge ----------------
class RegionLauncher(QtCore.QObject):
    trigger = QtCore.pyqtSignal(int, int, int, int)

    def __init__(self):
        super().__init__()
        self.trigger.connect(self.handle)

    def handle(self, x, y, w, h):
        launch_region(x, y, w, h)

region_launcher = None

# ---------------- Helper: Reconnect ----------------
def reconnect():
    """
    Attempt to connect to Borealis until successful.
    Sets IS_CONNECTED = True upon success.
    """
    global IS_CONNECTED, CONNECTION_LOST_REPORTED
    while not IS_CONNECTED:
        try:
            requests.post(CHECKIN_ENDPOINT, json={"agent_id": AGENT_ID, "hostname": HOSTNAME}, timeout=5)
            IS_CONNECTED = True
            CONNECTION_LOST_REPORTED = False
            print(f"[INFO] Agent ID: {AGENT_ID} connected to Borealis.")
        except Exception:
            if not CONNECTION_LOST_REPORTED:
                print(f"[CONNECTION LOST] Attempting to Reconnect to Borealis Server at {SERVER_URL}")
                CONNECTION_LOST_REPORTED = True
            time.sleep(10)

# ---------------- Networking ----------------
def poll_for_config():
    """
    Polls for agent configuration from Borealis.
    Returns a config dict or None on failure.
    """
    try:
        res = requests.get(CONFIG_ENDPOINT, params={"agent_id": AGENT_ID}, timeout=5)
        if res.status_code == 200:
            return res.json()
        else:
            print(f"[ERROR] Config polling returned status: {res.status_code}")
    except Exception:
        # We'll let the config_loop handle setting IS_CONNECTED = False
        pass
    return None

def send_image_data(image):
    """
    Attempts to POST screenshot data to Borealis if IS_CONNECTED is True.
    """
    global IS_CONNECTED, CONNECTION_LOST_REPORTED
    if not IS_CONNECTED:
        return  # Skip sending if not connected

    try:
        buffer = BytesIO()
        image.save(buffer, format="PNG")
        encoded = base64.b64encode(buffer.getvalue()).decode("utf-8")

        response = requests.post(DATA_POST_ENDPOINT, json={
            "agent_id": AGENT_ID,
            "type": "screenshot",
            "image_base64": encoded
        }, timeout=5)

        if response.status_code != 200:
            print(f"[ERROR] Screenshot POST failed: {response.status_code} - {response.text}")
    except Exception as e:
        if IS_CONNECTED and not CONNECTION_LOST_REPORTED:
            # Only report once
            print(f"[CONNECTION LOST] Attempting to Reconnect to Borealis Server at {SERVER_URL}")
            CONNECTION_LOST_REPORTED = True
        IS_CONNECTED = False

def send_heartbeat():
    """
    Attempts to send heartbeat if IS_CONNECTED is True.
    """
    global IS_CONNECTED, CONNECTION_LOST_REPORTED
    if not IS_CONNECTED:
        return

    try:
        response = requests.get(HEARTBEAT_ENDPOINT, params={"agent_id": AGENT_ID}, timeout=5)
        if response.status_code != 200:
            print(f"[ERROR] Heartbeat returned status: {response.status_code}")
            raise ValueError("Heartbeat not 200")
    except Exception:
        if IS_CONNECTED and not CONNECTION_LOST_REPORTED:
            print(f"[CONNECTION LOST] Attempting to Reconnect to Borealis Server at {SERVER_URL}")
            CONNECTION_LOST_REPORTED = True
        IS_CONNECTED = False

# ---------------- Region Overlay ----------------
class ScreenshotRegion(QtWidgets.QWidget):
    def __init__(self, x=100, y=100, w=300, h=200):
        super().__init__()
        self.setGeometry(x, y, w, h)
        self.setWindowFlags(QtCore.Qt.FramelessWindowHint | QtCore.Qt.WindowStaysOnTopHint)
        self.setAttribute(QtCore.Qt.WA_TranslucentBackground)
        self.drag_offset = None
        self.resizing = False
        self.resize_handle_size = 12
        self.setVisible(True)

        self.label = QtWidgets.QLabel(self)
        self.label.setText(AGENT_ID)
        self.label.setStyleSheet("color: lime; background: transparent; font-size: 10px;")
        self.label.move(8, 4)

        self.setMouseTracking(True)

    def paintEvent(self, event):
        painter = QtGui.QPainter(self)
        painter.setRenderHint(QtGui.QPainter.Antialiasing)

        # Transparent fill
        painter.setBrush(QtCore.Qt.transparent)
        painter.setPen(QtGui.QPen(QtGui.QColor(0, 255, 0), 2))
        painter.drawRect(self.rect())

        # Resize Handle Visual (Bottom-Right)
        handle_rect = QtCore.QRect(
            self.width() - self.resize_handle_size,
            self.height() - self.resize_handle_size,
            self.resize_handle_size,
            self.resize_handle_size
        )
        painter.fillRect(handle_rect, QtGui.QColor(0, 255, 0))

    def mousePressEvent(self, event):
        if event.button() == QtCore.Qt.LeftButton:
            if (event.pos().x() > self.width() - self.resize_handle_size and
                event.pos().y() > self.height() - self.resize_handle_size):
                self.resizing = True
            else:
                self.drag_offset = event.globalPos() - self.frameGeometry().topLeft()

    def mouseMoveEvent(self, event):
        if self.resizing:
            new_width = max(event.pos().x(), 100)
            new_height = max(event.pos().y(), 80)
            self.resize(new_width, new_height)
        elif event.buttons() & QtCore.Qt.LeftButton and self.drag_offset:
            self.move(event.globalPos() - self.drag_offset)

    def mouseReleaseEvent(self, event):
        self.resizing = False
        self.drag_offset = None

    def get_geometry(self):
        geo = self.geometry()
        return geo.x(), geo.y(), geo.width(), geo.height()

# ---------------- Threads ----------------
def capture_loop():
    """
    Continuously captures the user-defined region every current_interval ms if connected.
    """
    global current_interval
    print("[INFO] Screenshot capture loop started")
    config_ready.wait()

    while region_widget is None:
        print("[WAIT] Waiting for region widget to initialize...")
        time.sleep(0.2)

    print(f"[INFO] Agent Capturing Region: x:{region_widget.x()} y:{region_widget.y()} w:{region_widget.width()} h:{region_widget.height()}")

    while True:
        if overlay_visible and IS_CONNECTED:
            x, y, w, h = region_widget.get_geometry()
            try:
                img = ImageGrab.grab(bbox=(x, y, x + w, y + h))
                send_image_data(img)
            except Exception as e:
                print(f"[ERROR] Screenshot error: {e}")
        time.sleep(current_interval / 1000)

def heartbeat_loop():
    """
    Heartbeat every 10 seconds if connected.
    If it fails, we set IS_CONNECTED=False, and rely on config_loop to reconnect.
    """
    while True:
        send_heartbeat()
        time.sleep(10)

def config_loop():
    """
    1) Reconnect (if needed) until the agent can contact Borealis
    2) Poll for config. If new config is different from LAST_CONFIG, re-provision
    3) If poll_for_config fails or we see connection issues, set IS_CONNECTED=False
       and loop back to reconnect() on next iteration
    """
    global capture_thread_started, heartbeat_thread_started
    global current_interval, overlay_visible, LAST_CONFIG, IS_CONNECTED

    while True:
        # If we aren't connected, reconnect
        if not IS_CONNECTED:
            reconnect()

        # Attempt to get config
        config = poll_for_config()
        if config is None:
            # This means we had a poll failure, so mark disconnected and retry.
            IS_CONNECTED = False
            continue

        # If it has a "task" : "screenshot"
        if config.get("task") == "screenshot":
            # Compare to last known config
            if config != LAST_CONFIG:
                # Something changed, so provision
                print("[PROVISIONING] Agent Provisioning Command Issued by Borealis")

                x = config.get("x", 100)
                y = config.get("y", 100)
                w = config.get("w", 300)
                h = config.get("h", 200)
                current_interval = config.get("interval", 1000)
                overlay_visible = config.get("visible", True)

                print('[PROVISIONING] Agent Configured as "Screenshot" Collector')
                print(f'[PROVISIONING] Polling Rate: {current_interval / 1000:.1f}s')

                # Show or move region widget
                if not region_widget:
                    region_launcher.trigger.emit(x, y, w, h)
                else:
                    region_widget.setGeometry(x, y, w, h)
                    region_widget.setVisible(overlay_visible)

                LAST_CONFIG = config

            # Make sure capture thread is started
            if not capture_thread_started:
                threading.Thread(target=capture_loop, daemon=True).start()
                capture_thread_started = True

            # Make sure heartbeat thread is started
            if not heartbeat_thread_started:
                threading.Thread(target=heartbeat_loop, daemon=True).start()
                heartbeat_thread_started = True

            # Signal that provisioning is done so capture thread can run
            config_ready.set()

        # Sleep before next poll
        time.sleep(CONFIG_POLL_INTERVAL)

def launch_region(x, y, w, h):
    """
    Initializes the screenshot region overlay widget exactly once.
    """
    global region_widget
    if region_widget:
        return
    print("[INFO] Agent Starting...")
    region_widget = ScreenshotRegion(x, y, w, h)
    region_widget.show()

# ---------------- Main ----------------
if __name__ == "__main__":
    app_instance = QtWidgets.QApplication(sys.argv)
    region_launcher = RegionLauncher()

    # Start the config loop in a background thread
    threading.Thread(target=config_loop, daemon=True).start()

    # Enter Qt main event loop
    sys.exit(app_instance.exec_())
