////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/General Purpose/Node_Logical_Operators.jsx
import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
import { IconButton } from "@mui/material";
import SettingsIcon from "@mui/icons-material/Settings";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const ComparisonNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore(state => state.edges);
  const [renderValue, setRenderValue] = useState("0");
  const valueRef = useRef("0");

  useEffect(() => {
    let currentRate = window.BorealisUpdateRate;
    let intervalId = null;

    const runNodeLogic = () => {
      let inputType = data?.inputType || "Number";
      let operator = data?.operator || "Equal (==)";

      if (inputType === "String" && !["Equal (==)", "Not Equal (!=)"].includes(operator)) {
        operator = "Equal (==)";
        setNodes(nds =>
          nds.map(n =>
            n.id === id ? { ...n, data: { ...n.data, operator } } : n
          )
        );
      }

      const edgeInputsA = edges.filter(e => e?.target === id && e.targetHandle === "a");
      const edgeInputsB = edges.filter(e => e?.target === id && e.targetHandle === "b");

      const extractValues = (edgeList) => {
        const values = edgeList.map(e => window.BorealisValueBus[e.source]).filter(v => v !== undefined);
        if (inputType === "Number") {
          return values.reduce((sum, v) => sum + (parseFloat(v) || 0), 0);
        }
        return values.join("");
      };

      const a = extractValues(edgeInputsA);
      const b = extractValues(edgeInputsB);

      const resultMap = {
        "Equal (==)": a === b,
        "Not Equal (!=)": a !== b,
        "Greater Than (>)": a > b,
        "Less Than (<)": a < b,
        "Greater Than or Equal (>=)": a >= b,
        "Less Than or Equal (<=)": a <= b
      };

      const result = resultMap[operator] ? "1" : "0";

      valueRef.current = result;
      setRenderValue(result);
      window.BorealisValueBus[id] = result;

      setNodes(nds =>
        nds.map(n =>
          n.id === id ? { ...n, data: { ...n.data, value: result } } : n
        )
      );
    };

    intervalId = setInterval(runNodeLogic, currentRate);

    const monitor = setInterval(() => {
      const newRate = window.BorealisUpdateRate;
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
  }, [id, edges, data?.inputType, data?.operator, setNodes]);

  return (
    <div className="borealis-node">
      <div style={{ position: "absolute", left: -16, top: 12, fontSize: "8px", color: "#ccc" }}>A</div>
      <div style={{ position: "absolute", left: -16, top: 50, fontSize: "8px", color: "#ccc" }}>B</div>
      <Handle type="target" position={Position.Left} id="a" style={{ top: 12 }} className="borealis-handle" />
      <Handle type="target" position={Position.Left} id="b" style={{ top: 50 }} className="borealis-handle" />

      <div className="borealis-node-header" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span>{data?.label || "Logic Comparison"}</span>
        <IconButton
          size="small"
          onClick={() => window.BorealisOpenDrawer(id, { ...data, nodeId: id })}
          sx={{ color: "#888", padding: 0, marginLeft: "auto" }}
        >
          <SettingsIcon sx={{ fontSize: 16 }} />
        </IconButton>
      </div>

      <div className="borealis-node-content" style={{ fontSize: "9px", color: "#ccc", marginTop: 4 }}>
        Result: {renderValue}
      </div>

      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

export default {
  type: "ComparisonNode",
  label: "Logic Comparison",
  description: "Compare A vs B using logic operators",
  content: "Compare A and B using Logic",
  component: ComparisonNode,
  config: [
    {
      key: "inputType",
      label: "Input Type",
      type: "select",
      options: ["Number", "String"]
    },
    {
      key: "operator",
      label: "Operator",
      type: "select",
      options: [
        "Equal (==)",
        "Not Equal (!=)",
        "Greater Than (>)",
        "Less Than (<)",
        "Greater Than or Equal (>=)",
        "Less Than or Equal (<=)"
      ]
    }
  ],
  usage_documentation: `
### Logic Comparison Node

This node compares two inputs (A and B) using the selected operator.

**Modes:**
- **Number**: Sums all connected inputs and compares.
- **String**: Concatenates all inputs for comparison.
  - Only **Equal (==)** and **Not Equal (!=)** are valid for strings.

**Output:**
- Returns \`1\` if comparison is true.
- Returns \`0\` if comparison is false.

**Input Notes:**
- A and B can each have multiple inputs.
- Input order matters for strings (concatenation).
- Input handles:
  - **A** = Top left
  - **B** = Middle left
`.trim()
};
