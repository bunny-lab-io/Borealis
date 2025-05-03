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
    "borealis_server_url": "http://localhost:5000",
    "max_task_workers": 8,
    "config_file_watcher_interval": 2,
    "agent_id": "",
    "regions": {}
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
                    loaded = json.load(f)
                self.data = {**DEFAULT_CONFIG, **loaded}
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
CONFIG.data['regions'] = {}
CONFIG._write()
# //////////////////////////////////////////////////////////////////////////
# END CORE SECTION: CONFIG MANAGER
# //////////////////////////////////////////////////////////////////////////

host = socket.gethostname().lower()
stored_id = CONFIG.data.get('agent_id')
if stored_id:
    AGENT_ID = stored_id
else:
    AGENT_ID = f"{host}-agent-{uuid.uuid4().hex[:8]}"
    CONFIG.data['agent_id'] = AGENT_ID
    CONFIG._write()

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: WEBSOCKET SETUP & HANDLERS (do not modify unless absolutely necessary)
# //////////////////////////////////////////////////////////////////////////

sio = socketio.AsyncClient(reconnection=True, reconnection_attempts=0, reconnection_delay=5)
role_tasks = {}
overlay_widgets = {}

@sio.event
async def connect():
    print(f"[WebSocket] Connected to Agent ID: {AGENT_ID}.")
    await sio.emit('connect_agent', {"agent_id": AGENT_ID})
    await sio.emit('request_config', {"agent_id": AGENT_ID})

@sio.event
async def disconnect():
    print("[WebSocket] Disconnected from Borealis server.")
    for task in list(role_tasks.values()):
        task.cancel()
    role_tasks.clear()
    for widget in list(overlay_widgets.values()):
        try: widget.close()
        except: pass
    overlay_widgets.clear()
    CONFIG.data['regions'].clear()
    CONFIG._write()
    CONFIG.load()

@sio.on('agent_config')
async def on_agent_config(cfg):
    print(f"[CONNECTED] Received config with {len(cfg.get('roles',[]))} roles.")
    new_ids = {r.get('node_id') for r in cfg.get('roles', []) if r.get('node_id')}
    old_ids = set(role_tasks.keys())
    removed = old_ids - new_ids

    # Cancel removed roles
    for rid in removed:
        if rid in CONFIG.data['regions']:
            CONFIG.data['regions'].pop(rid, None)
        w = overlay_widgets.pop(rid, None)
        if w:
            try: w.close()
            except: pass

    if removed:
        CONFIG._write()

    # Cancel all existing to ensure clean state
    for task in list(role_tasks.values()):
        task.cancel()
    role_tasks.clear()

    # Restart everything to ensure roles are re-applied
    for role_cfg in cfg.get('roles', []):
        nid = role_cfg.get('node_id')
        if role_cfg.get('role') == 'screenshot':
            task = asyncio.create_task(screenshot_task(role_cfg))
            role_tasks[nid] = task
# //////////////////////////////////////////////////////////////////////////
# END CORE SECTION: WEBSOCKET SETUP & HANDLERS
# //////////////////////////////////////////////////////////////////////////

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
        p = QtGui.QPainter(self)
        p.setRenderHint(QtGui.QPainter.Antialiasing)
        p.setBrush(QtCore.Qt.transparent)
        p.setPen(QtGui.QPen(QtGui.QColor(0,255,0),2))
        p.drawRect(self.rect())
        hr = self.resize_handle_size
        hrect = QtCore.QRect(self.width()-hr, self.height()-hr, hr, hr)
        p.fillRect(hrect, QtGui.QColor(0,255,0))

    def mousePressEvent(self, e):
        if e.button()==QtCore.Qt.LeftButton:
            x, y = e.pos().x(), e.pos().y()
            if x > self.width() - self.resize_handle_size and y > self.height() - self.resize_handle_size:
                self.resizing = True
            else:
                self.drag_offset = e.globalPos() - self.frameGeometry().topLeft()

    def mouseMoveEvent(self, e):
        if self.resizing:
            nw = max(e.pos().x(), 100)
            nh = max(e.pos().y(), 80)
            self.resize(nw, nh)
        elif e.buttons() & QtCore.Qt.LeftButton and self.drag_offset:
            self.move(e.globalPos() - self.drag_offset)

    def mouseReleaseEvent(self, e):
        self.resizing = False
        self.drag_offset = None

    def get_geometry(self):
        g = self.geometry()
        return (g.x(), g.y(), g.width(), g.height())

# ---------------- Screenshot Task ----------------
async def screenshot_task(cfg):
    nid = cfg.get('node_id')
    # If existing region in config, honor that
    r = CONFIG.data['regions'].get(nid)
    if r:
        region = (r['x'], r['y'], r['w'], r['h'])
    else:
        region = (cfg.get('x', 100), cfg.get('y', 100), cfg.get('w', 300), cfg.get('h', 200))
        CONFIG.data['regions'][nid] = {
            'x': region[0], 'y': region[1], 'w': region[2], 'h': region[3]
        }
        CONFIG._write()

    if nid not in overlay_widgets:
        widget = ScreenshotRegion(nid, *region)
        overlay_widgets[nid] = widget
        widget.show()

    interval = cfg.get('interval', 1000) / 1000.0
    loop = asyncio.get_event_loop()
    executor = concurrent.futures.ThreadPoolExecutor(
        max_workers=CONFIG.data.get('max_task_workers', DEFAULT_CONFIG['max_task_workers'])
    )

    try:
        while True:
            x, y, w, h = overlay_widgets[nid].get_geometry()
            prev = CONFIG.data['regions'].get(nid)
            new_geom = {'x': x, 'y': y, 'w': w, 'h': h}
            if prev != new_geom:
                CONFIG.data['regions'][nid] = new_geom
                CONFIG._write()
            grab = partial(ImageGrab.grab, bbox=(x, y, x+w, y+h))
            img = await loop.run_in_executor(executor, grab)
            buf = BytesIO()
            img.save(buf, format='PNG')
            encoded = base64.b64encode(buf.getvalue()).decode('utf-8')
            await sio.emit('agent_screenshot_task', {
                'agent_id': AGENT_ID,
                'node_id': nid,
                'image_base64': encoded,
                'x': x, 'y': y, 'w': w, 'h': h  # Bi-directional live-sync
            })
            await asyncio.sleep(interval)
    except asyncio.CancelledError:
        return
    except Exception as e:
        print(f"[ERROR] Screenshot task {nid} failed: {e}")

# ---------------- Config Watcher ----------------
async def config_watcher():
    while True:
        if CONFIG.watch(): pass
        await asyncio.sleep(CONFIG.data.get('config_file_watcher_interval', DEFAULT_CONFIG['config_file_watcher_interval']))

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: MAIN & EVENT LOOP (do not modify unless you know what you’re doing)
# //////////////////////////////////////////////////////////////////////////
async def connect_loop():
    retry = 5
    while True:
        try:
            url = CONFIG.data.get('borealis_server_url', DEFAULT_CONFIG['borealis_server_url'])
            print(f"[WebSocket] Connecting to {url}...")
            await sio.connect(url, transports=['websocket'])
            break
        except:
            print(f"[WebSocket] Server unavailable, retrying in {retry}s...")
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
