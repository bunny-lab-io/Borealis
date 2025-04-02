/**
 * ==============================================
 * Borealis - Comparison Node (Logic Evaluation)
 * ==============================================
 *
 * COMPONENT ROLE:
 * This node takes two input values and evaluates them using a selected comparison operator.
 * It returns 1 (true) or 0 (false) depending on the result of the comparison.
 *
 * FEATURES:
 * - Dropdown to select input type: "Number" or "String"
 * - Dropdown to select comparison operator: ==, !=, >, <, >=, <=
 * - Dynamically disables numeric-only operators for string inputs
 * - Automatically resets operator to == when switching to String
 * - Supports summing multiple inputs per side (A, B)
 * - For "String" mode: concatenates inputs in connection order
 * - Uses BorealisValueBus for input/output
 * - Controlled by global update timer
 *
 * STRUCTURE:
 * - Label and Description
 * - Input A (top-left) and Input B (middle-left)
 * - Output (right edge) result: 1 (true) or 0 (false)
 * - Operator dropdown and Input Type dropdown
 */

import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const ComparisonNode = ({ id, data }) => {
    const { setNodes } = useReactFlow();
    const edges = useStore(state => state.edges);

    const [inputType, setInputType] = useState(data?.inputType || "Number");
    const [operator, setOperator] = useState(data?.operator || "Equal (==)");
    const [renderValue, setRenderValue] = useState("0");
    const valueRef = useRef("0");

    useEffect(() => {
        if (inputType === "String" && !["Equal (==)", "Not Equal (!=)"].includes(operator)) {
            setOperator("Equal (==)");
        }
    }, [inputType]);

    useEffect(() => {
        let currentRate = window.BorealisUpdateRate;
        let intervalId = null;

        const runNodeLogic = () => {
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

            const result = resultMap[operator] ? 1 : 0;

            valueRef.current = result;
            setRenderValue(result);
            window.BorealisValueBus[id] = result;

            setNodes(nds =>
                nds.map(n =>
                    n.id === id ? { ...n, data: { ...n.data, value: result, inputType, operator } } : n
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
    }, [id, edges, inputType, operator, setNodes]);

    return (
        <div className="borealis-node">
            <div style={{ position: "absolute", left: -16, top: 12, fontSize: "8px", color: "#ccc" }}>A</div>
            <div style={{ position: "absolute", left: -16, top: 50, fontSize: "8px", color: "#ccc" }}>B</div>
            <Handle type="target" position={Position.Left} id="a" style={{ top: 12 }} className="borealis-handle" />
            <Handle type="target" position={Position.Left} id="b" style={{ top: 50 }} className="borealis-handle" />

            <div className="borealis-node-header">
                {data?.label || "Comparison Node"}
            </div>

            <div className="borealis-node-content">
                <div style={{ marginBottom: "6px", fontSize: "9px", color: "#ccc" }}>
                    {data?.content || "Evaluates A vs B and outputs 1 (true) or 0 (false)."}
                </div>

                <label style={{ fontSize: "9px" }}>Input Type:</label>
                <select value={inputType} onChange={(e) => setInputType(e.target.value)} style={dropdownStyle}>
                    <option value="Number">Number</option>
                    <option value="String">String</option>
                </select>

                <label style={{ fontSize: "9px", marginTop: "6px" }}>Operator:</label>
                <select value={operator} onChange={(e) => setOperator(e.target.value)} style={dropdownStyle}>
                    <option>Equal (==)</option>
                    <option>Not Equal (!=)</option>
                    <option disabled={inputType === "String"}>Greater Than (&gt;)</option>
                    <option disabled={inputType === "String"}>Less Than (&lt;)</option>
                    <option disabled={inputType === "String"}>Greater Than or Equal (&gt;=)</option>
                    <option disabled={inputType === "String"}>Less Than or Equal (&lt;=)</option>
                </select>

                <div style={{ marginTop: "8px", fontSize: "9px" }}>Result: {renderValue}</div>
            </div>

            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

const dropdownStyle = {
    width: "100%",
    fontSize: "9px",
    padding: "4px",
    background: "#1e1e1e",
    color: "#ccc",
    border: "1px solid #444",
    borderRadius: "2px",
    marginBottom: "4px"
};

export default {
    type: "ComparisonNode",
    label: "Logic Comparison",
    description: `
Compare Two Inputs (A vs B)

- Uses configurable operator
- Supports numeric and string comparison
- Aggregates multiple inputs by summing (Number) or joining (String in connection order)
- Only == and != are valid for String mode
- Automatically resets operator when switching to String mode
- Outputs 1 (true) or 0 (false) into BorealisValueBus
- Live-updates based on global timer
    `.trim(),
    content: "Compare A and B using Logic",
    component: ComparisonNode
};
