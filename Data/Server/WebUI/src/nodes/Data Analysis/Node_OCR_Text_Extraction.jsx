////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: Node_OCR_Text_Extraction.jsx

import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// Base64 comparison using hash (lightweight)
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
    const [engine, setEngine] = useState("None");
    const [backend, setBackend] = useState("CPU");
    const [dataType, setDataType] = useState("Mixed");

    const [customRateEnabled, setCustomRateEnabled] = useState(true);
    const [customRateMs, setCustomRateMs] = useState(1000);

    const [changeThreshold, setChangeThreshold] = useState(0);

    const valueRef = useRef("");
    const lastUsed = useRef({ engine: "", backend: "", dataType: "" });
    const lastProcessedAt = useRef(0);
    const lastImageHash = useRef(0);

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

            // Only reprocess if config changed, or image changed AND time passed
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

                <label style={labelStyle}>OCR Engine:</label>
                <select value={engine} onChange={(e) => setEngine(e.target.value)} style={dropdownStyle}>
                    <option value="None">None</option>
                    <option value="TesseractOCR">TesseractOCR</option>
                    <option value="EasyOCR">EasyOCR</option>
                </select>

                <label style={labelStyle}>Compute:</label>
                <select value={backend} onChange={(e) => setBackend(e.target.value)} style={dropdownStyle} disabled={engine === "None"}>
                    <option value="CPU">CPU</option>
                    <option value="GPU">GPU</option>
                </select>

                <label style={labelStyle}>Data Type:</label>
                <select value={dataType} onChange={(e) => setDataType(e.target.value)} style={dropdownStyle}>
                    <option value="Mixed">Mixed Data</option>
                    <option value="Numerical">Numerical Data</option>
                    <option value="String">String Data</option>
                </select>

                <label style={labelStyle}>Custom API Rate-Limit (ms):</label>
                <div style={{ display: "flex", alignItems: "center", marginBottom: "8px" }}>
                    <input
                        type="checkbox"
                        checked={customRateEnabled}
                        onChange={(e) => setCustomRateEnabled(e.target.checked)}
                        style={{ marginRight: "8px" }}
                    />
                    <input
                        type="number"
                        min="100"
                        step="100"
                        value={customRateMs}
                        onChange={(e) => setCustomRateMs(Number(e.target.value))}
                        disabled={!customRateEnabled}
                        style={numberInputStyle}
                    />
                </div>

                <label style={labelStyle}>Change Detection Sensitivity Threshold:</label>
                <input
                    type="number"
                    min="0"
                    max="100"
                    step="1"
                    value={changeThreshold}
                    onChange={(e) => setChangeThreshold(Number(e.target.value))}
                    style={numberInputStyle}
                />

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

const dropdownStyle = {
    width: "100%",
    fontSize: "9px",
    padding: "4px",
    background: "#1e1e1e",
    color: "#ccc",
    border: "1px solid #444",
    borderRadius: "2px"
};

const numberInputStyle = {
    width: "80px",
    fontSize: "9px",
    background: "#1e1e1e",
    color: "#ccc",
    border: "1px solid #444",
    borderRadius: "2px",
    padding: "2px 4px",
    marginBottom: "8px"
};

export default {
    type: "OCR_Text_Extraction",
    label: "OCR Text Extraction",
    description: `
Extract text from upstream image using backend OCR engine via API.
Includes rate limiting and sensitivity detection for smart processing.`,
    content: "Extract Multi-Line Text from Upstream Image Node",
    component: OCRNode
};
