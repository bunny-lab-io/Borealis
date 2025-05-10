////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data Manipulation/Node_Regex_Replace.jsx
import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const RegexReplaceNode = ({ id, data }) => {
    const edges = useStore((state) => state.edges);
    const { setNodes } = useReactFlow();

    const [pattern, setPattern] = useState(data?.pattern || "");
    const [replacement, setReplacement] = useState(data?.replacement || "");
    const [flags, setFlags] = useState(data?.flags || "g");
    const [enabled, setEnabled] = useState(data?.enabled ?? true);
    const [result, setResult] = useState("");
    const [original, setOriginal] = useState("");

    const valueRef = useRef("");

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
            const inputValue = inputEdge
                ? window.BorealisValueBus[inputEdge.source] || ""
                : "";

            setOriginal(inputValue);

            let newVal = inputValue;

            try {
                if (enabled && pattern) {
                    const regex = new RegExp(pattern, flags);
                    let safeReplacement = replacement.trim();
                    if (
                        safeReplacement.startsWith('"') &&
                        safeReplacement.endsWith('"')
                    ) {
                        safeReplacement = safeReplacement.slice(1, -1);
                    }
                    newVal = inputValue.replace(regex, safeReplacement);
                }
            } catch (err) {
                newVal = `[Error] ${err.message}`;
            }

            if (newVal !== valueRef.current) {
                valueRef.current = newVal;
                setResult(newVal);
                window.BorealisValueBus[id] = newVal;
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
    }, [id, edges, pattern, replacement, flags, enabled]);

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />

            <div className="borealis-node-header">Regex Replace</div>

            <div className="borealis-node-content">
                <div style={{ marginBottom: "6px", fontSize: "9px", color: "#ccc" }}>
                    Perform regex replacement on upstream string
                </div>

                <label>Regex Pattern:</label>
                <input
                    type="text"
                    value={pattern}
                    onChange={(e) => {
                        setPattern(e.target.value);
                        updateNodeData("pattern", e.target.value);
                    }}
                    placeholder="e.g. \\d+"
                    style={inputStyle}
                />

                <label>Replacement:</label>
                <input
                    type="text"
                    value={replacement}
                    onChange={(e) => {
                        setReplacement(e.target.value);
                        updateNodeData("replacement", e.target.value);
                    }}
                    placeholder="e.g. $1"
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
                    placeholder="e.g. gi"
                    style={inputStyle}
                />

                <div style={{ margin: "6px 0" }}>
                    <label>
                        <input
                            type="checkbox"
                            checked={enabled}
                            onChange={(e) => {
                                setEnabled(e.target.checked);
                                updateNodeData("enabled", e.target.checked);
                            }}
                            style={{ marginRight: "6px" }}
                        />
                        Enable Replacement
                    </label>
                </div>

                <label>Original Input:</label>
                <textarea
                    readOnly
                    value={original}
                    rows={2}
                    style={textAreaStyle}
                />

                <label>Output:</label>
                <textarea
                    readOnly
                    value={result}
                    rows={2}
                    style={textAreaStyle}
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

const textAreaStyle = {
    width: "100%",
    fontSize: "9px",
    background: "#2a2a2a",
    color: "#ccc",
    border: "1px solid #444",
    borderRadius: "2px",
    padding: "3px",
    resize: "vertical",
    marginBottom: "6px"
};

export default {
    type: "RegexReplace",
    label: "Regex Replacer",
    description: `
Enhanced Regex Replacer:
- Add regex flags (g, i, m, etc)
- Live preview of input vs output
- Optional enable toggle for replacement logic
`.trim(),
    content: "Perform regex replacement on upstream string",
    component: RegexReplaceNode
};
