////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Image Processing/Node_Export_Image.jsx

import React, { useEffect, useState } from "react";
import { Handle, Position, useReactFlow } from "reactflow";

const ExportImageNode = ({ id }) => {
    const { getEdges } = useReactFlow();
    const [imageData, setImageData] = useState("");

    useEffect(() => {
        const interval = setInterval(() => {
            const edges = getEdges();
            const inputEdge = edges.find(e => e.target === id);
            if (inputEdge) {
                const base64 = window.BorealisValueBus?.[inputEdge.source];
                if (typeof base64 === "string") {
                    setImageData(base64);
                }
            }
        }, 1000);
        return () => clearInterval(interval);
    }, [id, getEdges]);

    const handleDownload = async () => {
        const blob = await (async () => {
            const res = await fetch(`data:image/png;base64,${imageData}`);
            return await res.blob();
        })();

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
            <div className="borealis-node-header">Export Image</div>
            <div className="borealis-node-content" style={{ fontSize: "9px" }}>
                Export upstream base64-encoded image data as a PNG on-disk.
                <button
                    style={{
                        marginTop: "6px",
                        width: "100%",
                        fontSize: "9px",
                        padding: "4px",
                        background: "#1e1e1e",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px"
                    }}
                    onClick={handleDownload}
                    disabled={!imageData}
                >
                    Download PNG
                </button>
            </div>
        </div>
    );
};

export default {
    type: "ExportImageNode",
    label: "Export Image",
    description: "Lets the user download the base64 PNG image to disk.",
    content: "Save base64 PNG to disk as a file.",
    component: ExportImageNode
};
