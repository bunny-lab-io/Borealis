////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: `<ProjectRoot>/readme.md`

![Borealis Logo](https://git.bunny-lab.io/Borealis/Borealis/raw/branch/main/Data/Server/WebUI/public/Borealis_Logo_Full.png)

**Borealis** is a cross-platform **visual automation platform** that lets you design and execute workflows using drag-and-drop "nodes" in an interactive graph. Think of it like building a flowchart that actually **runs** — in real-time.

Powered by a Flask backend and a React Flow frontend, Borealis is perfect for anyone looking to automate tasks, visualize data processing, or build reactive tools using a modular, extensible system.

---

## ✨ Key Features

| Feature | Description |
|--------|-------------|
| 🧠 **Visual Editor** | Intuitive graph-based UI powered by React Flow. Nodes represent data, logic, or actions. |
| ⚙️ **Dynamic Node Updates** | All nodes react to data changes live using a shared memory bus and global update timer. |
| 🔗 **Live Connections** | Connect nodes via "wires" to transmit values in real time — no refresh needed. |
| 🖼️ **On-Screen GUI Interactions** | Supports custom Python-based GUI prompts, like on-screen region selectors. |
| 🔍 **OCR and Vision Support** | Use EasyOCR and Tesseract to extract data from screenshots or webcam feeds. |
| 🧩 **Custom Node Support** | Easily define your own nodes using JSX — each one modular and reactive. |
| 🚀 **Cross-Platform** | Works on Windows, Linux, and macOS (via provided `.sh` and `.ps1` launch scripts). |

---

## 🧱 Core Components

| Component | Role |
|----------|------|
| **Flask Server** | Hosts API endpoints and serves the React frontend |
| **React Flow UI** | Visual canvas for building and managing workflows |
| **Python Virtual Env** | Encapsulates dependencies, avoids global installs |
| **Shared Value Bus** | All nodes communicate via `window.BorealisValueBus` |

---

## ⚡ Getting Started

### Windows:
```powershell
# Windows - Launch Borealis Server and/or Agent
# To Launch borealis itself, you can just right-click the "Launch-Borealis.ps1" file and select "Run with Powershell", or alternatively, run the command seen below, either in the same powershell session as the first command, or in its own non-administrative session.
Set-ExecutionPolicy Unrestricted -Scope Process; .\Launch-Borealis.ps1
```
### Linux:
-# Detailed explanations of how to get things working in Linux is not given at this time, but its mostly automated during the deployment, and only requires a single script to be ran.
```sh
# Linux / macOS
bash Launch-Borealis.sh
```

**The launch script will**:
- :snake: Create a virtual Python environment
- :package: Install all required Python + JS dependencies (Bundled in Windows / Installed in Linux)
- :atom: Build the React app
- :globe_with_meridians: Launch the Flask web server
---

## 🧠 How It Works

Borealis workflows run on **live data propagation**. Each node checks for incoming values (via edges) and processes them on a recurring timer (default: 200ms). This allows for highly reactive, composable logic graphs.

## ⚡Reverse Proxy Configuration
If you want to run Borealis behind a reverse proxy (e.g., Traefik), you can set up the following dynamic configuration:
```yml
http:
  routers:
    borealis:
      entryPoints:
        - websecure
      tls:
        certResolver: letsencrypt
      service: borealis
      rule: "Host(`borealis.bunny-lab.io`) && PathPrefix(`/`)"
      middlewares:
        - cors-headers

  middlewares:
    cors-headers:
      headers:
        accessControlAllowOriginList:
          - "*"
        accessControlAllowMethods:
          - GET
          - POST
          - OPTIONS
        accessControlAllowHeaders:
          - Content-Type
          - Upgrade
          - Connection
        accessControlMaxAge: 100
        addVaryHeader: true

  services:
    borealis:
      loadBalancer:
        servers:
          - url: "http://192.168.3.254:5000"
        passHostHeader: true
```