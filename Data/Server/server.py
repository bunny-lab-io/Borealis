#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/server.py

import eventlet
# Monkey-patch stdlib for cooperative sockets
eventlet.monkey_patch()

from flask import Flask, request, jsonify, send_from_directory, Response
from flask_socketio import SocketIO, emit

import time
import os
import base64

# Borealis Python API Endpoints
from Python_API_Endpoints.ocr_engines import run_ocr_on_base64

# ---------------------------------------------
# React Frontend Hosting Configuration
# ---------------------------------------------
build_folder = os.path.join(os.getcwd(), "web-interface", "build")
if not os.path.exists(build_folder):
    print("WARNING: web-interface build folder not found. Please build your React app.")

app = Flask(__name__, static_folder=build_folder, static_url_path="/")
# Use Eventlet, switch async mode, and raise HTTP buffer size to ~100 MB
socketio = SocketIO(
    app,
    cors_allowed_origins="*",
    async_mode="eventlet",
    engineio_options={'max_http_buffer_size': 100_000_000}
)

@app.route("/")
def serve_index():
    index_path = os.path.join(build_folder, "index.html")
    if os.path.exists(index_path):
        return send_from_directory(build_folder, "index.html")
    return ("<h1>Borealis React App Code Not Found</h1>"
            "<p>Please re-deploy Borealis Workflow Automation Tool</p>"), 404

@app.route("/<path:path>")
def serve_react_app(path):
    full_path = os.path.join(build_folder, path)
    if os.path.exists(full_path):
        return send_from_directory(build_folder, path)
    return send_from_directory(build_folder, "index.html")

# ---------------------------------------------
# Borealis Python API Endpoints
# ---------------------------------------------
# Process image data into OCR text output.
@app.route("/api/ocr", methods=["POST"])
def ocr_endpoint():
    payload = request.get_json()
    image_b64 = payload.get("image_base64")
    engine = payload.get("engine", "tesseract").lower().strip()
    backend = payload.get("backend", "cpu").lower().strip()

    # Normalize engine aliases
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
# Borealis Agent API Endpoints
# ---------------------------------------------
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
    roles = data.get("roles", [])  # <- MODULAR ROLES ARRAY

    if not agent_id or not isinstance(roles, list):
        return jsonify({"error": "Missing agent_id or roles[] in provision payload."}), 400

    # Save configuration
    config = {"roles": roles}
    agent_configurations[agent_id] = config

    # Update status if agent already registered
    if agent_id in registered_agents:
        registered_agents[agent_id]["status"] = "provisioned"

    # Emit config to the agent
    socketio.emit("agent_config", config)

    return jsonify({"status": "provisioned", "roles": roles})


# ----------------------------------------------
# Canvas Image Feed Viewer for Screenshot Agents
# ----------------------------------------------
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
# WebSocket Events
# ---------------------------------------------
@socketio.on("agent_screenshot_task")
def receive_screenshot_task(data):
    agent_id = data.get("agent_id")
    node_id = data.get("node_id")
    image = data.get("image_base64")

    if not agent_id or not node_id or not image:
        print("[WS] Screenshot task missing fields.")
        return

    # Optional: Store for debugging
    latest_images[f"{agent_id}:{node_id}"] = {
        "image_base64": image,
        "timestamp": time.time()
    }

    emit("agent_screenshot_task", {
        "agent_id": agent_id,
        "node_id": node_id,
        "image_base64": image
    }, broadcast=True)

@socketio.on('connect_agent')
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

@socketio.on('request_config')
def send_agent_config(data):
    agent_id = data.get("agent_id")
    config = agent_configurations.get(agent_id)
    if config:
        emit('agent_config', config)

@socketio.on('screenshot')
def receive_screenshot(data):
    agent_id = data.get("agent_id")
    image = data.get("image_base64")

    if agent_id and image:
        latest_images[agent_id] = {
            "image_base64": image,
            "timestamp": time.time()
        }
        emit("new_screenshot", {"agent_id": agent_id, "image_base64": image}, broadcast=True)

@socketio.on('disconnect')
def on_disconnect():
    print("[WS] Agent disconnected")

# ---------------------------------------------
# Server Start
# ---------------------------------------------
if __name__ == "__main__":
    import eventlet.wsgi
    listener = eventlet.listen(('0.0.0.0', 5000))
    eventlet.wsgi.server(listener, app)
