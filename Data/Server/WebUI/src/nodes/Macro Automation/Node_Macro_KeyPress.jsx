////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Macro Automation/Node_Macro_KeyPress.jsx
import React, { useState, useRef } from "react";
import { Handle, Position } from "reactflow";
import Keyboard from "react-simple-keyboard";
import "react-simple-keyboard/build/css/index.css";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

/**
 * KeyPressNode:
 * - Full keyboard with SHIFT toggling
 * - Press F-keys, digits, letters, or symbols
 * - Single key stored, overlay closes
 * - SHIFT or CAPS toggles "default" <-> "shift"
 */

const KeyPressNode = ({ id, data }) => {
  const [selectedWindow, setSelectedWindow] = useState(data?.selectedWindow || "");
  const [keyPressed, setKeyPressed] = useState(data?.keyPressed || "");
  const [intervalMs, setIntervalMs] = useState(data?.intervalMs || 1000);
  const [randomRangeEnabled, setRandomRangeEnabled] = useState(false);
  const [randomMin, setRandomMin] = useState(750);
  const [randomMax, setRandomMax] = useState(950);

  // Keyboard overlay
  const [showKeyboard, setShowKeyboard] = useState(false);
  const [layoutName, setLayoutName] = useState("default");

  // A simple set of Windows for demonstration
  const fakeWindows = ["Notepad", "Chrome", "Discord", "Visual Studio Code"];

  // This function is triggered whenever the user taps a key on the virtual keyboard
  const onKeyPress = (button) => {
    // SHIFT or CAPS toggling:
    if (button === "{shift}" || button === "{lock}") {
      handleShift();
      return;
    }

    // Example skip list: these won't be stored as final single key
    const skipKeys = [
      "{bksp}", "{space}", "{tab}", "{enter}", "{escape}",
      "{f1}", "{f2}", "{f3}", "{f4}", "{f5}", "{f6}",
      "{f7}", "{f8}", "{f9}", "{f10}", "{f11}", "{f12}",
      "{shift}", "{lock}"
    ];

    // If the pressed button is not in skipKeys, let's store it and close
    if (!skipKeys.includes(button)) {
      setKeyPressed(button);
      setShowKeyboard(false);
    }
  };

  // Toggle between "default" layout and "shift" layout
  const handleShift = () => {
    setLayoutName((prev) => (prev === "default" ? "shift" : "default"));
  };

  return (
    <div className="borealis-node" style={{ minWidth: 240, position: "relative" }}>
      {/* React Flow Handles */}
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <Handle type="source" position={Position.Right} className="borealis-handle" />

      {/* Node Header */}
      <div className="borealis-node-header" style={{ position: "relative" }}>
        Key Press
        <div
          style={{
            position: "absolute",
            top: "12px",
            right: "6px",
            width: "10px",
            height: "10px",
            borderRadius: "50%",
            backgroundColor: "#333",
            border: "1px solid #222"
          }}
        />
      </div>

      {/* Node Content */}
      <div className="borealis-node-content">
        <div style={{ fontSize: "9px", color: "#ccc", marginBottom: "8px" }}>
          Sends keypress to selected window on trigger.
        </div>

        {/* Window Selector */}
        <label>Window:</label>
        <select
          value={selectedWindow}
          onChange={(e) => setSelectedWindow(e.target.value)}
          style={inputStyle}
        >
          <option value="">-- Choose --</option>
          {fakeWindows.map((win) => (
            <option key={win} value={win}>
              {win}
            </option>
          ))}
        </select>

        {/* Key: "Select Key" button & readOnly input */}
        <label style={{ marginTop: "6px" }}>Key:</label>
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

        {/* Interval Configuration */}
        <label>Fixed Interval (ms):</label>
        <input
          type="number"
          min="100"
          step="50"
          value={intervalMs}
          onChange={(e) => setIntervalMs(Number(e.target.value))}
          disabled={randomRangeEnabled}
          style={{
            ...inputStyle,
            backgroundColor: randomRangeEnabled ? "#2a2a2a" : "#1e1e1e"
          }}
        />

        {/* Random Interval */}
        <label style={{ marginTop: "6px" }}>
          <input
            type="checkbox"
            checked={randomRangeEnabled}
            onChange={(e) => setRandomRangeEnabled(e.target.checked)}
            style={{ marginRight: "6px" }}
          />
          Randomize Interval (ms):
        </label>

        {randomRangeEnabled && (
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

/* Basic styling objects */
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
  label: "Key Press (GUI-ONLY)",
  description: `
Press a single character or function key on a full keyboard overlay.
Shift/caps toggles uppercase/symbols. 
F-keys are included, but pressing them won't store that value unless you remove them from "skip" logic, if desired.
`,
  content: "Send Key Press to Foreground Window via Borealis Agent",
  component: KeyPressNode
};
