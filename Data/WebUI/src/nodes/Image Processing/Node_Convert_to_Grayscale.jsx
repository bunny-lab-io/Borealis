import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useStore } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const GrayscaleNode = ({ id }) => {
    const edges = useStore((state) => state.edges);
    const [grayscaleLevel, setGrayscaleLevel] = useState(100); // percentage (0–100)
    const [renderValue, setRenderValue] = useState("");
    const valueRef = useRef("");

    const applyGrayscale = (base64Data, level) => {
        return new Promise((resolve) => {
            const img = new Image();
            img.crossOrigin = "anonymous";

            img.onload = () => {
                const canvas = document.createElement("canvas");
                canvas.width = img.width;
                canvas.height = img.height;
                const ctx = canvas.getContext("2d");
                ctx.drawImage(img, 0, 0);

                const imageData = ctx.getImageData(0, 0, canvas.width, canvas.height);
                const data = imageData.data;
                const alpha = level / 100;

                for (let i = 0; i < data.length; i += 4) {
                    const r = data[i];
                    const g = data[i + 1];
                    const b = data[i + 2];
                    const avg = (r + g + b) / 3;

                    data[i]     = r * (1 - alpha) + avg * alpha;
                    data[i + 1] = g * (1 - alpha) + avg * alpha;
                    data[i + 2] = b * (1 - alpha) + avg * alpha;
                }

                ctx.putImageData(imageData, 0, 0);
                resolve(canvas.toDataURL("image/png").replace(/^data:image\/png;base64,/, ""));
            };

            img.onerror = () => resolve(base64Data);
            img.src = `data:image/png;base64,${base64Data}`;
        });
    };

    useEffect(() => {
        const inputEdge = edges.find(e => e.target === id);
        if (!inputEdge?.source) return;

        const input = window.BorealisValueBus[inputEdge.source] ?? "";
        if (!input) return;

        applyGrayscale(input, grayscaleLevel).then((output) => {
            valueRef.current = input;
            setRenderValue(output);
            window.BorealisValueBus[id] = output;
        });
    }, [grayscaleLevel, edges, id]);

    useEffect(() => {
        let interval = null;

        const run = async () => {
            const edge = edges.find(e => e.target === id);
            const input = edge ? window.BorealisValueBus[edge.source] : "";

            if (input && input !== valueRef.current) {
                const result = await applyGrayscale(input, grayscaleLevel);
                valueRef.current = input;
                setRenderValue(result);
                window.BorealisValueBus[id] = result;
            }
        };

        interval = setInterval(run, window.BorealisUpdateRate);
        return () => clearInterval(interval);
    }, [id, edges, grayscaleLevel]);

    const handleLevelChange = (e) => {
        let val = parseInt(e.target.value, 10);
        if (isNaN(val)) val = 100;
        val = Math.min(100, Math.max(0, val));
        setGrayscaleLevel(val);
    };

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <div className="borealis-node-header">Convert to Grayscale</div>
            <div className="borealis-node-content" style={{ fontSize: "9px" }}>
                <label style={{ display: "block", marginBottom: "4px" }}>
                    Grayscale Intensity (0–100):
                </label>
                <input
                    type="number"
                    min="0"
                    max="100"
                    step="1"
                    value={grayscaleLevel}
                    onChange={handleLevelChange}
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        padding: "4px",
                        background: "#1e1e1e",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px"
                    }}
                />
            </div>
            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

export default {
    type: "GrayscaleNode",
    label: "Convert to Grayscale",
    description: `
Adjustable Grayscale Conversion

- Accepts base64 image input
- Applies grayscale effect using a % level
- 0% = no change, 100% = full grayscale
- Outputs result downstream as base64
`.trim(),
    content: "Convert image to grayscale with adjustable intensity.",
    component: GrayscaleNode
};
