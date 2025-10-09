import os
import sys
import asyncio
import tempfile
import uuid
import time
import json
import socket
import subprocess
import base64
from typing import Optional

try:
    import winrm  # type: ignore
except Exception:
    winrm = None

DEFAULT_SERVICE_ACCOUNT = '.\\svcBorealis'
LEGACY_SERVICE_ACCOUNTS = {'.\\svcBorealisAnsibleRunner', 'svcBorealisAnsibleRunner'}

ROLE_NAME = 'playbook_exec_system'
ROLE_CONTEXTS = ['system']

REQUIRED_MODULES = {
    'ansible': 'ansible-core>=2.15,<2.17',
    'ansible_runner': 'ansible-runner>=2.3',
    'winrm': 'pywinrm>=0.4.3',
    'requests_ntlm': 'requests-ntlm>=1.2.0',
    'requests_credssp': 'requests-credssp>=2.0',
    'pypsrp': 'pypsrp>=0.8.1',
}

WINRM_USERNAME_VAR = '__borealis_winrm_username'
WINRM_PASSWORD_VAR = '__borealis_winrm_password'
WINRM_TRANSPORT_VAR = '__borealis_winrm_transport'


def _project_root():
    try:
        cur = os.path.abspath(os.path.dirname(__file__))
        for _ in range(8):
            if (
                os.path.exists(os.path.join(cur, 'Borealis.ps1'))
                or os.path.isdir(os.path.join(cur, '.git'))
            ):
                return cur
            parent = os.path.dirname(cur)
            if parent == cur:
                break
            cur = parent
        return os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))
    except Exception:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), '..'))


def _decode_base64_text(value):
    if not isinstance(value, str):
        return None
    stripped = value.strip()
    if not stripped:
        return ""
    cleaned = ''.join(stripped.split())
    if not cleaned:
        return ""
    try:
        decoded = base64.b64decode(cleaned, validate=True)
    except Exception:
        return None
    try:
        return decoded.decode('utf-8')
    except Exception:
        return decoded.decode('utf-8', errors='replace')


def _decode_playbook_content(raw_content, encoding_hint):
    if isinstance(raw_content, str):
        encoding = str(encoding_hint or '').strip().lower()
        if encoding in ('base64', 'b64', 'base-64'):
            decoded = _decode_base64_text(raw_content)
            if decoded is not None:
                return decoded
        decoded = _decode_base64_text(raw_content)
        if decoded is not None:
            return decoded
        return raw_content
    return ''


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
        self._ansible_ready = False
        self._ansible_bootstrap_lock = None
        try:
            base = os.path.join(_project_root(), 'Logs', 'Agent')
            os.makedirs(base, exist_ok=True)
            self._ansible_log(f"[init] PlaybookExec role init agent_id={ctx.agent_id}")
        except Exception:
            pass

    def _bootstrap_marker_path(self) -> str:
        try:
            state_dir = os.path.join(_project_root(), 'Agent', 'Borealis', 'State')
            os.makedirs(state_dir, exist_ok=True)
            return os.path.join(state_dir, 'ansible_bootstrap.json')
        except Exception:
            tmp_dir = os.path.join(_project_root(), 'Temp')
            try:
                os.makedirs(tmp_dir, exist_ok=True)
            except Exception:
                pass
            return os.path.join(tmp_dir, 'ansible_bootstrap.json')

    def _detect_missing_modules(self) -> dict:
        missing = {}
        for module, spec in REQUIRED_MODULES.items():
            try:
                __import__(module)
            except Exception:
                missing[module] = spec
        return missing

    def _bootstrap_ansible_sync(self) -> bool:
        missing = self._detect_missing_modules()
        if not missing:
            return True
        specs = sorted({spec for spec in missing.values() if spec})
        python_exe = _venv_python() or sys.executable
        if not python_exe:
            self._ansible_log('[bootstrap] python executable not found for pip install', error=True)
            return False
        cmd = [python_exe, '-m', 'pip', 'install', '--disable-pip-version-check'] + specs
        self._ansible_log(f"[bootstrap] ensuring modules via pip: {', '.join(specs)}")
        try:
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=900)
        except Exception as exc:
            self._ansible_log(f"[bootstrap] pip install exception: {exc}", error=True)
            return False
        if result.returncode != 0:
            err_tail = (result.stderr or '').strip()
            if len(err_tail) > 500:
                err_tail = err_tail[-500:]
            self._ansible_log(f"[bootstrap] pip install failed rc={result.returncode} err={err_tail}", error=True)
            return False
        remaining = self._detect_missing_modules()
        if remaining:
            self._ansible_log(f"[bootstrap] modules still missing after install: {', '.join(sorted(remaining.keys()))}", error=True)
            return False
        try:
            marker = self._bootstrap_marker_path()
            payload = {
                'timestamp': int(time.time()),
                'modules': specs,
            }
            with open(marker, 'w', encoding='utf-8') as fh:
                json.dump(payload, fh)
        except Exception:
            pass
        return True

    async def _ensure_ansible_ready(self) -> bool:
        if getattr(self, '_ansible_ready', False):
            return True
        lock = getattr(self, '_ansible_bootstrap_lock', None)
        if lock is None:
            lock = asyncio.Lock()
            self._ansible_bootstrap_lock = lock
        async with lock:
            if getattr(self, '_ansible_ready', False):
                return True
            loop = asyncio.get_running_loop()
            success = await loop.run_in_executor(None, self._bootstrap_ansible_sync)
            self._ansible_ready = bool(success)
            if success:
                self._ansible_log('[bootstrap] ansible dependencies ready')
            else:
                self._ansible_log('[bootstrap] unable to prepare ansible dependencies', error=True)
            return success

    def _stage_payload_files(self, base_dir: str, files) -> list:
        staged = []
        if not base_dir or not isinstance(files, list):
            return staged
        root = os.path.abspath(base_dir)
        for idx, entry in enumerate(files):
            if not isinstance(entry, dict):
                continue
            raw_name = entry.get('name') or entry.get('path') or f'payload_{idx}'
            name = str(raw_name or '').replace('\\', '/').strip()
            if not name:
                continue
            while name.startswith('/'):
                name = name[1:]
            if not name or '..' in name.split('/'):
                continue
            dest = os.path.abspath(os.path.join(root, name))
            if not dest.startswith(root):
                continue
            try:
                os.makedirs(os.path.dirname(dest), exist_ok=True)
            except Exception:
                pass
            content = entry.get('content')
            if content is None:
                content = entry.get('data')
            if content is None:
                content = entry.get('blob')
            encoding = str(entry.get('encoding') or '').lower()
            is_binary = bool(entry.get('binary'))
            try:
                if encoding in ('base64', 'b64', 'base-64'):
                    raw = ''
                    if isinstance(content, str):
                        raw = ''.join(content.split())
                    data = base64.b64decode(raw or '', validate=True)
                    with open(dest, 'wb') as fh:
                        fh.write(data)
                elif is_binary:
                    if isinstance(content, bytes):
                        data = content
                    elif isinstance(content, str):
                        data = content.encode('utf-8')
                    else:
                        data = b''
                    with open(dest, 'wb') as fh:
                        fh.write(data)
                else:
                    text = content if isinstance(content, str) else ''
                    with open(dest, 'w', encoding='utf-8', newline='\n') as fh:
                        fh.write(text)
                staged.append(dest)
            except Exception as exc:
                self._ansible_log(f"[files] failed to stage '{name}': {exc}", error=True)
        return staged

    def _coerce_variable_value(self, var_type: str, value):
        typ = str(var_type or 'string').lower()
        if typ == 'boolean':
            if isinstance(value, bool):
                return value
            if isinstance(value, (int, float)):
                return value != 0
            if value is None:
                return False
            s = str(value).strip().lower()
            if s in {'true', '1', 'yes', 'on'}:
                return True
            if s in {'false', '0', 'no', 'off'}:
                return False
            return bool(s)
        if typ == 'number':
            if value is None or value == '':
                return ''
            try:
                if isinstance(value, (int, float)):
                    return value
                s = str(value)
                return int(s) if s.isdigit() else float(s)
            except Exception:
                return ''
        return '' if value is None else str(value)

    def _resolve_extra_vars(self, definitions, overrides: dict):
        extra_vars = {}
        meta = {}
        doc_names = set()
        defs = definitions if isinstance(definitions, list) else []
        ovs = {}
        if isinstance(overrides, dict):
            for key, val in overrides.items():
                name = str(key or '').strip()
                if name:
                    ovs[name] = val
        for var in defs:
            if not isinstance(var, dict):
                continue
            name = str(var.get('name') or '').strip()
            if not name:
                continue
            doc_names.add(name)
            var_type = str(var.get('type') or 'string').lower()
            default_val = ''
            for key in ('value', 'default', 'defaultValue', 'default_value'):
                if key in var:
                    default_val = var.get(key)
                    break
            if name in ovs:
                val = ovs[name]
            else:
                val = default_val
            extra_vars[name] = self._coerce_variable_value(var_type, val)
            meta[name] = {
                'type': var_type,
                'sensitive': (var_type == 'credential') or ('password' in name.lower()),
            }
        for name, val in ovs.items():
            if name in doc_names:
                continue
            extra_vars[name] = val
            meta[name] = {
                'type': 'string',
                'sensitive': ('password' in name.lower()),
            }
        return extra_vars, meta

    def _format_var_summary(self, meta: dict) -> str:
        if not isinstance(meta, dict) or not meta:
            return ''
        parts = []
        for name in sorted(meta.keys()):
            info = meta.get(name) or {}
            sensitive = bool(info.get('sensitive'))
            parts.append(f"{name}=<secret>" if sensitive else f"{name}=set")
        return ', '.join(parts)

    def _build_execution_context(self, variables, variable_values):
        overrides = {}
        if isinstance(variable_values, dict):
            for key, val in variable_values.items():
                name = str(key or '').strip()
                if name:
                    overrides[name] = val
        extra_vars, meta = self._resolve_extra_vars(variables if isinstance(variables, list) else [], overrides)
        # Remove sentinel metadata if they exist
        for sentinel in (WINRM_USERNAME_VAR, WINRM_PASSWORD_VAR, WINRM_TRANSPORT_VAR):
            meta.pop(sentinel, None)
        conn_user = overrides.get(WINRM_USERNAME_VAR)
        conn_pass = overrides.get(WINRM_PASSWORD_VAR)
        conn_transport = overrides.get(WINRM_TRANSPORT_VAR)
        if WINRM_USERNAME_VAR in extra_vars:
            extra_vars.pop(WINRM_USERNAME_VAR, None)
        if WINRM_PASSWORD_VAR in extra_vars:
            extra_vars.pop(WINRM_PASSWORD_VAR, None)
        if WINRM_TRANSPORT_VAR in extra_vars:
            extra_vars.pop(WINRM_TRANSPORT_VAR, None)
        if conn_user is None:
            conn_user = extra_vars.get('ansible_user')
        if conn_pass is None:
            conn_pass = extra_vars.get('ansible_password')
        if conn_transport is None:
            conn_transport = extra_vars.get('ansible_winrm_transport') or extra_vars.get('ansible_transport') or 'ntlm'
        ctx = {
            'extra_vars': extra_vars,
            'var_meta': meta,
            'conn_username': conn_user,
            'conn_password': conn_pass,
            'conn_transport': str(conn_transport or 'ntlm').strip().lower() or 'ntlm',
        }
        return ctx

    def _write_ansible_cfg(self, directory: str, python_path: Optional[str]) -> str:
        try:
            cfg_path = os.path.join(directory, 'ansible.cfg')
            lines = [
                "[defaults]",
                "host_key_checking = False",
                "retry_files_enabled = False",
                "stdout_callback = default",
                "inventory = inventory",
            ]
            if python_path:
                lines.append(f"interpreter_python = {python_path}")
            lines.append("deprecation_warnings = False")
            lines.append("timeout = 45")
            lines.append("")
            with open(cfg_path, 'w', encoding='utf-8', newline='\n') as fh:
                fh.write('\n'.join(lines) + '\n')
            return cfg_path
        except Exception as exc:
            self._ansible_log(f"[cfg] failed to write ansible.cfg: {exc}", error=True)
            return ''

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

    def _ansible_log(self, msg: str, error: bool = False, run_id: str = None):
        try:
            d = os.path.join(_project_root(), 'Logs', 'Agent')
            ts = time.strftime('%Y-%m-%d %H:%M:%S')
            path = os.path.join(d, 'ansible.log')
            try:
                os.makedirs(d, exist_ok=True)
            except Exception:
                pass
            # rotate daily
            try:
                if os.path.isfile(path):
                    import datetime as _dt
                    dt = _dt.datetime.fromtimestamp(os.path.getmtime(path))
                    if dt.date() != _dt.datetime.now().date():
                        base, ext = os.path.splitext(path)
                        os.replace(path, f"{base}.{dt.strftime('%Y-%m-%d')}{ext}")
            except Exception:
                pass
            with open(path, 'a', encoding='utf-8') as fh:
                fh.write(f'[{ts}] {msg}\n')
            if run_id:
                rp = os.path.join(d, f'run_{run_id}.log')
                with open(rp, 'a', encoding='utf-8') as rf:
                    rf.write(f'[{ts}] {msg}\n')
        except Exception:
            pass

    async def _fetch_service_creds(self) -> dict:
        if self._svc_creds and isinstance(self._svc_creds, dict):
            return self._svc_creds
        try:
            import aiohttp
            url = self._server_base().rstrip('/') + '/api/agent/checkin'
            payload = {
                'agent_id': self.ctx.agent_id,
                'hostname': socket.gethostname(),
                'username': DEFAULT_SERVICE_ACCOUNT,
            }
            self._ansible_log(f"[checkin] POST {url} agent_id={self.ctx.agent_id}")
            timeout = aiohttp.ClientTimeout(total=15)
            async with aiohttp.ClientSession(timeout=timeout) as sess:
                async with sess.post(url, json=payload) as resp:
                    js = await resp.json()
            u = (js or {}).get('username') or DEFAULT_SERVICE_ACCOUNT
            p = (js or {}).get('password') or ''
            if u in LEGACY_SERVICE_ACCOUNTS:
                self._ansible_log(f"[checkin] legacy service username {u!r}; requesting rotate", error=True)
                return await self._rotate_service_creds(reason='legacy_username', force_username=DEFAULT_SERVICE_ACCOUNT)
            self._svc_creds = {'username': u, 'password': p}
            self._ansible_log(f"[checkin] received user={u} pw_len={len(p)}")
            return self._svc_creds
        except Exception:
            self._ansible_log(f"[checkin] failed agent_id={self.ctx.agent_id}", error=True)
            return {'username': DEFAULT_SERVICE_ACCOUNT, 'password': ''}

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

    async def _rotate_service_creds(self, reason: str = 'bad_credentials', force_username: Optional[str] = None) -> dict:
        try:
            import aiohttp
            url = self._server_base().rstrip('/') + '/api/agent/service-account/rotate'
            payload = {
                'agent_id': self.ctx.agent_id,
                'reason': reason,
            }
            if force_username:
                payload['username'] = force_username
            self._ansible_log(f"[rotate] POST {url} agent_id={self.ctx.agent_id}")
            timeout = aiohttp.ClientTimeout(total=15)
            async with aiohttp.ClientSession(timeout=timeout) as sess:
                async with sess.post(url, json=payload) as resp:
                    js = await resp.json()
            u = (js or {}).get('username') or force_username or DEFAULT_SERVICE_ACCOUNT
            p = (js or {}).get('password') or ''
            if u in LEGACY_SERVICE_ACCOUNTS and force_username != DEFAULT_SERVICE_ACCOUNT:
                self._ansible_log(f"[rotate] legacy username {u!r} returned; retrying with default", error=True)
                return await self._rotate_service_creds(reason='legacy_username', force_username=DEFAULT_SERVICE_ACCOUNT)
            if u in LEGACY_SERVICE_ACCOUNTS:
                u = DEFAULT_SERVICE_ACCOUNT
            self._svc_creds = {'username': u, 'password': p}
            self._ansible_log(f"[rotate] received user={u} pw_len={len(p)}")
            return self._svc_creds
        except Exception:
            self._ansible_log(f"[rotate] failed agent_id={self.ctx.agent_id}", error=True)
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
        log_dir = os.path.join(_project_root(), 'Logs', 'Agent')
        try:
            os.makedirs(log_dir, exist_ok=True)
        except Exception:
            pass
        if not os.path.isfile(mod):
            # best effort with inline commands
            try:
                r = subprocess.run(['powershell', '-NoProfile', '-Command', 'Set-Service WinRM -StartupType Automatic; Start-Service WinRM; (Get-Service WinRM).Status'], capture_output=True, text=True, timeout=60)
                self._ansible_log(f"[ensure] basic winrm start rc={r.returncode} out={r.stdout} err={r.stderr}", error=r.returncode!=0)
            except Exception as e:
                self._ansible_log(f"[ensure] winrm start exception: {e}", error=True)
            return
        # Robust execution via temp PS file
        tmp_dir = os.path.join(_project_root(), 'Temp')
        os.makedirs(tmp_dir, exist_ok=True)
        ps_path = os.path.join(tmp_dir, f"ansible_bootstrap_{int(time.time())}.ps1")
        ensure_log = os.path.join(log_dir, f"ensure_winrm_{int(time.time())}.log")
        ps_template = r"""$ErrorActionPreference='Continue'
try {{
  Import-Module -Name '{mod}' -Force
  'Imported module: {mod}' | Out-File -FilePath '{ensure_log}' -Append -Encoding UTF8
  $user = '{username}'
  $pw = '{password}'
  Ensure-LocalhostWinRMHttps | Out-Null
  'Ensured WinRM HTTPS listener on 127.0.0.1:5986' | Out-File -FilePath '{ensure_log}' -Append -Encoding UTF8
  Ensure-BorealisServiceUser -UserName $user -PlaintextPassword $pw | Out-Null
  'Ensured service user: ' + $user | Out-File -FilePath '{ensure_log}' -Append -Encoding UTF8
  try {{
    $ln = $user
    if ($ln.StartsWith('.\')) {{ $ln = $ln.Substring(2) }}
    $exists = Get-LocalUser -Name $ln -ErrorAction SilentlyContinue
    if (-not $exists) {{
      'Fallback: Using NET USER to create account' | Out-File -FilePath '{ensure_log}' -Append -Encoding UTF8
      cmd /c "net user $ln `"{password}`" /ADD /Y" | Out-Null
      cmd /c "net localgroup Administrators $ln /ADD" | Out-Null
    }}
  }} catch {{
    'Fallback path failed: ' + $_ | Out-File -FilePath '{ensure_log}' -Append -Encoding UTF8
  }}
  try {{ (Get-WSManInstance -ResourceURI winrm/config/listener -Enumerate) | Out-File -FilePath '{ensure_log}' -Append -Encoding UTF8 }} catch {{}}
  try {{
    $ln2 = $user
    if ($ln2.StartsWith('.\')) {{ $ln2 = $ln2.Substring(2) }}
    Get-LocalUser | Where-Object {{ $_.Name -eq $ln2 }} | Format-List * | Out-File -FilePath '{ensure_log}' -Append -Encoding UTF8
  }} catch {{}}
  try {{ whoami | Out-File -FilePath '{ensure_log}' -Append -Encoding UTF8 }} catch {{}}
  exit 0
}} catch {{
  $_ | Out-File -FilePath '{ensure_log}' -Append -Encoding UTF8
  exit 1
}}
"""
        safe_mod = mod.replace("'", "''")
        safe_log = ensure_log.replace("'", "''")
        ps_content = ps_template.format(mod=safe_mod, ensure_log=safe_log, username=username.replace("'", "''"), password=password.replace("'", "''"))
        try:
            with open(ps_path, 'w', encoding='utf-8') as fh:
                fh.write(ps_content)
        except Exception as e:
            self._ansible_log(f"[ensure] write PS failed: {e}", error=True)
        try:
            r = subprocess.run(['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', ps_path], capture_output=True, text=True, timeout=180)
            self._ansible_log(f"[ensure] bootstrap rc={r.returncode} out_len={len(r.stdout or '')} err_len={len(r.stderr or '')}", error=r.returncode!=0)
        except Exception as e:
            self._ansible_log(f"[ensure] bootstrap exception: {e}", error=True)

    def _write_winrm_inventory(self, base_dir: str, username: str, password: str, transport: str = 'ntlm') -> str:
        inv_dir = os.path.join(base_dir, 'inventory')
        os.makedirs(inv_dir, exist_ok=True)
        hosts = os.path.join(inv_dir, 'hosts')
        t = str(transport or 'ntlm').strip().lower() or 'ntlm'
        try:
            content = (
                "[local]\n" \
                "localhost\n\n" \
                "[local:vars]\n" \
                "ansible_connection=winrm\n" \
                "ansible_host=127.0.0.1\n" \
                "ansible_port=5986\n" \
                "ansible_winrm_scheme=https\n" \
                f"ansible_winrm_transport={t}\n" \
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
            ok = (r.status_code == 0)
            try:
                so = getattr(r, 'std_out', b'')
                se = getattr(r, 'std_err', b'')
                self._ansible_log(f"[preflight] rc={r.status_code} out={so[:120]!r} err={se[:120]!r}")
            except Exception:
                pass
            return ok
        except Exception as exc:
            self._ansible_log(f"[preflight] exception during winrm session: {exc}", error=True)
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

    async def _run_playbook_runner(self, run_id: str, playbook_content: str, playbook_name: str = '', activity_job_id=None, connection: str = 'local', exec_ctx: dict = None, files=None):
        exec_ctx = exec_ctx or {}
        if not await self._ensure_ansible_ready():
            return False
        try:
            import ansible_runner  # type: ignore
        except Exception as e:
            self._ansible_log(f"[runner] ansible_runner import failed: {e}", error=True)
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
        _norm = self._normalize_playbook_content(playbook_content or '')
        with open(play_abs, 'w', encoding='utf-8', newline='\n') as fh:
            fh.write(_norm)
        self._ansible_log(f"[runner] prepared playbook={play_abs} bytes={len(_norm.encode('utf-8'))}")

        staged = self._stage_payload_files(project, files or [])
        if staged:
            self._ansible_log(f"[runner] staged {len(staged)} payload file(s)")

        cfg_path = self._write_ansible_cfg(project, _venv_python())
        if cfg_path:
            self._ansible_log(f"[runner] ansible.cfg={cfg_path}")

        extra_vars = dict(exec_ctx.get('extra_vars') or {})
        var_meta = exec_ctx.get('var_meta') or {}
        if extra_vars:
            summary = self._format_var_summary(var_meta)
            if summary:
                self._ansible_log(f"[runner] extra vars: {summary}")

        svc_creds = await self._fetch_service_creds()
        svc_user = svc_creds.get('username') or DEFAULT_SERVICE_ACCOUNT
        svc_pwd = svc_creds.get('password') or ''
        self._ensure_winrm_and_user(svc_user, svc_pwd)

        conn_user_override = exec_ctx.get('conn_username')
        conn_pass_override = exec_ctx.get('conn_password')
        conn_transport = str(exec_ctx.get('conn_transport') or 'ntlm').strip().lower() or 'ntlm'

        final_user = conn_user_override or svc_user
        if conn_pass_override is None:
            final_pwd = svc_pwd if final_user == svc_user else (extra_vars.get('ansible_password') or '')
        else:
            final_pwd = conn_pass_override
        final_pwd = '' if final_pwd is None else str(final_pwd)

        pre_ok = self._winrm_preflight(final_user, final_pwd)
        if not pre_ok:
            if final_user == svc_user:
                creds = await self._rotate_service_creds(reason='winrm_preflight_failure')
                final_user = creds.get('username') or svc_user
                final_pwd = creds.get('password') or ''
                self._ensure_winrm_and_user(final_user, final_pwd)
                pre_ok = self._winrm_preflight(final_user, final_pwd)
            else:
                self._ansible_log("[runner] winrm preflight failed for provided credentials; continuing", error=True)
        self._ansible_log(f"[runner] using user={final_user} transport={conn_transport} preflight_ok={pre_ok}")

        inv_file = self._write_winrm_inventory(pd, final_user, final_pwd, transport=conn_transport)
        self._ansible_log(f"[runner] inventory={inv_file}")

        env_payload = {
            'ANSIBLE_FORCE_COLOR': '0',
            'ANSIBLE_STDOUT_CALLBACK': 'default',
        }
        if cfg_path:
            env_payload['ANSIBLE_CONFIG'] = cfg_path
        coll_dir = _collections_dir()
        if coll_dir:
            env_payload['ANSIBLE_COLLECTIONS_PATHS'] = coll_dir
        with open(os.path.join(env_dir, 'envvars'), 'w', encoding='utf-8', newline='\n') as fh:
            json.dump(env_payload, fh)

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
                tx = ev.get('stdout') or ''
                if tx:
                    lines.append(str(tx))
                    if len(lines) > 5000:
                        lines = lines[-2500:]
                if (ev.get('event') or '') == 'playbook_on_stats':
                    d = ev.get('event_data') or {}
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
                extravars=extra_vars or {}
            )
            try:
                self._ansible_log(f"[runner] finished status={getattr(r,'status',None)} rc={getattr(r,'rc',None)}")
            except Exception:
                pass
            status = 'Cancelled' if _cancel_cb() else 'Success'
            try:
                tail = '\n'.join(lines[-50:]).lower()
                if ('access is denied' in tail) or ('unauthorized' in tail) or ('cannot process the request' in tail):
                    auth_failed = True
                    self._ansible_log("[runner] detected auth failure in output", error=True)
            except Exception:
                pass
        except Exception:
            status = 'Failed'
            self._ansible_log("[runner] exception in ansible-runner", error=True)

        recap_text = ''
        try:
            if isinstance(recap_json, dict):
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
        self._ansible_log(f"[runner] recap posted status={status}")
        if auth_failed:
            try:
                retried = bool(exec_ctx.get('_retried_auth'))
                if final_user == svc_user and not retried:
                    newc = await self._rotate_service_creds(reason='auth_failed_retry')
                    exec_ctx_retry = dict(exec_ctx)
                    exec_ctx_retry['_retried_auth'] = True
                    exec_ctx_retry['conn_username'] = newc.get('username') or svc_user
                    exec_ctx_retry['conn_password'] = newc.get('password') or ''
                    self._ensure_winrm_and_user(exec_ctx_retry['conn_username'], exec_ctx_retry['conn_password'])
                    await self._run_playbook_runner(
                        run_id,
                        playbook_content,
                        playbook_name=playbook_name,
                        activity_job_id=activity_job_id,
                        connection=connection,
                        exec_ctx=exec_ctx_retry,
                        files=files,
                    )
                return True
            except Exception:
                self._ansible_log("[runner] rotate+retry failed", error=True)
                pass
        return True

    async def _run_playbook(self, run_id: str, playbook_content: str, playbook_name: str = '', activity_job_id=None, connection: str = 'local', variables=None, variable_values=None, files=None):
        # Write playbook temp
        tmp_dir = os.path.join(_project_root(), 'Temp')
        os.makedirs(tmp_dir, exist_ok=True)
        fd, path = tempfile.mkstemp(prefix='pb_', suffix='.yml', dir=tmp_dir, text=True)
        _norm2 = self._normalize_playbook_content(playbook_content or '')
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
            fh.write(_norm2)
        self._ansible_log(f"[cli] prepared playbook={path} bytes={len(_norm2.encode('utf-8'))}")

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

        exec_ctx = self._build_execution_context(variables, variable_values)
        extra_vars = dict(exec_ctx.get('extra_vars') or {})
        var_meta = exec_ctx.get('var_meta') or {}
        if extra_vars:
            summary = self._format_var_summary(var_meta)
            if summary:
                self._ansible_log(f"[cli] extra vars: {summary}")

        ready = await self._ensure_ansible_ready()
        if not ready:
            await self._post_recap({
                'run_id': run_id,
                'hostname': hostname,
                'agent_id': agent_id,
                'status': 'Failed',
                'recap_text': 'Ansible dependencies unavailable; see agent ansible.log',
                'finished_ts': int(time.time()),
            })
            try:
                os.remove(path)
            except Exception:
                pass
            return

        staged = self._stage_payload_files(os.path.dirname(path), files or [])
        if staged:
            self._ansible_log(f"[cli] staged {len(staged)} payload file(s)")

        cfg_path_cli = self._write_ansible_cfg(os.path.dirname(path), _venv_python())
        if cfg_path_cli:
            self._ansible_log(f"[cli] ansible.cfg={cfg_path_cli}")

        conn = (connection or 'local').strip().lower()
        inv_file_cli = None
        svc_creds = await self._fetch_service_creds()
        svc_user = svc_creds.get('username') or DEFAULT_SERVICE_ACCOUNT
        svc_pwd = svc_creds.get('password') or ''
        self._ensure_winrm_and_user(svc_user, svc_pwd)

        conn_user_override = exec_ctx.get('conn_username')
        conn_pass_override = exec_ctx.get('conn_password')
        conn_transport = str(exec_ctx.get('conn_transport') or 'ntlm').strip().lower() or 'ntlm'

        final_user = conn_user_override or svc_user
        if conn_pass_override is None:
            final_pwd = svc_pwd if final_user == svc_user else (extra_vars.get('ansible_password') or '')
        else:
            final_pwd = conn_pass_override
        final_pwd = '' if final_pwd is None else str(final_pwd)

        if os.name == 'nt':
            try:
                pre_ok = self._winrm_preflight(final_user, final_pwd)
                if not pre_ok:
                    if final_user == svc_user:
                        creds = await self._rotate_service_creds(reason='winrm_preflight_failure')
                        final_user = creds.get('username') or svc_user
                        final_pwd = creds.get('password') or ''
                        self._ensure_winrm_and_user(final_user, final_pwd)
                    else:
                        self._ansible_log("[cli] winrm preflight failed for provided credentials; continuing", error=True)
                inv_file_cli = self._write_winrm_inventory(os.path.dirname(path), final_user, final_pwd, transport=conn_transport)
                self._ansible_log(f"[cli] inventory={inv_file_cli} user={final_user}")
            except Exception as exc:
                self._ansible_log(f"[cli] inventory setup failed: {exc}", error=True)
                inv_file_cli = None
        else:
            inv_file_cli = None

        extra_vars_file = None
        if extra_vars:
            try:
                extra_vars_file = os.path.join(os.path.dirname(path), 'extra_vars.json')
                with open(extra_vars_file, 'w', encoding='utf-8') as fh:
                    json.dump(extra_vars, fh)
            except Exception as exc:
                self._ansible_log(f"[cli] failed to write extra_vars.json: {exc}", error=True)
                extra_vars_file = None

        # Build CLI; resolve ansible-playbook or fallback to python -m ansible.cli.playbook
        ap = _ansible_playbook_cmd()
        use_module = False
        if os.path.dirname(ap) and not os.path.isfile(ap):
            # If we got a path but it doesn't exist, switch to module mode
            use_module = True
        elif not os.path.dirname(ap):
            # bare command; verify existence in PATH
            from shutil import which
            if which(ap) is None:
                use_module = True
        if use_module:
            py = _venv_python() or sys.executable
            base_cmd = [py, '-X', 'utf8', '-m', 'ansible.cli.playbook']
            self._ansible_log(f"[cli] ansible-playbook not found; using python -m ansible.cli.playbook via {py}")
        else:
            base_cmd = [ap]

        if inv_file_cli and os.path.isfile(inv_file_cli):
            cmd = base_cmd + [path, '-i', inv_file_cli]
            self._log_local(f"Launching ansible-playbook with WinRM inventory: {' '.join(cmd)}")
            self._ansible_log(f"[cli] cmd={' '.join(cmd)} inv={inv_file_cli}")
        else:
            if conn not in ('local', 'winrm', 'psrp'):
                conn = 'local'
            cmd = base_cmd + [path, '-i', 'localhost,', '-c', conn]
            self._log_local(f"Launching ansible-playbook: conn={conn} cmd={' '.join(cmd)}")
            self._ansible_log(f"[cli] cmd={' '.join(cmd)}")
        if extra_vars_file:
            cmd.extend(['-e', f"@{extra_vars_file}"])
        # Ensure clean, plain output and correct interpreter for localhost
        env = os.environ.copy()
        env['ANSIBLE_FORCE_COLOR'] = '0'
        env['ANSIBLE_NOCOLOR'] = '1'
        env['PYTHONIOENCODING'] = 'utf-8'
        env['PYTHONUTF8'] = '1'
        env['ANSIBLE_STDOUT_CALLBACK'] = 'default'
        env['ANSIBLE_LOCALHOST_WARNING'] = '0'
        coll_dir = _collections_dir()
        if coll_dir:
            env['ANSIBLE_COLLECTIONS_PATHS'] = coll_dir
        if cfg_path_cli:
            env['ANSIBLE_CONFIG'] = cfg_path_cli
        if os.name == 'nt':
            env['LANG'] = 'en_US.UTF-8'
            env['LC_ALL'] = 'en_US.UTF-8'
            env['LC_CTYPE'] = 'en_US.UTF-8'
        vp = _venv_python()
        if vp:
            env.setdefault('ANSIBLE_PYTHON_INTERPRETER', vp)

        self._ansible_log(f"[cli] locale env overrides: PYTHONUTF8={env.get('PYTHONUTF8')} LANG={env.get('LANG')} LC_ALL={env.get('LC_ALL')} LC_CTYPE={env.get('LC_CTYPE')}")

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
                runner_pref = (os.environ.get('BOREALIS_USE_ANSIBLE_RUNNER', '1') or '1').strip().lower()
                if runner_pref not in ('0', 'false', 'no'):
                    if os.name == 'nt' and runner_pref not in ('force',):
                        self._ansible_log('[runner] skipping ansible-runner on Windows platform')
                    else:
                        used = await self._run_playbook_runner(
                            run_id,
                            playbook_content,
                            playbook_name=playbook_name,
                            activity_job_id=activity_job_id,
                            connection=connection,
                            exec_ctx=exec_ctx,
                            files=files,
                        )
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
            self._ansible_log(f"[cli] failed to launch: {e}", error=True)
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
                self._ansible_log(f"[cli] {line}")
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

        # Proactive bootstrap: converge WinRM + service user at role load (SYSTEM only)
        async def _bootstrap_once():
            try:
                if os.name != 'nt':
                    return
                creds = await self._fetch_service_creds()
                user = creds.get('username') or DEFAULT_SERVICE_ACCOUNT
                pwd = creds.get('password') or ''
                self._ansible_log(f"[bootstrap] ensure winrm+user user={user} pw_len={len(pwd)}")
                self._ensure_winrm_and_user(user, pwd)
                ok = self._winrm_preflight(user, pwd)
                self._ansible_log(f"[bootstrap] preflight_ok={ok}")
                if not ok:
                    self._ansible_log("[bootstrap] preflight failed; rotating creds", error=True)
                    creds = await self._rotate_service_creds(reason='bootstrap_preflight_failed')
                    user = creds.get('username') or user
                    pwd = creds.get('password') or ''
                    self._ensure_winrm_and_user(user, pwd)
                    ok2 = self._winrm_preflight(user, pwd)
                    self._ansible_log(f"[bootstrap] preflight_ok_after_rotate={ok2}")
            except Exception:
                self._ansible_log("[bootstrap] exception", error=True)

        try:
            loop = getattr(self.ctx, 'loop', None)
            if loop and not loop.is_closed():
                loop.create_task(_bootstrap_once())
            else:
                self._ansible_log('[bootstrap] unable to schedule proactive task; no event loop available')
        except Exception as exc:
            self._ansible_log(f"[bootstrap] failed to schedule coroutine: {exc}", error=True)

        @sio.on('ansible_playbook_run')
        async def _on_ansible_playbook_run(payload):
            try:
                hostname = socket.gethostname()
                target = (payload.get('target_hostname') or '').strip().lower()
                if target and target != hostname.lower():
                    return
                # Accept provided run_id or generate one
                run_id = (payload.get('run_id') or '').strip() or uuid.uuid4().hex
                content = _decode_playbook_content(payload.get('playbook_content'), payload.get('playbook_encoding'))
                p_name = payload.get('playbook_name') or ''
                act_id = payload.get('activity_job_id')
                sched_job_id = payload.get('scheduled_job_id')
                sched_run_id = payload.get('scheduled_run_id')
                conn = (payload.get('connection') or 'local')
                var_defs = payload.get('variables') if isinstance(payload.get('variables'), list) else []
                var_values = payload.get('variable_values') if isinstance(payload.get('variable_values'), dict) else {}
                files = payload.get('files') if isinstance(payload.get('files'), list) else []
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
                        await self._run_playbook(
                            run_id,
                            content,
                            playbook_name=p_name,
                            activity_job_id=act_id,
                            connection=conn,
                            variables=var_defs,
                            variable_values=var_values,
                            files=files,
                        )
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
                content = _decode_playbook_content(payload.get('script_content'), payload.get('script_encoding'))
                p_name = payload.get('script_name') or ''
                self._runs[run_id] = {'cancel': False, 'proc': None}
                var_defs = payload.get('variables') if isinstance(payload.get('variables'), list) else []
                var_values = payload.get('variable_values') if isinstance(payload.get('variable_values'), dict) else {}
                files = payload.get('files') if isinstance(payload.get('files'), list) else []
                asyncio.create_task(self._run_playbook(
                    run_id,
                    content,
                    playbook_name=p_name,
                    activity_job_id=payload.get('job_id'),
                    connection='local',
                    variables=var_defs,
                    variable_values=var_values,
                    files=files,
                ))
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
