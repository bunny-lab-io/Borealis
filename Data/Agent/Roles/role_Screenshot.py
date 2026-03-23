import os
import asyncio
import concurrent.futures
from functools import partial
from io import BytesIO
import base64
import traceback

from PyQt5 import QtCore, QtGui, QtWidgets
from PIL import ImageGrab
import importlib.util


ROLE_NAME = 'screenshot'
ROLE_CONTEXTS = ['interactive']


# Load macro engines from the local Python_API_Endpoints directory for window listings
def _load_macro_engines():
    try:
        base = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir))
        path = os.path.join(base, 'Python_API_Endpoints', 'macro_engines.py')
        spec = importlib.util.spec_from_file_location('macro_engines', path)
        mod = importlib.util.module_from_spec(spec)
        assert spec and spec.loader
        spec.loader.exec_module(mod)
        return mod
    except Exception:
        class _Dummy:
            def list_windows(self):
                return []
        return _Dummy()


macro_engines = _load_macro_engines()


overlay_green_thickness = 4
overlay_gray_thickness = 2
handle_size = overlay_green_thickness * 2
extra_top_padding = overlay_green_thickness * 2 + 4


overlay_widgets = {}


def _coerce_int(value, default, minimum=None, maximum=None):
    try:
        if value is None:
            raise ValueError
        if isinstance(value, bool):
            raise ValueError
        if isinstance(value, (int, float)):
            ivalue = int(value)
        else:
            text = str(value).strip()
            if not text:
                raise ValueError
            ivalue = int(float(text))
        if minimum is not None and ivalue < minimum:
            return minimum
        if maximum is not None and ivalue > maximum:
            return maximum
        return ivalue
    except Exception:
        return default


def _coerce_bool(value, default=True):
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        text = value.strip().lower()
        if text in {'true', '1', 'yes', 'on'}:
            return True
        if text in {'false', '0', 'no', 'off'}:
            return False
    return default


def _coerce_text(value):
    if value is None:
        return ''
    try:
        return str(value)
    except Exception:
        return ''


def _normalize_mode(value):
    text = _coerce_text(value).strip().lower()
    if text in {'interactive', 'currentuser', 'user'}:
        return 'currentuser'
    if text in {'system', 'svc', 'service'}:
        return 'system'
    return ''


class ScreenshotRegion(QtWidgets.QWidget):
    def __init__(self, ctx, node_id, x=100, y=100, w=300, h=200, alias=None):
        super().__init__()
        self.ctx = ctx
        self.node_id = node_id
        self.alias = _coerce_text(alias)
        self._visible = True
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

        p.setPen(QtGui.QPen(QtGui.QColor(130, 130, 130), overlay_gray_thickness))
        p.drawRect(handle_size, handle_size + extra_top_padding, w - handle_size * 2, h - handle_size * 2 - extra_top_padding)

        p.setPen(QtCore.Qt.NoPen)
        p.setBrush(QtGui.QBrush(QtGui.QColor(0, 191, 255)))
        edge = overlay_green_thickness * 3

        p.drawRect(0, extra_top_padding, edge, overlay_green_thickness)
        p.drawRect(0, extra_top_padding, overlay_green_thickness, edge)
        p.drawRect(w - edge, extra_top_padding, edge, overlay_green_thickness)
        p.drawRect(w - overlay_green_thickness, extra_top_padding, overlay_green_thickness, edge)
        p.drawRect(0, h - overlay_green_thickness, edge, overlay_green_thickness)
        p.drawRect(0, h - edge, overlay_green_thickness, edge)
        p.drawRect(w - edge, h - overlay_green_thickness, edge, overlay_green_thickness)
        p.drawRect(w - overlay_green_thickness, h - edge, overlay_green_thickness, edge)

        long = overlay_green_thickness * 6
        p.drawRect((w - long) // 2, extra_top_padding, long, overlay_green_thickness)
        p.drawRect((w - long) // 2, h - overlay_green_thickness, long, overlay_green_thickness)
        p.drawRect(0, (h + extra_top_padding - long) // 2, overlay_green_thickness, long)
        p.drawRect(w - overlay_green_thickness, (h + extra_top_padding - long) // 2, overlay_green_thickness, long)

        bar_width = overlay_green_thickness * 6
        bar_height = overlay_green_thickness
        bar_x = (w - bar_width) // 2
        bar_y = 6
        p.setBrush(QtGui.QColor(0, 191, 255))
        p.drawRect(bar_x, bar_y - bar_height - 10, bar_width, bar_height * 4)

        if self.alias:
            p.setPen(QtGui.QPen(QtGui.QColor(255, 255, 255)))
            font = QtGui.QFont()
            font.setPointSize(10)
            p.setFont(font)
            text_rect = QtCore.QRect(
                overlay_green_thickness * 2,
                extra_top_padding + overlay_green_thickness * 2,
                w - overlay_green_thickness * 4,
                overlay_green_thickness * 10,
            )
            p.drawText(text_rect, QtCore.Qt.AlignLeft | QtCore.Qt.AlignTop, self.alias)

    def get_geometry(self):
        g = self.geometry()
        return (
            g.x() + handle_size,
            g.y() + handle_size + extra_top_padding,
            g.width() - handle_size * 2,
            g.height() - handle_size * 2 - extra_top_padding,
        )

    def set_region(self, x, y, w, h):
        self.setGeometry(
            x - handle_size,
            y - handle_size - extra_top_padding,
            w + handle_size * 2,
            h + handle_size * 2 + extra_top_padding,
        )

    def set_alias(self, alias):
        self.alias = _coerce_text(alias)
        self.update()

    def apply_visibility(self, visible: bool):
        self._visible = bool(visible)
        if self._visible:
            self.show()
            try:
                self.raise_()
            except Exception:
                pass
        else:
            self.hide()

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
        asyncio.create_task(self.ctx.sio.emit('agent_screenshot_task', {
            'agent_id': self.ctx.agent_id,
            'node_id': self.node_id,
            'image_base64': '',
            'x': x, 'y': y, 'w': w, 'h': h
        }))


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        self.tasks = {}
        self.running_configs = {}
        self.role_health_label = 'Screenshot Capture'

    def register_events(self):
        sio = self.ctx.sio

        @sio.on('list_agent_windows')
        async def _handle_list_windows(payload):
            try:
                windows = macro_engines.list_windows()
            except Exception:
                windows = []
            await sio.emit('agent_window_list', {
                'agent_id': self.ctx.agent_id,
                'windows': windows,
            })

    def health_report(self) -> dict:
        active_tasks = sum(1 for task in self.tasks.values() if task and not task.done())
        configured_regions = len(self.running_configs)
        overlay_count = len(overlay_widgets)
        if configured_regions and active_tasks == 0:
            return {
                'status': 'recovering',
                'role_label': self.role_health_label,
                'detail': 'Screenshot regions are configured but capture tasks are not active.',
                'details': {
                    'running_status': 'Recovering',
                    'configured_regions': str(configured_regions),
                    'active_tasks': str(active_tasks),
                    'visible_overlays': str(overlay_count),
                },
            }
        if active_tasks:
            return {
                'status': 'healthy',
                'role_label': self.role_health_label,
                'detail': f'{active_tasks} screenshot capture task(s) active.',
                'details': {
                    'running_status': 'Running',
                    'configured_regions': str(configured_regions),
                    'active_tasks': str(active_tasks),
                    'visible_overlays': str(overlay_count),
                },
            }
        return {
            'status': 'healthy',
            'role_label': self.role_health_label,
            'detail': 'Loaded and waiting for screenshot regions.',
            'details': {
                'running_status': 'Idle',
                'configured_regions': str(configured_regions),
                'active_tasks': str(active_tasks),
                'visible_overlays': str(overlay_count),
            },
        }

    def _close_overlay(self, node_id: str):
        w = overlay_widgets.pop(node_id, None)
        if w:
            try:
                w.close()
            except Exception:
                pass

    def stop_all(self):
        for t in list(self.tasks.values()):
            try:
                t.cancel()
            except Exception:
                pass
        self.tasks.clear()
        self.running_configs.clear()
        # Close all widgets
        for nid in list(overlay_widgets.keys()):
            self._close_overlay(nid)

    def on_config(self, roles_cfg):
        # Filter only screenshot roles
        screenshot_roles = [r for r in roles_cfg if (r.get('role') == 'screenshot')]

        sanitized_roles = []
        for rcfg in screenshot_roles:
            sanitized = self._normalize_config(rcfg)
            if sanitized:
                sanitized_roles.append(sanitized)

        # Optional: forward interval to SYSTEM helper via hook
        try:
            if sanitized_roles and 'send_service_control' in self.ctx.hooks:
                interval_ms = sanitized_roles[0]['interval']
                try:
                    self.ctx.hooks['send_service_control']({'type': 'screenshot_config', 'interval_ms': interval_ms})
                except Exception:
                    pass
        except Exception:
            pass

        # Cancel tasks that are no longer present
        new_ids = {r.get('node_id') for r in sanitized_roles if r.get('node_id')}
        old_ids = set(self.tasks.keys())
        removed = old_ids - new_ids
        for rid in removed:
            t = self.tasks.pop(rid, None)
            if t:
                try:
                    t.cancel()
                except Exception:
                    pass
            # Remove stored region and overlay
            self.ctx.config.data.get('regions', {}).pop(rid, None)
            try:
                self._close_overlay(rid)
            except Exception:
                pass
            self.running_configs.pop(rid, None)
        if removed:
            try:
                self.ctx.config._write()
            except Exception:
                pass

        # Start tasks for all screenshot roles in config
        for rcfg in sanitized_roles:
            nid = rcfg.get('node_id')
            if not nid:
                continue
            if nid in self.tasks:
                if self.running_configs.get(nid) == rcfg:
                    continue
                prev = self.tasks.pop(nid)
                try:
                    prev.cancel()
                except Exception:
                    pass
            task = asyncio.create_task(self._screenshot_task(rcfg))
            self.tasks[nid] = task
            self.running_configs[nid] = rcfg

    def _normalize_config(self, cfg):
        try:
            nid = cfg.get('node_id')
            if not nid:
                return None
            norm = {
                'node_id': nid,
                'interval': _coerce_int(cfg.get('interval'), 1000, minimum=100),
                'x': _coerce_int(cfg.get('x'), 100),
                'y': _coerce_int(cfg.get('y'), 100),
                'w': _coerce_int(cfg.get('w'), 300, minimum=1),
                'h': _coerce_int(cfg.get('h'), 200, minimum=1),
                'visible': _coerce_bool(cfg.get('visible'), True),
                'alias': _coerce_text(cfg.get('alias')),
                'target_agent_mode': _normalize_mode(cfg.get('target_agent_mode')),
                'target_agent_host': _coerce_text(cfg.get('target_agent_host')),
            }
            return norm
        except Exception:
            return None

    async def _screenshot_task(self, cfg):
        cfg = self._normalize_config(cfg) or {}
        nid = cfg.get('node_id')
        if not nid:
            return

        target_mode = cfg.get('target_agent_mode') or ''
        current_mode = getattr(self.ctx, 'service_mode', '') or ''
        if target_mode and current_mode and target_mode != current_mode:
            return

        alias = cfg.get('alias', '')
        visible = cfg.get('visible', True)
        reg = self.ctx.config.data.setdefault('regions', {})
        stored = reg.get(nid) if isinstance(reg.get(nid), dict) else None
        base_region = (
            cfg.get('x', 100),
            cfg.get('y', 100),
            cfg.get('w', 300),
            cfg.get('h', 200),
        )
        if stored:
            region = (
                _coerce_int(stored.get('x'), base_region[0]),
                _coerce_int(stored.get('y'), base_region[1]),
                _coerce_int(stored.get('w'), base_region[2], minimum=1),
                _coerce_int(stored.get('h'), base_region[3], minimum=1),
            )
        else:
            region = base_region
        reg[nid] = {'x': region[0], 'y': region[1], 'w': region[2], 'h': region[3]}
        try:
            self.ctx.config._write()
        except Exception:
            pass

        widget = overlay_widgets.get(nid)
        if widget is None:
            widget = ScreenshotRegion(self.ctx, nid, *region, alias=alias)
            overlay_widgets[nid] = widget
        else:
            widget.set_region(*region)
            widget.set_alias(alias)
        widget.apply_visibility(visible)

        await self.ctx.sio.emit('agent_screenshot_task', {
            'agent_id': self.ctx.agent_id,
            'node_id': nid,
            'image_base64': '',
            'x': region[0], 'y': region[1], 'w': region[2], 'h': region[3]
        })

        interval = max(cfg.get('interval', 1000), 50) / 1000.0
        loop = asyncio.get_event_loop()
        # Maximum number of screenshot roles you can assign to an agent. (8 already feels overkill)
        executor = concurrent.futures.ThreadPoolExecutor(max_workers=8)

        try:
            while True:
                widget = overlay_widgets.get(nid)
                if widget is None:
                    break
                x, y, w, h = widget.get_geometry()
                grab = partial(ImageGrab.grab, bbox=(x, y, x + w, y + h))
                img = await loop.run_in_executor(executor, grab)
                buf = BytesIO(); img.save(buf, format='PNG')
                encoded = base64.b64encode(buf.getvalue()).decode('utf-8')
                await self.ctx.sio.emit('agent_screenshot_task', {
                    'agent_id': self.ctx.agent_id,
                    'node_id': nid,
                    'image_base64': encoded,
                    'x': x, 'y': y, 'w': w, 'h': h
                })
                await asyncio.sleep(interval)
        except asyncio.CancelledError:
            pass
        except Exception:
            traceback.print_exc()
        finally:
            try:
                executor.shutdown(wait=False)
            except Exception:
                pass
