////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Image Processing/Node_BW_Threshold.jsx

import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useStore } from "reactflow";

// Ensure BorealisValueBus exists
if (!window.BorealisValueBus) {
    window.BorealisValueBus = {};
}

if (!window.BorealisUpdateRate) {
    window.BorealisUpdateRate = 100;
}

const BWThresholdNode = ({ id, data }) => {
    const edges = useStore((state) => state.edges);

    // Attempt to parse threshold from data.value (if present),
    // otherwise default to 128.
    const initial = parseInt(data?.value, 10);
    const [threshold, setThreshold] = useState(
        isNaN(initial) ? 128 : initial
    );

    const [renderValue, setRenderValue] = useState("");
    const valueRef = useRef("");
    const lastUpstreamRef = useRef("");

    // If the node is reimported and data.value changes externally,
    // update the threshold accordingly.
    useEffect(() => {
        const newVal = parseInt(data?.value, 10);
        if (!isNaN(newVal)) {
            setThreshold(newVal);
        }
    }, [data?.value]);

    const handleThresholdInput = (e) => {
        let val = parseInt(e.target.value, 10);
        if (isNaN(val)) {
            val = 128;
        }
        val = Math.max(0, Math.min(255, val));

        // Keep the Node's data.value updated
        data.value = val;

        setThreshold(val);
        window.BorealisValueBus[id] = val;
    };

    const applyThreshold = async (base64Data, cutoff) => {
        if (!base64Data || typeof base64Data !== "string") {
            return "";
        }

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
                const dataArr = imageData.data;

                for (let i = 0; i < dataArr.length; i += 4) {
                    const avg = (dataArr[i] + dataArr[i + 1] + dataArr[i + 2]) / 3;
                    const color = avg < cutoff ? 0 : 255;
                    dataArr[i] = color;
                    dataArr[i + 1] = color;
                    dataArr[i + 2] = color;
                }

                ctx.putImageData(imageData, 0, 0);
                resolve(canvas.toDataURL("image/png").replace(/^data:image\/png;base64,/, ""));
            };

            img.onerror = () => resolve(base64Data);
            img.src = "data:image/png;base64," + base64Data;
        });
    };

    // Main polling logic
    useEffect(() => {
        let currentRate = window.BorealisUpdateRate;
        let intervalId = null;

        const runNodeLogic = async () => {
            const inputEdge = edges.find(e => e.target === id);
            if (inputEdge?.source) {
                const upstreamValue = window.BorealisValueBus[inputEdge.source] ?? "";

                if (upstreamValue !== lastUpstreamRef.current) {
                    const transformed = await applyThreshold(upstreamValue, threshold);
                    lastUpstreamRef.current = upstreamValue;
                    valueRef.current = transformed;
                    setRenderValue(transformed);
                    window.BorealisValueBus[id] = transformed;
                }
            } else {
                lastUpstreamRef.current = "";
                valueRef.current = "";
                setRenderValue("");
                window.BorealisValueBus[id] = "";
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
        }, 250);

        return () => {
            clearInterval(intervalId);
            clearInterval(monitor);
        };
    }, [id, edges, threshold]);

    // Reapply when threshold changes (even if image didn't)
    useEffect(() => {
        const inputEdge = edges.find(e => e.target === id);
        if (!inputEdge?.source) {
            return;
        }

        const upstreamValue = window.BorealisValueBus[inputEdge.source] ?? "";
        if (!upstreamValue) {
            return;
        }

        applyThreshold(upstreamValue, threshold).then((result) => {
            valueRef.current = result;
            setRenderValue(result);
            window.BorealisValueBus[id] = result;
        });
    }, [threshold, edges, id]);

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />

            <div className="borealis-node-header">BW Threshold</div>

            <div className="borealis-node-content" style={{ fontSize: "9px" }}>
                <div style={{ marginBottom: "6px", color: "#ccc" }}>
                    Threshold Strength (0–255):
                </div>
                <input
                    type="number"
                    min="0"
                    max="255"
                    value={threshold}
                    onChange={handleThresholdInput}
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        padding: "4px",
                        background: "#1e1e1e",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        marginBottom: "6px"
                    }}
                />
            </div>

            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

export default {
    type: "BWThresholdNode",
    label: "BW Threshold",
    description: `
Black & White Threshold (Stateless)

- Converts a base64 image to black & white using a user-defined threshold value
- Reapplies threshold when the number changes, even if image stays the same
- Outputs a new base64 PNG with BW transformation
`.trim(),
    content: "Applies black & white threshold to base64 image input.",
    component: BWThresholdNode
};
