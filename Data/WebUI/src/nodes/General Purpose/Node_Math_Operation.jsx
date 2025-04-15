////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/General Purpose/Node_Math_Operations.jsx

/**
 * ============================================
 * Borealis - Math Operation Node (Multi-Input A/B)
 * ============================================
 *
 * COMPONENT ROLE:
 * Performs live math operations on *two grouped input sets* (A and B).
 * 
 * FUNCTIONALITY:
 * - Inputs connected to Handle A are summed
 * - Inputs connected to Handle B are summed
 * - Math operation is applied as: A &lt;operator&gt; B
 * - Result pushed via BorealisValueBus[id]
 *
 * SUPPORTED OPERATORS:
 * - Add, Subtract, Multiply, Divide, Average
 */

import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const MathNode = ({ id, data }) => {
    const { setNodes } = useReactFlow();
    const edges = useStore(state => state.edges);

    const [operator, setOperator] = useState(data?.operator || "Add");
    const [result, setResult] = useState("0");
    const resultRef = useRef(0);

    useEffect(() => {
        let intervalId = null;
        let currentRate = window.BorealisUpdateRate;

        const runLogic = () => {
            const inputsA = edges.filter(e => e.target === id && e.targetHandle === "a");
            const inputsB = edges.filter(e => e.target === id && e.targetHandle === "b");

            const sum = (list) =>
                list.map(e => parseFloat(window.BorealisValueBus[e.source]) || 0).reduce((a, b) => a + b, 0);

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
            setResult(value.toString());
            window.BorealisValueBus[id] = value.toString();

            setNodes(nds =>
                nds.map(n =>
                    n.id === id ? { ...n, data: { ...n.data, operator, value: value.toString() } } : n
                )
            );
        };

        intervalId = setInterval(runLogic, currentRate);

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
    }, [id, operator, edges, setNodes]);

    return (
        <div className="borealis-node">
            <div style={{ position: "absolute", left: -16, top: 12, fontSize: "8px", color: "#ccc" }}>A</div>
            <div style={{ position: "absolute", left: -16, top: 50, fontSize: "8px", color: "#ccc" }}>B</div>
            <Handle type="target" position={Position.Left} id="a" style={{ top: 12 }} className="borealis-handle" />
            <Handle type="target" position={Position.Left} id="b" style={{ top: 50 }} className="borealis-handle" />

            <div className="borealis-node-header">
                {data?.label || "Math Operation"}
            </div>

            <div className="borealis-node-content">
                <div style={{ marginBottom: "8px", fontSize: "9px", color: "#ccc" }}>
                    Aggregates A and B inputs then performs operation.
                </div>

                <label style={{ fontSize: "9px", display: "block", marginBottom: "4px" }}>
                    Operator:
                </label>
                <select
                    value={operator}
                    onChange={(e) => setOperator(e.target.value)}
                    style={dropdownStyle}
                >
                    <option value="Add">Add</option>
                    <option value="Subtract">Subtract</option>
                    <option value="Multiply">Multiply</option>
                    <option value="Divide">Divide</option>
                    <option value="Average">Average</option>
                </select>

                <label style={{ fontSize: "9px", display: "block", marginBottom: "4px" }}>
                    Result:
                </label>
                <input
                    type="text"
                    value={result}
                    disabled
                    style={resultBoxStyle}
                />
            </div>

            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

const dropdownStyle = {
    fontSize: "9px",
    padding: "4px",
    background: "#1e1e1e",
    color: "#ccc",
    border: "1px solid #444",
    borderRadius: "2px",
    width: "100%",
    marginBottom: "8px"
};

const resultBoxStyle = {
    fontSize: "9px",
    padding: "4px",
    background: "#2a2a2a",
    color: "#ccc",
    border: "1px solid #444",
    borderRadius: "2px",
    width: "100%"
};

export default {
    type: "MathNode",
    label: "Math Operation",
    description: `
Perform Math on Aggregated Inputs

- A and B groups are independently summed
- Performs: Add, Subtract, Multiply, Divide, or Average
- Result = A <op> B
- Emits result via BorealisValueBus every update tick
    `.trim(),
    content: "Perform Math Operations",
    component: MathNode
};
