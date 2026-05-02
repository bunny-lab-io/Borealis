////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Image Processing/Node_OCR_Text_Extraction.jsx

import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// Lightweight hash for image change detection
const getHashScore = (str = "") => {
    let hash = 0;
    for (let i = 0; i < str.length; i += 101) {
        const char = str.charCodeAt(i);
        hash = (hash << 5) - hash + char;
        hash |= 0;
    }
    return Math.abs(hash);
};

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const OCRNode = ({ id, data }) => {
    const edges = useStore((state) => state.edges);
    const { setNodes } = useReactFlow();

    const [ocrOutput, setOcrOutput] = useState("");
    const valueRef = useRef("");
    const lastUsed = useRef({ engine: "", backend: "", dataType: "" });
    const lastProcessedAt = useRef(0);
    const lastImageHash = useRef(0);

    // Always get config from props (sidebar sets these in node.data)
    const engine = data?.engine || "None";
    const backend = data?.backend || "CPU";
    const dataType = data?.dataType || "Mixed";
    const customRateEnabled = data?.customRateEnabled ?? true;
    const customRateMs = data?.customRateMs || 1000;
    const changeThreshold = data?.changeThreshold || 0;

    // OCR API Call
    const sendToOCRAPI = async (base64) => {
        const cleanBase64 = base64.replace(/^data:image\/[a-zA-Z]+;base64,/, "");
        try {
            const response = await fetch("/api/ocr", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ image_base64: cleanBase64, engine, backend })
            });
            const result = await response.json();
            return response.ok && Array.isArray(result.lines)
                ? result.lines
                : [`[ERROR] ${result.error || "Invalid OCR response."}`];
        } catch (err) {
            return [`[ERROR] OCR API request failed: ${err.message}`];
        }
    };

    // Filter lines based on user type
    const filterLines = (lines) => {
        if (dataType === "Numerical") {
            return lines.map(line => line.replace(/[^\d.%\s]/g, '').replace(/\s+/g, ' ').trim()).filter(Boolean);
        }
        if (dataType === "String") {
            return lines.map(line => line.replace(/[^a-zA-Z\s]/g, '').replace(/\s+/g, ' ').trim()).filter(Boolean);
        }
        return lines;
    };

    useEffect(() => {
        let intervalId = null;
        let currentRate = window.BorealisUpdateRate || 100;

        const runNodeLogic = async () => {
            const inputEdge = edges.find((e) => e.target === id);
            if (!inputEdge) {
                window.BorealisValueBus[id] = [];
                setOcrOutput("");
                return;
            }

            const upstreamValue = window.BorealisValueBus[inputEdge.source] || "";
            const now = Date.now();

            const effectiveRate = customRateEnabled ? customRateMs : window.BorealisUpdateRate || 100;
            const configChanged =
                lastUsed.current.engine !== engine ||
                lastUsed.current.backend !== backend ||
                lastUsed.current.dataType !== dataType;

            const upstreamHash = getHashScore(upstreamValue);
            const hashDelta = Math.abs(upstreamHash - lastImageHash.current);
            const hashThreshold = (changeThreshold / 100) * 1000000000;

            const imageChanged = hashDelta > hashThreshold;

            if (!configChanged && (!imageChanged || (now - lastProcessedAt.current < effectiveRate))) return;

            lastUsed.current = { engine, backend, dataType };
            lastProcessedAt.current = now;
            lastImageHash.current = upstreamHash;
            valueRef.current = upstreamValue;

            const lines = await sendToOCRAPI(upstreamValue);
            const filtered = filterLines(lines);
            setOcrOutput(filtered.join("\n"));
            window.BorealisValueBus[id] = filtered;
        };

        intervalId = setInterval(runNodeLogic, currentRate);

        const monitor = setInterval(() => {
            const newRate = window.BorealisUpdateRate || 100;
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
    }, [id, engine, backend, dataType, customRateEnabled, customRateMs, changeThreshold, edges]);

    return (
        <div className="borealis-node" style={{ minWidth: "200px" }}>
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <div className="borealis-node-header">OCR-Based Text Extraction</div>
            <div className="borealis-node-content">
                <div style={{ fontSize: "9px", marginBottom: "8px", color: "#ccc" }}>
                    Extract Multi-Line Text from Upstream Image Node
                </div>
                <label style={labelStyle}>OCR Output:</label>
                <textarea
                    readOnly
                    value={ocrOutput}
                    rows={6}
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        background: "#1e1e1e",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        padding: "4px",
                        resize: "vertical"
                    }}
                />
            </div>
            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

const labelStyle = {
    fontSize: "9px",
    display: "block",
    marginTop: "6px",
    marginBottom: "2px"
};

// Node registration for Borealis (modern, sidebar-enabled)
export default {
    type: "OCR_Text_Extraction",
    label: "OCR Text Extraction",
    description: `Extract text from upstream image using backend OCR engine via API. Includes rate limiting and sensitivity detection for smart processing.`,
    content: "Extract Multi-Line Text from Upstream Image Node",
    component: OCRNode,
    config: [
        {
            key: "engine",
            label: "OCR Engine",
            type: "select",
            options: ["None", "TesseractOCR", "EasyOCR"],
            defaultValue: "None"
        },
        {
            key: "backend",
            label: "Compute Backend",
            type: "select",
            options: ["CPU", "GPU"],
            defaultValue: "CPU"
        },
        {
            key: "dataType",
            label: "Data Type Filter",
            type: "select",
            options: ["Mixed", "Numerical", "String"],
            defaultValue: "Mixed"
        },
        {
            key: "customRateEnabled",
            label: "Custom API Rate Limit Enabled",
            type: "select",
            options: ["true", "false"],
            defaultValue: "true"
        },
        {
            key: "customRateMs",
            label: "Custom API Rate Limit (ms)",
            type: "text",
            defaultValue: "1000"
        },
        {
            key: "changeThreshold",
            label: "Change Detection Sensitivity (0-100)",
            type: "text",
            defaultValue: "0"
        }
    ],
    usage_documentation: `
### OCR Text Extraction Node

Extracts text (lines) from an **upstream image node** using a selectable backend OCR engine (Tesseract or EasyOCR). Designed for screenshots, scanned forms, and live image data pipelines.

**Features:**
- **Engine:** Select between None, TesseractOCR, or EasyOCR
- **Backend:** Choose CPU or GPU (if supported)
- **Data Type Filter:** Post-processes recognized lines for numerical-only or string-only content
- **Custom API Rate Limit:** When enabled, you can set a custom polling rate for OCR requests (in ms)
- **Change Detection Sensitivity:** Node will only re-OCR if the input image changes significantly (hash-based, 0 disables)

**Outputs:**
- Array of recognized lines, pushed to downstream nodes
- Output is displayed in the node (read-only)

**Usage:**
- Connect an image node (base64 output) to this node's input
- Configure OCR engine and options in the sidebar
- Useful for extracting values from screen regions, live screenshots, PDF scans, etc.

**Notes:**
- Setting Engine to 'None' disables OCR
- Use numerical/string filter for precise downstream parsing
- Polling rate too fast may cause backend overload
- Change threshold is a 0-100 scale (0 = always run, 100 = image must change completely)

`.trim()
};
