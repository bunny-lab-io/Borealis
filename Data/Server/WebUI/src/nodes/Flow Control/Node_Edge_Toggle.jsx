import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
import Switch from "@mui/material/Switch";
import Tooltip from "@mui/material/Tooltip";

/**
 * Borealis - Edge Toggle Node
 * ===========================
 * Allows users to toggle data flow between upstream and downstream nodes.
 * - When enabled: propagates upstream value.
 * - When disabled: outputs "0" (or null/blank) so downstream sees a cleared value.
 */
const EdgeToggleNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);

  // Local state for switch
  const [enabled, setEnabled] = useState(data?.enabled ?? true);
  // What was last passed to output
  const [outputValue, setOutputValue] = useState(undefined);
  const outputRef = useRef(outputValue);

  // Store enabled state persistently
  useEffect(() => {
    setNodes(nds =>
      nds.map(n =>
        n.id === id ? { ...n, data: { ...n.data, enabled } } : n
      )
    );
  }, [enabled, id, setNodes]);

  // Main logic: propagate upstream if enabled, else always set downstream to "0"
  useEffect(() => {
    let interval = null;
    let currentRate = window.BorealisUpdateRate || 100;

    const runNodeLogic = () => {
      const inputEdge = edges.find(e => e.target === id);
      const hasInput = Boolean(inputEdge && inputEdge.source);

      if (enabled && hasInput) {
        const upstreamValue = window.BorealisValueBus[inputEdge.source];
        if (upstreamValue !== outputRef.current) {
          outputRef.current = upstreamValue;
          setOutputValue(upstreamValue);
          window.BorealisValueBus[id] = upstreamValue;
          setNodes(nds =>
            nds.map(n =>
              n.id === id
                ? { ...n, data: { ...n.data, value: upstreamValue } }
                : n
            )
          );
        }
      } else if (!enabled) {
        // Always push zero (or blank/null) when disabled
        if (outputRef.current !== 0) {
          outputRef.current = 0;
          setOutputValue(0);
          window.BorealisValueBus[id] = 0;
          setNodes(nds =>
            nds.map(n =>
              n.id === id
                ? { ...n, data: { ...n.data, value: 0 } }
                : n
            )
          );
        }
      }
    };

    interval = setInterval(runNodeLogic, currentRate);

    const monitor = setInterval(() => {
      const newRate = window.BorealisUpdateRate;
      if (newRate !== currentRate) {
        clearInterval(interval);
        currentRate = newRate;
        interval = setInterval(runNodeLogic, currentRate);
      }
    }, 250);

    return () => {
      clearInterval(interval);
      clearInterval(monitor);
    };
  }, [id, edges, enabled, setNodes]);

  // Edge input detection
  const inputEdge = edges.find(e => e.target === id);
  const hasInput = Boolean(inputEdge && inputEdge.source);

  return (
    <div className="borealis-node">
      {/* Input handle */}
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      {/* Header */}
      <div className="borealis-node-header">
        {data?.label || "Edge Toggle"}
      </div>
      {/* Content */}
      <div className="borealis-node-content">
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <Tooltip title={enabled ? "Turn Off / Send Zero" : "Turn On / Allow Data"} arrow>
            <Switch
              checked={enabled}
              size="small"
              onChange={() => setEnabled(e => !e)}
              sx={{
                "& .MuiSwitch-switchBase.Mui-checked": {
                  color: "#58a6ff",
                },
                "& .MuiSwitch-switchBase.Mui-checked + .MuiSwitch-track": {
                  backgroundColor: "#58a6ff",
                }
              }}
            />
          </Tooltip>
          <span style={{
            fontSize: 9,
            color: enabled ? "#00d18c" : "#ff4f4f",
            fontWeight: "bold",
            marginLeft: 4,
            userSelect: "none"
          }}>
            {enabled ? "Flow Enabled" : "Flow Disabled"}
          </span>
        </div>
      </div>
      {/* Output handle */}
      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

// Node Export for Borealis
export default {
  type: "Edge_Toggle",
  label: "Edge Toggle",
  description: `
Toggles edge data flow ON/OFF using a switch.
- When enabled, passes upstream value to downstream.
- When disabled, sends zero (0) so downstream always sees a cleared value.
- Use to quickly enable/disable parts of your workflow without unlinking edges.
`.trim(),
  content: "Toggle ON/OFF to allow or send zero downstream",
  component: EdgeToggleNode
};
