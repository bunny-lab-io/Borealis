#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/borealis-agent.py

import sys
import uuid
import time
import base64
import threading
import socketio
from io import BytesIO
import socket
import os
import json

from PyQt5 import QtCore, QtGui, QtWidgets
from PIL import ImageGrab

# ---------------- Configuration ----------------
CONFIG_PATH = os.path.join(os.path.dirname(__file__), "agent_settings.json")
DEFAULT_SERVER_URL = "http://localhost:5000"

def load_config():
    """
    Load agent_settings.json or prompt the user for SERVER_URL.
    Returns a config dictionary with at least {"SERVER_URL": ...}
    """
    config = {}

    if os.path.exists(CONFIG_PATH):
        try:
            with open(CONFIG_PATH, "r") as f:
                config = json.load(f)
                if isinstance(config, dict) and "SERVER_URL" in config:
                    return config
        except Exception as e:
            print(f"[WARN] Failed to parse agent_settings.json: {e}")

    try:
        user_input = input(f"Enter Borealis Server URL [{DEFAULT_SERVER_URL}]: ").strip()
        config["SERVER_URL"] = user_input if user_input else DEFAULT_SERVER_URL
        with open(CONFIG_PATH, "w") as f:
            json.dump(config, f, indent=2)
    except Exception as e:
        print(f"[ERROR] Prompt failed: {e}")
        config["SERVER_URL"] = DEFAULT_SERVER_URL

    return config

config = load_config()
SERVER_URL = config["SERVER_URL"]

HOSTNAME = socket.gethostname().lower()
RANDOM_SUFFIX = uuid.uuid4().hex[:8]
AGENT_ID = f"{HOSTNAME}-agent-{RANDOM_SUFFIX}"

# ---------------- App State ----------------
app_instance = None
overlay_widgets = {}
region_launchers = {}
running_roles = {}
running_threads = {}

# ---------------- Socket Setup ----------------
# Enable automatic reconnection with retries in background
sio = socketio.Client(reconnection=True, reconnection_attempts=0, reconnection_delay=5)

@sio.event
def connect():
    print(f"[WebSocket] Agent ID: {AGENT_ID} connected to Borealis.")
    sio.emit('connect_agent', {"agent_id": AGENT_ID, "hostname": HOSTNAME})
    sio.emit('request_config', {"agent_id": AGENT_ID})

@sio.event
def disconnect():
    print("[WebSocket] Lost connection to Borealis server.")

@sio.on('agent_config')
def on_agent_config(config):
    print("[PROVISIONED] Received new configuration from Borealis.")

    roles = config.get("roles", [])
    stop_all_roles()
    for role in roles:
        start_role_thread(role)

# ---------------- Overlay Class ----------------
class ScreenshotRegion(QtWidgets.QWidget):
    def __init__(self, node_id, x=100, y=100, w=300, h=200):
        super().__init__()
        self.node_id = node_id
        self.setGeometry(x, y, w, h)
        self.setWindowFlags(QtCore.Qt.FramelessWindowHint | QtCore.Qt.WindowStaysOnTopHint)
        self.setAttribute(QtCore.Qt.WA_TranslucentBackground)
        self.drag_offset = None
        self.resizing = False
        self.resize_handle_size = 12
        self.setVisible(True)

        self.label = QtWidgets.QLabel(self)
        self.label.setText(f"{node_id[:8]}")
        self.label.setStyleSheet("color: lime; background: transparent; font-size: 10px;")
        self.label.move(8, 4)

        self.setMouseTracking(True)

    def paintEvent(self, event):
        painter = QtGui.QPainter(self)
        painter.setRenderHint(QtGui.QPainter.Antialiasing)
        painter.setBrush(QtCore.Qt.transparent)
        painter.setPen(QtGui.QPen(QtGui.QColor(0, 255, 0), 2))
        painter.drawRect(self.rect())

        handle_rect = QtCore.QRect(
            self.width() - self.resize_handle_size,
            self.height() - self.resize_handle_size,
            self.resize_handle_size,
            self.resize_handle_size
        )
        painter.fillRect(handle_rect, QtGui.QColor(0, 255, 0))

    def mousePressEvent(self, event):
        if event.button() == QtCore.Qt.LeftButton:
            if event.pos().x() > self.width() - self.resize_handle_size and \
               event.pos().y() > self.height() - self.resize_handle_size:
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

# ---------------- Region UI Handler ----------------
class RegionLauncher(QtCore.QObject):
    trigger = QtCore.pyqtSignal(int, int, int, int)

    def __init__(self, node_id):
        super().__init__()
        self.node_id = node_id
        self.trigger.connect(self.handle)

    def handle(self, x, y, w, h):
        print(f"[Overlay] Launching overlay for {self.node_id} at ({x},{y},{w},{h})")
        if self.node_id in overlay_widgets:
            return
        widget = ScreenshotRegion(self.node_id, x, y, w, h)
        overlay_widgets[self.node_id] = widget
        widget.show()

# ---------------- GUI Thread Helpers ----------------
def gui_create_launcher(node_id, x, y, w, h):
    launcher = RegionLauncher(node_id)
    region_launchers[node_id] = launcher
    launcher.handle(x, y, w, h)

def gui_update_widget(node_id, x, y, w, h, visible):
    widget = overlay_widgets.get(node_id)
    if widget:
        widget.setGeometry(x, y, w, h)
        widget.setVisible(visible)

# ---------------- Role Management ----------------
def stop_all_roles():
    for node_id, thread in running_threads.items():
        if thread and thread.is_alive():
            print(f"[Role] Terminating previous task: {node_id}")
    running_roles.clear()
    running_threads.clear()

def start_role_thread(role_cfg):
    role = role_cfg.get("role")
    node_id = role_cfg.get("node_id")
    if not role or not node_id:
        print("[ERROR] Invalid role configuration (missing role or node_id).")
        return

    if role == "screenshot":
        thread = threading.Thread(target=run_screenshot_loop, args=(node_id, role_cfg), daemon=True)
    else:
        print(f"[SKIP] Unknown role: {role}")
        return

    running_roles[node_id] = role_cfg
    running_threads[node_id] = thread
    thread.start()
    print(f"[Role] Started task: {role} ({node_id})")

# ---------------- Screenshot Role Loop ----------------
def run_screenshot_loop(node_id, cfg):
    interval = cfg.get("interval", 1000)
    visible = cfg.get("visible", True)
    x = cfg.get("x", 100)
    y = cfg.get("y", 100)
    w = cfg.get("w", 300)
    h = cfg.get("h", 200)

    # Schedule launcher creation in GUI thread
    if node_id not in region_launchers:
        QtCore.QTimer.singleShot(0, lambda nid=node_id, xx=x, yy=y, ww=w, hh=h: gui_create_launcher(nid, xx, yy, ww, hh))

    while True:
        try:
            # Use current widget geometry if available (after user moves/resizes)
            if node_id in overlay_widgets:
                widget = overlay_widgets[node_id]
                x, y, w, h = widget.get_geometry()

            print(f"[Capture] Screenshot task {node_id} at ({x},{y},{w},{h})")
            img = ImageGrab.grab(bbox=(x, y, x + w, y + h))
            buffer = BytesIO()
            img.save(buffer, format="PNG")
            encoded = base64.b64encode(buffer.getvalue()).decode("utf-8")

            sio.emit("agent_screenshot_task", {
                "agent_id": AGENT_ID,
                "node_id": node_id,
                "image_base64": encoded
            })

            # Schedule any visibility or geometry updates in GUI thread
            QtCore.QTimer.singleShot(
                0,
                lambda nid=node_id, xx=x, yy=y, ww=w, hh=h, vis=visible: gui_update_widget(nid, xx, yy, ww, hh, vis)
            )
        except Exception as e:
            print(f"[ERROR] Screenshot task {node_id} failed: {e}")

        time.sleep(interval / 1000)

# ---------------- Main ----------------
if __name__ == "__main__":
    app_instance = QtWidgets.QApplication(sys.argv)
    retry_interval = 5  # seconds between connection attempts

    def connect_loop():
        while True:
            try:
                print(f"[WebSocket] Connecting to {SERVER_URL}...")
                sio.connect(SERVER_URL, transports=["websocket"], wait=False)
                break
            except Exception:
                print(f"[WebSocket] Borealis Server is Not Running - Retrying in {retry_interval} seconds...")
                time.sleep(retry_interval)

    threading.Thread(target=connect_loop, daemon=True).start()
    sys.exit(app_instance.exec_())
