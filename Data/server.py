from flask import Flask, request, jsonify, send_from_directory
import time
import os

# ---------------------------------------------
# React Frontend Hosting Configuration
# ---------------------------------------------
build_folder = os.path.join(os.getcwd(), "web-interface", "build")
if not os.path.exists(build_folder):
    print("WARNING: web-interface build folder not found. Please build your React app.")

app = Flask(__name__, static_folder=build_folder, static_url_path="/")

@app.route("/")
def serve_index():
    index_path = os.path.join(build_folder, "index.html")
    if os.path.exists(index_path):
        return send_from_directory(build_folder, "index.html")
    return "<h1>Borealis React App Code Not Found</h1><p>Please re-deploy Borealis Workflow Automation Tool</p>", 404

# Wildcard route to serve React for sub-routes (e.g., /workflow)
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

@app.route("/api/agent/reset", methods=["POST"])
def reset_agent():
    agent_id = request.json.get("agent_id")
    if agent_id in agents:
        agents[agent_id]["status"] = "orphaned"
        agents[agent_id]["config"] = None
        latest_images.pop(agent_id, None)
        return jsonify({"status": "reset"}), 200
    return jsonify({"error": "Agent not found"}), 404

@app.route("/api/agent/provision", methods=["POST"])
def provision_agent():
    data = request.json
    agent_id = data.get("agent_id")
    config = {
        "task": "screenshot",
        "x": data.get("x", 100),
        "y": data.get("y", 100),
        "w": data.get("w", 300),
        "h": data.get("h", 200),
        "interval": data.get("interval", 1000)
    }
    agent_configurations[agent_id] = config
    if agent_id in registered_agents:
        registered_agents[agent_id]["status"] = "provisioned"
    return jsonify({"status": "provisioned"})

@app.route("/api/agent/config")
def get_agent_config():
    agent_id = request.args.get("agent_id")
    config = agent_configurations.get(agent_id)
    return jsonify(config or {})

@app.route("/api/agent/data", methods=["POST"])
def agent_data():
    data = request.json
    agent_id = data.get("agent_id")
    image = data.get("image_base64")

    if not agent_id or not image:
        return jsonify({"error": "Missing data"}), 400

    latest_images[agent_id] = {
        "image_base64": image,
        "timestamp": time.time()
    }
    return jsonify({"status": "received"})

@app.route("/api/agent/image")
def get_latest_image():
    agent_id = request.args.get("agent_id")
    entry = latest_images.get(agent_id)
    if entry:
        return jsonify(entry)
    return jsonify({"error": "No image"}), 404

@app.route("/api/agents")
def get_agents():
    return jsonify(registered_agents)

# ---------------------------------------------
# Server Start
# ---------------------------------------------
if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=False)
