from flask import Flask, request, jsonify, send_from_directory, Response
from flask_socketio import SocketIO, emit
import time
import os
import base64

# ---------------------------------------------
# React Frontend Hosting Configuration
# ---------------------------------------------
build_folder = os.path.join(os.getcwd(), "web-interface", "build")
if not os.path.exists(build_folder):
    print("WARNING: web-interface build folder not found. Please build your React app.")

app = Flask(__name__, static_folder=build_folder, static_url_path="/")
socketio = SocketIO(app, cors_allowed_origins="*", async_mode="threading")

@app.route("/")
def serve_index():
    index_path = os.path.join(build_folder, "index.html")
    if os.path.exists(index_path):
        return send_from_directory(build_folder, "index.html")
    return "<h1>Borealis React App Code Not Found</h1><p>Please re-deploy Borealis Workflow Automation Tool</p>", 404

@app.route("/<path:path>")
def serve_react_app(path):
    full_path = os.path.join(build_folder, path)
    if os.path.exists(full_path):
        return send_from_directory(build_folder, path)
    return send_from_directory(build_folder, "index.html")

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
    config = {
        "task": data.get("task", "screenshot"),
        "x": data.get("x", 100),
        "y": data.get("y", 100),
        "w": data.get("w", 300),
        "h": data.get("h", 200),
        "interval": data.get("interval", 1000),
        "visible": data.get("visible", True)
    }
    agent_configurations[agent_id] = config
    if agent_id in registered_agents:
        registered_agents[agent_id]["status"] = "provisioned"

    # NEW: Emit config update back to the agent via WebSocket
    socketio.emit("agent_config", config)

    return jsonify({"status": "provisioned"})


# ---------------------------------------------
# Raw Image Feed Viewer for Screenshot Agents
# ---------------------------------------------
@app.route("/api/agent/<agent_id>/screenshot")
def screenshot_viewer(agent_id):
    if agent_configurations.get(agent_id, {}).get("task") != "screenshot":
        return "<h1>Agent not provisioned as Screenshot Collector</h1>", 400

    html = f"""
    <html>
    <head>
        <title>Borealis - {agent_id} Screenshot</title>
        <script>
            setInterval(function() {{
                var img = document.getElementById('feed');
                img.src = '/api/agent/{agent_id}/screenshot/raw?rnd=' + Math.random();
            }}, 1000);
        </script>
    </head>
    <body style='background-color: black;'>
        <img id='feed' src='/api/agent/{agent_id}/screenshot/raw' style='max-width:100%; height:auto;' />
    </body>
    </html>
    """
    return html

@app.route("/api/agent/<agent_id>/screenshot/raw")
def screenshot_raw(agent_id):
    entry = latest_images.get(agent_id)
    if not entry:
        return "", 204
    try:
        raw_img = base64.b64decode(entry["image_base64"])
        return Response(raw_img, mimetype="image/png")
    except Exception:
        return "", 204

# ---------------------------------------------
# WebSocket Events
# ---------------------------------------------
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
        print(f"[DEBUG] Screenshot received from agent {agent_id}")
        emit("new_screenshot", {"agent_id": agent_id, "image_base64": image}, broadcast=True)

@socketio.on('disconnect')
def on_disconnect():
    print("[WS] Agent disconnected")

# ---------------------------------------------
# Server Start
# ---------------------------------------------
if __name__ == "__main__":
    socketio.run(app, host="0.0.0.0", port=5000, debug=False)
