////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data Analysis/Node_Regex_Search.jsx
////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data Manipulation/Node_Regex_Search.jsx
import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const RegexSearchNode = ({ id, data }) => {
    const edges = useStore((state) => state.edges);
    const { setNodes } = useReactFlow();

    const [pattern, setPattern] = useState(data?.pattern || "");
    const [flags, setFlags] = useState(data?.flags || "i");

    const valueRef = useRef("0");

    const updateNodeData = (key, val) => {
        setNodes(nds =>
            nds.map(n =>
                n.id === id ? { ...n, data: { ...n.data, [key]: val } } : n
            )
        );
    };

    useEffect(() => {
        let intervalId = null;
        let currentRate = window.BorealisUpdateRate;

        const runNodeLogic = () => {
            const inputEdge = edges.find((e) => e.target === id);
            const inputVal = inputEdge ? window.BorealisValueBus[inputEdge.source] || "" : "";

            let matched = false;
            try {
                if (pattern) {
                    const regex = new RegExp(pattern, flags);
                    matched = regex.test(inputVal);
                }
            } catch {
                matched = false;
            }

            const result = matched ? "1" : "0";

            if (result !== valueRef.current) {
                valueRef.current = result;
                window.BorealisValueBus[id] = result;
            }
        };

        intervalId = setInterval(runNodeLogic, currentRate);

        const monitor = setInterval(() => {
            const newRate = window.BorealisUpdateRate;
            if (newRate !== currentRate) {
                clearInterval(intervalId);
                intervalId = setInterval(runNodeLogic, newRate);
                currentRate = newRate;
            }
        }, 300);

        return () => {
            clearInterval(intervalId);
            clearInterval(monitor);
        };
    }, [id, edges, pattern, flags]);

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <div className="borealis-node-header">Regex Search</div>
            <div className="borealis-node-content">
                <label>Regex Pattern:</label>
                <input
                    type="text"
                    value={pattern}
                    onChange={(e) => {
                        setPattern(e.target.value);
                        updateNodeData("pattern", e.target.value);
                    }}
                    placeholder="e.g. World"
                    style={inputStyle}
                />

                <label>Regex Flags:</label>
                <input
                    type="text"
                    value={flags}
                    onChange={(e) => {
                        setFlags(e.target.value);
                        updateNodeData("flags", e.target.value);
                    }}
                    placeholder="e.g. i"
                    style={inputStyle}
                />
            </div>
            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

const inputStyle = {
    width: "100%",
    fontSize: "9px",
    background: "#1e1e1e",
    color: "#ccc",
    border: "1px solid #444",
    borderRadius: "2px",
    padding: "3px",
    marginBottom: "6px"
};

export default {
    type: "RegexSearch",
    label: "Regex Search",
    description: `
Minimal RegEx Matcher:
- Accepts pattern and flags
- Outputs "1" if match is found, else "0"
- No visual output display
`.trim(),
    content: "Outputs '1' if regex matches input, otherwise '0'",
    component: RegexSearchNode
};
