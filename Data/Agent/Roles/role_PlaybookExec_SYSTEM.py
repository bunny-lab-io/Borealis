import os
import sys
import asyncio
import tempfile
import uuid
import time
import json
import socket
import subprocess

try:
    import winrm  # type: ignore
except Exception:
    winrm = None


ROLE_NAME = 'playbook_exec_system'
ROLE_CONTEXTS = ['system']


def _project_root():
    try:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))
    except Exception:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), '..'))


def _agent_root():
    # Resolve Agent root at runtime.
    # Typical runtime: <ProjectRoot>/Agent/Borealis/Roles/<this_file>
    try:
        here = os.path.abspath(os.path.dirname(__file__))
        # Agent/Borealis/Roles -> Agent
        return os.path.abspath(os.path.join(here, '..', '..', '..'))
    except Exception:
        return os.path.abspath(os.path.join(_project_root(), 'Agent'))


def _scripts_bin():
    # Return the venv Scripts (Windows) or bin (POSIX) path adjacent to Borealis
    agent_root = _agent_root()
    candidates = [
        os.path.join(agent_root, 'Scripts'),  # Windows venv
        os.path.join(agent_root, 'bin'),      # POSIX venv
    ]
    for base in candidates:
        if os.path.isdir(base):
            return base
    return None


def _ansible_playbook_cmd():
    exe = 'ansible-playbook.exe' if os.name == 'nt' else 'ansible-playbook'
    sdir = _scripts_bin()
    if sdir:
        cand = os.path.join(sdir, exe)
        if os.path.isfile(cand):
            return cand
    return exe

def _ansible_galaxy_cmd():
    exe = 'ansible-galaxy.exe' if os.name == 'nt' else 'ansible-galaxy'
    sdir = _scripts_bin()
    if sdir:
        cand = os.path.join(sdir, exe)
        if os.path.isfile(cand):
            return cand
    return exe

def _collections_dir():
    base = os.path.join(_project_root(), 'Agent', 'Borealis', 'AnsibleCollections')
    try:
        os.makedirs(base, exist_ok=True)
    except Exception:
        pass
    return base

def _venv_python():
    try:
        sdir = _scripts_bin()
        if not sdir:
            return None
        cand = os.path.join(sdir, 'python.exe' if os.name == 'nt' else 'python3')
        return cand if os.path.isfile(cand) else None
    except Exception:
        return None


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        self._runs = {}  # run_id -> { proc, task, cancel }
        self._svc_creds = None  # cache per-process: {username, password}

    def _log_local(self, msg: str, error: bool = False):
        try:
            base = os.path.join(_project_root(), 'Logs', 'Agent')
            os.makedirs(base, exist_ok=True)
            fn = 'agent.error.log' if error else 'agent.log'
            ts = time.strftime('%Y-%m-%d %H:%M:%S')
            with open(os.path.join(base, fn), 'a', encoding='utf-8') as fh:
                fh.write(f'[{ts}] [PlaybookExec] {msg}\n')
        except Exception:
            pass

    def _server_base(self) -> str:
        try:
            fn = (self.ctx.hooks or {}).get('get_server_url')
            if callable(fn):
                return (fn() or 'http://localhost:5000').rstrip('/')
        except Exception:
            pass
        return 'http://localhost:5000'

    async def _fetch_service_creds(self) -> dict:
        if self._svc_creds and isinstance(self._svc_creds, dict):
            return self._svc_creds
        try:
            import aiohttp
            url = self._server_base().rstrip('/') + '/api/agent/checkin'
            payload = {
                'agent_id': self.ctx.agent_id,
                'hostname': socket.gethostname(),
                'username': '.\\svcBorealisAnsibleRunner',
            }
            timeout = aiohttp.ClientTimeout(total=15)
            async with aiohttp.ClientSession(timeout=timeout) as sess:
                async with sess.post(url, json=payload) as resp:
                    js = await resp.json()
            u = (js or {}).get('username') or '.\\svcBorealisAnsibleRunner'
            p = (js or {}).get('password') or ''
            self._svc_creds = {'username': u, 'password': p}
            return self._svc_creds
        except Exception:
            return {'username': '.\\svcBorealisAnsibleRunner', 'password': ''}

    def _normalize_playbook_content(self, content: str) -> str:
        try:
            # Heuristic fixes to honor our WinRM localhost inventory:
            # - Replace hosts: localhost -> hosts: local (group name used by inventory)
            # - Remove explicit "connection: local" if present
            lines = (content or '').splitlines()
            out = []
            for ln in lines:
                s = ln.strip().lower()
                if s.startswith('connection:') and 'local' in s:
                    continue
                if s.startswith('hosts:') and ('localhost' in s or '127.0.0.1' in s):
                    indent = ln.split('h')[0]
                    out.append(f"{indent}hosts: local")
                    continue
                out.append(ln)
            return '\n'.join(out) + ('\n' if not content.endswith('\n') else '')
        except Exception:
            return content

    async def _rotate_service_creds(self) -> dict:
        try:
            import aiohttp
            url = self._server_base().rstrip('/') + '/api/agent/service-account/rotate'
            payload = {
                'agent_id': self.ctx.agent_id,
                'reason': 'bad_credentials',
            }
            timeout = aiohttp.ClientTimeout(total=15)
            async with aiohttp.ClientSession(timeout=timeout) as sess:
                async with sess.post(url, json=payload) as resp:
                    js = await resp.json()
            u = (js or {}).get('username') or '.\\svcBorealisAnsibleRunner'
            p = (js or {}).get('password') or ''
            self._svc_creds = {'username': u, 'password': p}
            return self._svc_creds
        except Exception:
            return await self._fetch_service_creds()

    def _ps_module_path(self) -> str:
        # Place PS module under Roles so it's deployed with the agent
        try:
            here = os.path.abspath(os.path.dirname(__file__))
            p = os.path.join(here, 'Borealis.WinRM.Localhost.psm1')
            return p
        except Exception:
            return ''

    def _ensure_winrm_and_user(self, username: str, password: str):
        if os.name != 'nt':
            return
        mod = self._ps_module_path()
        if not os.path.isfile(mod):
            # best effort with inline commands
            try:
                subprocess.run(['powershell', '-NoProfile', '-Command', 'Set-Service WinRM -StartupType Automatic; Start-Service WinRM'], timeout=30)
            except Exception:
                pass
            return
        ps = f"""
Import-Module -Name '{mod}' -Force
Ensure-LocalhostWinRMHttps
Ensure-BorealisServiceUser -UserName '{username}' -PlaintextPassword '{password}'
"""
        try:
            subprocess.run(['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', ps], timeout=90)
        except Exception:
            pass

    def _write_winrm_inventory(self, base_dir: str, username: str, password: str) -> str:
        inv_dir = os.path.join(base_dir, 'inventory')
        os.makedirs(inv_dir, exist_ok=True)
        hosts = os.path.join(inv_dir, 'hosts')
        try:
            content = (
                "[local]\n" \
                "localhost\n\n" \
                "[local:vars]\n" \
                "ansible_connection=winrm\n" \
                "ansible_host=127.0.0.1\n" \
                "ansible_port=5986\n" \
                "ansible_winrm_scheme=https\n" \
                "ansible_winrm_transport=ntlm\n" \
                f"ansible_user={username}\n" \
                f"ansible_password={password}\n" \
                "ansible_winrm_server_cert_validation=ignore\n"
            )
            with open(hosts, 'w', encoding='utf-8', newline='\n') as fh:
                fh.write(content)
        except Exception:
            pass
        return hosts

    def _winrm_preflight(self, username: str, password: str) -> bool:
        if os.name != 'nt' or winrm is None:
            return True
        try:
            s = winrm.Session('https://127.0.0.1:5986', auth=(username, password), transport='ntlm', server_cert_validation='ignore')
            r = s.run_cmd('whoami')
            return r.status_code == 0
        except Exception:
            return False

    async def _post_recap(self, payload: dict):
        try:
            import aiohttp
            url = self._server_base().rstrip('/') + '/api/ansible/recap/report'
            timeout = aiohttp.ClientTimeout(total=30)
            async with aiohttp.ClientSession(timeout=timeout) as sess:
                async with sess.post(url, json=payload) as resp:
                    # best-effort; ignore body
                    await resp.read()
            self._log_local(f"Posted recap: run_id={payload.get('run_id')} status={payload.get('status')} bytes={len((payload.get('recap_text') or '').encode('utf-8'))}")
        except Exception:
            self._log_local(f"Failed to post recap for run_id={payload.get('run_id')}", error=True)

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
            fh.write(self._normalize_playbook_content(playbook_content or ''))
        # WinRM service account credentials
        creds = await self._fetch_service_creds()
        user = creds.get('username') or '.\\svcBorealisAnsibleRunner'
        pwd = creds.get('password') or ''
        # Converge endpoint state (listener + user)
        self._ensure_winrm_and_user(user, pwd)
        # Preflight auth and auto-rotate if needed
        pre_ok = self._winrm_preflight(user, pwd)
        if not pre_ok:
            # rotate and retry once
            creds = await self._rotate_service_creds()
            user = creds.get('username') or user
            pwd = creds.get('password') or ''
            self._ensure_winrm_and_user(user, pwd)
        # Write inventory for winrm localhost
        inv_file = self._write_winrm_inventory(pd, user, pwd)

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

        auth_failed = False
        try:
            r = ansible_runner.interface.run(
                private_data_dir=pd,
                playbook=play_rel,
                inventory=inv_file,
                quiet=True,
                event_handler=_on_event,
                cancel_callback=_cancel_cb,
                extravars={}
            )
            status = 'Cancelled' if _cancel_cb() else 'Success'
            try:
                # Some auth failures bubble up in events only; inspect last few lines
                tail = '\n'.join(lines[-50:]).lower()
                if ('access is denied' in tail) or ('unauthorized' in tail) or ('cannot process the request' in tail):
                    auth_failed = True
            except Exception:
                pass
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
        # If authentication failed on first pass, rotate password and try once more
        if auth_failed:
            try:
                newc = await self._rotate_service_creds()
                user2 = newc.get('username') or user
                pwd2 = newc.get('password') or ''
                self._ensure_winrm_and_user(user2, pwd2)
                # Recurse once with updated creds
                await self._run_playbook_runner(run_id, playbook_content, playbook_name=playbook_name, activity_job_id=activity_job_id, connection=connection)
                return True
            except Exception:
                pass
        return True

    async def _run_playbook(self, run_id: str, playbook_content: str, playbook_name: str = '', activity_job_id=None, connection: str = 'local'):
        # Write playbook temp
        tmp_dir = os.path.join(_project_root(), 'Temp')
        os.makedirs(tmp_dir, exist_ok=True)
        fd, path = tempfile.mkstemp(prefix='pb_', suffix='.yml', dir=tmp_dir, text=True)
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
            fh.write(self._normalize_playbook_content(playbook_content or ''))

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

        # Prefer WinRM localhost via inventory when on Windows; otherwise fallback to provided connection
        inv_file_cli = None
        conn = (connection or 'local').strip().lower()
        if os.name == 'nt':
            try:
                creds = await self._fetch_service_creds()
                user = creds.get('username') or '.\\svcBorealisAnsibleRunner'
                pwd = creds.get('password') or ''
                self._ensure_winrm_and_user(user, pwd)
                if not self._winrm_preflight(user, pwd):
                    creds = await self._rotate_service_creds()
                    user = creds.get('username') or user
                    pwd = creds.get('password') or ''
                    self._ensure_winrm_and_user(user, pwd)
                # Create temp inventory adjacent to playbook
                inv_file_cli = self._write_winrm_inventory(os.path.dirname(path), user, pwd)
            except Exception:
                inv_file_cli = None
        # Build CLI; if inv_file_cli present, omit -c and use '-i invfile'
        if inv_file_cli and os.path.isfile(inv_file_cli):
            cmd = [_ansible_playbook_cmd(), path, '-i', inv_file_cli]
            self._log_local(f"Launching ansible-playbook with WinRM inventory: {' '.join(cmd)}")
        else:
            if conn not in ('local', 'winrm', 'psrp'):
                conn = 'local'
            cmd = [_ansible_playbook_cmd(), path, '-i', 'localhost,', '-c', conn]
            self._log_local(f"Launching ansible-playbook: conn={conn} cmd={' '.join(cmd)}")
        # Ensure clean, plain output and correct interpreter for localhost
        env = os.environ.copy()
        env.setdefault('ANSIBLE_FORCE_COLOR', '0')
        env.setdefault('ANSIBLE_NOCOLOR', '1')
        env.setdefault('PYTHONIOENCODING', 'utf-8')
        env.setdefault('ANSIBLE_STDOUT_CALLBACK', 'default')
        # Help Ansible pick the correct python for localhost
        env.setdefault('ANSIBLE_LOCALHOST_WARNING', '0')
        # Ensure collections path is discoverable
        env.setdefault('ANSIBLE_COLLECTIONS_PATHS', _collections_dir())
        vp = _venv_python()
        if vp:
            env.setdefault('ANSIBLE_PYTHON_INTERPRETER', vp)

        creationflags = 0
        if os.name == 'nt':
            # CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
            creationflags = 0x00000200 | 0x08000000

        proc = None
        try:
            # Best-effort collection install for windows modules
            try:
                if 'ansible.windows' in (playbook_content or ''):
                    galaxy = _ansible_galaxy_cmd()
                    coll_dir = _collections_dir()
                    creation = 0x08000000 if os.name == 'nt' else 0
                    self._log_local("Ensuring ansible.windows collection is installed for this agent")
                    subprocess.run([galaxy, 'collection', 'install', 'ansible.windows', '-p', coll_dir], timeout=120, creationflags=creation)
            except Exception:
                self._log_local("Collection install failed (continuing)")

            # Prefer ansible-runner when available (default on). Set BOREALIS_USE_ANSIBLE_RUNNER=0 to disable.
            try:
                if os.environ.get('BOREALIS_USE_ANSIBLE_RUNNER', '1').lower() not in ('0', 'false', 'no'):
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
            self._log_local(f"Failed to launch ansible-playbook: {e}", error=True)
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
        self._log_local(f"ansible-playbook finished rc={rc}")
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
