import React, { useEffect, useState } from "react";
import { Handle, Position, useReactFlow } from "reactflow";

const ImageViewerNode = ({ id, data }) => {
    const { getEdges } = useReactFlow();
    const [imageBase64, setImageBase64] = useState("");
    const [selectedType, setSelectedType] = useState("base64");

    // Watch upstream value
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
                }
            }
        }, 1000);
        return () => clearInterval(interval);
    }, [id, getEdges]);

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
                    <img
                        src={`data:image/png;base64,${imageBase64}`}
                        alt="Live"
                        style={{ width: "100%", border: "1px solid #333", marginTop: "6px" }}
                    />
                ) : (
                    <div style={{ fontSize: "9px", color: "#888" }}>Waiting for image...</div>
                )}
            </div>
        </div>
    );
};

export default {
    type: "Image_Viewer",
    label: "Image Viewer",
    description: "Displays base64 image pulled from ValueBus of upstream node.",
    content: "Visual preview of base64 image",
    component: ImageViewerNode
};
