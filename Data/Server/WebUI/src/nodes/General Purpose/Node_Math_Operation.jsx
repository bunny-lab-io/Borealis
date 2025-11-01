////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/General Purpose/Node_Math_Operations.jsx
import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
import { IconButton } from "@mui/material";
import SettingsIcon from "@mui/icons-material/Settings";

// Init shared memory bus if not already set
if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const MathNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore(state => state.edges);
  const [renderResult, setRenderResult] = useState(data?.value || "0");
  const resultRef = useRef(renderResult);

  useEffect(() => {
    let intervalId = null;
    let currentRate = window.BorealisUpdateRate;

    const runLogic = () => {
      const operator = data?.operator || "Add";
      const inputsA = edges.filter(e => e.target === id && e.targetHandle === "a");
      const inputsB = edges.filter(e => e.target === id && e.targetHandle === "b");

      const sum = (list) =>
        list
          .map(e => parseFloat(window.BorealisValueBus[e.source]) || 0)
          .reduce((a, b) => a + b, 0);

      const valA = sum(inputsA);
      const valB = sum(inputsB);

      let value = 0;
      switch (operator) {
        case "Add":
          value = valA + valB;
          break;
        case "Subtract":
          value = valA - valB;
          break;
        case "Multiply":
          value = valA * valB;
          break;
        case "Divide":
          value = valB !== 0 ? valA / valB : 0;
          break;
        case "Average":
          const totalInputs = inputsA.length + inputsB.length;
          const totalSum = valA + valB;
          value = totalInputs > 0 ? totalSum / totalInputs : 0;
          break;
      }

      resultRef.current = value;
      setRenderResult(value.toString());
      window.BorealisValueBus[id] = value.toString();

      setNodes(nds =>
        nds.map(n =>
          n.id === id
            ? { ...n, data: { ...n.data, value: value.toString() } }
            : n
        )
      );
    };

    intervalId = setInterval(runLogic, currentRate);

    // Watch for update rate changes
    const monitor = setInterval(() => {
      const newRate = window.BorealisUpdateRate;
      if (newRate !== currentRate) {
        clearInterval(intervalId);
        currentRate = newRate;
        intervalId = setInterval(runLogic, currentRate);
      }
    }, 250);

    return () => {
      clearInterval(intervalId);
      clearInterval(monitor);
    };
  }, [id, edges, setNodes, data?.operator]);

  return (
    <div className="borealis-node">
      <div style={{ position: "absolute", left: -16, top: 12, fontSize: "8px", color: "#ccc" }}>A</div>
      <div style={{ position: "absolute", left: -16, top: 50, fontSize: "8px", color: "#ccc" }}>B</div>
      <Handle type="target" position={Position.Left} id="a" style={{ top: 12 }} className="borealis-handle" />
      <Handle type="target" position={Position.Left} id="b" style={{ top: 50 }} className="borealis-handle" />

      <div className="borealis-node-header" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span>{data?.label || "Math Operation"}</span>
      </div>

      <div className="borealis-node-content" style={{ fontSize: "9px", color: "#ccc", marginTop: 4 }}>
        Result: {renderResult}
      </div>

      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

export default {
  type: "MathNode",
  label: "Math Operation",
  description: `
Live math node for computing on two grouped inputs.

- Sums all A and B handle inputs separately
- Performs selected math operation: Add, Subtract, Multiply, Divide, Average
- Emits result as string via BorealisValueBus
- Updates at the global update rate

**Common Uses:**  
Live dashboard math, sensor fusion, calculation chains, dynamic thresholds
  `.trim(),
  content: "Perform Math Operations",
  component: MathNode,
  config: [
    {
      key: "operator",
      label: "Operator",
      type: "select",
      options: [
        "Add",
        "Subtract",
        "Multiply",
        "Divide",
        "Average"
      ]
    }
  ],
  usage_documentation: `
### Math Operation Node

Performs live math between two groups of inputs (**A** and **B**).

#### Usage

- Connect any number of nodes to the **A** and **B** input handles.
- The node **sums all values** from A and from B before applying the operator.
- Select the math operator in the sidebar config:
  - **Add**: A + B
  - **Subtract**: A - B
  - **Multiply**: A * B
  - **Divide**: A / B (0 if B=0)
  - **Average**: (A + B) / total number of inputs

#### Output

- The computed result is pushed as a string to downstream nodes every update tick.

#### Input Handles

- **A** (Top Left)
- **B** (Middle Left)

#### Example

If three nodes outputting 5, 10, 15 are connected to A,  
and one node outputs 2 is connected to B,  
and operator is Multiply:

- **A** = 5 + 10 + 15 = 30
- **B** = 2
- **Result** = 30 * 2 = 60

  `.trim()
};
