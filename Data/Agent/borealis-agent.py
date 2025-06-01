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
import traceback
import platform # OS Detection
import importlib.util

import socketio
from qasync import QEventLoop
from PyQt5 import QtCore, QtGui, QtWidgets
from PIL import ImageGrab

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: CONFIG MANAGER
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
        print("[DEBUG] Loading config from disk.")
        if not os.path.exists(self.path):
            print("[DEBUG] Config file not found. Creating default.")
            self.data = DEFAULT_CONFIG.copy()
            self._write()
        else:
            try:
                with open(self.path, 'r') as f:
                    loaded = json.load(f)
                self.data = {**DEFAULT_CONFIG, **loaded}
                print("[DEBUG] Config loaded:", self.data)
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
            print("[DEBUG] Config written to disk.")
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
CONFIG.load()

def init_agent_id():
    if not CONFIG.data.get('agent_id'):
        CONFIG.data['agent_id'] = f"{socket.gethostname().lower()}-agent-{uuid.uuid4().hex[:8]}"
        CONFIG._write()
    return CONFIG.data['agent_id']

AGENT_ID = init_agent_id()
print(f"[DEBUG] Using AGENT_ID: {AGENT_ID}")

def clear_regions_only():
    CONFIG.data['regions'] = CONFIG.data.get('regions', {})
    CONFIG._write()

clear_regions_only()

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: OPERATING SYSTEM DETECTION
# //////////////////////////////////////////////////////////////////////////
def detect_agent_os():
    plat = platform.system().lower()
    if plat.startswith('win'):
        return 'windows'
    elif plat.startswith('linux'):
        return 'linux'
    elif plat.startswith('darwin'):
        return 'macos'
    else:
        return 'unknown'

CONFIG.data['agent_operating_system'] = detect_agent_os()
CONFIG._write()

# //////////////////////////////////////////////////////////////////////////       
# CORE SECTION: MACRO AUTOMATION
# //////////////////////////////////////////////////////////////////////////
MACRO_ENGINE_PATH = os.path.join(os.path.dirname(__file__), "Python_API_Endpoints", "macro_engines.py")
spec = importlib.util.spec_from_file_location("macro_engines", MACRO_ENGINE_PATH)
macro_engines = importlib.util.module_from_spec(spec)
spec.loader.exec_module(macro_engines)

# //////////////////////////////////////////////////////////////////////////       
# CORE SECTION: ASYNC TASK / WEBSOCKET
# //////////////////////////////////////////////////////////////////////////

sio = socketio.AsyncClient(reconnection=True, reconnection_attempts=0, reconnection_delay=5)
role_tasks = {}
overlay_widgets = {}
background_tasks = []

async def stop_all_roles():
    print("[DEBUG] Stopping all roles.")
    for task in list(role_tasks.values()):
        print(f"[DEBUG] Cancelling task for node: {task}")
        task.cancel()
    role_tasks.clear()
    for node_id, widget in overlay_widgets.items():
        print(f"[DEBUG] Closing overlay widget: {node_id}")
        try:
            widget.close()
        except Exception as e:
            print(f"[WARN] Error closing widget: {e}")
    overlay_widgets.clear()

@sio.event
async def connect():
    print(f"[WebSocket] Connected to Borealis Server with Agent ID: {AGENT_ID}")
    await sio.emit('connect_agent', {"agent_id": AGENT_ID})
    await sio.emit('request_config', {"agent_id": AGENT_ID})

@sio.event
async def disconnect():
    print("[WebSocket] Disconnected from Borealis server.")
    await stop_all_roles()
    CONFIG.data['regions'].clear()
    CONFIG._write()

# //////////////////////////////////////////////////////////////////////////       
# CORE SECTION: AGENT CONFIG MANAGEMENT / WINDOW MANAGEMENT
# //////////////////////////////////////////////////////////////////////////
@sio.on('agent_config')
async def on_agent_config(cfg):
    print("[DEBUG] agent_config event received.")
    roles = cfg.get('roles', [])
    if not roles:
        print("[CONFIG] Config Reset by Borealis Server Operator - Awaiting New Config...")
        await stop_all_roles()
        return

    print(f"[CONFIG] Received New Agent Config with {len(roles)} Role(s).")

    new_ids = {r.get('node_id') for r in roles if r.get('node_id')}
    old_ids = set(role_tasks.keys())
    removed = old_ids - new_ids

    for rid in removed:
        print(f"[DEBUG] Removing node {rid} from regions/overlays.")
        CONFIG.data['regions'].pop(rid, None)
        w = overlay_widgets.pop(rid, None)
        if w:
            try:
                w.close()
            except:
                pass
    if removed:
        CONFIG._write()

    for task in list(role_tasks.values()):
        task.cancel()
    role_tasks.clear()

    for role_cfg in roles:
        nid = role_cfg.get('node_id')
        role = role_cfg.get('role')
        if role == 'screenshot':
            print(f"[DEBUG] Starting screenshot task for {nid}")
            task = asyncio.create_task(screenshot_task(role_cfg))
            role_tasks[nid] = task
        elif role == 'macro':
            print(f"[DEBUG] Starting macro task for {nid}")
            task = asyncio.create_task(macro_task(role_cfg))
            role_tasks[nid] = task

@sio.on('list_agent_windows')
async def handle_list_agent_windows(data):
    windows = macro_engines.list_windows()
    await sio.emit('agent_window_list', {
        'agent_id': AGENT_ID,
        'windows': windows
    })

# ---------------- Overlay Widget ----------------
overlay_green_thickness = 4
overlay_gray_thickness = 2
handle_size = overlay_green_thickness * 2
extra_top_padding = overlay_green_thickness * 2 + 4  # give space above the top-center green bar

class ScreenshotRegion(QtWidgets.QWidget):
    def __init__(self, node_id, x=100, y=100, w=300, h=200, alias=None):
        super().__init__()
        self.node_id = node_id
        self.alias = alias
        self.setGeometry(x - handle_size, y - handle_size - extra_top_padding, w + handle_size*2, h + handle_size*2 + extra_top_padding)
        self.setWindowFlags(QtCore.Qt.FramelessWindowHint | QtCore.Qt.WindowStaysOnTopHint)
        self.setAttribute(QtCore.Qt.WA_TranslucentBackground)
        self.resize_dir = None
        self.drag_offset = None
        self._start_geom = None
        self._start_pos = None
        self.setMouseTracking(True)

    def paintEvent(self, event):
        p = QtGui.QPainter(self)
        p.setRenderHint(QtGui.QPainter.Antialiasing)
        w = self.width()
        h = self.height()

        # draw gray capture box
        p.setPen(QtGui.QPen(QtGui.QColor(60,60,60), overlay_gray_thickness))
        p.drawRect(handle_size, handle_size + extra_top_padding, w-handle_size*2, h-handle_size*2 - extra_top_padding)

        p.setPen(QtCore.Qt.NoPen)
        p.setBrush(QtGui.QBrush(QtGui.QColor(0,255,0)))
        edge = overlay_green_thickness*3

        # corner handles
        p.drawRect(0,extra_top_padding,edge,overlay_green_thickness)
        p.drawRect(0,extra_top_padding,overlay_green_thickness,edge)
        p.drawRect(w-edge,extra_top_padding,edge,overlay_green_thickness)
        p.drawRect(w-overlay_green_thickness,extra_top_padding,overlay_green_thickness,edge)
        p.drawRect(0,h-overlay_green_thickness,edge,overlay_green_thickness)
        p.drawRect(0,h-edge,overlay_green_thickness,edge)
        p.drawRect(w-edge,h-overlay_green_thickness,edge,overlay_green_thickness)
        p.drawRect(w-overlay_green_thickness,h-edge,overlay_green_thickness,edge)

        # side handles
        long = overlay_green_thickness*6
        p.drawRect((w-long)//2,extra_top_padding,long,overlay_green_thickness)
        p.drawRect((w-long)//2,h-overlay_green_thickness,long,overlay_green_thickness)
        p.drawRect(0,(h+extra_top_padding-long)//2,overlay_green_thickness,long)
        p.drawRect(w-overlay_green_thickness,(h+extra_top_padding-long)//2,overlay_green_thickness,long)

        # draw grabber bar (same size as top-center bar, but above it)
        bar_width = overlay_green_thickness * 6
        bar_height = overlay_green_thickness
        bar_x = (w - bar_width) // 2
        bar_y = 6  # 6–8 px down from top

        p.setBrush(QtGui.QColor(0,191,255))  # Borealis Blue
        p.drawRect(bar_x, bar_y - bar_height - 2, bar_width, bar_height)  # 2px padding above green bar


    def get_geometry(self):
        g = self.geometry()
        return (g.x() + handle_size, g.y() + handle_size + extra_top_padding, g.width() - handle_size*2, g.height() - handle_size*2 - extra_top_padding)

    def mousePressEvent(self,e):
        if e.button()==QtCore.Qt.LeftButton:
            pos=e.pos()
            bar_width = overlay_green_thickness * 6
            bar_height = overlay_green_thickness
            bar_x = (self.width() - bar_width) // 2
            bar_y = 2
            bar_rect = QtCore.QRect(bar_x, bar_y, bar_width, bar_height)

            if bar_rect.contains(pos):
                self.drag_offset = e.globalPos() - self.frameGeometry().topLeft()
                return

            x1,y1,self_w,self_h = self.geometry().getRect()
            m=handle_size
            dirs = []
            if pos.x()<=m: dirs.append('left')
            if pos.x()>=self.width()-m: dirs.append('right')
            if pos.y()<=m+extra_top_padding: dirs.append('top')
            if pos.y()>=self.height()-m: dirs.append('bottom')
            if dirs:
                self.resize_dir = '_'.join(dirs)
                self._start_geom = self.geometry()
                self._start_pos = e.globalPos()
            else:
                self.drag_offset = e.globalPos()-self.frameGeometry().topLeft()

    def mouseMoveEvent(self,e):
        if self.resize_dir and self._start_geom and self._start_pos:
            dx = e.globalX() - self._start_pos.x()
            dy = e.globalY() - self._start_pos.y()
            geom = QtCore.QRect(self._start_geom)
            if 'left' in self.resize_dir:
                new_x = geom.x() + dx
                new_w = geom.width() - dx
                geom.setX(new_x)
                geom.setWidth(new_w)
            if 'right' in self.resize_dir:
                geom.setWidth(self._start_geom.width() + dx)
            if 'top' in self.resize_dir:
                new_y = geom.y() + dy
                new_h = geom.height() - dy
                geom.setY(new_y)
                geom.setHeight(new_h)
            if 'bottom' in self.resize_dir:
                geom.setHeight(self._start_geom.height() + dy)
            self.setGeometry(geom)
        elif self.drag_offset and e.buttons() & QtCore.Qt.LeftButton:
            self.move(e.globalPos()-self.drag_offset)

    def mouseReleaseEvent(self,e):
        self.drag_offset=None
        self.resize_dir=None
        self._start_geom=None
        self._start_pos=None
        x,y,w,h=self.get_geometry()
        CONFIG.data['regions'][self.node_id]={'x':x,'y':y,'w':w,'h':h}
        CONFIG._write()
        asyncio.create_task(sio.emit('agent_screenshot_task',{ 'agent_id':AGENT_ID,'node_id':self.node_id,'image_base64':'','x':x,'y':y,'w':w,'h':h}))

# ---------------- Screenshot Task ----------------
async def screenshot_task(cfg):
    nid=cfg.get('node_id')
    alias=cfg.get('alias','')
    print(f"[DEBUG] Running screenshot_task for {nid}")
    r=CONFIG.data['regions'].get(nid)
    if r:
        region=(r['x'],r['y'],r['w'],r['h'])
    else:
        region=(cfg.get('x',100),cfg.get('y',100),cfg.get('w',300),cfg.get('h',200))
        CONFIG.data['regions'][nid]={'x':region[0],'y':region[1],'w':region[2],'h':region[3]}
        CONFIG._write()
    if nid not in overlay_widgets:
        widget=ScreenshotRegion(nid,*region,alias=alias)
        overlay_widgets[nid]=widget; widget.show()
    await sio.emit('agent_screenshot_task',{'agent_id':AGENT_ID,'node_id':nid,'image_base64':'','x':region[0],'y':region[1],'w':region[2],'h':region[3]})
    interval=cfg.get('interval',1000)/1000.0
    loop=asyncio.get_event_loop()
    executor=concurrent.futures.ThreadPoolExecutor(max_workers=CONFIG.data.get('max_task_workers',8))
    try:
        while True:
            x,y,w,h=overlay_widgets[nid].get_geometry()
            grab=partial(ImageGrab.grab,bbox=(x,y,x+w,y+h))
            img=await loop.run_in_executor(executor,grab)
            buf=BytesIO(); img.save(buf,format='PNG'); encoded=base64.b64encode(buf.getvalue()).decode('utf-8')
            await sio.emit('agent_screenshot_task',{'agent_id':AGENT_ID,'node_id':nid,'image_base64':encoded,'x':x,'y':y,'w':w,'h':h})
            await asyncio.sleep(interval)
    except asyncio.CancelledError:
        print(f"[TASK] Screenshot role {nid} cancelled.")
    except Exception as e:
        print(f"[ERROR] Screenshot task {nid} failed: {e}")
        traceback.print_exc()

# ---------------- Macro Task ----------------
async def macro_task(cfg):
    nid = cfg.get('node_id')
    window_handle = cfg.get('window_handle')
    mode = cfg.get('operation_mode', 'keypress')  # 'keypress' or 'typed_text'
    key = cfg.get('key')
    text = cfg.get('text')
    interval_ms = int(cfg.get('interval_ms', 1000))
    randomize = cfg.get('randomize_interval', False)
    random_min = int(cfg.get('random_min', 750))
    random_max = int(cfg.get('random_max', 950))
    active = cfg.get('active', True)  # Whether macro is "started" or "paused"
    print(f"[DEBUG] Macro task for node {nid} started on window {window_handle}")

    import random
    try:
        while True:
            if not active:
                await asyncio.sleep(0.2)
                continue
            if mode == 'keypress' and key:
                macro_engines.send_keypress_to_window(window_handle, key)
            elif mode == 'typed_text' and text:
                macro_engines.type_text_to_window(window_handle, text)
            # Interval logic
            if randomize:
                ms = random.randint(random_min, random_max)
            else:
                ms = interval_ms
            await asyncio.sleep(ms / 1000.0)
    except asyncio.CancelledError:
        print(f"[TASK] Macro role {nid} cancelled.")
    except Exception as e:
        print(f"[ERROR] Macro task {nid} failed: {e}")
        import traceback
        traceback.print_exc()

# ---------------- Config Watcher ----------------
async def config_watcher():
    print("[DEBUG] Starting config watcher")
    while True:
        CONFIG.watch(); await asyncio.sleep(CONFIG.data.get('config_file_watcher_interval',2))

# ---------------- Persistent Idle Task ----------------
async def idle_task():
    print("[Agent] Entering idle state. Awaiting instructions...")
    try:
        while True:
            await asyncio.sleep(60); print("[DEBUG] Idle task still alive.")
    except asyncio.CancelledError:
        print("[FATAL] Idle task was cancelled!")
    except Exception as e:
        print(f"[FATAL] Idle task crashed: {e}"); traceback.print_exc()

# ---------------- Dummy Qt Widget to Prevent Exit ----------------
class PersistentWindow(QtWidgets.QWidget):
    def __init__(self):
        super().__init__()
        self.setWindowTitle("KeepAlive")
        self.setGeometry(-1000,-1000,1,1)
        self.setAttribute(QtCore.Qt.WA_DontShowOnScreen)
        self.hide()

# //////////////////////////////////////////////////////////////////////////
# MAIN & EVENT LOOP
# //////////////////////////////////////////////////////////////////////////
async def connect_loop():
    retry=5
    while True:
        try:
            url=CONFIG.data.get('borealis_server_url',"http://localhost:5000")
            print(f"[WebSocket] Connecting to {url}...")
            await sio.connect(url,transports=['websocket'])
            break
        except Exception as e:
            print(f"[WebSocket] Server unavailable: {e}. Retrying in {retry}s...")
            await asyncio.sleep(retry)

if __name__=='__main__':
    app=QtWidgets.QApplication(sys.argv)
    loop=QEventLoop(app); asyncio.set_event_loop(loop)
    dummy_window=PersistentWindow(); dummy_window.show()
    print("[DEBUG] Dummy window shown to prevent Qt exit")
    try:
        background_tasks.append(loop.create_task(config_watcher()))
        background_tasks.append(loop.create_task(connect_loop()))
        background_tasks.append(loop.create_task(idle_task()))
        loop.run_forever()
    except Exception as e:
        print(f"[FATAL] Event loop crashed: {e}")
        traceback.print_exc()
    finally:
        print("[FATAL] Agent exited unexpectedly.")
