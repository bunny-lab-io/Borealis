#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/borealis-agent.py

import sys
import uuid
import socket
import os
import json
import asyncio
import concurrent.futures
from functools import partial
from io import BytesIO
import base64

import socketio
from qasync import QEventLoop
from PyQt5 import QtCore, QtGui, QtWidgets
from PIL import ImageGrab

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: CONFIG MANAGER (do not modify unless you know what you’re doing)
# //////////////////////////////////////////////////////////////////////////
CONFIG_PATH = os.path.join(os.path.dirname(__file__), "agent_settings.json")
DEFAULT_CONFIG = {
    "SERVER_URL": "http://localhost:5000",
    "max_workers": 8,
    "config_watch_interval": 2
}

class ConfigManager:
    def __init__(self, path):
        self.path = path
        self._last_mtime = None
        self.data = {}
        self.load()

    def load(self):
        if not os.path.exists(self.path):
            self.data = DEFAULT_CONFIG.copy()
            self._write()
        else:
            try:
                with open(self.path, 'r') as f:
                    self.data = json.load(f)
            except Exception as e:
                print(f"[WARN] Failed to parse config: {e}")
                self.data = DEFAULT_CONFIG.copy()
        try:
            self._last_mtime = os.path.getmtime(self.path)
        except Exception:
            self._last_mtime = None

    def _write(self):
        try:
            with open(self.path, 'w') as f:
                json.dump(self.data, f, indent=2)
        except Exception as e:
            print(f"[ERROR] Could not write config: {e}")

    def watch(self):
        try:
            mtime = os.path.getmtime(self.path)
            if self._last_mtime is None or mtime != self._last_mtime:
                print("[CONFIG] Detected config change, reloading.")
                self.load()
                return True
        except Exception:
            pass
        return False

CONFIG = ConfigManager(CONFIG_PATH)
# //////////////////////////////////////////////////////////////////////////
# END CORE SECTION: CONFIG MANAGER
# //////////////////////////////////////////////////////////////////////////

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: WEBSOCKET SETUP & HANDLERS (do not modify unless absolutely necessary)
# //////////////////////////////////////////////////////////////////////////
AGENT_ID = f"{socket.gethostname().lower()}-agent-{uuid.uuid4().hex[:8]}"

sio = socketio.AsyncClient(reconnection=True, reconnection_attempts=0, reconnection_delay=5)
role_tasks = {}

@sio.event
async def connect():
    print(f"[WebSocket] Agent ID: {AGENT_ID} connected to Borealis.")
    await sio.emit('connect_agent', {"agent_id": AGENT_ID})
    await sio.emit('request_config', {"agent_id": AGENT_ID})

@sio.event
async def disconnect():
    print("[WebSocket] Lost connection to Borealis server.")

@sio.on('agent_config')
async def on_agent_config(cfg):
    print("[PROVISIONED] Received new configuration from Borealis.")
    # cancel existing role tasks
    for task in list(role_tasks.values()):
        task.cancel()
    role_tasks.clear()
    # start new tasks
    for role_cfg in cfg.get('roles', []):
        role = role_cfg.get('role')
        node_id = role_cfg.get('node_id')
        if role == 'screenshot' and node_id:
            task = asyncio.create_task(screenshot_task(role_cfg))
            role_tasks[node_id] = task
# //////////////////////////////////////////////////////////////////////////
# END CORE SECTION: WEBSOCKET SETUP & HANDLERS
# //////////////////////////////////////////////////////////////////////////

# ---------------- Overlay Widget ----------------
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
        handle = QtCore.QRect(self.width() - self.resize_handle_size,
                              self.height() - self.resize_handle_size,
                              self.resize_handle_size, self.resize_handle_size)
        painter.fillRect(handle, QtGui.QColor(0, 255, 0))

    def mousePressEvent(self, event):
        if event.button() == QtCore.Qt.LeftButton:
            px, py = event.pos().x(), event.pos().y()
            if px > self.width() - self.resize_handle_size and \
               py > self.height() - self.resize_handle_size:
                self.resizing = True
            else:
                self.drag_offset = event.globalPos() - self.frameGeometry().topLeft()

    def mouseMoveEvent(self, event):
        if self.resizing:
            nw = max(event.pos().x(), 100)
            nh = max(event.pos().y(), 80)
            self.resize(nw, nh)
        elif event.buttons() & QtCore.Qt.LeftButton and self.drag_offset:
            self.move(event.globalPos() - self.drag_offset)

    def mouseReleaseEvent(self, event):
        self.resizing = False
        self.drag_offset = None

    def get_geometry(self):
        geo = self.geometry()
        return geo.x(), geo.y(), geo.width(), geo.height()

# ---------------- Helper Functions ----------------
app = None
overlay_widgets = {}

def create_overlay(node_id, region):
    if node_id in overlay_widgets:
        return
    x, y, w, h = region
    widget = ScreenshotRegion(node_id, x, y, w, h)
    overlay_widgets[node_id] = widget
    widget.show()

def get_overlay_geometry(node_id):
    widget = overlay_widgets.get(node_id)
    if widget:
        return widget.get_geometry()
    return (0, 0, 0, 0)

# ---------------- Screenshot Task ----------------
async def screenshot_task(cfg):
    interval = cfg.get('interval', 1000) / 1000.0
    node_id = cfg.get('node_id')
    region = (cfg.get('x', 100), cfg.get('y', 100), cfg.get('w', 300), cfg.get('h', 200))
    create_overlay(node_id, region)
    loop = asyncio.get_event_loop()
    executor = concurrent.futures.ThreadPoolExecutor(max_workers=CONFIG.data.get('max_workers', 8))
    try:
        while True:
            x, y, w, h = get_overlay_geometry(node_id)
            grab = partial(ImageGrab.grab, bbox=(x, y, x + w, y + h))
            img = await loop.run_in_executor(executor, grab)
            buf = BytesIO()
            img.save(buf, format='PNG')
            encoded = base64.b64encode(buf.getvalue()).decode('utf-8')
            await sio.emit('agent_screenshot_task', {
                'agent_id': AGENT_ID,
                'node_id': node_id,
                'image_base64': encoded
            })
            await asyncio.sleep(interval)
    except asyncio.CancelledError:
        return
    except Exception as e:
        print(f"[ERROR] Screenshot task {node_id} failed: {e}")

# ---------------- Config Watcher ----------------
async def config_watcher():
    while True:
        if CONFIG.watch():
            # settings updated, e.g., executor pool size will apply on next task run
            pass
        await asyncio.sleep(CONFIG.data.get('config_watch_interval', 2))

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: MAIN & EVENT LOOP (do not modify unless you know what you’re doing)
# //////////////////////////////////////////////////////////////////////////
async def connect_loop():
    retry = 5
    while True:
        try:
            print(f"[WebSocket] Connecting to {CONFIG.data['SERVER_URL']}...")
            await sio.connect(CONFIG.data['SERVER_URL'], transports=['websocket'])
            break
        except Exception:
            print(f"[WebSocket] Server not available, retrying in {retry}s...")
            await asyncio.sleep(retry)

if __name__ == '__main__':
    app = QtWidgets.QApplication(sys.argv)
    loop = QEventLoop(app)
    asyncio.set_event_loop(loop)
    with loop:
        loop.create_task(config_watcher())
        loop.create_task(connect_loop())
        loop.run_forever()
# //////////////////////////////////////////////////////////////////////////
# END CORE SECTION: MAIN & EVENT LOOP
# //////////////////////////////////////////////////////////////////////////
