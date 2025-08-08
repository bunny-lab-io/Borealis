#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/server.py

import eventlet
# Monkey-patch stdlib for cooperative sockets
eventlet.monkey_patch()

import requests
from flask import Flask, request, jsonify, Response, send_from_directory, make_response
from flask_socketio import SocketIO, emit
from flask_cors import CORS

import time
import os  # To Read Production ReactJS Server Folder
import json  # For reading workflow JSON files
from typing import List, Dict

# Borealis Python API Endpoints
from Python_API_Endpoints.ocr_engines import run_ocr_on_base64

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

    Response:
    {
      "root": "<absolute path to Workflows>",
      "workflows": [
        {
          "name": "FolderA > Sub > File.json",   # breadcrumb styled name for table display
          "breadcrumb_prefix": "FolderA > Sub",  # prefix only, to allow UI styling
          "file_name": "File.json",              # base filename
          "rel_path": "FolderA/Sub/File.json",   # path relative to Workflows
          "tab_name": "Optional Tab Name",       # best-effort read from JSON (may be "")
          "description": "",                     # placeholder for future use
          "category": "",                        # placeholder for future use
          "last_edited": "YYYY-MM-DDTHH:MM:SS",  # local time ISO-like string
          "last_edited_epoch": 1712345678.123    # numeric, for client-side sorting
        },
        ...
      ]
    }
    """
    # Resolve <ProjectRoot>/Workflows relative to this file at <ProjectRoot>/Data/server.py
    workflows_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "Workflows"))
    results: List[Dict] = []

    if not os.path.isdir(workflows_root):
        # Directory missing is not a hard error; return empty set and the resolved path for visibility.
        return jsonify({
            "root": workflows_root,
            "workflows": [],
            "warning": "Workflows directory not found."
        }), 200

    for root, dirs, files in os.walk(workflows_root):
        for fname in files:
            if not fname.lower().endswith(".json"):
                continue

            full_path = os.path.join(root, fname)
            rel_path = os.path.relpath(full_path, workflows_root)  # e.g. SuperStuff/Example.json

            # Build breadcrumb-style display name: "SuperStuff > Example.json"
            parts = rel_path.split(os.sep)
            folder_parts = parts[:-1]
            breadcrumb_prefix = " > ".join(folder_parts) if folder_parts else ""
            display_name = f"{breadcrumb_prefix} > {fname}" if breadcrumb_prefix else fname

            # Best-effort read of tab name (not required for now)
            obj = _safe_read_json(full_path)
            tab_name = _extract_tab_name(obj)

            # File timestamps
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

    # Sort newest-first by modification time
    results.sort(key=lambda x: x.get("last_edited_epoch", 0.0), reverse=True)

    return jsonify({
        "root": workflows_root,
        "workflows": results
    })

# ---------------------------------------------
# Borealis Agent API Endpoints
# ---------------------------------------------
# These endpoints handle agent registration, provisioning, and image streaming.
registered_agents = {}
agent_configurations = {}
latest_images = {}

@app.route("/api/agents")
def get_agents():
    return jsonify(registered_agents)

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

    socketio.emit("agent_config", config)
    return jsonify({"status": "provisioned", "roles": roles})

# ---------------------------------------------
# Borealis External API Proxy Endpoint
# ---------------------------------------------
@app.route("/api/proxy", methods=["GET", "POST", "OPTIONS"])
def proxy():
    target = request.args.get("url")
    if not target:
        return {"error": "Missing ?url="}, 400

    # Forward method, headers, body
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
# Serves an HTML canvas that shows real-time screenshots from a given agent+node.
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

    # Emit the full payload, including geometry (even if image is empty)
    emit("agent_screenshot_task", data, broadcast=True)

@socketio.on("connect_agent")
def connect_agent(data):
    agent_id = data.get("agent_id")
    hostname = data.get("hostname", "unknown")
    print(f"Agent connected: {agent_id}")

    registered_agents[agent_id] = {
        "agent_id": agent_id,
        "hostname": hostname,
        "last_seen": time.time(),
        "status": "orphaned" if agent_id not in agent_configurations else "provisioned"
    }

@socketio.on("request_config")
def send_agent_config(data):
    agent_id = data.get("agent_id")
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
        emit("new_screenshot", {"agent_id": agent_id, "image_base64": image}, broadcast=True)

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
    emit("macro_status", data, broadcast=True)

@socketio.on("list_agent_windows")
def handle_list_agent_windows(data):
    """
    Forwards list_agent_windows event to all agents (or filter for a specific agent_id).
    """
    agent_id = data.get("agent_id")
    # You can target a specific agent if you track rooms/sessions.
    # For now, broadcast to all agents so the correct one can reply.
    emit("list_agent_windows", data, broadcast=True)

@socketio.on("agent_window_list")
def handle_agent_window_list(data):
    """
    Relay the list of windows from the agent back to all connected clients.
    """
    emit("agent_window_list", data, broadcast=True)


# ---------------------------------------------
# Server Launch
# ---------------------------------------------
if __name__ == "__main__":
    import eventlet.wsgi
    listener = eventlet.listen(('0.0.0.0', 5000))
    eventlet.wsgi.server(listener, app)
