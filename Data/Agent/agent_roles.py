import os
import asyncio
import concurrent.futures
from functools import partial
from io import BytesIO
import base64
import traceback
import random
import importlib.util

from PyQt5 import QtCore, QtGui, QtWidgets
from PIL import ImageGrab


class RolesContext:
    def __init__(self, sio, agent_id, config):
        self.sio = sio
        self.agent_id = agent_id
        self.config = config


# Load macro engines from the local Python_API_Endpoints directory
MACRO_ENGINE_PATH = os.path.join(os.path.dirname(__file__), "Python_API_Endpoints", "macro_engines.py")
spec = importlib.util.spec_from_file_location("macro_engines", MACRO_ENGINE_PATH)
macro_engines = importlib.util.module_from_spec(spec)
spec.loader.exec_module(macro_engines)


# Overlay visuals
overlay_green_thickness = 4
overlay_gray_thickness = 2
handle_size = overlay_green_thickness * 2
extra_top_padding = overlay_green_thickness * 2 + 4


# Track active screenshot overlay widgets per node_id
overlay_widgets: dict[str, QtWidgets.QWidget] = {}


def get_window_list():
    """Return a list of windows from macro engines."""
    try:
        return macro_engines.list_windows()
    except Exception:
        return []


def close_overlay(node_id: str):
    w = overlay_widgets.pop(node_id, None)
    if w:
        try:
            w.close()
        except Exception:
            pass


def close_all_overlays():
    for node_id, widget in list(overlay_widgets.items()):
        try:
            widget.close()
        except Exception:
            pass
    overlay_widgets.clear()


class ScreenshotRegion(QtWidgets.QWidget):
    def __init__(self, ctx: RolesContext, node_id, x=100, y=100, w=300, h=200, alias=None):
        super().__init__()
        self.ctx = ctx
        self.node_id = node_id
        self.alias = alias
        self.setGeometry(
            x - handle_size,
            y - handle_size - extra_top_padding,
            w + handle_size * 2,
            h + handle_size * 2 + extra_top_padding,
        )
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
        p.setPen(QtGui.QPen(QtGui.QColor(130, 130, 130), overlay_gray_thickness))
        p.drawRect(handle_size, handle_size + extra_top_padding, w - handle_size * 2, h - handle_size * 2 - extra_top_padding)

        p.setPen(QtCore.Qt.NoPen)
        p.setBrush(QtGui.QBrush(QtGui.QColor(0, 191, 255)))
        edge = overlay_green_thickness * 3

        # corner handles
        p.drawRect(0, extra_top_padding, edge, overlay_green_thickness)
        p.drawRect(0, extra_top_padding, overlay_green_thickness, edge)
        p.drawRect(w - edge, extra_top_padding, edge, overlay_green_thickness)
        p.drawRect(w - overlay_green_thickness, extra_top_padding, overlay_green_thickness, edge)
        p.drawRect(0, h - overlay_green_thickness, edge, overlay_green_thickness)
        p.drawRect(0, h - edge, overlay_green_thickness, edge)
        p.drawRect(w - edge, h - overlay_green_thickness, edge, overlay_green_thickness)
        p.drawRect(w - overlay_green_thickness, h - edge, overlay_green_thickness, edge)

        # side handles
        long = overlay_green_thickness * 6
        p.drawRect((w - long) // 2, extra_top_padding, long, overlay_green_thickness)
        p.drawRect((w - long) // 2, h - overlay_green_thickness, long, overlay_green_thickness)
        p.drawRect(0, (h + extra_top_padding - long) // 2, overlay_green_thickness, long)
        p.drawRect(w - overlay_green_thickness, (h + extra_top_padding - long) // 2, overlay_green_thickness, long)

        # grabber bar
        bar_width = overlay_green_thickness * 6
        bar_height = overlay_green_thickness
        bar_x = (w - bar_width) // 2
        bar_y = 6
        p.setBrush(QtGui.QColor(0, 191, 255))
        p.drawRect(bar_x, bar_y - bar_height - 10, bar_width, bar_height * 4)

    def get_geometry(self):
        g = self.geometry()
        return (
            g.x() + handle_size,
            g.y() + handle_size + extra_top_padding,
            g.width() - handle_size * 2,
            g.height() - handle_size * 2 - extra_top_padding,
        )

    def mousePressEvent(self, e):
        if e.button() == QtCore.Qt.LeftButton:
            pos = e.pos()
            bar_width = overlay_green_thickness * 6
            bar_height = overlay_green_thickness
            bar_x = (self.width() - bar_width) // 2
            bar_y = 2
            bar_rect = QtCore.QRect(bar_x, bar_y, bar_width, bar_height)

            if bar_rect.contains(pos):
                self.drag_offset = e.globalPos() - self.frameGeometry().topLeft()
                return

            m = handle_size
            dirs = []
            if pos.x() <= m:
                dirs.append('left')
            if pos.x() >= self.width() - m:
                dirs.append('right')
            if pos.y() <= m + extra_top_padding:
                dirs.append('top')
            if pos.y() >= self.height() - m:
                dirs.append('bottom')
            if dirs:
                self.resize_dir = '_'.join(dirs)
                self._start_geom = self.geometry()
                self._start_pos = e.globalPos()
            else:
                self.drag_offset = e.globalPos() - self.frameGeometry().topLeft()

    def mouseMoveEvent(self, e):
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
            self.move(e.globalPos() - self.drag_offset)

    def mouseReleaseEvent(self, e):
        self.drag_offset = None
        self.resize_dir = None
        self._start_geom = None
        self._start_pos = None
        x, y, w, h = self.get_geometry()
        self.ctx.config.data['regions'][self.node_id] = {'x': x, 'y': y, 'w': w, 'h': h}
        try:
            self.ctx.config._write()
        except Exception:
            pass
        # Emit a zero-image update so the server knows new geometry immediately
        asyncio.create_task(self.ctx.sio.emit('agent_screenshot_task', {
            'agent_id': self.ctx.agent_id,
            'node_id': self.node_id,
            'image_base64': '',
            'x': x, 'y': y, 'w': w, 'h': h
        }))


async def screenshot_task(ctx: RolesContext, cfg):
    nid = cfg.get('node_id')
    alias = cfg.get('alias', '')
    r = ctx.config.data['regions'].get(nid)
    if r:
        region = (r['x'], r['y'], r['w'], r['h'])
    else:
        region = (
            cfg.get('x', 100),
            cfg.get('y', 100),
            cfg.get('w', 300),
            cfg.get('h', 200),
        )
        ctx.config.data['regions'][nid] = {'x': region[0], 'y': region[1], 'w': region[2], 'h': region[3]}
        try:
            ctx.config._write()
        except Exception:
            pass

    if nid not in overlay_widgets:
        widget = ScreenshotRegion(ctx, nid, *region, alias=alias)
        overlay_widgets[nid] = widget
        widget.show()

    await ctx.sio.emit('agent_screenshot_task', {
        'agent_id': ctx.agent_id,
        'node_id': nid,
        'image_base64': '',
        'x': region[0], 'y': region[1], 'w': region[2], 'h': region[3]
    })

    interval = cfg.get('interval', 1000) / 1000.0
    loop = asyncio.get_event_loop()
    executor = concurrent.futures.ThreadPoolExecutor(max_workers=ctx.config.data.get('max_task_workers', 8))
    try:
        while True:
            x, y, w, h = overlay_widgets[nid].get_geometry()
            grab = partial(ImageGrab.grab, bbox=(x, y, x + w, y + h))
            img = await loop.run_in_executor(executor, grab)
            buf = BytesIO(); img.save(buf, format='PNG')
            encoded = base64.b64encode(buf.getvalue()).decode('utf-8')
            await ctx.sio.emit('agent_screenshot_task', {
                'agent_id': ctx.agent_id,
                'node_id': nid,
                'image_base64': encoded,
                'x': x, 'y': y, 'w': w, 'h': h
            })
            await asyncio.sleep(interval)
    except asyncio.CancelledError:
        print(f"[TASK] Screenshot role {nid} cancelled.")
    except Exception as e:
        print(f"[ERROR] Screenshot task {nid} failed: {e}")
        traceback.print_exc()


async def macro_task(ctx: RolesContext, cfg):
    """Improved macro task supporting operation modes, live config, and feedback."""
    nid = cfg.get('node_id')

    last_trigger_value = 0
    has_run_once = False

    while True:
        window_handle = cfg.get('window_handle')
        macro_type = cfg.get('macro_type', 'keypress')
        operation_mode = cfg.get('operation_mode', 'Continuous')
        key = cfg.get('key')
        text = cfg.get('text')
        interval_ms = int(cfg.get('interval_ms', 1000))
        randomize = cfg.get('randomize_interval', False)
        random_min = int(cfg.get('random_min', 750))
        random_max = int(cfg.get('random_max', 950))
        active = cfg.get('active', True)
        trigger = int(cfg.get('trigger', 0))

        async def emit_macro_status(success, message=""):
            await ctx.sio.emit('macro_status', {
                "agent_id": ctx.agent_id,
                "node_id": nid,
                "success": success,
                "message": message,
                "timestamp": int(asyncio.get_event_loop().time() * 1000)
            })

        if not (active is True or str(active).lower() == "true"):
            await asyncio.sleep(0.2)
            continue

        try:
            send_macro = False

            if operation_mode == "Run Once":
                if not has_run_once:
                    send_macro = True
                    has_run_once = True
            elif operation_mode == "Continuous":
                send_macro = True
            elif operation_mode == "Trigger-Continuous":
                send_macro = (trigger == 1)
            elif operation_mode == "Trigger-Once":
                if last_trigger_value == 0 and trigger == 1:
                    send_macro = True
                last_trigger_value = trigger
            else:
                send_macro = True

            if send_macro:
                if macro_type == 'keypress' and key:
                    result = macro_engines.send_keypress_to_window(window_handle, key)
                elif macro_type == 'typed_text' and text:
                    result = macro_engines.type_text_to_window(window_handle, text)
                else:
                    await emit_macro_status(False, "Invalid macro type or missing key/text")
                    await asyncio.sleep(0.2)
                    continue

                if isinstance(result, tuple):
                    success, err = result
                else:
                    success, err = bool(result), ""

                if success:
                    await emit_macro_status(True, f"Macro sent: {macro_type}")
                else:
                    await emit_macro_status(False, err or "Unknown macro engine failure")
            else:
                await asyncio.sleep(0.05)

            if send_macro:
                if randomize:
                    ms = random.randint(random_min, random_max)
                else:
                    ms = interval_ms
                await asyncio.sleep(ms / 1000.0)
            else:
                await asyncio.sleep(0.1)

        except asyncio.CancelledError:
            print(f"[TASK] Macro role {nid} cancelled.")
            break
        except Exception as e:
            print(f"[ERROR] Macro task {nid} failed: {e}")
            traceback.print_exc()
            await emit_macro_status(False, str(e))
            await asyncio.sleep(0.5)

