import os
import asyncio
import importlib.util


ROLE_NAME = 'macro'
ROLE_CONTEXTS = ['interactive']


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
            def send_keypress_to_window(self, handle, key):
                return False, 'unavailable'
            def type_text_to_window(self, handle, text):
                return False, 'unavailable'
        return _Dummy()


macro_engines = _load_macro_engines()


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        self.tasks = {}

    def stop_all(self):
        for t in list(self.tasks.values()):
            try:
                t.cancel()
            except Exception:
                pass
        self.tasks.clear()

    def on_config(self, roles_cfg):
        macro_roles = [r for r in roles_cfg if (r.get('role') == 'macro')]
        new_ids = {r.get('node_id') for r in macro_roles if r.get('node_id')}
        old_ids = set(self.tasks.keys())
        removed = old_ids - new_ids
        for rid in removed:
            t = self.tasks.pop(rid, None)
            if t:
                try:
                    t.cancel()
                except Exception:
                    pass
        for rcfg in macro_roles:
            nid = rcfg.get('node_id')
            if nid and nid not in self.tasks:
                self.tasks[nid] = asyncio.create_task(self._macro_task(rcfg))

    async def _macro_task(self, cfg):
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
                try:
                    await self.ctx.sio.emit('macro_status', {
                        'agent_id': self.ctx.agent_id,
                        'node_id': nid,
                        'success': success,
                        'message': message,
                    })
                except Exception:
                    pass

            if not (active is True or str(active).lower() == 'true'):
                await asyncio.sleep(0.2)
                continue
            try:
                send_macro = False
                if operation_mode == 'Run Once':
                    if not has_run_once:
                        send_macro = True
                        has_run_once = True
                elif operation_mode == 'Continuous':
                    send_macro = True
                elif operation_mode == 'Trigger-Continuous':
                    send_macro = (trigger == 1)
                elif operation_mode == 'Trigger-Once':
                    if trigger == 1 and last_trigger_value != 1:
                        send_macro = True
                else:
                    send_macro = False

                if send_macro:
                    ok = False
                    if macro_type == 'keypress' and key:
                        ok = bool(macro_engines.send_keypress_to_window(window_handle, key))
                    elif macro_type == 'text' and text:
                        ok = bool(macro_engines.type_text_to_window(window_handle, text))
                    await emit_macro_status(ok, 'sent' if ok else 'failed')
                last_trigger_value = trigger
            except Exception as e:
                await emit_macro_status(False, str(e))
            # interval wait
            await asyncio.sleep(max(0.05, (interval_ms or 1000) / 1000.0))

