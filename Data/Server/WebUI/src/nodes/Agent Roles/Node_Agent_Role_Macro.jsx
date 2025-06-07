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

const MacroKeyPressNode = ({ id, data }) => {
  const { setNodes, getNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);

  // Determine if agent is connected
  const agentEdge = edges.find((e) => e.target === id && e.targetHandle === "agent");
  const agentNode = agentEdge && getNodes().find((n) => n.id === agentEdge.source);
  const agentConnection = !!(agentNode && agentNode.data && agentNode.data.agent_id);

  // Macro run/trigger state (sidebar sets this via config, but node UI just shows status)
  const running = data?.active === true || data?.active === "true";

  // Node UI (no config fields, only status)
  return (
    <div className="borealis-node" style={{ minWidth: 240, position: "relative" }}>
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
            backgroundColor: running ? "#00d18c" : "#333",
            border: "1px solid #222"
          }}
        />
      </div>

      <div className="borealis-node-content">
        <strong>Status</strong>: {running ? "Running" : "Idle"}
        <br />
        <strong>Agent Connection</strong>: {agentConnection ? "Connected" : "Not Connected"}
      </div>
    </div>
  );
};

// ----- Node Catalog Export -----
export default {
  type: "Macro_KeyPress",
  label: "Agent Role: Macro",
  description: `
Send automated key presses or typed text to any open application window on the connected agent.
Supports manual, continuous, trigger, and one-shot modes for automation and event-driven workflows.
`,
  content: "Send Key Press or Typed Text to Window via Agent",
  component: MacroKeyPressNode,
  config: [
  { key: "window_handle", label: "Target Window", type: "text", defaultValue: "" },
  { key: "macro_type", label: "Macro Type", type: "select", options: ["keypress", "typed_text"], defaultValue: "keypress" },
  { key: "key", label: "Key", type: "text", defaultValue: "" },
  { key: "text", label: "Typed Text", type: "text", defaultValue: "" },
  { key: "interval_ms", label: "Interval (ms)", type: "text", defaultValue: "1000" },
  { key: "randomize_interval", label: "Randomize Interval", type: "select", options: ["true", "false"], defaultValue: "false" },
  { key: "random_min", label: "Random Min (ms)", type: "text", defaultValue: "750" },
  { key: "random_max", label: "Random Max (ms)", type: "text", defaultValue: "950" },
  { key: "operation_mode", label: "Operation Mode", type: "select", options: OPERATION_MODES, defaultValue: "Continuous" },
  { key: "active", label: "Macro Enabled", type: "select", options: ["true", "false"], defaultValue: "false" }
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

---
  `.trim()
};
