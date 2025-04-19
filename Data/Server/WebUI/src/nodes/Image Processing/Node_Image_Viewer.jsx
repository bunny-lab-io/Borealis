////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Image Processing/Node_Image_Viewer.jsx

import React, { useEffect, useState } from "react";
import { Handle, Position, useReactFlow } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const ImageViewerNode = ({ id, data }) => {
    const { getEdges } = useReactFlow();
    const [imageBase64, setImageBase64] = useState("");
    const [selectedType, setSelectedType] = useState("base64");

    // Monitor upstream input and propagate to ValueBus
    useEffect(() => {
        const interval = setInterval(() => {
            const edges = getEdges();
            const inputEdge = edges.find(e => e.target === id);
            if (inputEdge) {
                const sourceId = inputEdge.source;
                const valueBus = window.BorealisValueBus || {};
                const value = valueBus[sourceId];
                if (typeof value === "string") {
                    setImageBase64(value);
                    window.BorealisValueBus[id] = value;
                }
            } else {
                setImageBase64("");
                window.BorealisValueBus[id] = "";
            }
        }, window.BorealisUpdateRate || 100);

        return () => clearInterval(interval);
    }, [id, getEdges]);

    const handleDownload = async () => {
        if (!imageBase64) return;
        const blob = await (await fetch(`data:image/png;base64,${imageBase64}`)).blob();

        if (window.showSaveFilePicker) {
            try {
                const fileHandle = await window.showSaveFilePicker({
                    suggestedName: "image.png",
                    types: [{
                        description: "PNG Image",
                        accept: { "image/png": [".png"] }
                    }]
                });
                const writable = await fileHandle.createWritable();
                await writable.write(blob);
                await writable.close();
            } catch (e) {
                console.warn("Save cancelled:", e);
            }
        } else {
            const a = document.createElement("a");
            a.href = URL.createObjectURL(blob);
            a.download = "image.png";
            a.style.display = "none";
            document.body.appendChild(a);
            a.click();
            URL.revokeObjectURL(a.href);
            document.body.removeChild(a);
        }
    };

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <div className="borealis-node-header">Image Viewer</div>
            <div className="borealis-node-content">
                <label style={{ fontSize: "10px" }}>Data Type:</label>
                <select
                    value={selectedType}
                    onChange={(e) => setSelectedType(e.target.value)}
                    style={{ width: "100%", fontSize: "9px", marginBottom: "6px" }}
                >
                    <option value="base64">Base64 Encoded Image</option>
                </select>

                {imageBase64 ? (
                    <>
                        <img
                            src={`data:image/png;base64,${imageBase64}`}
                            alt="Live"
                            style={{
                                width: "100%",
                                border: "1px solid #333",
                                marginTop: "6px",
                                marginBottom: "6px"
                            }}
                        />
                        <button
                            onClick={handleDownload}
                            style={{
                                width: "100%",
                                fontSize: "9px",
                                padding: "4px",
                                background: "#1e1e1e",
                                color: "#ccc",
                                border: "1px solid #444",
                                borderRadius: "2px"
                            }}
                        >
                            Export to PNG
                        </button>
                    </>
                ) : (
                    <div style={{ fontSize: "9px", color: "#888", marginTop: "6px" }}>
                        Waiting for image...
                    </div>
                )}
            </div>
            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

export default {
    type: "Image_Viewer",
    label: "Image Viewer",
    description: `
Displays base64 image and exports it

- Accepts upstream base64 image
- Shows preview
- Provides "Export to PNG" button
- Outputs the same base64 to downstream
`.trim(),
    content: "Visual preview of base64 image with optional PNG export.",
    component: ImageViewerNode
};
