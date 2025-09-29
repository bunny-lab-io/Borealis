#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/server.py

import eventlet
# Monkey-patch stdlib for cooperative sockets
eventlet.monkey_patch()

import requests
from flask import Flask, request, jsonify, Response, send_from_directory, make_response, session
from flask_socketio import SocketIO, emit, join_room
from flask_cors import CORS
from werkzeug.middleware.proxy_fix import ProxyFix
from itsdangerous import URLSafeTimedSerializer, BadSignature, SignatureExpired

import time
import os  # To Read Production ReactJS Server Folder
import json  # For reading workflow JSON files
import shutil  # For moving workflow files and folders
from typing import List, Dict
import sqlite3
import io

# Borealis Python API Endpoints
from Python_API_Endpoints.ocr_engines import run_ocr_on_base64
from Python_API_Endpoints.script_engines import run_powershell_script
from job_scheduler import register as register_job_scheduler
from job_scheduler import set_online_lookup as scheduler_set_online_lookup

# ---------------------------------------------
# Flask + WebSocket Server Configuration
# ---------------------------------------------
app = Flask(
    __name__,
    static_folder=os.path.join(os.path.dirname(__file__), '../web-interface/build'),
    static_url_path=''
)

# Respect reverse proxy headers for scheme/host so cookies and redirects behave
app.wsgi_app = ProxyFix(app.wsgi_app, x_proto=1, x_host=1)

# Enable CORS on All Routes (allow credentials). Optionally lock down via env.
_cors_origins = os.environ.get('BOREALIS_CORS_ORIGINS')  # e.g. "https://ui.example.com,https://admin.example.com"
if _cors_origins:
    origins = [o.strip() for o in _cors_origins.split(',') if o.strip()]
    CORS(app, supports_credentials=True, origins=origins)
else:
    CORS(app, supports_credentials=True)

# Basic secret key for session cookies (can be overridden via env)
app.secret_key = os.environ.get('BOREALIS_SECRET', 'borealis-dev-secret')

# Session cookie policy (tunable for dev/prod/reverse proxy)
# Defaults keep dev working; override via env in production/proxy scenarios.
app.config.update(
    SESSION_COOKIE_HTTPONLY=True,
    SESSION_COOKIE_SAMESITE=os.environ.get('BOREALIS_COOKIE_SAMESITE', 'Lax'),  # set to 'None' when UI/API are on different sites
    SESSION_COOKIE_SECURE=(os.environ.get('BOREALIS_COOKIE_SECURE', '0').lower() in ('1', 'true', 'yes')),
)

# Optionally pin cookie domain if served under a fixed hostname (leave unset for host-only/IP dev)
_cookie_domain = os.environ.get('BOREALIS_COOKIE_DOMAIN')  # e.g. ".example.com" or "borealis.bunny-lab.io"
if _cookie_domain:
    app.config['SESSION_COOKIE_DOMAIN'] = _cookie_domain

socketio = SocketIO(
    app,
    cors_allowed_origins="*",
    async_mode="eventlet",
    engineio_options={
        'max_http_buffer_size':       100_000_000,
        'max_websocket_message_size': 100_000_000
    }
)

# ---------------------------------------------
# Serve ReactJS Production Vite Build from dist/
# ---------------------------------------------
@app.route('/', defaults={'path': ''})
@app.route('/<path:path>')
def serve_dist(path):
    full_path = os.path.join(app.static_folder, path)
    if path and os.path.isfile(full_path):
        return send_from_directory(app.static_folder, path)
    else:
        # SPA entry point
        return send_from_directory(app.static_folder, 'index.html')


# ---------------------------------------------
# Health Check Endpoint
# ---------------------------------------------
@app.route("/health")
def health():
    return jsonify({"status": "ok"})

# ---------------------------------------------
# Server Time Endpoint
# ---------------------------------------------
@app.route("/api/server/time", methods=["GET"])
def api_server_time():
    try:
        from datetime import datetime, timezone
        now_local = datetime.now().astimezone()
        now_utc = datetime.now(timezone.utc)
        tzinfo = now_local.tzinfo
        offset = tzinfo.utcoffset(now_local) if tzinfo else None
        # Friendly display string, e.g., "September 23rd 2025 @ 12:49AM"
        def _ordinal(n: int) -> str:
            if 11 <= (n % 100) <= 13:
                suf = 'th'
            else:
                suf = {1: 'st', 2: 'nd', 3: 'rd'}.get(n % 10, 'th')
            return f"{n}{suf}"
        month = now_local.strftime("%B")
        day_disp = _ordinal(now_local.day)
        year = now_local.strftime("%Y")
        hour24 = now_local.hour
        hour12 = hour24 % 12 or 12
        minute = now_local.minute
        ampm = "AM" if hour24 < 12 else "PM"
        display = f"{month} {day_disp} {year} @ {hour12}:{minute:02d}{ampm}"
        return jsonify({
            "epoch": int(now_local.timestamp()),
            "iso": now_local.isoformat(),
            "utc_iso": now_utc.isoformat().replace("+00:00", "Z"),
            "timezone": str(tzinfo) if tzinfo else "",
            "offset_seconds": int(offset.total_seconds()) if offset else 0,
            "display": display,
        })
    except Exception as e:
        return jsonify({"error": str(e)}), 500

# ---------------------------------------------
# Auth + Users (DB-backed)
# ---------------------------------------------
def _now_ts() -> int:
    return int(time.time())


def _sha512_hex(s: str) -> str:
    import hashlib
    return hashlib.sha512((s or '').encode('utf-8')).hexdigest()


def _db_conn():
    conn = sqlite3.connect(DB_PATH, timeout=15)
    try:
        cur = conn.cursor()
        # Enable better read/write concurrency
        cur.execute("PRAGMA journal_mode=WAL")
        cur.execute("PRAGMA busy_timeout=5000")
        cur.execute("PRAGMA synchronous=NORMAL")
        conn.commit()
    except Exception:
        pass
    return conn


def _user_row_to_dict(row):
    # id, username, display_name, role, last_login, created_at, updated_at
    return {
        "id": row[0],
        "username": row[1],
        "display_name": row[2] or row[1],
        "role": row[3] or "User",
        "last_login": row[4] or 0,
        "created_at": row[5] or 0,
        "updated_at": row[6] or 0,
    }


def _current_user():
    # Prefer server-side session if present
    u = session.get('username')
    role = session.get('role')
    if u:
        return {"username": u, "role": role or "User"}

    # Otherwise allow token-based auth (Authorization: Bearer <token> or borealis_auth cookie)
    token = None
    auth = request.headers.get('Authorization') or ''
    if auth.lower().startswith('bearer '):
        token = auth.split(' ', 1)[1].strip()
    if not token:
        token = request.cookies.get('borealis_auth')
    if token:
        user = _verify_token(token)
        if user:
            return user
    return None


def _require_login():
    user = _current_user()
    if not user:
        return jsonify({"error": "unauthorized"}), 401
    return None


def _require_admin():
    user = _current_user()
    if not user:
        return jsonify({"error": "unauthorized"}), 401
    if (user.get('role') or '').lower() != 'admin':
        return jsonify({"error": "forbidden"}), 403
    return None


# ---------------------------------------------
# Token helpers (for dev/proxy-friendly auth)
# ---------------------------------------------
def _token_serializer():
    secret = app.secret_key or 'borealis-dev-secret'
    return URLSafeTimedSerializer(secret, salt='borealis-auth')


def _make_token(username: str, role: str) -> str:
    s = _token_serializer()
    payload = {"u": username, "r": role or 'User', "ts": _now_ts()}
    return s.dumps(payload)


def _verify_token(token: str):
    try:
        s = _token_serializer()
        max_age = int(os.environ.get('BOREALIS_TOKEN_TTL_SECONDS', 60*60*24*30))  # 30 days
        data = s.loads(token, max_age=max_age)
        return {"username": data.get('u'), "role": data.get('r') or 'User'}
    except (BadSignature, SignatureExpired, Exception):
        return None


@app.route("/api/auth/login", methods=["POST"])
def api_login():
    payload = request.get_json(silent=True) or {}
    username = (payload.get('username') or '').strip()
    password = payload.get('password')  # plain (optional)
    password_sha512 = (payload.get('password_sha512') or '').strip().lower()
    if not username or (not password and not password_sha512):
        return jsonify({"error": "missing credentials"}), 400

    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, username, display_name, password_sha512, role, last_login, created_at, updated_at FROM users WHERE LOWER(username)=LOWER(?)",
            (username,)
        )
        row = cur.fetchone()
        if not row:
            conn.close()
            return jsonify({"error": "invalid username or password"}), 401
        stored_hash = (row[3] or '').lower()
        check_hash = password_sha512 or _sha512_hex(password or '')
        if stored_hash != (check_hash or '').lower():
            conn.close()
            return jsonify({"error": "invalid username or password"}), 401
        role = row[4] or 'User'
        # update last_login
        now = _now_ts()
        cur.execute("UPDATE users SET last_login=?, updated_at=? WHERE id=?", (now, now, row[0]))
        conn.commit()
        conn.close()
        # set session cookie
        session['username'] = row[1]
        session['role'] = role

        # also issue a signed bearer token and set a dev-friendly cookie
        token = _make_token(row[1], role)
        resp = jsonify({"status": "ok", "username": row[1], "role": role, "token": token})
        # mirror session cookie flags for the token cookie
        samesite = app.config.get('SESSION_COOKIE_SAMESITE', 'Lax')
        secure = bool(app.config.get('SESSION_COOKIE_SECURE', False))
        domain = app.config.get('SESSION_COOKIE_DOMAIN', None)
        resp.set_cookie('borealis_auth', token, httponly=False, samesite=samesite, secure=secure, domain=domain, path='/')
        return resp
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/auth/logout", methods=["POST"])  # simple logout
def api_logout():
    session.clear()
    resp = jsonify({"status": "ok"})
    # Clear token cookie as well
    resp.set_cookie('borealis_auth', '', expires=0, path='/')
    return resp


@app.route("/api/auth/me", methods=["GET"])  # whoami
def api_me():
    user = _current_user()
    if not user:
        return jsonify({"error": "unauthorized"}), 401
    # Enrich with display_name if possible
    username = (user.get('username') or '').strip()
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, username, display_name, role, last_login, created_at, updated_at FROM users WHERE LOWER(username)=LOWER(?)",
            (username,)
        )
        row = cur.fetchone()
        conn.close()
        if row:
            info = _user_row_to_dict(row)
            # Return minimal fields but include display_name
            return jsonify({
                "username": info['username'],
                "display_name": info['display_name'],
                "role": info['role']
            })
    except Exception:
        pass
    # Fallback to original shape
    return jsonify({
        "username": username,
        "display_name": username,
        "role": user.get('role') or 'User'
    })


@app.route("/api/users", methods=["GET"])
def api_users_list():
    chk = _require_admin()
    if chk:
        return chk
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, username, display_name, role, last_login, created_at, updated_at FROM users ORDER BY LOWER(username) ASC"
        )
        rows = cur.fetchall()
        conn.close()
        users = [
            {
                "id": r[0],
                "username": r[1],
                "display_name": r[2] or r[1],
                "role": r[3] or 'User',
                "last_login": r[4] or 0,
                "created_at": r[5] or 0,
                "updated_at": r[6] or 0,
            }
            for r in rows
        ]
        return jsonify({"users": users})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/users", methods=["POST"])  # create user
def api_users_create():
    chk = _require_admin()
    if chk:
        return chk
    data = request.get_json(silent=True) or {}
    username = (data.get('username') or '').strip()
    display_name = (data.get('display_name') or username).strip()
    role = (data.get('role') or 'User').strip().title()
    password_sha512 = (data.get('password_sha512') or '').strip().lower()
    if not username or not password_sha512:
        return jsonify({"error": "username and password_sha512 are required"}), 400
    if role not in ('User', 'Admin'):
        return jsonify({"error": "invalid role"}), 400
    now = _now_ts()
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "INSERT INTO users(username, display_name, password_sha512, role, created_at, updated_at) VALUES(?,?,?,?,?,?)",
            (username, display_name or username, password_sha512, role, now, now)
        )
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except sqlite3.IntegrityError:
        return jsonify({"error": "username already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/users/<username>", methods=["DELETE"])  # delete user
def api_users_delete(username):
    chk = _require_admin()
    if chk:
        return chk
    username = (username or '').strip()
    if not username:
        return jsonify({"error": "invalid username"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        # prevent deleting current user
        me = _current_user()
        if me and (me.get('username','').lower() == username.lower()):
            conn.close()
            return jsonify({"error": "You cannot delete the user you are currently logged in as."}), 400
        # ensure at least one other user remains
        cur.execute("SELECT COUNT(*) FROM users")
        total = cur.fetchone()[0] or 0
        if total <= 1:
            conn.close()
            return jsonify({"error": "There is only one user currently configured, you cannot delete this user until you have created another."}), 400
        cur.execute("DELETE FROM users WHERE LOWER(username)=LOWER(?)", (username,))
        deleted = cur.rowcount
        conn.commit()
        conn.close()
        if deleted == 0:
            return jsonify({"error": "user not found"}), 404
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/users/<username>/reset_password", methods=["POST"])  # reset password
def api_users_reset_password(username):
    chk = _require_admin()
    if chk:
        return chk
    data = request.get_json(silent=True) or {}
    password_sha512 = (data.get('password_sha512') or '').strip().lower()
    if not password_sha512 or len(password_sha512) != 128:
        return jsonify({"error": "invalid password hash"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        now = _now_ts()
        cur.execute(
            "UPDATE users SET password_sha512=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
            (password_sha512, now, username)
        )
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "user not found"}), 404
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/users/<username>/role", methods=["POST"])  # change role
def api_users_change_role(username):
    chk = _require_admin()
    if chk:
        return chk
    data = request.get_json(silent=True) or {}
    role = (data.get('role') or '').strip().title()
    if role not in ('User', 'Admin'):
        return jsonify({"error": "invalid role"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        # Prevent removing last admin
        if role == 'User':
            cur.execute("SELECT COUNT(*) FROM users WHERE LOWER(role)='admin'")
            admin_cnt = cur.fetchone()[0] or 0
            cur.execute("SELECT LOWER(role) FROM users WHERE LOWER(username)=LOWER(?)", (username,))
            row = cur.fetchone()
            if row and (row[0] or '').lower() == 'admin' and admin_cnt <= 1:
                conn.close()
                return jsonify({"error": "cannot demote the last admin"}), 400
        now = _now_ts()
        cur.execute(
            "UPDATE users SET role=?, updated_at=? WHERE LOWER(username)=LOWER(?)",
            (role, now, username)
        )
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "user not found"}), 404
        conn.commit()
        conn.close()
        # If current user changed their own role, refresh session role
        me = _current_user()
        if me and me.get('username','').lower() == username.lower():
            session['role'] = role
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500

# ---------------------------------------------
# Borealis Python API Endpoints
# ---------------------------------------------
# /api/ocr: Accepts a base64 image and OCR engine selection,
# and returns extracted text lines.
@app.route("/api/ocr", methods=["POST"])
def ocr_endpoint():
    payload = request.get_json()
    image_b64 = payload.get("image_base64")
    engine = payload.get("engine", "tesseract").lower().strip()
    backend = payload.get("backend", "cpu").lower().strip()

    if engine in ["tesseractocr", "tesseract"]:
        engine = "tesseract"
    elif engine == "easyocr":
        engine = "easyocr"
    else:
        return jsonify({"error": f"OCR engine '{engine}' not recognized."}), 400

    try:
        lines = run_ocr_on_base64(image_b64, engine=engine, backend=backend)
        return jsonify({"lines": lines})
    except Exception as e:
        return jsonify({"error": str(e)}), 500

# New storage management endpoints

@app.route("/api/storage/move_workflow", methods=["POST"])
def move_workflow():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_rel = (data.get("new_path") or "").strip()
    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Workflows")
    )
    old_abs = os.path.abspath(os.path.join(workflows_root, rel_path))
    new_abs = os.path.abspath(os.path.join(workflows_root, new_rel))
    if not old_abs.startswith(workflows_root) or not os.path.isfile(old_abs):
        return jsonify({"error": "Workflow not found"}), 404
    if not new_abs.startswith(workflows_root):
        return jsonify({"error": "Invalid destination"}), 400
    os.makedirs(os.path.dirname(new_abs), exist_ok=True)
    try:
        shutil.move(old_abs, new_abs)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/storage/delete_workflow", methods=["POST"])
def delete_workflow():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Workflows")
    )
    abs_path = os.path.abspath(os.path.join(workflows_root, rel_path))
    if not abs_path.startswith(workflows_root) or not os.path.isfile(abs_path):
        return jsonify({"error": "Workflow not found"}), 404
    try:
        os.remove(abs_path)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/storage/delete_folder", methods=["POST"])
def delete_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Workflows")
    )
    abs_path = os.path.abspath(os.path.join(workflows_root, rel_path))
    if not abs_path.startswith(workflows_root) or not os.path.isdir(abs_path):
        return jsonify({"error": "Folder not found"}), 404
    try:
        shutil.rmtree(abs_path)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500

@app.route("/api/storage/create_folder", methods=["POST"])
def create_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Workflows")
    )
    abs_path = os.path.abspath(os.path.join(workflows_root, rel_path))
    if not abs_path.startswith(workflows_root):
        return jsonify({"error": "Invalid path"}), 400
    try:
        os.makedirs(abs_path, exist_ok=True)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/storage/rename_folder", methods=["POST"])
def rename_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Workflows")
    )
    old_abs = os.path.abspath(os.path.join(workflows_root, rel_path))
    if not old_abs.startswith(workflows_root) or not os.path.isdir(old_abs):
        return jsonify({"error": "Folder not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    new_abs = os.path.join(os.path.dirname(old_abs), new_name)
    try:
        os.rename(old_abs, new_abs)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500

# ---------------------------------------------
# Borealis Storage API Endpoints
# ---------------------------------------------
def _safe_read_json(path: str) -> Dict:
    """
    Try to read JSON safely. Returns {} on failure.
    """
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except Exception:
        return {}

def _extract_tab_name(obj: Dict) -> str:
    """
    Best-effort extraction of a workflow tab name from a JSON object.
    Falls back to empty string when unknown.
    """
    if not isinstance(obj, dict):
        return ""
    for key in ["tabName", "tab_name", "name", "title"]:
        val = obj.get(key)
        if isinstance(val, str) and val.strip():
            return val.strip()
    return ""

@app.route("/api/storage/load_workflows", methods=["GET"])
def load_workflows():
    """
    Scan <ProjectRoot>/Assemblies/Workflows for *.json files and return a table-friendly list.
    """
    # Resolve <ProjectRoot>/Assemblies/Workflows relative to this file at <ProjectRoot>/Data/server.py
    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Workflows")
    )
    results: List[Dict] = []
    folders: List[str] = []

    if not os.path.isdir(workflows_root):
        return jsonify({
            "root": workflows_root,
            "workflows": [],
            "warning": "Workflows directory not found."
        }), 200

    for root, dirs, files in os.walk(workflows_root):
        rel_root = os.path.relpath(root, workflows_root)
        if rel_root != ".":
            folders.append(rel_root.replace(os.sep, "/"))
        for fname in files:
            if not fname.lower().endswith(".json"):
                continue

            full_path = os.path.join(root, fname)
            rel_path = os.path.relpath(full_path, workflows_root)

            parts = rel_path.split(os.sep)
            folder_parts = parts[:-1]
            breadcrumb_prefix = " > ".join(folder_parts) if folder_parts else ""
            display_name = f"{breadcrumb_prefix} > {fname}" if breadcrumb_prefix else fname

            obj = _safe_read_json(full_path)
            tab_name = _extract_tab_name(obj)

            try:
                mtime = os.path.getmtime(full_path)
            except Exception:
                mtime = 0.0
            last_edited_str = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime))

            results.append({
                "name": display_name,
                "breadcrumb_prefix": breadcrumb_prefix,
                "file_name": fname,
                "rel_path": rel_path.replace(os.sep, "/"),
                "tab_name": tab_name,
                "description": "",
                "category": "",
                "last_edited": last_edited_str,
                "last_edited_epoch": mtime
            })

    results.sort(key=lambda x: x.get("last_edited_epoch", 0.0), reverse=True)

    return jsonify({
        "root": workflows_root,
        "workflows": results,
        "folders": folders
    })


@app.route("/api/storage/load_workflow", methods=["GET"])
def load_workflow():
    """Load a single workflow JSON by its relative path."""
    rel_path = request.args.get("path", "")
    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Workflows")
    )
    abs_path = os.path.abspath(os.path.join(workflows_root, rel_path))

    if not abs_path.startswith(workflows_root) or not os.path.isfile(abs_path):
        return jsonify({"error": "Workflow not found"}), 404

    obj = _safe_read_json(abs_path)
    return jsonify(obj)


@app.route("/api/storage/save_workflow", methods=["POST"])
def save_workflow():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    name = (data.get("name") or "").strip()
    workflow = data.get("workflow")
    if not isinstance(workflow, dict):
        return jsonify({"error": "Invalid payload"}), 400

    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Workflows")
    )
    os.makedirs(workflows_root, exist_ok=True)

    if rel_path:
        if not rel_path.lower().endswith(".json"):
            rel_path += ".json"
        abs_path = os.path.abspath(os.path.join(workflows_root, rel_path))
    else:
        if not name:
            return jsonify({"error": "Invalid payload"}), 400
        if not name.lower().endswith(".json"):
            name += ".json"
        abs_path = os.path.abspath(os.path.join(workflows_root, os.path.basename(name)))

    if not abs_path.startswith(workflows_root):
        return jsonify({"error": "Invalid path"}), 400

    os.makedirs(os.path.dirname(abs_path), exist_ok=True)
    try:
        with open(abs_path, "w", encoding="utf-8") as fh:
            json.dump(workflow, fh, indent=2)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/storage/rename_workflow", methods=["POST"])
def rename_workflow():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Workflows")
    )
    old_abs = os.path.abspath(os.path.join(workflows_root, rel_path))
    if not old_abs.startswith(workflows_root) or not os.path.isfile(old_abs):
        return jsonify({"error": "Workflow not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    if not new_name.lower().endswith(".json"):
        new_name += ".json"
    new_abs = os.path.join(os.path.dirname(old_abs), os.path.basename(new_name))
    base_name = os.path.splitext(os.path.basename(new_abs))[0]
    try:
        os.rename(old_abs, new_abs)
        obj = _safe_read_json(new_abs)
        for k in ["tabName", "tab_name", "name", "title"]:
            if k in obj:
                obj[k] = base_name
        if "tab_name" not in obj:
            obj["tab_name"] = base_name
        with open(new_abs, "w", encoding="utf-8") as fh:
            json.dump(obj, fh, indent=2)
        rel_new = os.path.relpath(new_abs, workflows_root).replace(os.sep, "/")
        return jsonify({"status": "ok", "rel_path": rel_new})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# ---------------------------------------------
# Scripts Storage API Endpoints
# ---------------------------------------------
def _scripts_root() -> str:
    # Scripts live under Assemblies. We unify listing under Assemblies and
    # only allow access within top-level folders: "Scripts" and "Ansible Playbooks".
    return os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies")
    )

def _scripts_allowed_top_levels() -> List[str]:
    # Scripts API is scoped strictly to the Scripts top-level.
    return ["Scripts"]

def _is_valid_scripts_relpath(rel_path: str) -> bool:
    try:
        p = (rel_path or "").replace("\\", "/").lstrip("/")
        if not p:
            return False
        top = p.split("/", 1)[0]
        return top in _scripts_allowed_top_levels()
    except Exception:
        return False


def _detect_script_type(filename: str) -> str:
    fn = (filename or "").lower()
    if fn.endswith(".yml"):
        return "ansible"
    if fn.endswith(".ps1"):
        return "powershell"
    if fn.endswith(".bat"):
        return "batch"
    if fn.endswith(".sh"):
        return "bash"
    return "unknown"


def _ext_for_type(script_type: str) -> str:
    t = (script_type or "").lower()
    if t == "ansible":
        return ".yml"
    if t == "powershell":
        return ".ps1"
    if t == "batch":
        return ".bat"
    if t == "bash":
        return ".sh"
    return ""


@app.route("/api/scripts/list", methods=["GET"])
def list_scripts():
    """Scan <ProjectRoot>/Assemblies/Scripts for script files and return list + folders."""
    scripts_root = _scripts_root()
    results: List[Dict] = []
    folders: List[str] = []

    if not os.path.isdir(scripts_root):
        return jsonify({
            "root": scripts_root,
            "scripts": [],
            "folders": []
        }), 200

    exts = (".yml", ".ps1", ".bat", ".sh")
    for top in _scripts_allowed_top_levels():
        base_dir = os.path.join(scripts_root, top)
        if not os.path.isdir(base_dir):
            continue
        for root, dirs, files in os.walk(base_dir):
            rel_root = os.path.relpath(root, scripts_root)
            if rel_root != ".":
                folders.append(rel_root.replace(os.sep, "/"))
            for fname in files:
                if not fname.lower().endswith(exts):
                    continue

                full_path = os.path.join(root, fname)
                rel_path = os.path.relpath(full_path, scripts_root)
                parts = rel_path.split(os.sep)
                folder_parts = parts[:-1]
                breadcrumb_prefix = " > ".join(folder_parts) if folder_parts else ""
                display_name = f"{breadcrumb_prefix} > {fname}" if breadcrumb_prefix else fname

                try:
                    mtime = os.path.getmtime(full_path)
                except Exception:
                    mtime = 0.0

                results.append({
                    "name": display_name,
                    "breadcrumb_prefix": breadcrumb_prefix,
                    "file_name": fname,
                    "rel_path": rel_path.replace(os.sep, "/"),
                    "type": _detect_script_type(fname),
                    "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                    "last_edited_epoch": mtime
                })

    results.sort(key=lambda x: x.get("last_edited_epoch", 0.0), reverse=True)

    return jsonify({
        "root": scripts_root,
        "scripts": results,
        "folders": folders
    })


@app.route("/api/scripts/load", methods=["GET"])
def load_script():
    rel_path = request.args.get("path", "")
    scripts_root = _scripts_root()
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if (not abs_path.startswith(scripts_root)) or (not _is_valid_scripts_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "Script not found"}), 404
    try:
        with open(abs_path, "r", encoding="utf-8", errors="replace") as fh:
            content = fh.read()
        return jsonify({
            "file_name": os.path.basename(abs_path),
            "rel_path": os.path.relpath(abs_path, scripts_root).replace(os.sep, "/"),
            "type": _detect_script_type(abs_path),
            "content": content
        })
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/scripts/save", methods=["POST"])
def save_script():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    name = (data.get("name") or "").strip()
    content = data.get("content")
    script_type = (data.get("type") or "").strip().lower()

    if content is None:
        return jsonify({"error": "Missing content"}), 400

    scripts_root = _scripts_root()
    os.makedirs(scripts_root, exist_ok=True)

    # Determine target path
    if rel_path:
        # Append extension only if none provided
        base, ext = os.path.splitext(rel_path)
        if not ext:
            desired_ext = _ext_for_type(script_type)
            if desired_ext:
                rel_path = base + desired_ext
        abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
        if not _is_valid_scripts_relpath(rel_path):
            return jsonify({"error": "Invalid path (must be under 'Scripts')"}), 400
    else:
        if not name:
            return jsonify({"error": "Missing name"}), 400
        # Append extension only if none provided
        ext = os.path.splitext(name)[1]
        if not ext:
            desired_ext = _ext_for_type(script_type) or ".txt"
            name = os.path.splitext(name)[0] + desired_ext
        # Default top-level folder is Scripts only (Playbooks handled separately)
        if (script_type or "").lower() == "ansible":
            return jsonify({"error": "Ansible playbooks are managed separately from scripts."}), 400
        abs_path = os.path.abspath(os.path.join(scripts_root, "Scripts", os.path.basename(name)))

    if not abs_path.startswith(scripts_root):
        return jsonify({"error": "Invalid path"}), 400

    os.makedirs(os.path.dirname(abs_path), exist_ok=True)
    try:
        with open(abs_path, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(str(content))
        rel_new = os.path.relpath(abs_path, scripts_root).replace(os.sep, "/")
        return jsonify({"status": "ok", "rel_path": rel_new})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/scripts/rename_file", methods=["POST"])
def rename_script_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    script_type = (data.get("type") or "").strip().lower()
    scripts_root = _scripts_root()
    old_abs = os.path.abspath(os.path.join(scripts_root, rel_path))
    if not old_abs.startswith(scripts_root) or not os.path.isfile(old_abs):
        return jsonify({"error": "File not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    # Append extension only if none provided
    if not os.path.splitext(new_name)[1]:
        desired_ext = _ext_for_type(script_type)
        if desired_ext:
            new_name = os.path.splitext(new_name)[0] + desired_ext
    new_abs = os.path.join(os.path.dirname(old_abs), os.path.basename(new_name))
    try:
        os.rename(old_abs, new_abs)
        rel_new = os.path.relpath(new_abs, scripts_root).replace(os.sep, "/")
        return jsonify({"status": "ok", "rel_path": rel_new})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/scripts/move_file", methods=["POST"])
def move_script_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_rel = (data.get("new_path") or "").strip()
    scripts_root = _scripts_root()
    old_abs = os.path.abspath(os.path.join(scripts_root, rel_path))
    new_abs = os.path.abspath(os.path.join(scripts_root, new_rel))
    if not old_abs.startswith(scripts_root) or not os.path.isfile(old_abs):
        return jsonify({"error": "File not found"}), 404
    if (not new_abs.startswith(scripts_root)) or (not _is_valid_scripts_relpath(new_rel)):
        return jsonify({"error": "Invalid destination"}), 400
    os.makedirs(os.path.dirname(new_abs), exist_ok=True)
    try:
        shutil.move(old_abs, new_abs)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/scripts/delete_file", methods=["POST"])
def delete_script_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    scripts_root = _scripts_root()
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if (not abs_path.startswith(scripts_root)) or (not _is_valid_scripts_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "File not found"}), 404
    try:
        os.remove(abs_path)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500

# ---------------------------------------------
# Ansible Playbooks Storage API Endpoints
# ---------------------------------------------
def _ansible_root() -> str:
    return os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Assemblies", "Ansible_Playbooks")
    )


def _is_valid_ansible_relpath(rel_path: str) -> bool:
    try:
        p = (rel_path or "").replace("\\", "/").lstrip("/")
        # allow any subpath; prevent empty
        return bool(p)
    except Exception:
        return False


@app.route("/api/ansible/list", methods=["GET"])
def list_ansible():
    """Scan <ProjectRoot>/Assemblies/Ansible_Playbooks for .yml playbooks and return list + folders."""
    root = _ansible_root()
    results: List[Dict] = []
    folders: List[str] = []
    if not os.path.isdir(root):
        os.makedirs(root, exist_ok=True)
        return jsonify({ "root": root, "items": [], "folders": [] }), 200
    for r, dirs, files in os.walk(root):
        rel_root = os.path.relpath(r, root)
        if rel_root != ".":
            folders.append(rel_root.replace(os.sep, "/"))
        for fname in files:
            if not fname.lower().endswith(".yml"):
                continue
            full_path = os.path.join(r, fname)
            rel_path = os.path.relpath(full_path, root).replace(os.sep, "/")
            try:
                mtime = os.path.getmtime(full_path)
            except Exception:
                mtime = 0.0
            results.append({
                "file_name": fname,
                "rel_path": rel_path,
                "type": "ansible",
                "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                "last_edited_epoch": mtime
            })
    results.sort(key=lambda x: x.get("last_edited_epoch", 0.0), reverse=True)
    return jsonify({ "root": root, "items": results, "folders": folders })


@app.route("/api/ansible/load", methods=["GET"])
def load_ansible():
    rel_path = request.args.get("path", "")
    root = _ansible_root()
    abs_path = os.path.abspath(os.path.join(root, rel_path))
    if (not abs_path.startswith(root)) or (not _is_valid_ansible_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "Playbook not found"}), 404
    try:
        with open(abs_path, "r", encoding="utf-8", errors="replace") as fh:
            content = fh.read()
        return jsonify({
            "file_name": os.path.basename(abs_path),
            "rel_path": os.path.relpath(abs_path, root).replace(os.sep, "/"),
            "type": "ansible",
            "content": content
        })
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/ansible/save", methods=["POST"])
def save_ansible():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    name = (data.get("name") or "").strip()
    content = data.get("content")
    if content is None:
        return jsonify({"error": "Missing content"}), 400
    root = _ansible_root()
    os.makedirs(root, exist_ok=True)
    if rel_path:
        base, ext = os.path.splitext(rel_path)
        if not ext:
            rel_path = base + ".yml"
        abs_path = os.path.abspath(os.path.join(root, rel_path))
    else:
        if not name:
            return jsonify({"error": "Missing name"}), 400
        ext = os.path.splitext(name)[1]
        if not ext:
            name = os.path.splitext(name)[0] + ".yml"
        abs_path = os.path.abspath(os.path.join(root, os.path.basename(name)))
    if not abs_path.startswith(root):
        return jsonify({"error": "Invalid path"}), 400
    os.makedirs(os.path.dirname(abs_path), exist_ok=True)
    try:
        with open(abs_path, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(str(content))
        rel_new = os.path.relpath(abs_path, root).replace(os.sep, "/")
        return jsonify({"status": "ok", "rel_path": rel_new})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/ansible/rename_file", methods=["POST"])
def rename_ansible_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    root = _ansible_root()
    old_abs = os.path.abspath(os.path.join(root, rel_path))
    if not old_abs.startswith(root) or not os.path.isfile(old_abs):
        return jsonify({"error": "File not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    if not os.path.splitext(new_name)[1]:
        new_name = os.path.splitext(new_name)[0] + ".yml"
    new_abs = os.path.join(os.path.dirname(old_abs), os.path.basename(new_name))
    try:
        os.rename(old_abs, new_abs)
        rel_new = os.path.relpath(new_abs, root).replace(os.sep, "/")
        return jsonify({"status": "ok", "rel_path": rel_new})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/ansible/move_file", methods=["POST"])
def move_ansible_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_rel = (data.get("new_path") or "").strip()
    root = _ansible_root()
    old_abs = os.path.abspath(os.path.join(root, rel_path))
    new_abs = os.path.abspath(os.path.join(root, new_rel))
    if not old_abs.startswith(root) or not os.path.isfile(old_abs):
        return jsonify({"error": "File not found"}), 404
    if (not new_abs.startswith(root)) or (not _is_valid_ansible_relpath(new_rel)):
        return jsonify({"error": "Invalid destination"}), 400
    os.makedirs(os.path.dirname(new_abs), exist_ok=True)
    try:
        shutil.move(old_abs, new_abs)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/ansible/delete_file", methods=["POST"])
def delete_ansible_file():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    root = _ansible_root()
    abs_path = os.path.abspath(os.path.join(root, rel_path))
    if (not abs_path.startswith(root)) or (not _is_valid_ansible_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "File not found"}), 404
    try:
        os.remove(abs_path)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/ansible/create_folder", methods=["POST"])
def ansible_create_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    root = _ansible_root()
    rel_path = (rel_path or "").replace("\\", "/").strip("/")
    abs_path = os.path.abspath(os.path.join(root, rel_path))
    if not abs_path.startswith(root):
        return jsonify({"error": "Invalid path"}), 400
    try:
        os.makedirs(abs_path, exist_ok=True)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/ansible/delete_folder", methods=["POST"])
def ansible_delete_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    root = _ansible_root()
    abs_path = os.path.abspath(os.path.join(root, rel_path))
    if (not abs_path.startswith(root)) or (not _is_valid_ansible_relpath(rel_path)) or (not os.path.isdir(abs_path)):
        return jsonify({"error": "Folder not found"}), 404
    rel_norm = (rel_path or "").replace("\\", "/").strip("/")
    if rel_norm in ("",):
        return jsonify({"error": "Cannot delete top-level folder"}), 400
    try:
        shutil.rmtree(abs_path)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/ansible/rename_folder", methods=["POST"])
def ansible_rename_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    root = _ansible_root()
    old_abs = os.path.abspath(os.path.join(root, rel_path))
    if not old_abs.startswith(root) or not os.path.isdir(old_abs):
        return jsonify({"error": "Folder not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    rel_norm = (rel_path or "").replace("\\", "/").strip("/")
    if rel_norm in ("",):
        return jsonify({"error": "Cannot rename top-level folder"}), 400
    new_abs = os.path.join(os.path.dirname(old_abs), new_name)
    try:
        os.rename(old_abs, new_abs)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/scripts/create_folder", methods=["POST"])
def scripts_create_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    scripts_root = _scripts_root()
    # If caller provided a path that does not include a valid top-level,
    # default to creating under the "Scripts" top-level for convenience.
    if not _is_valid_scripts_relpath(rel_path):
        rel_path = os.path.join("Scripts", rel_path) if rel_path else "Scripts"
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if not abs_path.startswith(scripts_root):
        return jsonify({"error": "Invalid path"}), 400
    try:
        os.makedirs(abs_path, exist_ok=True)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/scripts/delete_folder", methods=["POST"])
def scripts_delete_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    scripts_root = _scripts_root()
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if (not abs_path.startswith(scripts_root)) or (not _is_valid_scripts_relpath(rel_path)) or (not os.path.isdir(abs_path)):
        return jsonify({"error": "Folder not found"}), 404
    rel_norm = (rel_path or "").replace("\\", "/").strip("/")
    if rel_norm in ("Scripts", "Ansible Playbooks"):
        return jsonify({"error": "Cannot delete top-level folder"}), 400
    try:
        shutil.rmtree(abs_path)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/scripts/rename_folder", methods=["POST"])
def scripts_rename_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    new_name = (data.get("new_name") or "").strip()
    scripts_root = _scripts_root()
    old_abs = os.path.abspath(os.path.join(scripts_root, rel_path))
    if not old_abs.startswith(scripts_root) or not os.path.isdir(old_abs):
        return jsonify({"error": "Folder not found"}), 404
    if not new_name:
        return jsonify({"error": "Invalid new_name"}), 400
    rel_norm = (rel_path or "").replace("\\", "/").strip("/")
    if rel_norm in ("Scripts", "Ansible Playbooks"):
        return jsonify({"error": "Cannot rename top-level folder"}), 400
    new_abs = os.path.join(os.path.dirname(old_abs), new_name)
    try:
        os.rename(old_abs, new_abs)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500

# ---------------------------------------------
# Borealis Agent API Endpoints
# ---------------------------------------------
# These endpoints handle agent registration, provisioning, image streaming, and heartbeats.
# Shape expected by UI for each agent:
# { "agent_id", "hostname", "agent_operating_system", "last_seen", "status" }
registered_agents: Dict[str, Dict] = {}
agent_configurations: Dict[str, Dict] = {}
latest_images: Dict[str, Dict] = {}

# Database initialization (merged into a single SQLite database)
DB_PATH = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", "database.db"))
os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)


def init_db():
    """Initialize all required tables in the unified database."""
    conn = _db_conn()
    cur = conn.cursor()

    # Device details table
    cur.execute(
        "CREATE TABLE IF NOT EXISTS device_details (hostname TEXT PRIMARY KEY, description TEXT, details TEXT, created_at INTEGER)"
    )
    # Backfill missing created_at column on existing installs
    try:
        cur.execute("PRAGMA table_info(device_details)")
        cols = [r[1] for r in cur.fetchall()]
        if 'created_at' not in cols:
            cur.execute("ALTER TABLE device_details ADD COLUMN created_at INTEGER")
    except Exception:
        pass

    # Activity history table for script/job runs
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS activity_history (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            hostname TEXT,
            script_path TEXT,
            script_name TEXT,
            script_type TEXT,
            ran_at INTEGER,
            status TEXT,
            stdout TEXT,
            stderr TEXT
        )
        """
    )

    # Saved device list views
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS device_list_views (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            columns_json TEXT NOT NULL,
            filters_json TEXT,
            created_at INTEGER,
            updated_at INTEGER
        )
        """
    )

    # Sites master table
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS sites (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            description TEXT,
            created_at INTEGER
        )
        """
    )

    # Device assignments. A device (hostname) can be assigned to at most one site.
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS device_sites (
            device_hostname TEXT UNIQUE NOT NULL,
            site_id INTEGER NOT NULL,
            assigned_at INTEGER,
            FOREIGN KEY(site_id) REFERENCES sites(id) ON DELETE CASCADE
        )
        """
    )

    # Users table
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            username TEXT UNIQUE NOT NULL,
            display_name TEXT,
            password_sha512 TEXT NOT NULL,
            role TEXT NOT NULL DEFAULT 'Admin',
            last_login INTEGER,
            created_at INTEGER,
            updated_at INTEGER
        )
        """
    )

    conn.commit()
    # Scheduled jobs table
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS scheduled_jobs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            components_json TEXT NOT NULL,
            targets_json TEXT NOT NULL,
            schedule_type TEXT NOT NULL,
            start_ts INTEGER,
            duration_stop_enabled INTEGER DEFAULT 0,
            expiration TEXT,
            execution_context TEXT NOT NULL,
            enabled INTEGER DEFAULT 1,
            created_at INTEGER,
            updated_at INTEGER
        )
        """
    )
    conn.commit()
    conn.close()


init_db()


def ensure_default_admin():
    """Ensure at least one admin user exists.

    If no user with role 'Admin' exists, create the default
    admin account (username 'admin', password 'Password').
    If an admin already exists, leave the user table untouched.
    """
    try:
        conn = _db_conn()
        cur = conn.cursor()

        # Check if any admin role exists (case-insensitive)
        cur.execute("SELECT COUNT(*) FROM users WHERE LOWER(role)='admin'")
        has_admin = (cur.fetchone()[0] or 0) > 0

        if not has_admin:
            now = _now_ts()
            default_hash = "e6c83b282aeb2e022844595721cc00bbda47cb24537c1779f9bb84f04039e1676e6ba8573e588da1052510e3aa0a32a9e55879ae22b0c2d62136fc0a3e85f8bb"

            # Prefer to (re)create the built-in 'admin' user if missing.
            # If a non-admin 'admin' user exists, promote it rather than failing insert.
            cur.execute("SELECT COUNT(*) FROM users WHERE LOWER(username)='admin'")
            admin_user_exists = (cur.fetchone()[0] or 0) > 0

            if not admin_user_exists:
                cur.execute(
                    "INSERT INTO users(username, display_name, password_sha512, role, created_at, updated_at) VALUES(?,?,?,?,?,?)",
                    ("admin", "Administrator", default_hash, "Admin", now, now)
                )
            else:
                # Promote existing 'admin' user to Admin if needed (preserve password)
                cur.execute(
                    "UPDATE users SET role='Admin', updated_at=? WHERE LOWER(username)='admin' AND LOWER(role)!='admin'",
                    (now,)
                )
            conn.commit()

        conn.close()
    except Exception:
        # Non-fatal if this fails; /health etc still work
        pass


ensure_default_admin()

# ---------------------------------------------
# Scheduler Registration
# ---------------------------------------------
job_scheduler = register_job_scheduler(app, socketio, DB_PATH)
job_scheduler.start()

# Provide scheduler with online device lookup based on registered agents
def _online_hostnames_snapshot():
    # Consider agent online if we saw collector activity within last 5 minutes
    try:
        now = time.time()
        out = []
        for rec in (registered_agents or {}).values():
            host = rec.get('hostname')
            last = float(rec.get('collector_active_ts') or 0)
            if host and (now - last) <= 300:
                out.append(str(host))
        return out
    except Exception:
        return []

scheduler_set_online_lookup(job_scheduler, _online_hostnames_snapshot)

# ---------------------------------------------
# Sites API
# ---------------------------------------------

def _row_to_site(row):
    # id, name, description, created_at, device_count
    return {
        "id": row[0],
        "name": row[1],
        "description": row[2] or "",
        "created_at": row[3] or 0,
        "device_count": row[4] or 0,
    }


@app.route("/api/sites", methods=["GET"])
def list_sites():
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            """
            SELECT s.id, s.name, s.description, s.created_at,
                   COALESCE(ds.cnt, 0) AS device_count
            FROM sites s
            LEFT JOIN (
                SELECT site_id, COUNT(*) AS cnt
                FROM device_sites
                GROUP BY site_id
            ) ds ON ds.site_id = s.id
            ORDER BY LOWER(s.name) ASC
            """
        )
        rows = cur.fetchall()
        conn.close()
        return jsonify({"sites": [_row_to_site(r) for r in rows]})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/sites", methods=["POST"])
def create_site():
    payload = request.get_json(silent=True) or {}
    name = (payload.get("name") or "").strip()
    description = (payload.get("description") or "").strip()
    if not name:
        return jsonify({"error": "name is required"}), 400
    now = int(time.time())
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "INSERT INTO sites(name, description, created_at) VALUES (?, ?, ?)",
            (name, description, now),
        )
        site_id = cur.lastrowid
        conn.commit()
        # Return created row with device_count = 0
        cur.execute(
            "SELECT id, name, description, created_at, 0 FROM sites WHERE id = ?",
            (site_id,),
        )
        row = cur.fetchone()
        conn.close()
        return jsonify(_row_to_site(row))
    except sqlite3.IntegrityError:
        return jsonify({"error": "name already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/sites/delete", methods=["POST"])
def delete_sites():
    payload = request.get_json(silent=True) or {}
    ids = payload.get("ids") or []
    if not isinstance(ids, list) or not all(isinstance(x, (int, str)) for x in ids):
        return jsonify({"error": "ids must be a list"}), 400
    # Normalize to ints where possible
    norm_ids = []
    for x in ids:
        try:
            norm_ids.append(int(x))
        except Exception:
            pass
    if not norm_ids:
        return jsonify({"status": "ok", "deleted": 0})
    try:
        conn = _db_conn()
        cur = conn.cursor()
        # Clean assignments first (in case FK ON DELETE CASCADE not enforced)
        cur.execute(
            f"DELETE FROM device_sites WHERE site_id IN ({','.join('?'*len(norm_ids))})",
            tuple(norm_ids),
        )
        cur.execute(
            f"DELETE FROM sites WHERE id IN ({','.join('?'*len(norm_ids))})",
            tuple(norm_ids),
        )
        deleted = cur.rowcount
        conn.commit()
        conn.close()
        return jsonify({"status": "ok", "deleted": deleted})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/sites/device_map", methods=["GET"])
def sites_device_map():
    """
    Map hostnames to assigned site.
    Optional query param: hostnames=comma,separated,list to filter.
    Returns: { mapping: { hostname: { site_id, site_name } } }
    """
    try:
        host_param = (request.args.get("hostnames") or "").strip()
        filter_set = set()
        if host_param:
            for part in host_param.split(','):
                p = part.strip()
                if p:
                    filter_set.add(p)
        conn = _db_conn()
        cur = conn.cursor()
        if filter_set:
            placeholders = ','.join('?' * len(filter_set))
            cur.execute(
                f"""
                SELECT ds.device_hostname, s.id, s.name
                FROM device_sites ds
                JOIN sites s ON s.id = ds.site_id
                WHERE ds.device_hostname IN ({placeholders})
                """,
                tuple(filter_set),
            )
        else:
            cur.execute(
                """
                SELECT ds.device_hostname, s.id, s.name
                FROM device_sites ds
                JOIN sites s ON s.id = ds.site_id
                """
            )
        mapping = {}
        for hostname, site_id, site_name in cur.fetchall():
            mapping[str(hostname)] = {"site_id": site_id, "site_name": site_name}
        conn.close()
        return jsonify({"mapping": mapping})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/sites/assign", methods=["POST"])
def assign_devices_to_site():
    payload = request.get_json(silent=True) or {}
    site_id = payload.get("site_id")
    hostnames = payload.get("hostnames") or []
    try:
        site_id = int(site_id)
    except Exception:
        return jsonify({"error": "invalid site_id"}), 400
    if not isinstance(hostnames, list) or not all(isinstance(x, str) and x.strip() for x in hostnames):
        return jsonify({"error": "hostnames must be a list of strings"}), 400
    now = int(time.time())
    try:
        conn = _db_conn()
        cur = conn.cursor()
        # Ensure site exists
        cur.execute("SELECT 1 FROM sites WHERE id = ?", (site_id,))
        if not cur.fetchone():
            conn.close()
            return jsonify({"error": "site not found"}), 404
        # Assign each hostname (replace existing assignment if present)
        for hn in hostnames:
            hn = hn.strip()
            if not hn:
                continue
            cur.execute(
                "INSERT INTO device_sites(device_hostname, site_id, assigned_at) VALUES (?, ?, ?)\n"
                "ON CONFLICT(device_hostname) DO UPDATE SET site_id=excluded.site_id, assigned_at=excluded.assigned_at",
                (hn, site_id, now),
            )
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# Rename a site (update name only)
@app.route("/api/sites/rename", methods=["POST"])
def rename_site():
    payload = request.get_json(silent=True) or {}
    site_id = payload.get("id")
    new_name = (payload.get("new_name") or "").strip()
    try:
        site_id = int(site_id)
    except Exception:
        return jsonify({"error": "invalid id"}), 400
    if not new_name:
        return jsonify({"error": "new_name is required"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute("UPDATE sites SET name = ? WHERE id = ?", (new_name, site_id))
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "site not found"}), 404
        conn.commit()
        cur.execute(
            """
            SELECT s.id, s.name, s.description, s.created_at,
                   COALESCE(ds.cnt, 0) AS device_count
            FROM sites s
            LEFT JOIN (
                SELECT site_id, COUNT(*) AS cnt
                FROM device_sites
                GROUP BY site_id
            ) ds ON ds.site_id = s.id
            WHERE s.id = ?
            """,
            (site_id,)
        )
        row = cur.fetchone()
        conn.close()
        return jsonify(_row_to_site(row))
    except sqlite3.IntegrityError:
        return jsonify({"error": "name already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# ---------------------------------------------
# Global Search (suggestions)
# ---------------------------------------------

def _load_device_records(limit: int = 0):
    """
    Load device records from SQLite and flatten commonly-searched fields
    from the JSON details column. Returns a list of dicts with keys:
      hostname, description, last_user, internal_ip, external_ip, site_id, site_name
    """
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute("SELECT hostname, description, details FROM device_details")
        rows = cur.fetchall()

        # Build device -> site mapping
        cur.execute(
            """
            SELECT ds.device_hostname, s.id, s.name
            FROM device_sites ds
            JOIN sites s ON s.id = ds.site_id
            """
        )
        site_map = {r[0]: {"site_id": r[1], "site_name": r[2]} for r in cur.fetchall()}

        conn.close()
    except Exception:
        rows = []
        site_map = {}

    out = []
    for hostname, description, details_json in rows:
        d = {}
        try:
            d = json.loads(details_json or "{}")
        except Exception:
            d = {}
        summary = d.get("summary") or {}
        rec = {
            "hostname": hostname or summary.get("hostname") or "",
            "description": (description or summary.get("description") or ""),
            "last_user": summary.get("last_user") or summary.get("last_user_name") or "",
            "internal_ip": summary.get("internal_ip") or "",
            "external_ip": summary.get("external_ip") or "",
        }
        site_info = site_map.get(rec["hostname"]) or {}
        rec.update({
            "site_id": site_info.get("site_id"),
            "site_name": site_info.get("site_name") or "",
        })
        out.append(rec)
        if limit and len(out) >= limit:
            break
    return out


@app.route("/api/search/suggest", methods=["GET"])
def search_suggest():
    """
    Suggest results for the top-bar search with category selector.
    Query parameters:
      field: one of hostname|description|last_user|internal_ip|external_ip|serial_number|site_name|site_description
      q: text fragment (case-insensitive contains)
      limit: max results per group (default 5)
    Returns: { devices: [...], sites: [...], field: "...", q: "..." }
    """
    field = (request.args.get("field") or "hostname").strip().lower()
    q = (request.args.get("q") or "").strip()
    try:
        limit = int(request.args.get("limit") or 5)
    except Exception:
        limit = 5

    q_lc = q.lower()
    # Do not suggest on very short queries to avoid dumping all rows
    if len(q_lc) < 3:
        return jsonify({"field": field, "q": q, "devices": [], "sites": []})

    device_fields = {
        "hostname": "hostname",
        "description": "description",
        "last_user": "last_user",
        "internal_ip": "internal_ip",
        "external_ip": "external_ip",
        "serial_number": "serial_number",  # placeholder, currently not stored
    }
    site_fields = {
        "site_name": "name",
        "site_description": "description",
    }

    devices = []
    sites = []

    # Device suggestions
    if field in device_fields:
        key = device_fields[field]
        for rec in _load_device_records():
            # serial_number is not currently tracked; produce no suggestions
            if key == "serial_number":
                break
            val = str(rec.get(key) or "")
            if not q or q_lc in val.lower():
                devices.append({
                    "hostname": rec.get("hostname") or "",
                    "value": val,
                    "site_id": rec.get("site_id"),
                    "site_name": rec.get("site_name") or "",
                    "description": rec.get("description") or "",
                    "last_user": rec.get("last_user") or "",
                    "internal_ip": rec.get("internal_ip") or "",
                    "external_ip": rec.get("external_ip") or "",
                })
            if len(devices) >= limit:
                break

    # Site suggestions
    if field in site_fields:
        column = site_fields[field]
        try:
            conn = _db_conn()
            cur = conn.cursor()
            cur.execute("SELECT id, name, description FROM sites")
            for sid, name, desc in cur.fetchall():
                val = name if column == "name" else (desc or "")
                if not q or q_lc in str(val).lower():
                    sites.append({
                        "site_id": sid,
                        "site_name": name,
                        "site_description": desc or "",
                        "value": val or "",
                    })
                if len(sites) >= limit:
                    break
            conn.close()
        except Exception:
            pass

    return jsonify({
        "field": field,
        "q": q,
        "devices": devices,
        "sites": sites,
    })


# ---------------------------------------------
# Device List Views API
# ---------------------------------------------
def _row_to_view(row):
    return {
        "id": row[0],
        "name": row[1],
        "columns": json.loads(row[2] or "[]"),
        "filters": json.loads(row[3] or "{}"),
        "created_at": row[4],
        "updated_at": row[5],
    }


@app.route("/api/device_list_views", methods=["GET"])
def list_device_list_views():
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views ORDER BY name COLLATE NOCASE ASC"
        )
        rows = cur.fetchall()
        conn.close()
        return jsonify({"views": [_row_to_view(r) for r in rows]})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/device_list_views/<int:view_id>", methods=["GET"])
def get_device_list_view(view_id: int):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views WHERE id = ?",
            (view_id,),
        )
        row = cur.fetchone()
        conn.close()
        if not row:
            return jsonify({"error": "not found"}), 404
        return jsonify(_row_to_view(row))
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/device_list_views", methods=["POST"])
def create_device_list_view():
    payload = request.get_json(silent=True) or {}
    name = (payload.get("name") or "").strip()
    columns = payload.get("columns") or []
    filters = payload.get("filters") or {}

    if not name:
        return jsonify({"error": "name is required"}), 400
    if name.lower() == "default view":
        return jsonify({"error": "reserved name"}), 400
    if not isinstance(columns, list) or not all(isinstance(x, str) for x in columns):
        return jsonify({"error": "columns must be a list of strings"}), 400
    if not isinstance(filters, dict):
        return jsonify({"error": "filters must be an object"}), 400

    now = int(time.time())
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "INSERT INTO device_list_views(name, columns_json, filters_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
            (name, json.dumps(columns), json.dumps(filters), now, now),
        )
        vid = cur.lastrowid
        conn.commit()
        cur.execute(
            "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views WHERE id = ?",
            (vid,),
        )
        row = cur.fetchone()
        conn.close()
        return jsonify(_row_to_view(row)), 201
    except sqlite3.IntegrityError:
        return jsonify({"error": "name already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/device_list_views/<int:view_id>", methods=["PUT"])
def update_device_list_view(view_id: int):
    payload = request.get_json(silent=True) or {}
    name = payload.get("name")
    columns = payload.get("columns")
    filters = payload.get("filters")
    if name is not None:
        name = (name or "").strip()
        if not name:
            return jsonify({"error": "name cannot be empty"}), 400
        if name.lower() == "default view":
            return jsonify({"error": "reserved name"}), 400
    if columns is not None:
        if not isinstance(columns, list) or not all(isinstance(x, str) for x in columns):
            return jsonify({"error": "columns must be a list of strings"}), 400
    if filters is not None and not isinstance(filters, dict):
        return jsonify({"error": "filters must be an object"}), 400

    fields = []
    params = []
    if name is not None:
        fields.append("name = ?")
        params.append(name)
    if columns is not None:
        fields.append("columns_json = ?")
        params.append(json.dumps(columns))
    if filters is not None:
        fields.append("filters_json = ?")
        params.append(json.dumps(filters))
    fields.append("updated_at = ?")
    params.append(int(time.time()))
    params.append(view_id)

    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(f"UPDATE device_list_views SET {', '.join(fields)} WHERE id = ?", params)
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "not found"}), 404
        conn.commit()
        cur.execute(
            "SELECT id, name, columns_json, filters_json, created_at, updated_at FROM device_list_views WHERE id = ?",
            (view_id,),
        )
        row = cur.fetchone()
        conn.close()
        return jsonify(_row_to_view(row))
    except sqlite3.IntegrityError:
        return jsonify({"error": "name already exists"}), 409
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/device_list_views/<int:view_id>", methods=["DELETE"])
def delete_device_list_view(view_id: int):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute("DELETE FROM device_list_views WHERE id = ?", (view_id,))
        if cur.rowcount == 0:
            conn.close()
            return jsonify({"error": "not found"}), 404
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


def _persist_last_seen(hostname: str, last_seen: int, agent_id: str = None):
    """Persist last_seen (and agent_id if provided) into device_details.details JSON.

    Ensures that after a server restart, we can restore last_seen from DB even
    if the agent is offline, and helps merge entries by keeping track of the
    last known agent_id for a hostname.
    """
    if not hostname or str(hostname).strip().lower() == "unknown":
        return
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT details, description, created_at FROM device_details WHERE hostname = ?",
            (hostname,),
        )
        row = cur.fetchone()
        if row and row[0]:
            try:
                details = json.loads(row[0])
            except Exception:
                details = {}
            description = row[1] or ""
            created_at = int(row[2] or 0)
        else:
            details = {}
            description = ""
            created_at = 0

        summary = details.get("summary") or {}
        summary["hostname"] = summary.get("hostname") or hostname
        try:
            summary["last_seen"] = int(last_seen or 0)
        except Exception:
            summary["last_seen"] = int(time.time())
        if agent_id:
            try:
                summary["agent_id"] = str(agent_id)
            except Exception:
                pass
        details["summary"] = summary

        now = int(time.time())
        # Ensure 'created' string aligns with created_at we will store
        target_created_at = created_at or now
        try:
            from datetime import datetime, timezone
            human = datetime.fromtimestamp(target_created_at, timezone.utc).strftime('%Y-%m-%d %H:%M:%S')
            details.setdefault('summary', {})['created'] = details.get('summary', {}).get('created') or human
        except Exception:
            pass

        # Single upsert to avoid unique-constraint races
        cur.execute(
            """
            INSERT INTO device_details(hostname, description, details, created_at)
            VALUES (?, ?, ?, ?)
            ON CONFLICT(hostname) DO UPDATE SET
                description=excluded.description,
                details=excluded.details,
                created_at=COALESCE(device_details.created_at, excluded.created_at)
            """,
            (hostname, description, json.dumps(details), target_created_at),
        )
        conn.commit()
        conn.close()
    except Exception as e:
        print(f"[WARN] Failed to persist last_seen for {hostname}: {e}")


def load_agents_from_db():
    """Populate registered_agents with any devices stored in the database."""
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute("SELECT hostname, details FROM device_details")
        for hostname, details_json in cur.fetchall():
            try:
                details = json.loads(details_json or "{}")
            except Exception:
                details = {}
            summary = details.get("summary", {})
            agent_id = summary.get("agent_id") or hostname
            registered_agents[agent_id] = {
                "agent_id": agent_id,
                "hostname": summary.get("hostname") or hostname,
                "agent_operating_system": summary.get("operating_system")
                or summary.get("agent_operating_system")
                or "-",
                "device_type": summary.get("device_type") or "",
                "last_seen": summary.get("last_seen") or 0,
                "status": "Offline",
            }
        conn.close()
    except Exception as e:
        print(f"[WARN] Failed to load agents from DB: {e}")


load_agents_from_db()

@app.route("/api/agents")
def get_agents():
    """Return agents with collector activity indicator."""
    now = time.time()
    # Collapse duplicates by hostname; prefer newer last_seen and non-script entries
    seen_by_hostname = {}
    for aid, info in (registered_agents or {}).items():
        # Hide script-execution agents from the public list
        if aid and isinstance(aid, str) and aid.lower().endswith('-script'):
            continue
        if info.get('is_script_agent'):
            continue
        d = dict(info)
        ts = d.get('collector_active_ts') or 0
        d['collector_active'] = bool(ts and (now - float(ts) < 130))
        host = (d.get('hostname') or '').strip() or 'unknown'
        # Select best record per hostname: highest last_seen wins
        cur = seen_by_hostname.get(host)
        if not cur or int(d.get('last_seen') or 0) >= int(cur[1].get('last_seen') or 0):
            seen_by_hostname[host] = (aid, d)
    out = { aid: d for host, (aid, d) in seen_by_hostname.items() }
    return jsonify(out)


"""Scheduled Jobs API moved to Data/Server/job_scheduler.py"""


## dayjs_to_ts removed; scheduling parsing now lives in job_scheduler


def _is_empty(v):
    return v is None or v == '' or v == [] or v == {}


def _deep_merge_preserve(prev: dict, incoming: dict) -> dict:
    out = dict(prev or {})
    for k, v in (incoming or {}).items():
        if isinstance(v, dict):
            out[k] = _deep_merge_preserve(out.get(k) or {}, v)
        elif isinstance(v, list):
            # Only replace list if incoming has content; else keep prev
            if v:
                out[k] = v
        else:
            # Keep previous non-empty values when incoming is empty
            if _is_empty(v):
                # do not overwrite
                continue
            out[k] = v
    return out


@app.route("/api/agent/details", methods=["POST"])
def save_agent_details():
    data = request.get_json(silent=True) or {}
    hostname = data.get("hostname")
    details = data.get("details")
    agent_id = data.get("agent_id")
    if not hostname and isinstance(details, dict):
        hostname = (details.get("summary") or {}).get("hostname")
    if not hostname or not isinstance(details, dict):
        return jsonify({"error": "invalid payload"}), 400
    try:
        conn = _db_conn()
        cur = conn.cursor()
        # Load existing row to preserve description and created_at and merge fields
        cur.execute(
            "SELECT details, description, created_at FROM device_details WHERE hostname = ?",
            (hostname,),
        )
        row = cur.fetchone()
        prev_details = {}
        description = ""
        created_at = 0
        if row:
            try:
                prev_details = json.loads(row[0] or '{}')
            except Exception:
                prev_details = {}
            description = row[1] or ""
            try:
                created_at = int(row[2] or 0)
            except Exception:
                created_at = 0

        # Ensure summary exists and attach hostname/agent_id if missing
        incoming_summary = details.setdefault("summary", {})
        if agent_id and not incoming_summary.get("agent_id"):
            try:
                incoming_summary["agent_id"] = str(agent_id)
            except Exception:
                pass
        if hostname and not incoming_summary.get("hostname"):
            incoming_summary["hostname"] = hostname

        # Preserve last_seen if incoming omitted it
        if not incoming_summary.get("last_seen"):
            last_seen = None
            if agent_id and agent_id in registered_agents:
                last_seen = registered_agents[agent_id].get("last_seen")
            if last_seen is None:
                last_seen = (prev_details.get("summary") or {}).get("last_seen")
            if last_seen is not None:
                try:
                    incoming_summary["last_seen"] = int(last_seen)
                except Exception:
                    pass

        # Deep-merge incoming over previous, but do not overwrite with empties
        merged = _deep_merge_preserve(prev_details, details)

        # Preserve last_user if incoming omitted/empty
        try:
            prev_last_user = (prev_details.get('summary') or {}).get('last_user')
            cur_last_user = (merged.get('summary') or {}).get('last_user')
            if _is_empty(cur_last_user) and prev_last_user:
                merged.setdefault('summary', {})['last_user'] = prev_last_user
        except Exception:
            pass

        # Refresh server-side in-memory registry for OS and device type
        try:
            if agent_id and agent_id in registered_agents:
                rec = registered_agents[agent_id]
                os_name = (merged.get("summary") or {}).get("operating_system") or (merged.get("summary") or {}).get("agent_operating_system")
                if os_name:
                    rec["agent_operating_system"] = os_name
                dt = ((merged.get("summary") or {}).get("device_type") or "").strip()
                if dt:
                    rec["device_type"] = dt
        except Exception:
            pass

        now = int(time.time())
        # Ensure created_at is set on first insert and mirror into merged.summary.created as human string
        if created_at <= 0:
            created_at = now
        try:
            from datetime import datetime, timezone
            human = datetime.fromtimestamp(created_at, timezone.utc).strftime('%Y-%m-%d %H:%M:%S')
            merged.setdefault('summary', {})
            if not merged['summary'].get('created'):
                merged['summary']['created'] = human
        except Exception:
            pass

        # Upsert row without destroying created_at; keep previous created_at if exists
        cur.execute(
            """
            INSERT INTO device_details(hostname, description, details, created_at)
            VALUES (?,?,?,?)
            ON CONFLICT(hostname) DO UPDATE SET
                description=excluded.description,
                details=excluded.details,
                created_at=COALESCE(device_details.created_at, excluded.created_at)
            """,
            (hostname, description, json.dumps(merged), created_at),
        )
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/device/details/<hostname>", methods=["GET"])
def get_device_details(hostname: str):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT details, description, created_at FROM device_details WHERE hostname = ?",
            (hostname,),
        )
        row = cur.fetchone()
        conn.close()
        if row:
            try:
                details = json.loads(row[0])
            except Exception:
                details = {}
            description = row[1] if len(row) > 1 else ""
            created_at = int(row[2] or 0) if len(row) > 2 else 0
            if description:
                details.setdefault("summary", {})["description"] = description
            # Ensure created string exists from created_at
            try:
                if created_at and not (details.get('summary') or {}).get('created'):
                    from datetime import datetime
                    details.setdefault('summary', {})['created'] = datetime.utcfromtimestamp(created_at).strftime('%Y-%m-%d %H:%M:%S')
            except Exception:
                pass
            return jsonify(details)
    except Exception:
        pass
    return jsonify({})


@app.route("/api/device/description/<hostname>", methods=["POST"])
def set_device_description(hostname: str):
    data = request.get_json(silent=True) or {}
    description = (data.get("description") or "").strip()
    try:
        conn = _db_conn()
        cur = conn.cursor()
        now = int(time.time())
        # Insert row if missing with created_at; otherwise update description only
        cur.execute(
            "INSERT INTO device_details(hostname, description, details, created_at) "
            "VALUES (?, COALESCE(?, ''), COALESCE((SELECT details FROM device_details WHERE hostname = ?), '{}'), ?) "
            "ON CONFLICT(hostname) DO NOTHING",
            (hostname, description, hostname, now),
        )
        cur.execute(
            "UPDATE device_details SET description=? WHERE hostname=?",
            (description, hostname),
        )
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# ---------------------------------------------
# Quick Job Execution + Activity History
# ---------------------------------------------
def _detect_script_type(fn: str) -> str:
    fn = (fn or "").lower()
    if fn.endswith(".yml"):
        return "ansible"
    if fn.endswith(".ps1"):
        return "powershell"
    if fn.endswith(".bat"):
        return "batch"
    if fn.endswith(".sh"):
        return "bash"
    return "unknown"


def _safe_filename(rel_path: str) -> str:
    try:
        return os.path.basename(rel_path or "")
    except Exception:
        return rel_path or ""


@app.route("/api/scripts/quick_run", methods=["POST"])
def scripts_quick_run():
    """Queue a Quick Job to agents via WebSocket and record Running status.

    Payload: { script_path: str, hostnames: [str], run_mode?: 'current_user'|'admin'|'system', admin_user?, admin_pass? }
    """
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("script_path") or "").strip()
    hostnames = data.get("hostnames") or []
    run_mode = (data.get("run_mode") or "system").strip().lower()
    admin_user = ""
    admin_pass = ""

    if not rel_path or not isinstance(hostnames, list) or not hostnames:
        return jsonify({"error": "Missing script_path or hostnames[]"}), 400

    scripts_root = _scripts_root()
    abs_path = os.path.abspath(os.path.join(scripts_root, rel_path))
    if (not abs_path.startswith(scripts_root)) or (not _is_valid_scripts_relpath(rel_path)) or (not os.path.isfile(abs_path)):
        return jsonify({"error": "Script not found"}), 404

    script_type = _detect_script_type(abs_path)
    if script_type != "powershell":
        return jsonify({"error": f"Unsupported script type '{script_type}'. Only powershell is supported for Quick Job currently."}), 400

    try:
        with open(abs_path, "r", encoding="utf-8", errors="replace") as fh:
            content = fh.read()
    except Exception as e:
        return jsonify({"error": f"Failed to read script: {e}"}), 500

    now = int(time.time())
    results = []
    for host in hostnames:
        job_id = None
        try:
            conn = _db_conn()
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO activity_history(hostname, script_path, script_name, script_type, ran_at, status, stdout, stderr)
                VALUES(?,?,?,?,?,?,?,?)
                """,
                (
                    host,
                    rel_path.replace(os.sep, "/"),
                    _safe_filename(rel_path),
                    script_type,
                    now,
                    "Running",
                    "",
                    "",
                ),
            )
            job_id = cur.lastrowid
            conn.commit()
            conn.close()
        except Exception as db_err:
            return jsonify({"error": f"DB insert failed: {db_err}"}), 500

        payload = {
            "job_id": job_id,
            "target_hostname": host,
            "script_type": script_type,
            "script_name": _safe_filename(rel_path),
            "script_path": rel_path.replace(os.sep, "/"),
            "script_content": content,
            "run_mode": run_mode,
            "admin_user": admin_user,
            "admin_pass": admin_pass,
        }
        # Broadcast to all connected clients; no broadcast kw in python-socketio v5
        socketio.emit("quick_job_run", payload)
        results.append({"hostname": host, "job_id": job_id, "status": "Running"})

    return jsonify({"results": results})


@app.route("/api/device/activity/<hostname>", methods=["GET", "DELETE"])
def device_activity(hostname: str):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        if request.method == "DELETE":
            cur.execute("DELETE FROM activity_history WHERE hostname = ?", (hostname,))
            conn.commit()
            conn.close()
            return jsonify({"status": "ok"})

        cur.execute(
            "SELECT id, script_name, script_path, script_type, ran_at, status, LENGTH(stdout), LENGTH(stderr) FROM activity_history WHERE hostname = ? ORDER BY ran_at DESC, id DESC",
            (hostname,),
        )
        rows = cur.fetchall()
        conn.close()
        out = []
        for (jid, name, path, stype, ran_at, status, so_len, se_len) in rows:
            out.append({
                "id": jid,
                "script_name": name,
                "script_path": path,
                "script_type": stype,
                "ran_at": ran_at,
                "status": status,
                "has_stdout": bool(so_len or 0),
                "has_stderr": bool(se_len or 0),
            })
        return jsonify({"history": out})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/device/activity/job/<int:job_id>", methods=["GET"])
def device_activity_job(job_id: int):
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "SELECT id, hostname, script_name, script_path, script_type, ran_at, status, stdout, stderr FROM activity_history WHERE id = ?",
            (job_id,),
        )
        row = cur.fetchone()
        conn.close()
        if not row:
            return jsonify({"error": "Not found"}), 404
        (jid, hostname, name, path, stype, ran_at, status, stdout, stderr) = row
        return jsonify({
            "id": jid,
            "hostname": hostname,
            "script_name": name,
            "script_path": path,
            "script_type": stype,
            "ran_at": ran_at,
            "status": status,
            "stdout": stdout or "",
            "stderr": stderr or "",
        })
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@socketio.on("quick_job_result")
def handle_quick_job_result(data):
    """Agent reports back stdout/stderr/status for a job."""
    try:
        job_id = int(data.get("job_id"))
    except Exception:
        return
    status = (data.get("status") or "").strip() or "Failed"
    stdout = data.get("stdout") or ""
    stderr = data.get("stderr") or ""
    try:
        conn = _db_conn()
        cur = conn.cursor()
        cur.execute(
            "UPDATE activity_history SET status=?, stdout=?, stderr=? WHERE id=?",
            (status, stdout, stderr, job_id),
        )
        conn.commit()
        conn.close()
    except Exception as e:
        print(f"[ERROR] quick_job_result DB update failed for job {job_id}: {e}")


@socketio.on("collector_status")
def handle_collector_status(data):
    """Collector agent reports activity and optional last_user.

    To avoid flapping of summary.last_user between the SYSTEM service and the
    interactive user helper, we only accept last_user updates that look like a
    real interactive user and, by preference, only from the interactive agent
    (agent_id ending with "-script"). Machine accounts (..$) and built-in
    service principals (SYSTEM/LOCAL SERVICE/NETWORK SERVICE) are ignored.
    """
    agent_id = (data or {}).get('agent_id')
    hostname = (data or {}).get('hostname')
    active = bool((data or {}).get('active'))
    last_user = (data or {}).get('last_user')
    if not agent_id:
        return

    rec = registered_agents.setdefault(agent_id, {})
    rec['agent_id'] = agent_id
    if hostname:
        rec['hostname'] = hostname
    if active:
        rec['collector_active_ts'] = time.time()

    # Helper: decide if a reported user string is a real interactive user
    def _is_valid_interactive_user(s: str) -> bool:
        try:
            if not s:
                return False
            t = str(s).strip()
            if not t:
                return False
            # Reject machine accounts and well-known service identities
            upper = t.upper()
            if t.endswith('$'):
                return False
            if any(x in upper for x in ('NT AUTHORITY\\', 'NT SERVICE\\')):
                return False
            if upper.endswith('\\SYSTEM') or upper.endswith('\\LOCAL SERVICE') or upper.endswith('\\NETWORK SERVICE') or upper == 'ANONYMOUS LOGON':
                return False
            # Looks acceptable (DOMAIN\\user or user)
            return True
        except Exception:
            return False

    # Prefer interactive/script agent as the source of truth for last_user
    is_script_agent = False
    try:
        is_script_agent = bool((isinstance(agent_id, str) and agent_id.lower().endswith('-script')) or rec.get('is_script_agent'))
    except Exception:
        is_script_agent = False

    # If we have a usable last_user and a hostname, persist it
    if last_user and _is_valid_interactive_user(last_user) and (hostname or rec.get('hostname')):
        # If this event is coming from the SYSTEM service agent, ignore it to
        # prevent clobbering the interactive user's value.
        try:
            if isinstance(agent_id, str) and ('-svc-' in agent_id.lower() or agent_id.lower().endswith('-svc')) and not is_script_agent:
                return
        except Exception:
            pass
        try:
            host = hostname or rec.get('hostname')
            conn = _db_conn()
            cur = conn.cursor()
            cur.execute(
                "SELECT details, description, created_at FROM device_details WHERE hostname = ?",
                (host,),
            )
            row = cur.fetchone()
            if row and row[0]:
                try:
                    details = json.loads(row[0])
                except Exception:
                    details = {}
                description = row[1] or ""
                created_at = int(row[2] or 0)
            else:
                details = {}
                description = ""
                created_at = 0
            summary = details.get('summary') or {}
            # Only update last_user if provided; do not clear other fields
            summary['last_user'] = last_user
            details['summary'] = summary
            now = int(time.time())
            cur.execute(
                """
                INSERT INTO device_details(hostname, description, details, created_at)
                VALUES (?,?,?,?)
                ON CONFLICT(hostname) DO UPDATE SET
                    description=excluded.description,
                    details=excluded.details,
                    created_at=COALESCE(device_details.created_at, excluded.created_at)
                """,
                (host, description, json.dumps(details), created_at or now),
            )
            conn.commit()
            conn.close()
        except Exception:
            pass


@app.route("/api/agent/<agent_id>", methods=["DELETE"])
def delete_agent(agent_id: str):
    """Remove an agent from the registry and database."""
    info = registered_agents.pop(agent_id, None)
    agent_configurations.pop(agent_id, None)
    # IMPORTANT: Do NOT delete device_details here. Multiple in-memory agent
    # records can refer to the same hostname; removing one should not wipe the
    # persisted device inventory for the hostname. A dedicated endpoint can be
    # added later to purge device_details by hostname if needed.
    if info:
        return jsonify({"status": "removed"})
    return jsonify({"error": "agent not found"}), 404

@app.route("/api/agent/provision", methods=["POST"])
def provision_agent():
    data = request.json
    agent_id = data.get("agent_id")
    roles = data.get("roles", [])

    if not agent_id or not isinstance(roles, list):
        return jsonify({"error": "Missing agent_id or roles[] in provision payload."}), 400

    config = {"roles": roles}
    agent_configurations[agent_id] = config

    if agent_id in registered_agents:
        registered_agents[agent_id]["status"] = "provisioned"

    # Target only the intended agent by emitting to its room
    try:
        socketio.emit("agent_config", {**config, "agent_id": agent_id}, room=agent_id)
    except TypeError:
        # Compatibility with older flask-socketio versions that use 'to'
        socketio.emit("agent_config", {**config, "agent_id": agent_id}, to=agent_id)
    return jsonify({"status": "provisioned", "roles": roles})

# ---------------------------------------------
# Borealis External API Proxy Endpoint
# ---------------------------------------------
@app.route("/api/proxy", methods=["GET", "POST", "OPTIONS"])
def proxy():
    target = request.args.get("url")
    if not target:
        return {"error": "Missing ?url="}, 400

    upstream = requests.request(
        method  = request.method,
        url     = target,
        headers = {k:v for k,v in request.headers if k.lower() != "host"},
        data    = request.get_data(),
        timeout = 10
    )

    excluded = ["content-encoding","content-length","transfer-encoding","connection"]
    headers  = [(k,v) for k,v in upstream.raw.headers.items() if k.lower() not in excluded]

    resp = make_response(upstream.content, upstream.status_code)
    for k,v in headers:
        resp.headers[k] = v
    return resp

# ---------------------------------------------
# Live Screenshot Viewer for Debugging
# ---------------------------------------------
@app.route("/api/agent/<agent_id>/node/<node_id>/screenshot/live")
def screenshot_node_viewer(agent_id, node_id):
    return f"""
    <!DOCTYPE html>
    <html>
    <head>
        <title>Borealis Live View - {agent_id}:{node_id}</title>
        <style>
            body {{
                margin: 0;
                background-color: #000;
                display: flex;
                align-items: center;
                justify-content: center;
                height: 100vh;
            }}
            canvas {{
                border: 1px solid #444;
                max-width: 90vw;
                max-height: 90vh;
                background-color: #111;
            }}
        </style>
    </head>
    <body>
        <canvas id="viewerCanvas"></canvas>
        <script src="https://cdn.socket.io/4.8.1/socket.io.min.js"></script>
        <script>
            const agentId = "{agent_id}";
            const nodeId = "{node_id}";
            const canvas = document.getElementById("viewerCanvas");
            const ctx = canvas.getContext("2d");
            const socket = io(window.location.origin, {{ transports: ["websocket"] }});

            socket.on("agent_screenshot_task", (data) => {{
                if (data.agent_id !== agentId || data.node_id !== nodeId) return;
                const base64 = data.image_base64;
                if (!base64 || base64.length < 100) return;

                const img = new Image();
                img.onload = () => {{
                    if (canvas.width !== img.width || canvas.height !== img.height) {{
                        canvas.width = img.width;
                        canvas.height = img.height;
                    }}
                    ctx.clearRect(0, 0, canvas.width, canvas.height);
                    ctx.drawImage(img, 0, 0);
                }};
                img.src = "data:image/png;base64," + base64;
            }});
        </script>
    </body>
    </html>
    """

# ---------------------------------------------
# WebSocket Events for Real-Time Communication
# ---------------------------------------------
@socketio.on("agent_screenshot_task")
def receive_screenshot_task(data):
    agent_id = data.get("agent_id")
    node_id = data.get("node_id")
    image = data.get("image_base64", "")
    
    if not agent_id or not node_id:
        print("[WS] Screenshot task missing agent_id or node_id.")
        return

    if image:
        latest_images[f"{agent_id}:{node_id}"] = {
            "image_base64": image,
            "timestamp": time.time()
        }

    # Relay to all connected clients; use server-level emit
    socketio.emit("agent_screenshot_task", data)

@socketio.on("connect_agent")
def connect_agent(data):
    """
    Initial agent connect. Agent may only send agent_id here.
    Hostname/OS are filled in by subsequent heartbeats.
    """
    agent_id = (data or {}).get("agent_id")
    if not agent_id:
        return
    print(f"Agent connected: {agent_id}")

    # Join per-agent room so we can address this connection specifically
    try:
        join_room(agent_id)
    except Exception:
        pass

    rec = registered_agents.setdefault(agent_id, {})
    rec["agent_id"] = agent_id
    rec["hostname"] = rec.get("hostname", "unknown")
    rec["agent_operating_system"] = rec.get("agent_operating_system", "-")
    rec["last_seen"] = int(time.time())
    rec["status"] = "provisioned" if agent_id in agent_configurations else "orphaned"
    # Flag script agents so they can be filtered out elsewhere if desired
    try:
        if isinstance(agent_id, str) and agent_id.lower().endswith('-script'):
            rec['is_script_agent'] = True
    except Exception:
        pass
    # If we already know the hostname for this agent, persist last_seen so it
    # can be restored after server restarts.
    try:
        _persist_last_seen(rec.get("hostname"), rec["last_seen"], rec.get("agent_id"))
    except Exception:
        pass

@socketio.on("agent_heartbeat")
def on_agent_heartbeat(data):
    """
    Heartbeat payload from agent:
      { agent_id, hostname, agent_operating_system, last_seen }
    Updates registry so Devices view can display OS/hostname and recency.
    """
    if not data:
        return
    agent_id = data.get("agent_id")
    if not agent_id:
        return
    hostname = data.get("hostname")

    if hostname:
        # Avoid duplicate entries per-hostname by collapsing to the newest agent_id.
        # Prefer non-script agents; we do not surface script agents in /api/agents.
        try:
            is_current_script = isinstance(agent_id, str) and agent_id.lower().endswith('-script')
        except Exception:
            is_current_script = False
        # Transfer any existing configuration from displaced entries to this agent if needed
        transferred_cfg = False
        for aid, info in list(registered_agents.items()):
            if aid == agent_id:
                continue
            if info.get("hostname") == hostname:
                # If the incoming is a script helper and there is a non-script entry, keep non-script
                if is_current_script and not info.get('is_script_agent'):
                    # Do not register duplicate script entry; just update last_seen persistence below
                    # and return after persistence to avoid creating a second record.
                    try:
                        _persist_last_seen(hostname, int(data.get("last_seen") or time.time()), info.get("agent_id") or aid)
                    except Exception:
                        pass
                    return
                # Otherwise, evict the older/placeholder/script entry and transfer config if present
                if not transferred_cfg and aid in agent_configurations and agent_id not in agent_configurations:
                    agent_configurations[agent_id] = agent_configurations.get(aid)
                    transferred_cfg = True
                registered_agents.pop(aid, None)
                agent_configurations.pop(aid, None)

    rec = registered_agents.setdefault(agent_id, {})
    rec["agent_id"] = agent_id
    if hostname:
        rec["hostname"] = hostname
    if data.get("agent_operating_system"):
        rec["agent_operating_system"] = data.get("agent_operating_system")
    rec["last_seen"] = int(data.get("last_seen") or time.time())
    rec["status"] = "provisioned" if agent_id in agent_configurations else rec.get("status", "orphaned")
    # Persist last_seen (and agent_id) into DB keyed by hostname so it survives restarts.
    try:
        _persist_last_seen(rec.get("hostname") or hostname, rec["last_seen"], rec.get("agent_id"))
    except Exception:
        pass

@socketio.on("request_config")
def send_agent_config(data):
    agent_id = (data or {}).get("agent_id")
    config = agent_configurations.get(agent_id)
    if config:
        emit("agent_config", config)

@socketio.on("screenshot")
def receive_screenshot(data):
    agent_id = data.get("agent_id")
    image = data.get("image_base64")

    if agent_id and image:
        latest_images[agent_id] = {
            "image_base64": image,
            "timestamp": time.time()
        }
        # Broadcast to all clients; use server-level emit
        socketio.emit("new_screenshot", {"agent_id": agent_id, "image_base64": image})

@socketio.on("disconnect")
def on_disconnect():
    print("[WebSocket] Connection Disconnected")

# Macro Websocket Handlers
@socketio.on("macro_status")
def receive_macro_status(data):
    """
    Receives macro status/errors from agent and relays to all clients
    Expected payload: {
        "agent_id": ...,
        "node_id": ...,
        "success": True/False,
        "message": "...",
        "timestamp": ...
    }
    """
    print(f"[Macro Status] Agent {data.get('agent_id')} Node {data.get('node_id')} Success: {data.get('success')} Msg: {data.get('message')}")
    # Broadcast to all; use server-level emit for v5 API
    socketio.emit("macro_status", data)

@socketio.on("list_agent_windows")
def handle_list_agent_windows(data):
    """
    Forwards list_agent_windows event to all agents (or filter for a specific agent_id).
    """
    # Forward to all agents/clients
    socketio.emit("list_agent_windows", data)

@socketio.on("agent_window_list")
def handle_agent_window_list(data):
    """
    Relay the list of windows from the agent back to all connected clients.
    """
    # Relay the list to all interested clients
    socketio.emit("agent_window_list", data)

# ---------------------------------------------
# Server Launch
# ---------------------------------------------
if __name__ == "__main__":
    # Use SocketIO runner so WebSocket transport works with eventlet.
    socketio.run(app, host="0.0.0.0", port=5000)
