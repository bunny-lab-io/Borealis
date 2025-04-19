////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Image Processing/Node_Adjust_Contrast.jsx

import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useStore } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const ContrastNode = ({ id }) => {
    const edges = useStore((state) => state.edges);
    const [contrast, setContrast] = useState(100);
    const valueRef = useRef("");
    const [renderValue, setRenderValue] = useState("");

    const applyContrast = (base64Data, contrastVal) => {
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

                const factor = (259 * (contrastVal + 255)) / (255 * (259 - contrastVal));

                for (let i = 0; i < data.length; i += 4) {
                    data[i] = factor * (data[i] - 128) + 128;
                    data[i + 1] = factor * (data[i + 1] - 128) + 128;
                    data[i + 2] = factor * (data[i + 2] - 128) + 128;
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

        applyContrast(input, contrast).then((output) => {
            setRenderValue(output);
            window.BorealisValueBus[id] = output;
        });
    }, [contrast, edges, id]);

    useEffect(() => {
        let interval = null;
        const tick = async () => {
            const edge = edges.find(e => e.target === id);
            const input = edge ? window.BorealisValueBus[edge.source] : "";

            if (input && input !== valueRef.current) {
                const result = await applyContrast(input, contrast);
                valueRef.current = input;
                setRenderValue(result);
                window.BorealisValueBus[id] = result;
            }
        };

        interval = setInterval(tick, window.BorealisUpdateRate);
        return () => clearInterval(interval);
    }, [id, contrast, edges]);

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <div className="borealis-node-header">Adjust Contrast</div>
            <div className="borealis-node-content" style={{ fontSize: "9px" }}>
                <label>Contrast (1–255):</label>
                <input
                    type="number"
                    min="1"
                    max="255"
                    value={contrast}
                    onChange={(e) => setContrast(parseInt(e.target.value) || 100)}
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        padding: "4px",
                        background: "#1e1e1e",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        marginTop: "4px"
                    }}
                />
            </div>
            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

export default {
    type: "ContrastNode",
    label: "Adjust Contrast",
    description: "Modify contrast of base64 image using a contrast multiplier.",
    content: "Adjusts contrast of image using canvas pixel transform.",
    component: ContrastNode
};
