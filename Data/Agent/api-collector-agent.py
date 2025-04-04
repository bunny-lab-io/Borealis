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
SERVER_URL = "http://localhost:5000"
CHECKIN_ENDPOINT = f"{SERVER_URL}/api/agent/checkin"
CONFIG_ENDPOINT = f"{SERVER_URL}/api/agent/config"
DATA_POST_ENDPOINT = f"{SERVER_URL}/api/agent/data"

HOSTNAME = socket.gethostname().lower()
RANDOM_SUFFIX = uuid.uuid4().hex[:8]
AGENT_ID = f"{HOSTNAME}-agent-{RANDOM_SUFFIX}"

CONFIG_POLL_INTERVAL = 5

# ---------------- State ----------------
app_instance = None
region_widget = None
capture_thread_started = False
current_interval = 1000
config_ready = threading.Event()
overlay_visible = True

# ---------------- Signal Bridge ----------------
class RegionLauncher(QtCore.QObject):
    trigger = QtCore.pyqtSignal(int, int, int, int)

    def __init__(self):
        super().__init__()
        self.trigger.connect(self.handle)

    def handle(self, x, y, w, h):
        launch_region(x, y, w, h)

region_launcher = None

# ---------------- Agent Networking ----------------
def check_in():
    try:
        requests.post(CHECKIN_ENDPOINT, json={"agent_id": AGENT_ID, "hostname": HOSTNAME})
        print(f"[INFO] Agent ID: {AGENT_ID}")
    except Exception as e:
        print(f"[ERROR] Check-in failed: {e}")

def poll_for_config():
    try:
        res = requests.get(CONFIG_ENDPOINT, params={"agent_id": AGENT_ID})
        if res.status_code == 200:
            return res.json()
    except Exception as e:
        print(f"[ERROR] Config polling failed: {e}")
    return None

def send_image_data(image):
    try:
        buffer = BytesIO()
        image.save(buffer, format="PNG")
        encoded = base64.b64encode(buffer.getvalue()).decode("utf-8")

        response = requests.post(DATA_POST_ENDPOINT, json={
            "agent_id": AGENT_ID,
            "type": "screenshot",
            "image_base64": encoded
        })

        if response.status_code != 200:
            print(f"[ERROR] Screenshot POST failed: {response.status_code} - {response.text}")
    except Exception as e:
        print(f"[ERROR] Failed to send image: {e}")

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
    global current_interval
    print("[INFO] Screenshot capture loop started")
    config_ready.wait()
    while region_widget is None:
        print("[WAIT] Waiting for region widget to initialize...")
        time.sleep(0.2)

    print(f"[INFO] Agent Capturing Region: x:{region_widget.x()} y:{region_widget.y()} w:{region_widget.width()} h:{region_widget.height()}")

    while True:
        if overlay_visible:
            x, y, w, h = region_widget.get_geometry()
            try:
                img = ImageGrab.grab(bbox=(x, y, x + w, y + h))
                send_image_data(img)
            except Exception as e:
                print(f"[ERROR] Screenshot error: {e}")
        time.sleep(current_interval / 1000)

def config_loop():
    global region_widget, capture_thread_started, current_interval, overlay_visible
    check_in()
    while True:
        config = poll_for_config()
        if config and config.get("task") == "screenshot":
            print("[PROVISIONING] Agent Provisioning Command Issued by Borealis")
            x = config.get("x", 100)
            y = config.get("y", 100)
            w = config.get("w", 300)
            h = config.get("h", 200)
            current_interval = config.get("interval", 1000)
            overlay_visible = config.get("visible", True)

            print(f"[PROVISIONING] Agent Configured as \"Screenshot\" Collector w/ Polling Rate of <{current_interval/1000:.1f}s>")

            if not region_widget:
                region_launcher.trigger.emit(x, y, w, h)
            elif region_widget:
                region_widget.setVisible(overlay_visible)

            if not capture_thread_started:
                threading.Thread(target=capture_loop, daemon=True).start()
                capture_thread_started = True

            config_ready.set()
        time.sleep(CONFIG_POLL_INTERVAL)

def launch_region(x, y, w, h):
    global region_widget
    if region_widget:
        return
    print(f"[INFO] Agent Starting...")
    region_widget = ScreenshotRegion(x, y, w, h)
    region_widget.show()

# ---------------- Main ----------------
if __name__ == "__main__":
    app_instance = QtWidgets.QApplication(sys.argv)
    region_launcher = RegionLauncher()
    threading.Thread(target=config_loop, daemon=True).start()
    sys.exit(app_instance.exec_())
