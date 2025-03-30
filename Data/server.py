from flask import Flask, send_from_directory
import os

# Determine the absolute path for the React build folder
build_folder = os.path.join(os.getcwd(), "web-interface", "build")
if not os.path.exists(build_folder):
    print("WARNING: web-interface build folder not found. Please build your React app.")

app = Flask(__name__, static_folder=build_folder, static_url_path="/")

@app.route("/")
def serve_frontend():
    """Serve the React app."""
    index_path = os.path.join(build_folder, "index.html")
    if os.path.exists(index_path):
        return send_from_directory(app.static_folder, "index.html")
    return "<h1>Borealis React App Code Not Found</h1><p>Please re-deploy Borealis Workflow Automation Tool</p>", 404

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=False)
