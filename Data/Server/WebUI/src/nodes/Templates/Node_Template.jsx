////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Templates/Node_Template.jsx

/**
 * ==================================================
 * Borealis - Node Template (Golden Template)
 * ==================================================
 *
 * COMPONENT ROLE:
 * Serves as a comprehensive template for creating new
 * Borealis nodes. This file includes detailed commentary
 * for human developers and AI systems, explaining every
 * aspect of a node's structure, state management, data flow,
 * and integration with the shared memory bus.
 *
 * METADATA:
 * - type: unique identifier for the node type (Data entry)
 * - label: display name in Borealis UI
 * - description: explanatory tooltip shown to users
 * - content: short summary of node functionality
 *
 * USAGE:
 * Copy and rename this file when building a new node.
 * Update the metadata and customize logic inside runNodeLogic().
 */

import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// Ensure the global shared bus exists for inter-node communication
if (!window.BorealisValueBus) {
  window.BorealisValueBus = {};
}

// Ensure a default global update rate (ms) for polling loops
if (!window.BorealisUpdateRate) {
  window.BorealisUpdateRate = 100;
}

/**
 * TemplateNode Component
 * ----------------------
 * A single-input, single-output node that propagates a string value.
 *
 * @param {Object} props
 * @param {string} props.id - Unique node identifier in the flow
 * @param {Object} props.data - Node-specific data and settings
 */
const TemplateNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);

  // Local state holds the current string value shown in the textbox
  // AI Note: valueRef.current tracks the last emitted value to prevent redundant bus writes
  const [renderValue, setRenderValue] = useState(data?.value || "/Data/Server/WebUI/src/nodes/Templates/Node_Template.jsx");
  const valueRef = useRef(renderValue);

  /**
   * handleManualInput
   * -----------------
   * Called when the user types into the textbox (only when no input edge).
   * Writes the new value to the shared bus and updates node state.
   */
  const handleManualInput = (e) => {
    const newValue = e.target.value;

    // Update local ref and component state
    valueRef.current = newValue;
    setRenderValue(newValue);

    // Broadcast value on the shared bus
    window.BorealisValueBus[id] = newValue;

    // Persist the new value in node.data for workflow serialization
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id
          ? { ...n, data: { ...n.data, value: newValue } }
          : n
      )
    );
  };

  /**
   * Polling effect: runNodeLogic
   * ----------------------------
   * On mount, start an interval that:
   * - Checks for an upstream connection
   * - If connected, reads from bus and updates state/bus
   * - If not, broadcasts manual input value
   * - Monitors for global rate changes and reconfigures
   */
  useEffect(() => {
    let currentRate = window.BorealisUpdateRate;
    let intervalId;

    const runNodeLogic = () => {
      // Detect if a source edge is connected to this node's input
      const inputEdge = edges.find((e) => e.target === id);
      const hasInput = Boolean(inputEdge && inputEdge.source);

      if (hasInput) {
        // Read upstream value
        const upstreamValue = window.BorealisValueBus[inputEdge.source] || "";

        // Only update if value changed
        if (upstreamValue !== valueRef.current) {
          valueRef.current = upstreamValue;
          setRenderValue(upstreamValue);
          window.BorealisValueBus[id] = upstreamValue;

          // Persist to node.data for serialization
          setNodes((nds) =>
            nds.map((n) =>
              n.id === id
                ? { ...n, data: { ...n.data, value: upstreamValue } }
                : n
            )
          );
        }
      } else {
        // No upstream: broadcast manual input value
        window.BorealisValueBus[id] = valueRef.current;
      }
    };

    // Start polling
    intervalId = setInterval(runNodeLogic, currentRate);

    // Watch for global rate changes
    const monitor = setInterval(() => {
      const newRate = window.BorealisUpdateRate;
      if (newRate !== currentRate) {
        clearInterval(intervalId);
        currentRate = newRate;
        intervalId = setInterval(runNodeLogic, currentRate);
      }
    }, 250);

    // Cleanup on unmount
    return () => {
      clearInterval(intervalId);
      clearInterval(monitor);
    };
  }, [id, edges, setNodes]);

  // Determine connection status for UI control disabling
  const inputEdge = edges.find((e) => e.target === id);
  const hasInput = Boolean(inputEdge && inputEdge.source);

  return (
    <div className="borealis-node">
      {/* Input connector on left */}
      <Handle type="target" position={Position.Left} className="borealis-handle" />

      {/* Header: displays node title */}
      <div className="borealis-node-header">
        {data?.label || "Node Template"}
      </div>

      {/* Content area: description and input control */}
      <div className="borealis-node-content">
        {/* Description: guideline for human users */}
        <div style={{ marginBottom: "8px", fontSize: "9px", color: "#ccc" }}>
          {data?.content || "Template acting as a design scaffold for designing nodes for Borealis."}
        </div>

        {/* Label for the textbox */}
        <label style={{ fontSize: "9px", display: "block", marginBottom: "4px" }}>
          Template Location:
        </label>

        {/* Textbox: disabled if upstream data present */}
        <input
          type="text"
          value={renderValue}
          onChange={handleManualInput}
          disabled={hasInput}
          style={{
            fontSize: "9px",
            padding: "4px",
            background: hasInput ? "#2a2a2a" : "#1e1e1e",
            color: "#ccc",
            border: "1px solid #444",
            borderRadius: "2px",
            width: "100%"
          }}
        />
      </div>

      {/* Output connector on right */}
      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

// Export node metadata for Borealis
export default {
  type: "Node_Template",                // Unique node type identifier
  label: "Node Template",               // Display name in UI
  description: `Node structure template to be used as a scaffold when building new nodes for Borealis.`, // Node Sidebar Tooltip Description
  content: "Template acting as a design scaffold for designing nodes for Borealis.",  // Short summary
  component: TemplateNode              // React component
};
