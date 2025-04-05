/**
 * =================================================================
 * Borealis Threshold Node
 * with Per-Node Unique ID & BorealisValueBus Persistence
 * =================================================================
 *
 * GOALS:
 * 1) Generate a unique ID for this node instance (the "threshold node instance ID").
 * 2) Store that ID in node.data._thresholdId if it’s not already there.
 * 3) Use that ID to read/write the threshold from window.BorealisValueBus,
 *    so that if the node is forcibly unmounted & remounted but re-uses the
 *    same node.data._thresholdId, we restore the slider.
 *
 * HOW IT WORKS:
 * - On mount, we check if node.data._thresholdId exists.
 *   - If yes, we use that as our "instance ID".
 *   - If no, we create a new random ID (like a GUID) and store it in node.data._thresholdId
 *     and also do a one-time `setNodes()` call to update the node data so it persists.
 * - Once we have that instance ID, we look up
 *   `window.BorealisValueBus["th|" + instanceId]` for a saved threshold.
 * - If found, we initialize the slider with that. Otherwise 128.
 * - On slider change, we update that bus key with the new threshold.
 * - On each unmount/mount cycle, if the node’s data still has `_thresholdId`,
 *   we reload from the same bus key, preventing a “rubber-banding” reset.
 *
 * NOTE:
 * - We do call setNodes() once if we have to embed the newly generated ID into node.data.
 *   But we do it carefully, minimal, so we don't forcibly re-create the node. 
 *   If your parent code overwrites node.data, we lose the ID.
 *
 * WARNING:
 * - If the parent changes the node’s ID or data every time, there's no fix. This is the
 *   best we can do entirely inside this node’s code.
 */

import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useStore, useReactFlow } from "reactflow";

// Ensure BorealisValueBus exists
if (!window.BorealisValueBus) {
    window.BorealisValueBus = {};
}

// Default global update rate
if (!window.BorealisUpdateRate) {
    window.BorealisUpdateRate = 100;
}

/**
 * Utility to generate a random GUID-like string
 */
function generateGUID() {
    // Very simple random hex approach
    return "xxxx-4xxx-yxxx-xxxx".replace(/[xy]/g, function (c) {
        const r = Math.random() * 16 | 0;
        const v = c === "x" ? r : (r & 0x3 | 0x8);
        return v.toString(16);
    });
}

const PersistentThresholdNode = ({ id, data }) => {
    const edges = useStore((state) => state.edges);
    const { setNodes } = useReactFlow(); // so we can store our unique ID in node.data if needed

    // We'll store:
    // 1) The node’s unique threshold instance ID (guid)
    // 2) The slider threshold in local React state
    const [instanceId, setInstanceId] = useState(() => {
        // See if we already have an ID in data._thresholdId
        const existing = data?._thresholdId;
        return existing || ""; // If not found, empty string for now
    });

    const [threshold, setThreshold] = useState(128);

    // For checking upstream changes
    const [renderValue, setRenderValue] = useState("");
    const valueRef = useRef(renderValue);

    // MOUNT / UNMOUNT debug
    useEffect(() => {
        console.log(`[ThresholdNode:${id}] MOUNTED (instanceId=${instanceId || "none"})`);
        return () => {
            console.log(`[ThresholdNode:${id}] UNMOUNTED (instanceId=${instanceId || "none"})`);
        };
    }, [id, instanceId]);

    /**
     * On first mount, we see if we have an instanceId in node.data.
     * If not, we create one, call setNodes() to store it in data._thresholdId.
     */
    useEffect(() => {
        if (!instanceId) {
            // Generate a new ID
            const newId = generateGUID();
            console.log(`[ThresholdNode:${id}] Generating new instanceId=${newId}`);

            // Insert it into this node’s data
            setNodes((prevNodes) =>
                prevNodes.map((n) => {
                    if (n.id === id) {
                        return {
                            ...n,
                            data: {
                                ...n.data,
                                _thresholdId: newId
                            }
                        };
                    }
                    return n;
                })
            );
            setInstanceId(newId);
        } else {
            console.log(`[ThresholdNode:${id}] Found existing instanceId=${instanceId}`);
        }
    }, [id, instanceId, setNodes]);

    /**
     * Once we have an instanceId (existing or new), load the threshold from BorealisValueBus
     * We skip if we haven't assigned instanceId yet.
     */
    useEffect(() => {
        if (!instanceId) return; // wait for the ID to be set

        // Look for a previously saved threshold in the bus
        const savedKey = `th|${instanceId}`;
        let savedVal = window.BorealisValueBus[savedKey];
        if (typeof savedVal !== "number") {
            // default to 128
            savedVal = 128;
        }
        console.log(`[ThresholdNode:${id}] init threshold from bus key=${savedKey} => ${savedVal}`);
        setThreshold(savedVal);
    }, [id, instanceId]);

    /**
     * Threshold slider handle
     */
    const handleSliderChange = (e) => {
        const newVal = parseInt(e.target.value, 10);
        setThreshold(newVal);
        console.log(`[ThresholdNode:${id}] Slider => ${newVal}`);

        // Immediately store in BorealisValueBus
        if (instanceId) {
            const savedKey = `th|${instanceId}`;
            window.BorealisValueBus[savedKey] = newVal;
        }
    };

    /**
     * Helper function to apply threshold to base64
     */
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

                resolve(canvas.toDataURL("image/png"));
            };

            img.onerror = () => resolve(base64Data);
            img.src = base64Data;
        });
    };

    /**
     * Main logic loop (polling)
     */
    useEffect(() => {
        let currentRate = window.BorealisUpdateRate || 100;
        let intervalId = null;

        const runNodeLogic = async () => {
            // find upstream edge
            const inputEdge = edges.find((e) => e.target === id);
            if (inputEdge && inputEdge.source) {
                const upstreamValue = window.BorealisValueBus[inputEdge.source] ?? "";
                if (upstreamValue !== valueRef.current) {
                    const thresholded = await applyThreshold(upstreamValue, threshold);
                    valueRef.current = thresholded;
                    setRenderValue(thresholded);
                    window.BorealisValueBus[id] = thresholded;
                }
            } else {
                // No upstream
                if (valueRef.current) {
                    console.log(`[ThresholdNode:${id}] no upstream => clear`);
                }
                valueRef.current = "";
                setRenderValue("");
                window.BorealisValueBus[id] = "";
            }
        };

        const startInterval = () => {
            intervalId = setInterval(runNodeLogic, currentRate);
        };
        startInterval();

        // watch for update rate changes
        const monitor = setInterval(() => {
            const newRate = window.BorealisUpdateRate || 100;
            if (newRate !== currentRate) {
                currentRate = newRate;
                clearInterval(intervalId);
                startInterval();
            }
        }, 500);

        return () => {
            clearInterval(intervalId);
            clearInterval(monitor);
        };
    }, [id, edges, threshold]);

    /**
     * If threshold changes, re-apply to upstream immediately (if we have upstream)
     */
    useEffect(() => {
        const inputEdge = edges.find((e) => e.target === id);
        if (!inputEdge) {
            return;
        }
        const upstreamVal = window.BorealisValueBus[inputEdge.source] ?? "";
        if (!upstreamVal) {
            return;
        }
        applyThreshold(upstreamVal, threshold).then((transformed) => {
            valueRef.current = transformed;
            setRenderValue(transformed);
            window.BorealisValueBus[id] = transformed;
        });
    }, [threshold, edges, id]);

    // Render the node
    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <div className="borealis-node-header" style={{ fontSize: "11px" }}>
                Threshold Node
            </div>

            <div className="borealis-node-content" style={{ fontSize: "9px" }}>
                <div style={{ marginBottom: "6px", color: "#ccc" }}>
                    <strong>Node ID:</strong> {id} <br />
                    <strong>Instance ID:</strong> {instanceId || "(none)"} <br />
                    (Slider persists across re-mount if data._thresholdId is preserved)
                </div>

                <label>Threshold ({threshold}):</label>
                <input
                    type="range"
                    min="0"
                    max="255"
                    value={threshold}
                    onChange={handleSliderChange}
                    style={{
                        width: "100%",
                        marginBottom: "6px",
                        background: "#2a2a2a"
                    }}
                />
            </div>
            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

// Export as a React Flow Node
export default {
    type: "PersistentThresholdNode",
    label: "Persistent Threshold Node",
    description: `
Stores a unique ID in node.data._thresholdId and uses it to track the slider threshold
in BorealisValueBus, so the slider doesn't reset if the node is re-mounted with the same data.
`,
    content: "Convert incoming base64 image to black & white thresholded image, with node-level persistence.",
    component: PersistentThresholdNode
};
