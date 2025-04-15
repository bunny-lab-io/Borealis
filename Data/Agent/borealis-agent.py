#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/borealis-agent.py

import sys
import uuid
import time
import base64
import threading
import socketio
from io import BytesIO
import socket

from PyQt5 import QtCore, QtGui, QtWidgets
from PIL import ImageGrab

# ---------------- Configuration ----------------
SERVER_URL = "http://localhost:5000"  # WebSocket-enabled Internal URL
#SERVER_URL = "https://borealis.bunny-lab.io"  # WebSocket-enabled Public URL"

HOSTNAME = socket.gethostname().lower()
RANDOM_SUFFIX = uuid.uuid4().hex[:8]
AGENT_ID = f"{HOSTNAME}-agent-{RANDOM_SUFFIX}"

# ---------------- State ----------------
app_instance = None
region_widget = None
current_interval = 1000
config_ready = threading.Event()
overlay_visible = True

LAST_CONFIG = {}

# WebSocket client setup
sio = socketio.Client()

# ---------------- WebSocket Handlers ----------------
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
    global current_interval, overlay_visible, LAST_CONFIG

    if config != LAST_CONFIG:
        print("[PROVISIONED] Received new configuration from Borealis.")
        x = config.get("x", 100)
        y = config.get("y", 100)
        w = config.get("w", 300)
        h = config.get("h", 200)
        current_interval = config.get("interval", 1000)
        overlay_visible = config.get("visible", True)

        if not region_widget:
            region_launcher.trigger.emit(x, y, w, h)
        else:
            region_widget.setGeometry(x, y, w, h)
            region_widget.setVisible(overlay_visible)

        LAST_CONFIG = config
        config_ready.set()

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

# ---------------- Screenshot Capture ----------------
def capture_loop():
    config_ready.wait()

    while region_widget is None:
        time.sleep(0.2)

    while True:
        if overlay_visible:
            x, y, w, h = region_widget.get_geometry()
            try:
                img = ImageGrab.grab(bbox=(x, y, x + w, y + h))
                buffer = BytesIO()
                img.save(buffer, format="PNG")
                encoded_image = base64.b64encode(buffer.getvalue()).decode("utf-8")

                sio.emit('screenshot', {
                    'agent_id': AGENT_ID,
                    'image_base64': encoded_image
                })
            except Exception as e:
                print(f"[ERROR] Screenshot error: {e}")

        time.sleep(current_interval / 1000)

# ---------------- UI Launcher ----------------
class RegionLauncher(QtCore.QObject):
    trigger = QtCore.pyqtSignal(int, int, int, int)

    def __init__(self):
        super().__init__()
        self.trigger.connect(self.handle)

    def handle(self, x, y, w, h):
        launch_region(x, y, w, h)

region_launcher = None

def launch_region(x, y, w, h):
    global region_widget
    if region_widget:
        return
    region_widget = ScreenshotRegion(x, y, w, h)
    region_widget.show()

# ---------------- Main ----------------
if __name__ == "__main__":
    app_instance = QtWidgets.QApplication(sys.argv)
    region_launcher = RegionLauncher()

    sio.connect(SERVER_URL, transports=['websocket'])

    threading.Thread(target=capture_loop, daemon=True).start()

    sys.exit(app_instance.exec_())
