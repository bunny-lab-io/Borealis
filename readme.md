![Borealis Logo](https://github.com/bunny-lab-io/Borealis/blob/main/Data/Server/WebUI/public/Borealis_Logo_Full.png)

**Borealis** is an all-in-one visual automation platform.
Whether you want to automate data flows, control a computer, extract data from images, or connect to APIs and webhooks, Borealis turns these advanced tasks into a simple, visual drag-and-drop experience.

Borealis began as a visual node-graph builder. It is now evolving into a centralized remote management and automation platform that still embraces visual workflows, while prioritizing fleet orchestration, inventory, and scripted automations at-scale. Items marked (*roadmap*) indicate forward-looking capabilities under active design.

![Device List](Data/Repository_Resources/borealis_device_list.png)

![Script Editor](Data/Repository_Resources/borealis_device_details.png)

[![Workflow Editor Demonstration](Data/Repository_Resources/borealis_flow_editor.png)](https://www.youtube.com/watch?v=6GLolR70CTo)

## 🚀 What Is Borealis?

Imagine building automations and powerful data workflows by *connecting blocks on a canvas* - and having them come to life in real time. Borealis is a cross-platform, live-editable automation tool that makes it possible to create, test, and run workflows with **zero coding required** (unless you want to).

Beyond the canvas, Borealis is growing into a centralized control-plane for remote device management and automation (*roadmap*). The canvas remains your design surface, while remote agents and a management console coordinate inventory, policy, and script execution across fleets.

Borealis combines:

* **A powerful visual graph editor** (built with React Flow)
* **A real-time backend server** (Python Flask *or* Vite + WebSockets)
* **Optional Python Agent** for advanced computer automation (screenshot, overlays, and more)
* **Live node updates, instant feedback, no restarts required**
* **Emerging remote agent fleet** for Windows/Linux/macOS management (*roadmap*)
* **Central management console** for inventory, targeting, and job orchestration (*roadmap*)

---

## ✨ Key Features

| Feature                               | What It Means For You                                                                                                               |
| ------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| **Visual Editor**                     | Drag-and-drop nodes to build workflows just like drawing a flowchart                                                                |
| **Live Data Updates**                 | Every node reacts instantly as data changes, thanks to a shared “memory bus”                                                        |
| **No-Code and Low-Code Friendly**     | Start with zero code — but add custom nodes if you want power-user features                                                         |
| **Hot-Reloading, Developer-Friendly** | Add, edit, or even break nodes while Borealis is running. UI recovers, dev server restarts instantly — *no crashes, no frustration* |
| **Real-Time Computer Automation**     | Take screenshots, control keyboard, and send actions to your PC with the Borealis Agent                                             |
| **OCR & Computer Vision**             | Extract text from images and screenshots with Tesseract and EasyOCR, ready to flow into your automation                             |
| **Data Ingestion & Manipulation**     | Ingest data from files, APIs, manual input, or other sources — then filter, search, transform, or export it                         |
| **API Requests & Webhooks**           | Talk to REST APIs, integrate with web services, and hook Borealis into other tools                                                  |
| **Extensible Node Library**           | Add your own custom nodes or use the included library for data, images, math, logic, reporting, and more                            |
| **Undo & Resilience**                 | Made a mistake editing a node or the UI? No worries - just fix it and reload. The included Vite dev server makes changes appear instantly        |
| **Cross-Platform**                    | Works on Windows, Linux, macOS - one-click launch scripts included                                                                  |
| **Centralized Management (roadmap)**  | Manage agents, groups, policies, and jobs from one console                                                                          |
| **Agent Inventory (roadmap)**         | Collect device facts: installed software, OS, storage, networking, last logged-in user                                              |
| **Script Orchestration (roadmap)**    | Push/schedule PowerShell, Bash, Batch scripts and Ansible playbooks with outputs and audit trails                                   |
| **Compliance & Audit (roadmap)**      | Define baselines, detect drift, and export reports for fleet state                                                                 |

---

## 🧰 What Can You Build With Borealis?

* **Data Pipelines:** Move data between files, APIs, spreadsheets, and apps
* **Automated Reports:** Process and transform information, then export to CSV
* **Image Analysis:** Use OCR nodes to read text from screenshots and images (great for extracting data from anything on screen)
* **Computer Automation:** Trigger keypresses, capture screens, and even run scheduled macros
* **APIs & Webhooks:** Build visual flows that connect to any REST API, fetch data, and process responses
* **Real-Time Dashboards:** Build custom, live-updating dashboards by wiring together data nodes and viewers
* **Rapid Prototyping:** Instantly test ideas - drop in new nodes, see your changes live, and never fear breaking things
* **Remote Fleet Automations (*roadmap*):** Run scripts/playbooks across targeted devices, roll out changes safely at scale
* **Inventory & Auditing (*roadmap*):** Query installed software, OS versions, storage/networking, and last logged-in activity
* **Runbook Automation (*roadmap*):** Design visual runbooks in the canvas and execute them remotely with approvals and logging

---

## 🏆 Why Borealis Stands Out

* **Instant Feedback:** Every change is live. No need to restart — just drag, connect, or edit and see results instantly.
* **No Fear Development:** Messed up a node file? UI won't crash; just fix the file, save, and Borealis will hot-reload it.
* **Beginner Friendly:** If you can use a mouse, you can use Borealis. But it grows with you as you learn.
* **Unified Canvas + Control Plane (*roadmap*):** Design visually, orchestrate remotely, and observe everything from a single place.

---

## 🔭 Future Potential (What Borealis Could Become)

Borealis is already a swiss-army knife in the works - but here's just a taste of what's coming, or at-least possible, thanks to its modular, node-based design:

As Borealis pivots toward centralized remote management, these *roadmap* items lead the way:

* **Centralized Remote Management (*roadmap*):** Unified console for device inventory, status, tagging, grouping, and targeting.
* **Script & Playbook Distribution (*roadmap*):** Deploy and run PowerShell, Bash, Batch, and Ansible playbooks across fleets; collect outputs and act upon them in meaningful ways.
* **Inventory & Insights (*roadmap*):** Agents report installed software, OS details, storage, networking, users (e.g., last logged-in), and custom facts.
* **Policy & Compliance (*roadmap*):** Define desired state baselines, detect drift, and trigger remediation workflows.
* **Governance & Safety (*roadmap*):** RBAC, approvals, change windows, versioned script catalog, and secrets management.
* **Event-Driven Automations (*roadmap*):** Triggers from inventory changes, schedules, or webhooks to run workflows or scripts.
* **Scalable Orchestration (*roadmap*):** Target by groups/tags/queries, staggered rollouts, retries, and robust job auditing.

* **Smart Automation:** Automatically detect patterns, trigger workflows from email, websites, or events.
* **Machine Learning Nodes:** Drag-and-drop AI tasks — run models, analyze data, or classify images/text.
* **IoT & Device Integration:** Control or monitor physical devices, sensors, and smart home gadgets.
* **Advanced Scheduling:** Visual “when/if/then” automation, with scheduling, event triggers, and more.
* **Multi-User Collaboration:** Share, co-edit, or run workflows with others — team automation made simple.
* **Plug-in Marketplace:** Install new nodes and integrations from the community, just like adding apps.
* **Web Scraping & Automation:** Visual web scraping, browser automation, and data collection tools.
* **Full API/Webhook In/Out:** Integrate with Zapier, IFTTT, or any tool that speaks HTTP.

---

## 💡 How Borealis Works (In Plain English)

1. **You build a flow** by dragging “nodes” onto a canvas and connecting them.
2. **Each node** does something — like reading a file, fetching an API, or processing an image.
3. **Live data flows** through the graph in real time. Change a value? Every downstream node updates automatically.
4. **The shared “Value Bus”** means nodes always see the latest data. No refreshes needed.
5. **Power-users and developers** can add their own nodes, or tweak the UI, with changes appearing instantly - even if you make a mistake.
6. **Agents report facts (*roadmap*):** Lightweight agents send inventory and telemetry back to the server.
7. **Control-plane orchestrates jobs (*roadmap*):** Target devices by groups/tags and run scripts/playbooks with audit trails.

---

## 📈 Current Node Library (and Always Growing!)

* **Data Nodes:** Manual input, data passing, array extraction, JSON handling, etc.
* **Image Nodes:** Upload, preview, adjust contrast, grayscale, threshold, export, etc.
* **OCR Nodes:** Extract text from images/screenshots using Tesseract/EasyOCR.
* **Logic/Math Nodes:** Comparisons, math operations, logic gates, and more.
* **Agent Nodes:** Remote screenshot, macro keypress (*work-in-progress*), automation of foreground windows.
* **Reporting Nodes:** Export to CSV (*work-in-progress*) (future: other formats).
* **API/Web Nodes:** API requests, regex, text manipulation, etc.
* **Organization Nodes:** Grouping/backdrop for neat, readable graphs. (*work-in-progress*)
* **Remote Action Nodes:** Dispatch and track script/playbook runs across agents (*roadmap*).

---

## 🤝 Who Is Borealis For?

* **Automation geeks** who want a real-time, visual playground
* **Data analysts** who want to prototype and visualize data flows
* **Developers** who want a safe, forgiving testbed for new ideas
* **IT admins / SREs (*roadmap focus*):** Centralize fleet inventory, enforce policy, and orchestrate scripted changes at scale

---

## 🚦 Getting Started

1. **Download Borealis**
2. **Run the launcher:**

   * Windows: `.\Borealis.ps1`
   * Linux/macOS: `bash Borealis.sh`
3. **Open the web UI** in your browser. 

---

## 🎁 Borealis Is Just Getting Started

Borealis is **more than a tool** - As the platform pivots toward centralized remote management, expect deeper inventory, script orchestration (PowerShell/Bash/Batch/Ansible), and governance-first workflows to land over time (*roadmap*).

**Start automating your ideas, today.**

---

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
          - url: "http://192.168.3.254:5173"
        passHostHeader: true
```
