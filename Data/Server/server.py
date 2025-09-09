#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/server.py

import eventlet
# Monkey-patch stdlib for cooperative sockets
eventlet.monkey_patch()

import requests
from flask import Flask, request, jsonify, Response, send_from_directory, make_response
from flask_socketio import SocketIO, emit, join_room
from flask_cors import CORS

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

# ---------------------------------------------
# Flask + WebSocket Server Configuration
# ---------------------------------------------
app = Flask(
    __name__,
    static_folder=os.path.join(os.path.dirname(__file__), '../web-interface/build'),
    static_url_path=''
)

# Enable CORS on All Routes
CORS(app)

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
# User Authentication Endpoint
# ---------------------------------------------
@app.route("/api/users", methods=["GET"])
def get_users():
    users_path = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "users.json")
    )
    try:
        with open(users_path, "r", encoding="utf-8") as fh:
            return jsonify(json.load(fh))
    except Exception:
        return jsonify({"users": []})

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
        os.path.join(os.path.dirname(__file__), "..", "..", "Workflows")
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
        os.path.join(os.path.dirname(__file__), "..", "..", "Workflows")
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
        os.path.join(os.path.dirname(__file__), "..", "..", "Workflows")
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
        os.path.join(os.path.dirname(__file__), "..", "..", "Workflows")
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
        os.path.join(os.path.dirname(__file__), "..", "..", "Workflows")
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
    Scan <ProjectRoot>/Workflows for *.json files and return a table-friendly list.
    """
    # Resolve <ProjectRoot>/Workflows relative to this file at <ProjectRoot>/Data/server.py
    workflows_root = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Workflows")
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
        os.path.join(os.path.dirname(__file__), "..", "..", "Workflows")
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
        os.path.join(os.path.dirname(__file__), "..", "..", "Workflows")
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
        os.path.join(os.path.dirname(__file__), "..", "..", "Workflows")
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
    return os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "..", "Scripts")
    )


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
    """Scan <ProjectRoot>/Scripts for known script files and return list + folders."""
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
    for root, dirs, files in os.walk(scripts_root):
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
    if not abs_path.startswith(scripts_root) or not os.path.isfile(abs_path):
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
    else:
        if not name:
            return jsonify({"error": "Missing name"}), 400
        # Append extension only if none provided
        ext = os.path.splitext(name)[1]
        if not ext:
            desired_ext = _ext_for_type(script_type) or ".txt"
            name = os.path.splitext(name)[0] + desired_ext
        abs_path = os.path.abspath(os.path.join(scripts_root, os.path.basename(name)))

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
    if not new_abs.startswith(scripts_root):
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
    if not abs_path.startswith(scripts_root) or not os.path.isfile(abs_path):
        return jsonify({"error": "File not found"}), 404
    try:
        os.remove(abs_path)
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/scripts/create_folder", methods=["POST"])
def scripts_create_folder():
    data = request.get_json(silent=True) or {}
    rel_path = (data.get("path") or "").strip()
    scripts_root = _scripts_root()
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
    if not abs_path.startswith(scripts_root) or not os.path.isdir(abs_path):
        return jsonify({"error": "Folder not found"}), 404
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
# Track websocket connections by session id so we can log disconnects
socket_connections: Dict[str, Dict] = {}

# Device database initialization
DB_PATH = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "..", "Databases", "devices.db")
)
os.makedirs(os.path.dirname(DB_PATH), exist_ok=True)

def init_db():
    conn = sqlite3.connect(DB_PATH)
    cur = conn.cursor()
    cur.execute(
        "CREATE TABLE IF NOT EXISTS device_details (hostname TEXT PRIMARY KEY, description TEXT, details TEXT)"
    )
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
    conn.commit()
    conn.close()

init_db()

# Views database (device list saved views)
VIEWS_DB_PATH = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "..", "Databases", "devices_list_views.db")
)
os.makedirs(os.path.dirname(VIEWS_DB_PATH), exist_ok=True)

def init_views_db():
    conn = sqlite3.connect(VIEWS_DB_PATH)
    cur = conn.cursor()
    # Store name, ordered column ids as JSON, and filters as JSON
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
    conn.commit()
    conn.close()

init_views_db()

# ---------------------------------------------
# Sites database (site list + device assignments)
# ---------------------------------------------
SITES_DB_PATH = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "..", "Databases", "sites.db")
)
os.makedirs(os.path.dirname(SITES_DB_PATH), exist_ok=True)

def init_sites_db():
    conn = sqlite3.connect(SITES_DB_PATH)
    cur = conn.cursor()
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
    conn.commit()
    conn.close()

init_sites_db()

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
        conn = sqlite3.connect(SITES_DB_PATH)
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
        conn = sqlite3.connect(SITES_DB_PATH)
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
        conn = sqlite3.connect(SITES_DB_PATH)
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
        conn = sqlite3.connect(SITES_DB_PATH)
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
        conn = sqlite3.connect(SITES_DB_PATH)
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
        conn = sqlite3.connect(VIEWS_DB_PATH)
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
        conn = sqlite3.connect(VIEWS_DB_PATH)
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
        conn = sqlite3.connect(VIEWS_DB_PATH)
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
        conn = sqlite3.connect(VIEWS_DB_PATH)
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
        conn = sqlite3.connect(VIEWS_DB_PATH)
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
        conn = sqlite3.connect(DB_PATH)
        cur = conn.cursor()
        cur.execute(
            "SELECT details, description FROM device_details WHERE hostname = ?",
            (hostname,),
        )
        row = cur.fetchone()
        # Load existing details JSON or create a minimal one
        if row and row[0]:
            try:
                details = json.loads(row[0])
            except Exception:
                details = {}
            description = row[1] if len(row) > 1 else ""
        else:
            details = {}
            description = ""

        summary = details.get("summary") or {}
        summary["hostname"] = summary.get("hostname") or hostname
        summary["last_seen"] = int(last_seen or 0)
        if agent_id:
            try:
                summary["agent_id"] = str(agent_id)
            except Exception:
                pass
        details["summary"] = summary

        cur.execute(
            "REPLACE INTO device_details (hostname, description, details) VALUES (?, ?, ?)",
            (hostname, description, json.dumps(details)),
        )
        conn.commit()
        conn.close()
    except Exception as e:
        print(f"[WARN] Failed to persist last_seen for {hostname}: {e}")


def load_agents_from_db():
    """Populate registered_agents with any devices stored in the database."""
    try:
        conn = sqlite3.connect(DB_PATH)
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


@app.route("/api/agent/details", methods=["POST"])
def save_agent_details():
    data = request.get_json(silent=True) or {}
    hostname = data.get("hostname")
    details = data.get("details")
    agent_id = data.get("agent_id")
    if not hostname and isinstance(details, dict):
        hostname = details.get("summary", {}).get("hostname")
    if not hostname or not isinstance(details, dict):
        return jsonify({"error": "invalid payload"}), 400
    try:
        conn = sqlite3.connect(DB_PATH)
        cur = conn.cursor()
        # Load existing details/description so we can preserve description and merge last_seen
        cur.execute(
            "SELECT details, description FROM device_details WHERE hostname = ?",
            (hostname,),
        )
        row = cur.fetchone()
        prev_details = {}
        if row and row[0]:
            try:
                prev_details = json.loads(row[0])
            except Exception:
                prev_details = {}
        description = row[1] if row and len(row) > 1 else ""

        # Ensure details.summary.last_seen is preserved/merged so it survives restarts
        try:
            incoming_summary = details.setdefault("summary", {})
            # Attach agent_id and hostname if provided/missing to aid future merges
            try:
                if agent_id and not incoming_summary.get("agent_id"):
                    incoming_summary["agent_id"] = str(agent_id)
            except Exception:
                pass
            if hostname and not incoming_summary.get("hostname"):
                incoming_summary["hostname"] = hostname
            if not incoming_summary.get("last_seen"):
                last_seen = None
                if agent_id and agent_id in registered_agents:
                    last_seen = registered_agents[agent_id].get("last_seen")
                if not last_seen:
                    last_seen = (prev_details.get("summary") or {}).get("last_seen")
                if last_seen:
                    incoming_summary["last_seen"] = int(last_seen)
            # Refresh server-side cache so /api/agents includes latest OS and device type
            try:
                if agent_id and agent_id in registered_agents:
                    rec = registered_agents[agent_id]
                    os_name = incoming_summary.get("operating_system") or incoming_summary.get("agent_operating_system")
                    if os_name:
                        rec["agent_operating_system"] = os_name
                    dt = (incoming_summary.get("device_type") or "").strip()
                    if dt:
                        rec["device_type"] = dt
            except Exception:
                pass
        except Exception:
            pass

        cur.execute(
            "REPLACE INTO device_details (hostname, description, details) VALUES (?, ?, ?)",
            (hostname, description, json.dumps(details)),
        )
        conn.commit()
        conn.close()
        return jsonify({"status": "ok"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@app.route("/api/device/details/<hostname>", methods=["GET"])
def get_device_details(hostname: str):
    try:
        conn = sqlite3.connect(DB_PATH)
        cur = conn.cursor()
        cur.execute(
            "SELECT details, description FROM device_details WHERE hostname = ?",
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
            if description:
                details.setdefault("summary", {})["description"] = description
            return jsonify(details)
    except Exception:
        pass
    return jsonify({})


@app.route("/api/device/description/<hostname>", methods=["POST"])
def set_device_description(hostname: str):
    data = request.get_json(silent=True) or {}
    description = (data.get("description") or "").strip()
    try:
        conn = sqlite3.connect(DB_PATH)
        cur = conn.cursor()
        cur.execute(
            "INSERT INTO device_details(hostname, description, details) VALUES (?, ?, COALESCE((SELECT details FROM device_details WHERE hostname = ?), '{}')) "
            "ON CONFLICT(hostname) DO UPDATE SET description=excluded.description",
            (hostname, description, hostname),
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
    if not abs_path.startswith(scripts_root) or not os.path.isfile(abs_path):
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
            conn = sqlite3.connect(DB_PATH)
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
        conn = sqlite3.connect(DB_PATH)
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
        conn = sqlite3.connect(DB_PATH)
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
        conn = sqlite3.connect(DB_PATH)
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
    """Collector agent reports activity and optional last_user."""
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
    if last_user and (hostname or rec.get('hostname')):
        try:
            conn = sqlite3.connect(DB_PATH)
            cur = conn.cursor()
            cur.execute(
                "SELECT details, description FROM device_details WHERE hostname = ?",
                (hostname or rec.get('hostname'),),
            )
            row = cur.fetchone()
            details = {}
            if row and row[0]:
                try:
                    details = json.loads(row[0])
                except Exception:
                    details = {}
            summary = details.get('summary') or {}
            summary['last_user'] = last_user
            details['summary'] = summary
            cur.execute(
                "REPLACE INTO device_details (hostname, description, details) VALUES (?, COALESCE((SELECT description FROM device_details WHERE hostname=?), ''), ?)",
                ((hostname or rec.get('hostname')), (hostname or rec.get('hostname')), json.dumps(details))
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


@socketio.on("connect")
def on_connect():
    """Log when a new websocket client connects."""
    sid = request.sid
    remote = request.remote_addr
    socket_connections[sid] = {
        "type": "client",
        "agent_id": None,
        "remote_addr": remote,
        "connected_at": time.time(),
        "last_heartbeat": None,
    }
    print(f"[WebSocket] Client connected: sid={sid} remote={remote}")

@socketio.on("connect_agent")
def connect_agent(data):
    """
    Initial agent connect. Agent may only send agent_id here.
    Hostname/OS are filled in by subsequent heartbeats.
    """
    agent_id = (data or {}).get("agent_id")
    if not agent_id:
        return
    sid = request.sid
    remote = request.remote_addr
    conn_type = "script_agent" if isinstance(agent_id, str) and agent_id.lower().endswith('-script') else "agent"
    now = time.time()
    existing = socket_connections.get(sid)
    if existing and existing.get("agent_id") == agent_id:
        # Avoid duplicate logs if the agent re-sends connect_agent for the same sid.
        existing.update({
            "type": conn_type,
            "remote_addr": remote,
            "last_heartbeat": now,
        })
        existing.setdefault("connected_at", now)
        return

    socket_connections[sid] = {
        "type": conn_type,
        "agent_id": agent_id,
        "remote_addr": remote,
        "connected_at": now,
        "last_heartbeat": now,
    }
    print(f"[WebSocket] {conn_type.replace('_', ' ').title()} connected: {agent_id} (sid={sid}, remote={remote})")

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
    if conn_type == "script_agent":
        rec['is_script_agent'] = True
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

    sid = request.sid
    now = time.time()
    conn = socket_connections.get(sid)
    if conn:
        delta = now - (conn.get("last_heartbeat") or conn.get("connected_at") or now)
        conn["last_heartbeat"] = now
        print(f"[WebSocket] Heartbeat from {agent_id} (sid={sid}) {delta:.1f}s since last")
    else:
        # Record unknown connections to help debug unexpected heartbeats
        socket_connections[sid] = {
            "type": "agent",
            "agent_id": agent_id,
            "remote_addr": request.remote_addr,
            "connected_at": now,
            "last_heartbeat": now,
        }
        print(f"[WebSocket] Heartbeat from untracked sid={sid} agent={agent_id}")

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
    sid = request.sid
    remote = request.remote_addr
    conn = socket_connections.pop(sid, None)
    if not conn:
        print(f"[WebSocket] Unknown connection disconnected: sid={sid} remote={remote}")
        return

    conn_type = conn.get("type", "client")
    agent_id = conn.get("agent_id")
    last_hb = conn.get("last_heartbeat")
    hb_info = ""
    if last_hb:
        hb_info = f", last heartbeat {int(time.time() - last_hb)}s ago"
    connected_at = conn.get("connected_at")
    duration_info = ""
    if connected_at:
        duration_info = f", connected for {int(time.time() - connected_at)}s"

    if conn_type in ("agent", "script_agent"):
        role = "Script agent" if conn_type == "script_agent" else "Agent"
        print(f"[WebSocket] {role} disconnected: {agent_id} (sid={sid}, remote={conn.get('remote_addr')}{hb_info}{duration_info})")
    else:
        print(f"[WebSocket] Client disconnected: sid={sid} remote={conn.get('remote_addr')}{hb_info}{duration_info}")

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
