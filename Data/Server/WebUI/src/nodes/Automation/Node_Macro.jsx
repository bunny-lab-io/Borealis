////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Automation/Node_Macro.jsx
import React, { useState, useEffect, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
import Keyboard from "react-simple-keyboard";
import "react-simple-keyboard/build/css/index.css";

// Default update interval for window list refresh (in ms)
const WINDOW_LIST_REFRESH_MS = 4000;

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const DEFAULT_OPERATION_MODE = "Continuous";
const OPERATION_MODES = [
  "Continuous",
  "Trigger-Continuous",
  "Trigger-Once",
  "Run Once"
];

const MACRO_TYPES = [
  { value: "keypress", label: "Single Keypress" },
  { value: "typed_text", label: "Typed Text" }
];

const MacroKeyPressNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);

  // State for agent window list dropdown
  const [windowList, setWindowList] = useState([]);
  const [selectedWindow, setSelectedWindow] = useState(data?.window_handle || "");
  const [windowListStatus, setWindowListStatus] = useState("Loading...");

  // State for keyboard/text settings
  const [macroType, setMacroType] = useState(data?.macro_type || "keypress");
  const [keyPressed, setKeyPressed] = useState(data?.key || "");
  const [typedText, setTypedText] = useState(data?.text || "");
  const [showKeyboard, setShowKeyboard] = useState(false);
  const [layoutName, setLayoutName] = useState("default");

  // Interval/randomization settings
  const [intervalMs, setIntervalMs] = useState(data?.interval_ms || 1000);
  const [randomize, setRandomize] = useState(!!data?.randomize_interval);
  const [randomMin, setRandomMin] = useState(data?.random_min || 750);
  const [randomMax, setRandomMax] = useState(data?.random_max || 950);

  // Macro run/trigger state
  const [operationMode, setOperationMode] = useState(data?.operation_mode || DEFAULT_OPERATION_MODE);
  const [running, setRunning] = useState(data?.active ?? false);

  // For Trigger-Once logic
  const triggerActiveRef = useRef(false);

  // Fetch windows from agent using WebSocket
  useEffect(() => {
    let isMounted = true;
    let interval = null;

    const fetchWindowList = () => {
      setWindowListStatus("Loading...");
      if (!window.BorealisSocket) return setWindowListStatus("No agent connection");
      // Find the upstream agent node, get its agent_id
      let agentId = null;
      for (const e of edges) {
        if (e.target === id && e.sourceHandle === "provisioner") {
          const agentNode = window.BorealisFlowNodes?.find((n) => n.id === e.source);
          agentId = agentNode?.data?.agent_id;
        }
      }
      if (!agentId) return setWindowListStatus("No agent connected");
      window.BorealisSocket.emit("list_agent_windows", { agent_id: agentId });
    };

    // Listen for reply
    function handleAgentWindowList(payload) {
      if (!isMounted) return;
      if (!payload || !payload.windows) return setWindowListStatus("No windows found");
      setWindowList(payload.windows);
      setWindowListStatus(payload.windows.length ? "" : "No windows found");
    }
    if (window.BorealisSocket) {
      window.BorealisSocket.on("agent_window_list", handleAgentWindowList);
    }
    fetchWindowList();
    interval = setInterval(fetchWindowList, WINDOW_LIST_REFRESH_MS);

    return () => {
      isMounted = false;
      if (window.BorealisSocket) {
        window.BorealisSocket.off("agent_window_list", handleAgentWindowList);
      }
      clearInterval(interval);
    };
  }, [id, edges]);

  // Macro state (simulate agent push)
  useEffect(() => {
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id
          ? {
              ...n,
              data: {
                ...n.data,
                window_handle: selectedWindow,
                macro_type: macroType,
                key: keyPressed,
                text: typedText,
                interval_ms: intervalMs,
                randomize_interval: randomize,
                random_min: randomMin,
                random_max: randomMax,
                operation_mode: operationMode,
                active: running
              }
            }
          : n
      )
    );
    // BorealisValueBus: Set running status as "1" or "0"
    window.BorealisValueBus[id] = running ? "1" : "0";
  }, [
    id,
    setNodes,
    selectedWindow,
    macroType,
    keyPressed,
    typedText,
    intervalMs,
    randomize,
    randomMin,
    randomMax,
    operationMode,
    running
  ]);

  // Input trigger/handle logic for trigger-based modes
  useEffect(() => {
    if (!["Trigger-Continuous", "Trigger-Once"].includes(operationMode)) return;
    // Find first left input edge
    const edge = edges.find((e) => e.target === id);
    if (!edge) return;
    const upstreamValue = window.BorealisValueBus[edge.source];
    if (operationMode === "Trigger-Continuous") {
      setRunning(upstreamValue === "1");
    } else if (operationMode === "Trigger-Once") {
      // Only fire once per rising edge
      if (upstreamValue === "1" && !triggerActiveRef.current) {
        setRunning(true);
        triggerActiveRef.current = true;
        setTimeout(() => setRunning(false), 10); // Simulate a quick one-shot macro
      } else if (upstreamValue !== "1" && triggerActiveRef.current) {
        triggerActiveRef.current = false;
      }
    }
  }, [edges, id, operationMode]);

  // Handle Start/Stop button for manual modes
  const handleStartStop = () => {
    setRunning((v) => !v);
  };

  // Keyboard overlay logic
  const onKeyPress = (button) => {
    // SHIFT or CAPS toggling:
    if (button === "{shift}" || button === "{lock}") {
      setLayoutName((prev) => (prev === "default" ? "shift" : "default"));
      return;
    }
    // Accept only standard keys (not function/meta keys)
    const skipKeys = [
      "{bksp}", "{space}", "{tab}", "{enter}", "{escape}",
      "{f1}", "{f2}", "{f3}", "{f4}", "{f5}", "{f6}",
      "{f7}", "{f8}", "{f9}", "{f10}", "{f11}", "{f12}",
      "{shift}", "{lock}"
    ];
    if (!skipKeys.includes(button)) {
      setKeyPressed(button);
      setShowKeyboard(false);
    }
  };

  // Node UI
  return (
    <div className="borealis-node" style={{ minWidth: 240, position: "relative" }}>
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <Handle type="source" position={Position.Right} className="borealis-handle" />

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
        <div style={{ fontSize: "9px", color: "#ccc", marginBottom: "8px" }}>
          Sends macro input to selected window via agent.
        </div>
        <label>Window:</label>
        <select
          value={selectedWindow}
          onChange={(e) => setSelectedWindow(e.target.value)}
          style={inputStyle}
        >
          <option value="">-- Choose --</option>
          {windowList.map((win) => (
            <option key={win.handle} value={win.handle}>
              {win.title}
            </option>
          ))}
        </select>
        <div style={{ fontSize: "8px", color: "#ff8c00", marginBottom: 4 }}>
          {windowListStatus}
        </div>

        {/* Macro Type */}
        <label>Macro Type:</label>
        <select
          value={macroType}
          onChange={(e) => setMacroType(e.target.value)}
          style={inputStyle}
        >
          {MACRO_TYPES.map((m) => (
            <option key={m.value} value={m.value}>
              {m.label}
            </option>
          ))}
        </select>

        {/* Key or Text Selection */}
        {macroType === "keypress" ? (
          <>
            <label>Key:</label>
            <div style={{ display: "flex", gap: "6px", alignItems: "center", marginBottom: "6px" }}>
              <button onClick={() => setShowKeyboard(true)} style={buttonStyle}>
                Select Key
              </button>
              <input
                type="text"
                value={keyPressed}
                disabled
                readOnly
                style={{
                  ...inputStyle,
                  width: "60px",
                  backgroundColor: "#2a2a2a",
                  color: "#aaa",
                  cursor: "default"
                }}
              />
            </div>
          </>
        ) : (
          <>
            <label>Typed Text:</label>
            <input
              type="text"
              value={typedText}
              onChange={(e) => setTypedText(e.target.value)}
              style={{ ...inputStyle, width: "100%" }}
              maxLength={256}
            />
          </>
        )}

        {/* Interval */}
        <label>Interval (ms):</label>
        <input
          type="number"
          min="100"
          step="50"
          value={intervalMs}
          onChange={(e) => setIntervalMs(Number(e.target.value))}
          disabled={randomize}
          style={{
            ...inputStyle,
            backgroundColor: randomize ? "#2a2a2a" : "#1e1e1e"
          }}
        />

        {/* Randomize Interval */}
        <label>
          <input
            type="checkbox"
            checked={randomize}
            onChange={(e) => setRandomize(e.target.checked)}
            style={{ marginRight: "6px" }}
          />
          Randomize Interval
        </label>
        {randomize && (
          <div style={{ display: "flex", gap: "4px", marginTop: "4px" }}>
            <input
              type="number"
              min="100"
              value={randomMin}
              onChange={(e) => setRandomMin(Number(e.target.value))}
              style={{ ...inputStyle, flex: 1 }}
            />
            <input
              type="number"
              min="100"
              value={randomMax}
              onChange={(e) => setRandomMax(Number(e.target.value))}
              style={{ ...inputStyle, flex: 1 }}
            />
          </div>
        )}

        {/* Operation Mode */}
        <label>Operation Mode:</label>
        <select
          value={operationMode}
          onChange={(e) => setOperationMode(e.target.value)}
          style={inputStyle}
        >
          {OPERATION_MODES.map((mode) => (
            <option key={mode} value={mode}>
              {mode}
            </option>
          ))}
        </select>

        {/* Start/Stop Button */}
        {operationMode === "Continuous" || operationMode === "Run Once" ? (
          <button
            onClick={handleStartStop}
            style={{
              ...buttonStyle,
              marginTop: "8px",
              width: "100%",
              backgroundColor: running ? "#ff4f4f" : "#00d18c",
              color: "#fff",
              fontWeight: "bold"
            }}
          >
            {running ? "Pause" : operationMode === "Run Once" ? "Run Once" : "Start"}
          </button>
        ) : null}
      </div>

      {/* Keyboard Overlay */}
      {showKeyboard && (
        <div style={keyboardOverlay}>
          <div style={keyboardContainer}>
            <div
              style={{
                fontSize: "11px",
                color: "#ccc",
                marginBottom: "6px",
                textAlign: "center"
              }}
            >
              Full Keyboard
            </div>
            <Keyboard
              onKeyPress={onKeyPress}
              layoutName={layoutName}
              theme="hg-theme-dark hg-layout-default"
              layout={{
                default: [
                  "{escape} {f1} {f2} {f3} {f4} {f5} {f6} {f7} {f8} {f9} {f10} {f11} {f12}",
                  "` 1 2 3 4 5 6 7 8 9 0 - = {bksp}",
                  "{tab} q w e r t y u i o p [ ] \\",
                  "{lock} a s d f g h j k l ; ' {enter}",
                  "{shift} z x c v b n m , . / {shift}",
                  "{space}"
                ],
                shift: [
                  "{escape} {f1} {f2} {f3} {f4} {f5} {f6} {f7} {f8} {f9} {f10} {f11} {f12}",
                  "~ ! @ # $ % ^ & * ( ) _ + {bksp}",
                  "{tab} Q W E R T Y U I O P { } |",
                  "{lock} A S D F G H J K L : \" {enter}",
                  "{shift} Z X C V B N M < > ? {shift}",
                  "{space}"
                ]
              }}
              display={{
                "{bksp}": "⌫",
                "{escape}": "esc",
                "{tab}": "tab",
                "{lock}": "caps",
                "{enter}": "enter",
                "{shift}": "shift",
                "{space}": "space",
                "{f1}": "F1",
                "{f2}": "F2",
                "{f3}": "F3",
                "{f4}": "F4",
                "{f5}": "F5",
                "{f6}": "F6",
                "{f7}": "F7",
                "{f8}": "F8",
                "{f9}": "F9",
                "{f10}": "F10",
                "{f11}": "F11",
                "{f12}": "F12"
              }}
            />
            <div style={{ display: "flex", justifyContent: "center", marginTop: "8px" }}>
              <button onClick={() => setShowKeyboard(false)} style={buttonStyle}>
                Close
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

// ----- Node Catalog Export -----
const inputStyle = {
  width: "100%",
  fontSize: "9px",
  padding: "4px",
  color: "#ccc",
  border: "1px solid #444",
  borderRadius: "2px",
  marginBottom: "6px"
};

const buttonStyle = {
  fontSize: "9px",
  padding: "4px 8px",
  backgroundColor: "#1e1e1e",
  color: "#ccc",
  border: "1px solid #444",
  borderRadius: "2px",
  cursor: "pointer"
};

const keyboardOverlay = {
  position: "fixed",
  top: 0,
  left: 0,
  width: "100vw",
  height: "100vh",
  zIndex: 1000,
  backgroundColor: "rgba(0, 0, 0, 0.8)",
  display: "flex",
  justifyContent: "center",
  alignItems: "center"
};

const keyboardContainer = {
  backgroundColor: "#1e1e1e",
  padding: "16px",
  borderRadius: "6px",
  border: "1px solid #444",
  zIndex: 1001,
  maxWidth: "650px"
};

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
    { key: "window_handle", label: "Target Window", type: "text" },
    { key: "macro_type", label: "Macro Type", type: "select", options: ["keypress", "typed_text"] },
    { key: "key", label: "Key", type: "text" },
    { key: "text", label: "Typed Text", type: "text" },
    { key: "interval_ms", label: "Interval (ms)", type: "text" },
    { key: "randomize_interval", label: "Randomize Interval", type: "select", options: ["true", "false"] },
    { key: "random_min", label: "Random Min (ms)", type: "text" },
    { key: "random_max", label: "Random Max (ms)", type: "text" },
    { key: "operation_mode", label: "Operation Mode", type: "select", options: OPERATION_MODES },
    { key: "active", label: "Active", type: "select", options: ["true", "false"] }
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
