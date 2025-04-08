from flask import Flask, request, jsonify, send_from_directory
from flask_socketio import SocketIO, emit
import time
import os

# ---------------------------------------------
# React Frontend Hosting Configuration
# ---------------------------------------------
build_folder = os.path.join(os.getcwd(), "web-interface", "build")
if not os.path.exists(build_folder):
    print("WARNING: web-interface build folder not found. Please build your React app.")

app = Flask(__name__, static_folder=build_folder, static_url_path="/")
socketio = SocketIO(app, cors_allowed_origins="*", transports=['websocket'])

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
# Borealis Agent Management (Hybrid: API + WebSockets)
# ---------------------------------------------
registered_agents = {}
agent_configurations = {}
latest_images = {}

# API Endpoints (kept for provisioning and status)
@app.route("/api/agent/checkin", methods=["POST"])
def agent_checkin():
    data = request.json
    agent_id = data.get("agent_id")
    hostname = data.get("hostname", "unknown")

    registered_agents[agent_id] = {
        "agent_id": agent_id,
        "hostname": hostname,
        "last_seen": time.time(),
        "status": "orphaned" if agent_id not in agent_configurations else "provisioned"
    }
    return jsonify({"status": "ok"})

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
    return jsonify({"status": "provisioned"})

@app.route("/api/agents")
def get_agents():
    return jsonify(registered_agents)

# WebSocket Handlers
@socketio.on('connect_agent')
def handle_agent_connect(data):
    agent_id = data.get('agent_id')
    hostname = data.get('hostname', 'unknown')

    registered_agents[agent_id] = {
        "agent_id": agent_id,
        "hostname": hostname,
        "last_seen": time.time(),
        "status": "connected"
    }

    print(f"Agent connected: {agent_id}")
    emit('agent_connected', {'status': 'connected'})

@socketio.on('screenshot')
def handle_screenshot(data):
    agent_id = data.get('agent_id')
    image_base64 = data.get('image_base64')

    if agent_id and image_base64:
        latest_images[agent_id] = {
            'image_base64': image_base64,
            'timestamp': time.time()
        }

        # Real-time broadcast to connected dashboards
        emit('new_screenshot', {
            'agent_id': agent_id,
            'image_base64': image_base64
        }, broadcast=True)

        emit('screenshot_received', {'status': 'ok'})
        print(f"Screenshot received from agent: {agent_id}")
    else:
        emit('error', {'message': 'Invalid screenshot data'})

@socketio.on('request_config')
def handle_request_config(data):
    agent_id = data.get('agent_id')
    config = agent_configurations.get(agent_id, {})

    emit('agent_config', config)

# ---------------------------------------------
# Server Start
# ---------------------------------------------
if __name__ == "__main__":
    socketio.run(app, host="0.0.0.0", port=5000, debug=False)