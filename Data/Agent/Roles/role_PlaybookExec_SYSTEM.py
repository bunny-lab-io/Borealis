import os
import sys
import asyncio
import tempfile
import uuid
import time
import json
import socket
import subprocess


ROLE_NAME = 'playbook_exec_system'
ROLE_CONTEXTS = ['system']


def _project_root():
    try:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))
    except Exception:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), '..'))


def _scripts_bin():
    # Return the venv Scripts (Windows) or bin (POSIX) path
    base = os.path.join(_project_root(), 'Agent', 'Scripts')
    if os.path.isdir(base):
        return base
    # Fallback to PATH
    return None


def _ansible_playbook_cmd():
    exe = 'ansible-playbook.exe' if os.name == 'nt' else 'ansible-playbook'
    sdir = _scripts_bin()
    if sdir:
        cand = os.path.join(sdir, exe)
        if os.path.isfile(cand):
            return cand
    return exe


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        self._runs = {}  # run_id -> { proc, task, cancel }

    def _server_base(self) -> str:
        try:
            fn = (self.ctx.hooks or {}).get('get_server_url')
            if callable(fn):
                return (fn() or 'http://localhost:5000').rstrip('/')
        except Exception:
            pass
        return 'http://localhost:5000'

    async def _post_recap(self, payload: dict):
        try:
            import aiohttp
            url = self._server_base().rstrip('/') + '/api/ansible/recap/report'
            timeout = aiohttp.ClientTimeout(total=30)
            async with aiohttp.ClientSession(timeout=timeout) as sess:
                async with sess.post(url, json=payload) as resp:
                    # best-effort; ignore body
                    await resp.read()
        except Exception:
            pass

    async def _run_playbook_runner(self, run_id: str, playbook_content: str, playbook_name: str = '', activity_job_id=None, connection: str = 'local'):
        try:
            import ansible_runner  # type: ignore
        except Exception:
            return False

        tmp_dir = os.path.join(_project_root(), 'Temp')
        os.makedirs(tmp_dir, exist_ok=True)
        pd = tempfile.mkdtemp(prefix='ar_', dir=tmp_dir)
        project = os.path.join(pd, 'project')
        inventory_dir = os.path.join(pd, 'inventory')
        env_dir = os.path.join(pd, 'env')
        os.makedirs(project, exist_ok=True)
        os.makedirs(inventory_dir, exist_ok=True)
        os.makedirs(env_dir, exist_ok=True)

        play_rel = 'playbook.yml'
        play_abs = os.path.join(project, play_rel)
        with open(play_abs, 'w', encoding='utf-8', newline='\n') as fh:
            fh.write(playbook_content or '')
        with open(os.path.join(inventory_dir, 'hosts'), 'w', encoding='utf-8', newline='\n') as fh:
            fh.write('localhost,\n')
        # Set connection via envvars
        with open(os.path.join(env_dir, 'envvars'), 'w', encoding='utf-8', newline='\n') as fh:
            json.dump({ 'ANSIBLE_FORCE_COLOR': '0', 'ANSIBLE_STDOUT_CALLBACK': 'default' }, fh)

        hostname = socket.gethostname()
        agent_id = self.ctx.agent_id
        started = int(time.time())
        await self._post_recap({
            'run_id': run_id,
            'hostname': hostname,
            'agent_id': agent_id,
            'playbook_path': play_abs,
            'playbook_name': playbook_name or os.path.basename(play_abs),
            'activity_job_id': activity_job_id,
            'status': 'Running',
            'started_ts': started,
        })

        lines = []
        recap_json = None

        def _on_event(ev):
            nonlocal lines, recap_json
            try:
                if not isinstance(ev, dict):
                    return
                # Capture minimal textual progress
                tx = ev.get('stdout') or ''
                if tx:
                    lines.append(str(tx))
                    if len(lines) > 5000:
                        lines = lines[-2500:]
                # Capture final stats
                if (ev.get('event') or '') == 'playbook_on_stats':
                    d = ev.get('event_data') or {}
                    # ansible-runner provides per-host stats under 'res'
                    recap_json = d.get('res') or d.get('stats') or d
            except Exception:
                pass

        cancel_token = self._runs.get(run_id)
        def _cancel_cb():
            try:
                return bool(cancel_token and cancel_token.get('cancel'))
            except Exception:
                return False

        try:
            ansible_runner.interface.run(
                private_data_dir=pd,
                playbook=play_rel,
                inventory=os.path.join(inventory_dir, 'hosts'),
                quiet=True,
                event_handler=_on_event,
                cancel_callback=_cancel_cb,
                extravars={}
            )
            status = 'Cancelled' if _cancel_cb() else 'Success'
        except Exception:
            status = 'Failed'

        # Synthesize recap text from recap_json if available
        recap_text = ''
        try:
            if isinstance(recap_json, dict):
                # Expect a single host 'localhost'
                stats = recap_json.get('localhost') or recap_json
                ok = int(stats.get('ok') or 0)
                changed = int(stats.get('changed') or 0)
                unreachable = int(stats.get('unreachable') or 0)
                failed = int(stats.get('failures') or stats.get('failed') or 0)
                skipped = int(stats.get('skipped') or 0)
                rescued = int(stats.get('rescued') or 0)
                ignored = int(stats.get('ignored') or 0)
                recap_text = (
                    'PLAY RECAP *********************************************************************\n'
                    f"localhost : ok={ok} changed={changed} unreachable={unreachable} failed={failed} skipped={skipped} rescued={rescued} ignored={ignored}"
                )
        except Exception:
            recap_text = ''

        await self._post_recap({
            'run_id': run_id,
            'hostname': hostname,
            'agent_id': agent_id,
            'status': status,
            'recap_text': recap_text,
            'recap_json': recap_json,
            'finished_ts': int(time.time()),
        })
        return True

    async def _run_playbook(self, run_id: str, playbook_content: str, playbook_name: str = '', activity_job_id=None, connection: str = 'local'):
        # Write playbook temp
        tmp_dir = os.path.join(_project_root(), 'Temp')
        os.makedirs(tmp_dir, exist_ok=True)
        fd, path = tempfile.mkstemp(prefix='pb_', suffix='.yml', dir=tmp_dir, text=True)
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
            fh.write(playbook_content or '')

        hostname = socket.gethostname()
        agent_id = self.ctx.agent_id

        started = int(time.time())
        await self._post_recap({
            'run_id': run_id,
            'hostname': hostname,
            'agent_id': agent_id,
            'playbook_path': path,
            'playbook_name': playbook_name or os.path.basename(path),
            'activity_job_id': activity_job_id,
            'status': 'Running',
            'started_ts': started,
        })

        conn = (connection or 'local').strip().lower()
        if conn not in ('local', 'winrm', 'psrp'):
            conn = 'local'
        cmd = [_ansible_playbook_cmd(), path, '-i', 'localhost,', '-c', conn]
        # Ensure clean, plain output and correct interpreter for localhost
        env = os.environ.copy()
        env.setdefault('ANSIBLE_FORCE_COLOR', '0')
        env.setdefault('ANSIBLE_NOCOLOR', '1')
        env.setdefault('PYTHONIOENCODING', 'utf-8')
        env.setdefault('ANSIBLE_STDOUT_CALLBACK', 'default')
        # Help Ansible pick the correct python for localhost
        env.setdefault('ANSIBLE_LOCALHOST_WARNING', '0')

        creationflags = 0
        if os.name == 'nt':
            # CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
            creationflags = 0x00000200 | 0x08000000

        proc = None
        try:
            # Prefer ansible-runner when available and enabled
            try:
                if os.environ.get('BOREALIS_USE_ANSIBLE_RUNNER', '0').lower() not in ('0', 'false', 'no'):
                    used = await self._run_playbook_runner(run_id, playbook_content, playbook_name=playbook_name, activity_job_id=activity_job_id, connection=connection)
                    if used:
                        return
            except Exception:
                pass

            proc = await asyncio.create_subprocess_exec(
                *cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.STDOUT,
                cwd=os.path.dirname(path),
                env=env,
                creationflags=creationflags,
            )
        except Exception as e:
            await self._post_recap({
                'run_id': run_id,
                'hostname': hostname,
                'agent_id': agent_id,
                'status': 'Failed',
                'recap_text': f'Failed to launch ansible-playbook: {e}',
                'finished_ts': int(time.time()),
            })
            try:
                os.remove(path)
            except Exception:
                pass
            return

        # Track run for cancellation
        self._runs[run_id]['proc'] = proc

        lines = []
        recap_buffer = []
        seen_recap = False
        last_emit = 0

        async def emit_update(force=False):
            nonlocal last_emit
            now = time.time()
            if not force and (now - last_emit) < 1.0:
                return
            last_emit = now
            txt = '\n'.join(recap_buffer[-80:]) if recap_buffer else ''
            if not txt and lines:
                # show tail while running
                txt = '\n'.join(lines[-25:])
            if txt:
                await self._post_recap({
                    'run_id': run_id,
                    'hostname': hostname,
                    'agent_id': agent_id,
                    'recap_text': txt,
                    'status': 'Running',
                })

        try:
            # Read combined stdout
            while True:
                if proc.stdout is None:
                    break
                bs = await proc.stdout.readline()
                if not bs:
                    break
                try:
                    line = bs.decode('utf-8', errors='replace').rstrip('\r\n')
                except Exception:
                    line = str(bs)
                lines.append(line)
                if len(lines) > 5000:
                    lines = lines[-2500:]
                # Detect recap section
                if not seen_recap and line.strip().upper().startswith('PLAY RECAP'):
                    seen_recap = True
                    recap_buffer.append(line)
                elif seen_recap:
                    recap_buffer.append(line)
                await emit_update(False)
        finally:
            try:
                await proc.wait()
            except Exception:
                pass

        rc = proc.returncode if proc else -1
        status = 'Success' if rc == 0 else ('Cancelled' if self._runs.get(run_id, {}).get('cancel') else 'Failed')

        # Final recap text
        final_txt = '\n'.join(recap_buffer[-120:]) if recap_buffer else ('\n'.join(lines[-60:]) if lines else '')
        await self._post_recap({
            'run_id': run_id,
            'hostname': hostname,
            'agent_id': agent_id,
            'status': status,
            'recap_text': final_txt,
            'finished_ts': int(time.time()),
        })

        # Cleanup
        try:
            os.remove(path)
        except Exception:
            pass
        self._runs.pop(run_id, None)

    def register_events(self):
        sio = self.ctx.sio

        @sio.on('ansible_playbook_run')
        async def _on_ansible_playbook_run(payload):
            try:
                hostname = socket.gethostname()
                target = (payload.get('target_hostname') or '').strip().lower()
                if target and target != hostname.lower():
                    return
                # Accept provided run_id or generate one
                run_id = (payload.get('run_id') or '').strip() or uuid.uuid4().hex
                content = payload.get('playbook_content') or ''
                p_name = payload.get('playbook_name') or ''
                act_id = payload.get('activity_job_id')
                sched_job_id = payload.get('scheduled_job_id')
                sched_run_id = payload.get('scheduled_run_id')
                conn = (payload.get('connection') or 'local')
                # Track run
                self._runs[run_id] = {'cancel': False, 'proc': None}
                # Include scheduled ids on first recap post
                async def run_and_tag():
                    # First recap (Running) will include activity_job_id and scheduled ids
                    # by temporarily monkey patching _post_recap for initial call only
                    first = {'done': False}
                    orig = self._post_recap
                    async def _wrapped(payload2: dict):
                        if not first['done']:
                            if sched_job_id is not None:
                                payload2['scheduled_job_id'] = sched_job_id
                            if sched_run_id is not None:
                                payload2['scheduled_run_id'] = sched_run_id
                            first['done'] = True
                        await orig(payload2)
                    self._post_recap = _wrapped
                    try:
                        await self._run_playbook(run_id, content, playbook_name=p_name, activity_job_id=act_id, connection=conn)
                    finally:
                        self._post_recap = orig
                asyncio.create_task(run_and_tag())
            except Exception:
                pass

        @sio.on('ansible_playbook_cancel')
        async def _on_ansible_playbook_cancel(payload):
            try:
                run_id = (payload.get('run_id') or '').strip()
                if not run_id:
                    return
                obj = self._runs.get(run_id)
                if not obj:
                    return
                obj['cancel'] = True
                proc = obj.get('proc')
                if proc and proc.returncode is None:
                    try:
                        if os.name == 'nt':
                            proc.terminate()
                            await asyncio.sleep(0.5)
                            if proc.returncode is None:
                                proc.kill()
                        else:
                            proc.terminate()
                            await asyncio.sleep(0.5)
                            if proc.returncode is None:
                                proc.kill()
                    except Exception:
                        pass
            except Exception:
                pass

        @sio.on('quick_job_run')
        async def _compat_quick_job_run(payload):
            """Compatibility: allow scheduled jobs to dispatch .yml as ansible playbooks.
            Expects payload fields similar to script quick runs but with script_type='ansible'.
            """
            try:
                stype = (payload.get('script_type') or '').lower()
                if stype != 'ansible':
                    return
                hostname = socket.gethostname()
                target = (payload.get('target_hostname') or '').strip().lower()
                if target and target != hostname.lower():
                    return
                run_id = uuid.uuid4().hex
                content = payload.get('script_content') or ''
                p_name = payload.get('script_name') or ''
                self._runs[run_id] = {'cancel': False, 'proc': None}
                asyncio.create_task(self._run_playbook(run_id, content, playbook_name=p_name, activity_job_id=payload.get('job_id'), connection='local'))
            except Exception:
                pass

    def on_config(self, roles):
        # No scheduled tasks to manage for now
        return

    def stop_all(self):
        # Attempt to cancel any running playbooks
        for rid, obj in list(self._runs.items()):
            try:
                obj['cancel'] = True
                p = obj.get('proc')
                if p and p.returncode is None:
                    try:
                        if os.name == 'nt':
                            p.terminate()
                            time.sleep(0.2)
                            if p.returncode is None:
                                p.kill()
                        else:
                            p.terminate()
                    except Exception:
                        pass
            except Exception:
                pass
        self._runs.clear()
