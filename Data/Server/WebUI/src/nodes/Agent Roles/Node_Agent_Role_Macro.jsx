////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Agent Roles/Node_Agent_Role_Macro.jsx
import React, { useState, useEffect, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
import "react-simple-keyboard/build/css/index.css";

// Default update interval for window list refresh (in ms)
const WINDOW_LIST_REFRESH_MS = 4000;

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const DEFAULT_OPERATION_MODE = "Continuous";
const OPERATION_MODES = [
  "Run Once",
  "Continuous",
  "Trigger-Once",
  "Trigger-Continuous"
];

const MACRO_TYPES = [
  "keypress",
  "typed_text"
];

const statusColors = {
  idle: "#333",
  running: "#00d18c",
  error: "#ff4f4f",
  success: "#00d18c"
};

const MacroKeyPressNode = ({ id, data }) => {
  const { setNodes, getNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);
  const [windowList, setWindowList] = useState([]);
  const [status, setStatus] = useState({ state: "idle", message: "" });
  const socketRef = useRef(null);

  // Determine if agent is connected
  const agentEdge = edges.find((e) => e.target === id && e.targetHandle === "agent");
  const agentNode = agentEdge && getNodes().find((n) => n.id === agentEdge.source);
  const agentConnection = !!(agentNode && agentNode.data && agentNode.data.agent_id);
  const agent_id = agentNode && agentNode.data && agentNode.data.agent_id;

  // Macro run/trigger state (sidebar sets this via config, but node UI just shows status)
  const running = data?.active === true || data?.active === "true";

  // Store for last macro error/status
  const [lastMacroStatus, setLastMacroStatus] = useState({ success: true, message: "", timestamp: null });

  // Setup WebSocket for agent macro status updates
  useEffect(() => {
    if (!window.BorealisSocket) return;
    const socket = window.BorealisSocket;
    socketRef.current = socket;

    function handleMacroStatus(payload) {
      if (
        payload &&
        payload.agent_id === agent_id &&
        payload.node_id === id
      ) {
        setLastMacroStatus({
          success: !!payload.success,
          message: payload.message || "",
          timestamp: payload.timestamp || Date.now()
        });
        setStatus({
          state: payload.success ? "success" : "error",
          message: payload.message || (payload.success ? "Success" : "Error")
        });
      }
    }

    socket.on("macro_status", handleMacroStatus);
    return () => {
      socket.off("macro_status", handleMacroStatus);
    };
  }, [agent_id, id]);

  // Auto-refresh window list from agent
  useEffect(() => {
    let intervalId = null;
    async function fetchWindows() {
      if (window.BorealisSocket && agentConnection) {
        window.BorealisSocket.emit("list_agent_windows", {
          agent_id
        });
      }
    }
    fetchWindows();
    intervalId = setInterval(fetchWindows, WINDOW_LIST_REFRESH_MS);

    // Listen for agent_window_list updates
    function handleAgentWindowList(payload) {
      if (payload?.agent_id === agent_id && Array.isArray(payload.windows)) {
        setWindowList(payload.windows);

        // Store windowList in node data for sidebar dynamic dropdowns
        setNodes(nds =>
          nds.map(n =>
            n.id === id
              ? { ...n, data: { ...n.data, windowList: payload.windows } }
              : n
          )
        );
      }
    }
    if (window.BorealisSocket) {
      window.BorealisSocket.on("agent_window_list", handleAgentWindowList);
    }

    return () => {
      clearInterval(intervalId);
      if (window.BorealisSocket) {
        window.BorealisSocket.off("agent_window_list", handleAgentWindowList);
      }
    };
  }, [agent_id, agentConnection, setNodes, id]);

  // Register this node for agent provisioning
  window.__BorealisInstructionNodes = window.__BorealisInstructionNodes || {};
  window.__BorealisInstructionNodes[id] = () => ({
    node_id: id,
    role: "macro",
    window_handle: data?.window_handle || "",
    macro_type: data?.macro_type || "keypress",
    key: data?.key || "",
    text: data?.text || "",
    interval_ms: parseInt(data?.interval_ms || 1000, 10),
    randomize_interval: data?.randomize_interval === true || data?.randomize_interval === "true",
    random_min: parseInt(data?.random_min || 750, 10),
    random_max: parseInt(data?.random_max || 950, 10),
    operation_mode: data?.operation_mode || DEFAULT_OPERATION_MODE,
    active: data?.active === true || data?.active === "true",
    trigger: parseInt(data?.trigger || 0, 10)
  });

  // UI: Start/Pause Button
  const handleToggleMacro = () => {
    setNodes(nds =>
      nds.map(n =>
        n.id === id
          ? {
              ...n,
              data: {
                ...n.data,
                active: n.data?.active === true || n.data?.active === "true" ? "false" : "true"
              }
            }
          : n
      )
    );
  };

  // Optional: Show which window is targeted by name
  const selectedWindow = (windowList || []).find(w => String(w.handle) === String(data?.window_handle));

  // Node UI (no config fields, only status + window list)
  return (
    <div className="borealis-node" style={{ minWidth: 280, position: "relative" }}>
      {/* --- INPUT LABELS & HANDLES --- */}
      <div style={{
        position: "absolute",
        left: -30,
        top: 26,
        fontSize: "8px",
        color: "#6ef9fb",
        letterSpacing: 0.5,
        pointerEvents: "none"
      }}>
        Agent
      </div>
      <Handle
        type="target"
        position={Position.Left}
        id="agent"
        style={{
          top: 25,
        }}
        className="borealis-handle"
      />
      <div style={{
        position: "absolute",
        left: -34,
        top: 70,
        fontSize: "8px",
        color: "#6ef9fb",
        letterSpacing: 0.5,
        pointerEvents: "none"
      }}>
        Trigger
      </div>
      <Handle
        type="target"
        position={Position.Left}
        id="trigger"
        style={{
          top: 68,
        }}
        className="borealis-handle"
      />

      <div className="borealis-node-header" style={{ position: "relative" }}>
        Agent Role: Macro
        <div
          style={{
            position: "absolute",
            top: "50%",
            right: "8px",
            width: "10px",
            transform: "translateY(-50%)",
            height: "10px",
            borderRadius: "50%",
            backgroundColor:
              status.state === "error"
                ? statusColors.error
                : running
                ? statusColors.running
                : statusColors.idle,
            border: "1px solid #222"
          }}
        />
      </div>

      <div className="borealis-node-content">
        <strong>Status</strong>:{" "}
        {status.state === "error"
          ? (
            <span style={{ color: "#ff4f4f" }}>
              Error{lastMacroStatus.message ? `: ${lastMacroStatus.message}` : ""}
            </span>
          )
          : running
            ? (
              <span style={{ color: "#00d18c" }}>
                Running{lastMacroStatus.message ? ` (${lastMacroStatus.message})` : ""}
              </span>
            )
            : "Idle"}
        <br />
        <strong>Agent Connection</strong>: {agentConnection ? "Connected" : "Not Connected"}
        <br />
        <strong>Target Window</strong>:{" "}
        {selectedWindow
          ? `${selectedWindow.title} (${selectedWindow.handle})`
          : data?.window_handle
            ? `Handle: ${data.window_handle}`
            : <span style={{ color: "#888" }}>Not set</span>}
        <br />
        <strong>Mode</strong>: {data?.operation_mode || DEFAULT_OPERATION_MODE}
        <br />
        <strong>Macro Type</strong>: {data?.macro_type || "keypress"}
        <br />
        <button
          onClick={handleToggleMacro}
          style={{
            marginTop: 8,
            padding: "4px 10px",
            background: running ? "#3a3a3a" : "#0475c2",
            color: running ? "#fff" : "#fff",
            border: "1px solid #0475c2",
            borderRadius: 3,
            fontSize: "11px",
            cursor: "pointer"
          }}
        >
          {running ? "Pause Macro" : "Start Macro"}
        </button>
        <br />
        <span style={{ fontSize: "9px", color: "#aaa" }}>
          {lastMacroStatus.timestamp
            ? `Last event: ${new Date(lastMacroStatus.timestamp).toLocaleTimeString()}`
            : ""}
        </span>
      </div>
    </div>
  );
};

// ----- Node Catalog Export -----
export default {
  type: "Macro_KeyPress",
  role: "macro",
  label: "Agent Role: Macro",
  description: `
Send automated key presses or typed text to any open application window on the connected agent.
Supports manual, continuous, trigger, and one-shot modes for automation and event-driven workflows.
`,
  content: "Send Key Press or Typed Text to Window via Agent (AutoHotKey)",
  component: MacroKeyPressNode,
  config: [
    { key: "window_handle", label: "Target Window", type: "select", dynamicOptions: true, defaultValue: "" },
    { key: "macro_type", label: "Macro Type", type: "select", options: ["keypress", "typed_text"], defaultValue: "keypress" },
    { key: "key", label: "Key", type: "text", defaultValue: "" },
    { key: "text", label: "Typed Text", type: "text", defaultValue: "" },
    { key: "interval_ms", label: "Interval (ms)", type: "text", defaultValue: "1000" },
    { key: "randomize_interval", label: "Randomize Interval", type: "select", options: ["true", "false"], defaultValue: "false" },
    { key: "random_min", label: "Random Min (ms)", type: "text", defaultValue: "750" },
    { key: "random_max", label: "Random Max (ms)", type: "text", defaultValue: "950" },
    { key: "operation_mode", label: "Operation Mode", type: "select", options: OPERATION_MODES, defaultValue: "Continuous" },
    { key: "active", label: "Macro Enabled", type: "select", options: ["true", "false"], defaultValue: "false" },
    { key: "trigger", label: "Trigger Value", type: "text", defaultValue: "0" }
  ],
  usage_documentation: `
### Agent Role: Macro

**Modes:**
- **Continuous**: Macro sends input non-stop when started by button.
- **Trigger-Continuous**: Macro sends input as long as upstream trigger is "1".
- **Trigger-Once**: Macro fires once per upstream "1" (one-shot edge).
- **Run Once**: Macro runs only once when started by button.

**Macro Types:**
- **Single Keypress**: Press a single key.
- **Typed Text**: Types out a string.

**Window Target:**
- Dropdown of live windows from agent, stays updated.

**Event-Driven Support:**
- Chain with other Borealis nodes (text recognition, event triggers, etc).

**Live Status:**
- Displays last agent macro event and error feedback in node.

---
  `.trim()
};
