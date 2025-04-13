import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const ArrayIndexExtractorNode = ({ id, data }) => {
    const edges = useStore((state) => state.edges);
    const { setNodes } = useReactFlow();

    const [lineNumber, setLineNumber] = useState(data?.lineNumber || 1);
    const [result, setResult] = useState("Line Does Not Exist");

    const valueRef = useRef(result);

    const handleLineNumberChange = (e) => {
        const num = parseInt(e.target.value, 10);
        const clamped = isNaN(num) ? 1 : Math.max(1, num);
        setLineNumber(clamped);

        setNodes((nds) =>
            nds.map((n) =>
                n.id === id ? { ...n, data: { ...n.data, lineNumber: clamped } } : n
            )
        );
    };

    useEffect(() => {
        let intervalId = null;
        let currentRate = window.BorealisUpdateRate;

        const runNodeLogic = () => {
            const inputEdge = edges.find((e) => e.target === id);
            if (!inputEdge) {
                valueRef.current = "Line Does Not Exist";
                setResult("Line Does Not Exist");
                window.BorealisValueBus[id] = "Line Does Not Exist";
                return;
            }

            const upstreamValue = window.BorealisValueBus[inputEdge.source];
            if (!Array.isArray(upstreamValue)) {
                valueRef.current = "Line Does Not Exist";
                setResult("Line Does Not Exist");
                window.BorealisValueBus[id] = "Line Does Not Exist";
                return;
            }

            const index = Math.max(0, lineNumber - 1); // Convert 1-based input to 0-based
            const selected = upstreamValue[index] ?? "Line Does Not Exist";

            if (selected !== valueRef.current) {
                valueRef.current = selected;
                setResult(selected);
                window.BorealisValueBus[id] = selected;
            }
        };

        intervalId = setInterval(runNodeLogic, currentRate);

        const monitor = setInterval(() => {
            const newRate = window.BorealisUpdateRate;
            if (newRate !== currentRate) {
                clearInterval(intervalId);
                currentRate = newRate;
                intervalId = setInterval(runNodeLogic, currentRate);
            }
        }, 300);

        return () => {
            clearInterval(intervalId);
            clearInterval(monitor);
        };
    }, [id, edges, lineNumber]);

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <div className="borealis-node-header">Array Index Extractor</div>
            <div className="borealis-node-content" style={{ fontSize: "9px" }}>
                <div style={{ marginBottom: "6px", color: "#ccc" }}>
                    Output a Specific Array Index's Value
                </div>

                <label style={{ display: "block", marginBottom: "2px" }}>
                    Line Number (1 = First Line):
                </label>
                <input
                    type="number"
                    min="1"
                    step="1"
                    value={lineNumber}
                    onChange={handleLineNumberChange}
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
                <input
                    type="text"
                    value={result}
                    disabled
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        background: "#2a2a2a",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        padding: "3px"
                    }}
                />
            </div>
            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

export default {
    type: "ArrayIndexExtractor",
    label: "Array Index Extractor",
    description: `
Outputs a specific line from an upstream array (e.g., OCR multi-line output).

- User specifies the line number (1-based index)
- Outputs the value from that line if it exists
- If the index is out of bounds, outputs "Line Does Not Exist"
`.trim(),
    content: "Output a Specific Array Index's Value",
    component: ArrayIndexExtractorNode
};
