////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/General Purpose/Node_Data.jsx
import React, { useEffect, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
import { IconButton } from "@mui/material";
import { Settings as SettingsIcon } from "@mui/icons-material";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const DataNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);
  const valueRef = useRef(data?.value || "");

  useEffect(() => {
    valueRef.current = data?.value || "";
    window.BorealisValueBus[id] = valueRef.current;
  }, [data?.value, id]);

  useEffect(() => {
    let currentRate = window.BorealisUpdateRate || 100;
    let intervalId = null;

    const runNodeLogic = () => {
      const inputEdge = edges.find((e) => e?.target === id);
      const hasInput = Boolean(inputEdge?.source);

      if (hasInput) {
        const upstreamValue = window.BorealisValueBus[inputEdge.source] ?? "";
        if (upstreamValue !== valueRef.current) {
          valueRef.current = upstreamValue;
          window.BorealisValueBus[id] = upstreamValue;
          setNodes((nds) =>
            nds.map((n) =>
              n.id === id ? { ...n, data: { ...n.data, value: upstreamValue } } : n
            )
          );
        }
      } else {
        window.BorealisValueBus[id] = valueRef.current;
      }
    };

    intervalId = setInterval(runNodeLogic, currentRate);
    const monitor = setInterval(() => {
      const newRate = window.BorealisUpdateRate || 100;
      if (newRate !== currentRate) {
        clearInterval(intervalId);
        currentRate = newRate;
        intervalId = setInterval(runNodeLogic, currentRate);
      }
    }, 250);

    return () => {
      clearInterval(intervalId);
      clearInterval(monitor);
    };
  }, [id, setNodes, edges]);

  return (
    <div className="borealis-node">
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <div className="borealis-node-header" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span>{data?.label || "Data Node"}</span>
        <IconButton
          size="small"
          onClick={() =>
            window.BorealisOpenDrawer &&
            window.BorealisOpenDrawer(id, { ...data, nodeId: id })
          }
          sx={{ padding: 0, marginRight: "-3px", color: "#58a6ff", width: "20px", height: "20px" }}
        >
          <SettingsIcon sx={{ fontSize: "16px" }} />
        </IconButton>
      </div>
      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

export default {
  type: "DataNode",
  label: "String / Number",
  description: "Foundational node for live value propagation.\n\n- Accepts input or manual value\n- Pushes downstream\n- Uses shared memory",
  content: "Store a String or Number",
  component: DataNode,
  config: [
    { key: "value", label: "Value", type: "text" }
  ],
  usage_documentation: `
### Description:
This node acts as a basic live data emitter. When connected to an upstream node, it inherits its value, otherwise it accepts user-defined input of either a number or a string.

**Acceptable Inputs**:
- **Static Value** (*Number or String*)

**Behavior**:
- **Pass-through Conduit** (*If Upstream Node is Connected*) > Value cannot be manually changed while connected to an upstream node.
- Uses global Borealis "**Update Rate**" for updating value if connected to an upstream node.
  `.trim()
};
