from flask import Flask, send_from_directory, jsonify, request, abort
import os
import importlib
import inspect
import uuid
from OdenGraphQt import BaseNode

# Determine the absolute path for the React build folder
build_folder = os.path.join(os.getcwd(), "web-interface", "build")
if not os.path.exists(build_folder):
    print("WARNING: web-interface build folder not found. Please build your React app.")

app = Flask(__name__, static_folder=build_folder, static_url_path="/")

# Directory where nodes are stored
NODES_PACKAGE = "Nodes"

# In-memory workflow storage
workflow_data = {
    "nodes": [],
    "edges": []  # Store connections separately
}

def import_nodes_from_folder(package_name):
    """Dynamically import node classes from the given package and list them."""
    nodes_by_category = {}
    package = importlib.import_module(package_name)
    package_path = package.__path__[0]

    for root, _, files in os.walk(package_path):
        rel_path = os.path.relpath(root, package_path).replace(os.sep, ".")
        module_prefix = f"{package_name}.{rel_path}" if rel_path != "." else package_name
        category_name = os.path.basename(root)

        for file in files:
            if file.endswith(".py") and file != "__init__.py":
                module_name = f"{module_prefix}.{file[:-3]}"
                try:
                    module = importlib.import_module(module_name)
                    for name, obj in inspect.getmembers(module, inspect.isclass):
                        if issubclass(obj, BaseNode) and obj.__module__ == module.__name__:
                            if category_name not in nodes_by_category:
                                nodes_by_category[category_name] = []
                            nodes_by_category[category_name].append(obj.NODE_NAME)
                except Exception as e:
                    print(f"Failed to import {module_name}: {e}")

    return nodes_by_category

@app.route("/")
def serve_frontend():
    """Serve the React app."""
    index_path = os.path.join(build_folder, "index.html")
    if os.path.exists(index_path):
        return send_from_directory(app.static_folder, "index.html")
    return "<h1>Borealis React App Code Not Found</h1><p>Please re-deploy Borealis Workflow Automation Tool</p>", 404

@app.route("/api/nodes", methods=["GET"])
def get_available_nodes():
    """Return available node types."""
    nodes = import_nodes_from_folder(NODES_PACKAGE)
    return jsonify(nodes)

@app.route("/api/workflow", methods=["GET", "POST"])
def handle_workflow():
    """Retrieve or update the workflow."""
    global workflow_data
    if request.method == "GET":
        return jsonify(workflow_data)
    elif request.method == "POST":
        data = request.get_json()
        if not data:
            abort(400, "Invalid workflow data")
        workflow_data = data
        return jsonify({"status": "success", "workflow": workflow_data})

@app.route("/api/node", methods=["POST"])
def create_node():
    """Create a new node with a unique UUID."""
    data = request.get_json()
    if not data or "nodeType" not in data:
        abort(400, "Invalid node data")

    node_id = str(uuid.uuid4())  # Generate a unique ID
    node = {
        "id": node_id,
        "type": data["nodeType"],
        "position": data.get("position", {"x": 100, "y": 100}),
        "properties": data.get("properties", {})
    }
    workflow_data["nodes"].append(node)
    return jsonify({"status": "success", "node": node})

@app.route("/api/node/<string:node_id>", methods=["PUT", "DELETE"])
def modify_node(node_id):
    """Update or delete a node."""
    global workflow_data
    if request.method == "PUT":
        data = request.get_json()
        for node in workflow_data["nodes"]:
            if node["id"] == node_id:
                node["position"] = data.get("position", node["position"])
                node["properties"] = data.get("properties", node["properties"])
                return jsonify({"status": "success", "node": node})
        abort(404, "Node not found")

    elif request.method == "DELETE":
        workflow_data["nodes"] = [n for n in workflow_data["nodes"] if n["id"] != node_id]
        return jsonify({"status": "success", "deletedNode": node_id})

@app.route("/api/edge", methods=["POST"])
def create_edge():
    """Create a new connection (edge) between nodes."""
    data = request.get_json()
    if not data or "source" not in data or "target" not in data:
        abort(400, "Invalid edge data")

    edge_id = str(uuid.uuid4())
    edge = {"id": edge_id, "source": data["source"], "target": data["target"]}
    workflow_data["edges"].append(edge)
    return jsonify({"status": "success", "edge": edge})

@app.route("/api/edge/<string:edge_id>", methods=["DELETE"])
def delete_edge(edge_id):
    """Delete an edge by ID."""
    global workflow_data
    workflow_data["edges"] = [e for e in workflow_data["edges"] if e["id"] != edge_id]
    return jsonify({"status": "success", "deletedEdge": edge_id})

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=False)
