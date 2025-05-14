////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/General Purpose/Node_Data.jsx

/**
 * ============================================
 * Borealis - Standard Live Data Node Template
 * ============================================
 *
 * COMPONENT ROLE:
 * This component defines a "data conduit" node that can accept input,
 * process/override it with local logic, and forward the output on a timed basis.
 * 
 * It serves as the core behavior model for other nodes that rely on live propagation.
 * Clone and extend this file to create nodes with specialized logic.
 *
 * CORE CONCEPTS:
 * - Uses a centralized shared memory (window.BorealisValueBus) for value sharing
 * - Synchronizes with upstream nodes based on ReactFlow edges
 * - Emits to downstream nodes by updating its own BorealisValueBus[id] value
 * - Controlled by a global polling timer (window.BorealisUpdateRate)
 * 
 * LIFECYCLE SUMMARY:
 * - onMount: initializes logic loop and sync monitor
 * - onUpdate: watches edges and global rate, reconfigures as needed
 * - onUnmount: cleans up all timers
 *
 * DATA FLOW OVERVIEW:
 * - INPUT: if a left-edge (target) is connected, disables manual editing
 * - OUTPUT: propagates renderValue to downstream nodes via right-edge (source)
 *
 * STRUCTURE:
 * - Node UI includes:
 *    * Label (from data.label)
 *    * Body description (from data.content)
 *    * Input textbox (disabled if input is connected)
 *
 * HOW TO EXTEND:
 * - For transformations, insert logic into runNodeLogic()
 * - To validate or restrict input types, modify handleManualInput()
 * - For side-effects or external API calls, add hooks inside runNodeLogic()
 */

import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
import { IconButton } from "@mui/material";
import { Settings as SettingsIcon } from "@mui/icons-material";

// Global Shared Bus for Node Data Propagation
if (!window.BorealisValueBus) {
    window.BorealisValueBus = {};
}

// Global Update Rate (ms) for All Data Nodes
if (!window.BorealisUpdateRate) {
    window.BorealisUpdateRate = 100;
}

const DataNode = ({ id, data }) => {
    const { setNodes } = useReactFlow();
    const edges = useStore(state => state.edges);

    const [renderValue, setRenderValue] = useState(data?.value || "");
    const valueRef = useRef(renderValue);

    // Manual input handler (disabled if connected to input)
    const handleManualInput = (e) => {
        const newValue = e.target.value;

        // TODO: Add input validation/sanitization here if needed
        valueRef.current = newValue;
        setRenderValue(newValue);

        window.BorealisValueBus[id] = newValue;

        setNodes(nds =>
            nds.map(n =>
                n.id === id
                    ? { ...n, data: { ...n.data, value: newValue } }
                    : n
            )
        );
    };

    useEffect(() => {
        let currentRate = window.BorealisUpdateRate || 100;
        let intervalId = null;

        const runNodeLogic = () => {
            const inputEdge = edges.find(e => e?.target === id);
            const hasInput = Boolean(inputEdge);

            if (hasInput && inputEdge.source) {
                const upstreamValue = window.BorealisValueBus[inputEdge.source] ?? "";

                // TODO: Insert custom transform logic here (e.g., parseInt, apply formula)

                if (upstreamValue !== valueRef.current) {
                    valueRef.current = upstreamValue;
                    setRenderValue(upstreamValue);
                    window.BorealisValueBus[id] = upstreamValue;

                    setNodes(nds =>
                        nds.map(n =>
                            n.id === id
                                ? { ...n, data: { ...n.data, value: upstreamValue } }
                                : n
                        )
                    );
                }
            } else {
                // OUTPUT BROADCAST: emits to downstream via shared memory
                window.BorealisValueBus[id] = valueRef.current;
            }
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
    }, [id, setNodes, edges]);

    const inputEdge = edges.find(e => e?.target === id);
    const hasInput = Boolean(inputEdge);
    const upstreamId = inputEdge?.source || "";
    const upstreamValue = window.BorealisValueBus[upstreamId] || "";

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />

            <div className="borealis-node-header" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
            <span>{data?.label || "Data Node"}</span>
                <IconButton
                size="small"
                  onClick={() =>
                    window.BorealisOpenDrawer &&
                    window.BorealisOpenDrawer(data?.label || "Unknown Node")
                }
                sx={{
                    padding: "0px",
                    marginRight: "-3px",
                    color: "#58a6ff",
                    fontSize: "14px", // affects inner icon when no size prop
                    width: "20px",
                    height: "20px",
                    minWidth: "20px"
                }}
                >
                <SettingsIcon sx={{ fontSize: "16px" }} />
                </IconButton>
            </div>


            <div className="borealis-node-content">
                {/* Description visible in node body */}
                <div style={{ marginBottom: "8px", fontSize: "9px", color: "#ccc" }}>
                    {data?.content || "Foundational node for live value propagation."}
                </div>

                <label style={{ fontSize: "9px", display: "block", marginBottom: "4px" }}>
                    Value:
                </label>
                <input
                    type="text"
                    value={renderValue}
                    onChange={handleManualInput}
                    disabled={hasInput}
                    style={{
                        fontSize: "9px",
                        padding: "4px",
                        background: hasInput ? "#2a2a2a" : "#1e1e1e",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        width: "100%"
                    }}
                />
            </div>

            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

export default {
    type: "DataNode", // REQUIRED: unique identifier for the node type
    label: "String / Number",
    description: `
Foundational Data Node

- Accepts input from another node
- If no input is connected, allows user-defined value
- Pushes value to downstream nodes every X ms
- Uses BorealisValueBus to communicate with other nodes
`.trim(),
    content: "Store a String or Number",
    component: DataNode
};
