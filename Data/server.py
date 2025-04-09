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


# ----------------------------------------------
# Canvas Image Feed Viewer for Screenshot Agents
# ----------------------------------------------
@app.route("/api/agent/<agent_id>/screenshot")
def screenshot_viewer(agent_id):
    if agent_configurations.get(agent_id, {}).get("task") != "screenshot":
        return "<h1>Agent not provisioned as Screenshot Collector</h1>", 400

    return f"""
    <!DOCTYPE html>
    <html>
    <head>
        <title>Borealis Live View - {agent_id}</title>
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
                width: auto;
                height: auto;
                background-color: #111;
            }}
        </style>
    </head>
    <body>
        <canvas id="viewerCanvas"></canvas>

        <script src="https://cdn.socket.io/4.8.1/socket.io.min.js"></script>
        <script>
            const agentId = "{agent_id}";
            const socket = io(window.location.origin, {{ transports: ["websocket"] }});
            const canvas = document.getElementById("viewerCanvas");
            const ctx = canvas.getContext("2d");

            console.log("[Viewer] Canvas initialized for agent:", agentId);

            socket.on("connect", () => {{
                console.log("[WebSocket] Connected to Borealis server at", window.location.origin);
            }});

            socket.on("disconnect", () => {{
                console.warn("[WebSocket] Disconnected from Borealis server");
            }});

            socket.on("new_screenshot", (data) => {{
                console.log("[WebSocket] Received screenshot event");

                if (!data || typeof data !== "object") {{
                    console.error("[Viewer] Screenshot event was not an object:", data);
                    return;
                }}

                if (data.agent_id !== agentId) {{
                    console.log("[Viewer] Ignored screenshot from different agent:", data.agent_id);
                    return;
                }}

                const base64 = data.image_base64;
                console.log("[Viewer] Base64 length:", base64?.length || 0);

                if (!base64 || base64.length < 100) {{
                    console.warn("[Viewer] Empty or too short base64 string.");
                    return;
                }}

                // Peek at base64 to determine MIME type
                let mimeType = "image/png";
                try {{
                    const header = atob(base64.substring(0, 32));
                    if (header.charCodeAt(0) === 0xFF && header.charCodeAt(1) === 0xD8) {{
                        mimeType = "image/jpeg";
                    }}
                }} catch (e) {{
                    console.warn("[Viewer] Failed to decode base64 header", e);
                }}

                const img = new Image();
                img.onload = () => {{
                    console.log("[Viewer] Image loaded successfully:", img.width + "x" + img.height);

                    console.log("[Viewer] Canvas size before:", canvas.width + "x" + canvas.height);

                    if (canvas.width !== img.width || canvas.height !== img.height) {{
                        canvas.width = img.width;
                        canvas.height = img.height;
                        console.log("[Viewer] Canvas resized to match image");
                    }}

                    ctx.clearRect(0, 0, canvas.width, canvas.height);
                    ctx.drawImage(img, 0, 0);

                    console.log("[Viewer] Image drawn on canvas");
                }};
                img.onerror = (err) => {{
                    console.error("[Viewer] Failed to load image from base64. Possibly corrupted data?", err);
                }};
                img.src = "data:" + mimeType + ";base64," + base64;
            }});
        </script>
    </body>
    </html>
    """



@app.route("/api/agent/<agent_id>/screenshot/raw") # Fallback Non-Live Screenshot Preview Code for Legacy Purposes
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
        emit("new_screenshot", {"agent_id": agent_id, "image_base64": image}, broadcast=True)

@socketio.on('disconnect')
def on_disconnect():
    print("[WS] Agent disconnected")

# ---------------------------------------------
# Server Start
# ---------------------------------------------
if __name__ == "__main__":
    import eventlet
    import eventlet.wsgi
    socketio.run(app, host="0.0.0.0", port=5000, debug=False)

