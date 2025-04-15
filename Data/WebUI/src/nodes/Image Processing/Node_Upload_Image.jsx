////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Image Processing/Node_Upload_Image.jsx

/**
 * ==================================================
 * Borealis - Image Upload Node (Raw Base64 Output)
 * ==================================================
 *
 * COMPONENT ROLE:
 * This node lets the user upload an image file (JPG/JPEG/PNG),
 * reads it as a data URL, then strips off the "data:image/*;base64,"
 * prefix, storing only the raw base64 data in BorealisValueBus.
 *
 * IMPORTANT:
 * - No upstream connector (target handle) is provided.
 * - The raw base64 is pushed out to downstream nodes via source handle.
 * - Your viewer (or other downstream node) must prepend "data:image/png;base64,"
 *   or the appropriate MIME string for display.
 */

import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow } from "reactflow";

// Global Shared Bus for Node Data Propagation
if (!window.BorealisValueBus) {
    window.BorealisValueBus = {};
}

// Global Update Rate (ms) for All Data Nodes
if (!window.BorealisUpdateRate) {
    window.BorealisUpdateRate = 100;
}

const ImageUploadNode = ({ id, data }) => {
    const { setNodes } = useReactFlow();

    const [renderValue, setRenderValue] = useState(data?.value || "");
    const valueRef = useRef(renderValue);

    // Handler for file uploads
    const handleFileUpload = (event) => {
        console.log("handleFileUpload triggered for node:", id);

        // Get the file list
        const files = event.target.files || event.currentTarget.files;
        if (!files || files.length === 0) {
            console.log("No files selected or files array is empty");
            return;
        }

        const file = files[0];
        if (!file) {
            console.log("File object not found");
            return;
        }

        // Debugging info
        console.log("Selected file:", file.name, file.type, file.size);

        // Validate file type
        const validTypes = ["image/jpeg", "image/png"];
        if (!validTypes.includes(file.type)) {
            console.warn("Unsupported file type in node:", id, file.type);
            return;
        }

        // Setup FileReader
        const reader = new FileReader();
        reader.onload = (loadEvent) => {
            console.log("FileReader onload in node:", id);
            const base64DataUrl = loadEvent?.target?.result || "";

            // Strip off the data:image/...;base64, prefix to store raw base64
            const rawBase64 = base64DataUrl.replace(/^data:image\/[a-zA-Z]+;base64,/, "");
            console.log("Raw Base64 (truncated):", rawBase64.substring(0, 50));

            valueRef.current = rawBase64;
            setRenderValue(rawBase64);
            window.BorealisValueBus[id] = rawBase64;

            // Update node data
            setNodes((nds) =>
                nds.map((n) =>
                    n.id === id
                        ? { ...n, data: { ...n.data, value: rawBase64 } }
                        : n
                )
            );
        };
        reader.onerror = (errorEvent) => {
            console.error("FileReader error in node:", id, errorEvent);
        };

        // Read the file as a data URL
        reader.readAsDataURL(file);
    };

    // Poll-based output (no upstream)
    useEffect(() => {
        let currentRate = window.BorealisUpdateRate || 100;
        let intervalId = null;

        const runNodeLogic = () => {
            // Simply emit current value (raw base64) to the bus
            window.BorealisValueBus[id] = valueRef.current;
        };

        const startInterval = () => {
            intervalId = setInterval(runNodeLogic, currentRate);
        };

        startInterval();

        // Monitor for global update rate changes
        const monitor = setInterval(() => {
            const newRate = window.BorealisUpdateRate || 100;
            if (newRate !== currentRate) {
                currentRate = newRate;
                clearInterval(intervalId);
                startInterval();
            }
        }, 250);

        return () => {
            clearInterval(intervalId);
            clearInterval(monitor);
        };
    }, [id, setNodes]);

    return (
        <div className="borealis-node" style={{ minWidth: "160px" }}>
            {/* No target handle because we don't accept upstream data */}
            <div className="borealis-node-header">
                {data?.label || "Raw Base64 Image Upload"}
            </div>

            <div className="borealis-node-content">
                <div style={{ marginBottom: "8px", fontSize: "9px", color: "#ccc" }}>
                    {data?.content || "Upload a JPG/PNG, store only the raw base64 in ValueBus."}
                </div>

                <label style={{ fontSize: "9px", display: "block", marginBottom: "4px" }}>
                    Upload Image File
                </label>

                <input
                    type="file"
                    accept=".jpg,.jpeg,.png"
                    onChange={handleFileUpload}
                    style={{
                        fontSize: "9px",
                        padding: "4px",
                        background: "#1e1e1e",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        width: "100%",
                        marginBottom: "8px"
                    }}
                />
            </div>

            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

export default {
    type: "ImageUploadNode_RawBase64", // Unique ID for the node type
    label: "Upload Image",
    description: `
A node to upload an image (JPG/PNG) and store it in base64 format for later use downstream.
`.trim(),
    content: "Upload an image, output only the raw base64 string.",
    component: ImageUploadNode
};
