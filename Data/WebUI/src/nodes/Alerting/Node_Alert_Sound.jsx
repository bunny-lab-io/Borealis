/**
 * ==================================================
 * Borealis - Alert Sound Node (with Base64 Restore)
 * ==================================================
 *
 * COMPONENT ROLE:
 * Plays a sound when input = "1". Provides a visual indicator:
 * - Green dot: input is 0
 * - Red dot: input is 1
 *
 * Modes:
 * - "Once": Triggers once when going 0 -> 1
 * - "Constant": Triggers repeatedly every X ms while input = 1
 *
 * Supports embedding base64 audio directly into the workflow.
 */

import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const AlertSoundNode = ({ id, data }) => {
    const edges = useStore(state => state.edges);
    const { setNodes } = useReactFlow();

    const [alertType, setAlertType] = useState(data?.alertType || "Once");
    const [intervalMs, setIntervalMs] = useState(data?.interval || 1000);
    const [prevInput, setPrevInput] = useState("0");
    const [customAudioBase64, setCustomAudioBase64] = useState(data?.audio || null);
    const [currentInput, setCurrentInput] = useState("0");

    const audioRef = useRef(null);

    const playSound = () => {
        if (audioRef.current) {
            console.log(`[Alert Node ${id}] Attempting to play sound`);
            try {
                audioRef.current.pause();
                audioRef.current.currentTime = 0;
                audioRef.current.load();
                audioRef.current.play().then(() => {
                    console.log(`[Alert Node ${id}] Sound played successfully`);
                }).catch((err) => {
                    console.warn(`[Alert Node ${id}] Audio play blocked or errored:`, err);
                });
            } catch (err) {
                console.error(`[Alert Node ${id}] Failed to play sound:`, err);
            }
        } else {
            console.warn(`[Alert Node ${id}] No audioRef loaded`);
        }
    };

    const handleFileUpload = (event) => {
        const file = event.target.files[0];
        if (!file) return;

        console.log(`[Alert Node ${id}] File selected:`, file.name, file.type);

        const supportedTypes = ["audio/wav", "audio/mp3", "audio/mpeg", "audio/ogg"];
        if (!supportedTypes.includes(file.type)) {
            console.warn(`[Alert Node ${id}] Unsupported audio type: ${file.type}`);
            return;
        }

        const reader = new FileReader();
        reader.onload = (e) => {
            const base64 = e.target.result;
            const mimeType = file.type || "audio/mpeg";
            const safeURL = base64.startsWith("data:")
                ? base64
                : `data:${mimeType};base64,${base64}`;

            if (audioRef.current) {
                audioRef.current.pause();
                audioRef.current.src = "";
                audioRef.current.load();
                audioRef.current = null;
            }

            const newAudio = new Audio();
            newAudio.src = safeURL;

            let readyFired = false;

            newAudio.addEventListener("canplaythrough", () => {
                if (readyFired) return;
                readyFired = true;
                console.log(`[Alert Node ${id}] Audio is decodable and ready: ${file.name}`);

                setCustomAudioBase64(safeURL);
                audioRef.current = newAudio;
                newAudio.load();

                setNodes(nds =>
                    nds.map(n =>
                        n.id === id
                            ? { ...n, data: { ...n.data, audio: safeURL } }
                            : n
                    )
                );
            });

            setTimeout(() => {
                if (!readyFired) {
                    console.warn(`[Alert Node ${id}] WARNING: Audio not marked ready in time. May fail silently.`);
                }
            }, 2000);
        };

        reader.onerror = (e) => {
            console.error(`[Alert Node ${id}] File read error:`, e);
        };

        reader.readAsDataURL(file);
    };

    // Restore embedded audio from saved workflow
    useEffect(() => {
        if (customAudioBase64) {
            console.log(`[Alert Node ${id}] Loading embedded audio from workflow`);

            if (audioRef.current) {
                audioRef.current.pause();
                audioRef.current.src = "";
                audioRef.current.load();
                audioRef.current = null;
            }

            const loadedAudio = new Audio(customAudioBase64);
            loadedAudio.addEventListener("canplaythrough", () => {
                console.log(`[Alert Node ${id}] Embedded audio ready`);
            });

            audioRef.current = loadedAudio;
            loadedAudio.load();
        } else {
            console.log(`[Alert Node ${id}] No custom audio, using fallback silent wav`);
            audioRef.current = new Audio("data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAESsAACJWAAACABAAZGF0YRAAAAAA");
            audioRef.current.load();
        }
    }, [customAudioBase64]);

    useEffect(() => {
        let currentRate = window.BorealisUpdateRate;
        let intervalId = null;

        const runLogic = () => {
            const inputEdge = edges.find(e => e.target === id);
            const sourceId = inputEdge?.source || null;
            const val = sourceId ? (window.BorealisValueBus[sourceId] || "0") : "0";

            setCurrentInput(val);

            if (alertType === "Once") {
                if (val === "1" && prevInput !== "1") {
                    console.log(`[Alert Node ${id}] Triggered ONCE playback`);
                    playSound();
                }
            }

            setPrevInput(val);
        };

        const start = () => {
            if (alertType === "Constant") {
                intervalId = setInterval(() => {
                    const inputEdge = edges.find(e => e.target === id);
                    const sourceId = inputEdge?.source || null;
                    const val = sourceId ? (window.BorealisValueBus[sourceId] || "0") : "0";
                    setCurrentInput(val);
                    if (String(val) === "1") {
                        console.log(`[Alert Node ${id}] Triggered CONSTANT playback`);
                        playSound();
                    }
                }, intervalMs);
            } else {
                intervalId = setInterval(runLogic, currentRate);
            }
        };

        start();

        const monitor = setInterval(() => {
            const newRate = window.BorealisUpdateRate;
            if (newRate !== currentRate && alertType === "Once") {
                currentRate = newRate;
                clearInterval(intervalId);
                start();
            }
        }, 250);

        return () => {
            clearInterval(intervalId);
            clearInterval(monitor);
        };
    }, [edges, alertType, intervalMs, prevInput]);

    const indicatorColor = currentInput === "1" ? "#ff4444" : "#44ff44";

    return (
        <div className="borealis-node" style={{ position: "relative" }}>
            <Handle type="target" position={Position.Left} className="borealis-handle" />

            {/* Header with indicator dot */}
            <div className="borealis-node-header" style={{ position: "relative" }}>
                {data?.label || "Alert Sound"}
                <div style={{
                    position: "absolute",
                    top: "12px", // Adjusted from 6px to 12px for better centering
                    right: "6px",
                    width: "10px",
                    height: "10px",
                    borderRadius: "50%",
                    backgroundColor: indicatorColor,
                    border: "1px solid #222"
                }} />
            </div>

            <div className="borealis-node-content">
                <div style={{ marginBottom: "8px", fontSize: "9px", color: "#ccc" }}>
                    Play a sound when input equals "1"
                </div>

                <label style={{ fontSize: "9px", display: "block", marginBottom: "4px" }}>
                    Alerting Type:
                </label>
                <select
                    value={alertType}
                    onChange={(e) => setAlertType(e.target.value)}
                    style={dropdownStyle}
                >
                    <option value="Once">Once</option>
                    <option value="Constant">Constant</option>
                </select>

                <label style={{ fontSize: "9px", display: "block", marginBottom: "4px" }}>
                    Alert Interval (ms):
                </label>
                <input
                    type="number"
                    min="100"
                    step="100"
                    value={intervalMs}
                    onChange={(e) => setIntervalMs(parseInt(e.target.value))}
                    disabled={alertType === "Once"}
                    style={{
                        ...inputStyle,
                        background: alertType === "Once" ? "#2a2a2a" : "#1e1e1e"
                    }}
                />

                <label style={{ fontSize: "9px", display: "block", marginTop: "6px", marginBottom: "4px" }}>
                    Custom Sound:
                </label>

                <div style={{ display: "flex", gap: "4px" }}>
                    <input
                        type="file"
                        accept=".wav,.mp3,.mpeg,.ogg"
                        onChange={handleFileUpload}
                        style={{ ...inputStyle, marginBottom: 0, flex: 1 }}
                    />
                    <button
                        style={{
                            fontSize: "9px",
                            padding: "4px 8px",
                            backgroundColor: "#1e1e1e",
                            color: "#ccc",
                            border: "1px solid #444",
                            borderRadius: "2px",
                            cursor: "pointer"
                        }}
                        onClick={playSound}
                        title="Test playback"
                    >
                        Test
                    </button>
                </div>
            </div>
        </div>
    );
};

const dropdownStyle = {
    fontSize: "9px",
    padding: "4px",
    background: "#1e1e1e",
    color: "#ccc",
    border: "1px solid #444",
    borderRadius: "2px",
    width: "100%",
    marginBottom: "8px"
};

const inputStyle = {
    fontSize: "9px",
    padding: "4px",
    color: "#ccc",
    border: "1px solid #444",
    borderRadius: "2px",
    width: "100%",
    marginBottom: "8px"
};

export default {
    type: "AlertSoundNode",
    label: "Alert Sound",
    description: `
Plays a sound alert when input = "1"

- "Once" = Only when 0 -> 1 transition
- "Constant" = Repeats every X ms while input stays 1
- Custom audio supported (MP3/WAV/OGG)
- Base64 audio embedded in workflow and restored
- Visual status indicator (green = 0, red = 1)
- Manual "Test" button for validation
`.trim(),
    content: "Sound alert when input value = 1",
    component: AlertSoundNode
};
