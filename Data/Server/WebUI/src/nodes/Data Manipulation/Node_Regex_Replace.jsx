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
    const [result, setResult] = useState("");

    const valueRef = useRef("");

    const handlePatternChange = (e) => {
        const val = e.target.value;
        setPattern(val);
        setNodes((nds) =>
            nds.map((n) =>
                n.id === id ? { ...n, data: { ...n.data, pattern: val } } : n
            )
        );
    };

    const handleReplacementChange = (e) => {
        const val = e.target.value;
        setReplacement(val);
        setNodes((nds) =>
            nds.map((n) =>
                n.id === id ? { ...n, data: { ...n.data, replacement: val } } : n
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
            let newVal = inputValue;

            try {
                const regex = new RegExp(pattern, 'g');
                newVal = inputValue.replace(regex, replacement);
            } catch (err) {
                newVal = `[Error] ${err.message}`;
            }

            if (newVal !== valueRef.current) {
                valueRef.current = newVal;
                setResult(newVal);
                window.BorealisValueBus[id] = newVal;
            }
        };

        const startInterval = () => {
            intervalId = setInterval(runNodeLogic, currentRate);
        };

        startInterval();

        const monitor = setInterval(() => {
            const newRate = window.BorealisUpdateRate;
            if (newRate !== currentRate) {
                clearInterval(intervalId);
                currentRate = newRate;
                startInterval();
            }
        }, 300);

        return () => {
            clearInterval(intervalId);
            clearInterval(monitor);
        };
    }, [id, edges, pattern, replacement]);

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />

            <div className="borealis-node-header">Regex Replace</div>

            <div className="borealis-node-content">
                <div style={{ marginBottom: "6px", fontSize: "9px", color: "#ccc" }}>
                    Perform regex replacement on upstream string
                </div>

                <label style={{ display: "block", marginBottom: "2px" }}>
                    Regular Expression:
                </label>
                <input
                    type="text"
                    value={pattern}
                    onChange={handlePatternChange}
                    placeholder="e.g. \\d+"
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        background: "#1e1e1e",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        padding: "3px",
                        marginBottom: "6px"
                    }}
                />

                <label style={{ display: "block", marginBottom: "2px" }}>
                    Replacement:
                </label>
                <input
                    type="text"
                    value={replacement}
                    onChange={handleReplacementChange}
                    placeholder="e.g. '#'
                    "
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        background: "#1e1e1e",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        padding: "3px",
                        marginBottom: "6px"
                    }}
                />

                <label style={{ display: "block", marginBottom: "2px" }}>
                    Output:
                </label>
                <textarea
                    readOnly
                    value={result}
                    rows={3}
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        background: "#2a2a2a",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        padding: "3px",
                        resize: "vertical"
                    }}
                />
            </div>

            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

export default {
    type: "RegexReplace",
    label: "Regex Replacer",
    description: `
Perform a user-specified regular expression replacement on an input string.

- User enters a regex pattern
- User enters a replacement string
- Outputs the transformed string to downstream nodes
`.trim(),
    content: "Perform regex replacement on upstream string",
    component: RegexReplaceNode
};
