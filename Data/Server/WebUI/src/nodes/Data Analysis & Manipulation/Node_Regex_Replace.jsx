////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data Manipulation/Node_Regex_Replace.jsx
import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// Shared memory bus setup
if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

// -- Modern Regex Replace Node -- //
const RegexReplaceNode = ({ id, data }) => {
    const edges = useStore((state) => state.edges);
    const { setNodes } = useReactFlow();

    // Maintain output live value
    const [result, setResult] = useState("");
    const [original, setOriginal] = useState("");
    const valueRef = useRef("");

    useEffect(() => {
        let intervalId = null;
        let currentRate = window.BorealisUpdateRate;

        const runNodeLogic = () => {
            const inputEdge = edges.find((e) => e.target === id);
            const inputValue = inputEdge
                ? window.BorealisValueBus[inputEdge.source] || ""
                : "";

            setOriginal(inputValue);

            let newVal = inputValue;
            try {
                if ((data?.enabled ?? true) && data?.pattern) {
                    const regex = new RegExp(data.pattern, data.flags || "g");
                    let safeReplacement = (data.replacement ?? "").trim();
                    // Remove quotes if user adds them
                    if (
                        safeReplacement.startsWith('"') &&
                        safeReplacement.endsWith('"')
                    ) {
                        safeReplacement = safeReplacement.slice(1, -1);
                    }
                    newVal = inputValue.replace(regex, safeReplacement);
                }
            } catch (err) {
                newVal = `[Error] ${err.message}`;
            }

            if (newVal !== valueRef.current) {
                valueRef.current = newVal;
                setResult(newVal);
                window.BorealisValueBus[id] = newVal;
            }
        };

        intervalId = setInterval(runNodeLogic, currentRate);

        // Monitor update rate changes
        const monitor = setInterval(() => {
            const newRate = window.BorealisUpdateRate;
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
    }, [id, edges, data?.pattern, data?.replacement, data?.flags, data?.enabled]);

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />

            <div className="borealis-node-header">
                {data?.label || "Regex Replace"}
            </div>

            <div className="borealis-node-content">
                <div style={{ marginBottom: "6px", fontSize: "9px", color: "#ccc" }}>
                    Performs live regex-based find/replace on incoming string value.
                </div>
                <div style={{ fontSize: "9px", color: "#ccc", marginBottom: 2 }}>
                    <b>Pattern:</b> {data?.pattern || <i>(not set)</i>}<br />
                    <b>Flags:</b> {data?.flags || "g"}<br />
                    <b>Enabled:</b> {(data?.enabled ?? true) ? "Yes" : "No"}
                </div>
                <label style={{ fontSize: "8px", color: "#888" }}>Original:</label>
                <textarea
                    readOnly
                    value={original}
                    rows={2}
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        background: "#222",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        padding: "3px",
                        resize: "vertical",
                        marginBottom: "6px"
                    }}
                />
                <label style={{ fontSize: "8px", color: "#888" }}>Output:</label>
                <textarea
                    readOnly
                    value={result}
                    rows={2}
                    style={{
                        width: "100%",
                        fontSize: "9px",
                        background: "#2a2a2a",
                        color: "#ccc",
                        border: "1px solid #444",
                        borderRadius: "2px",
                        padding: "3px",
                        resize: "vertical"
                    }}
                />
            </div>

            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

// Modern Node Export: Sidebar config, usage docs, sensible defaults
export default {
    type: "RegexReplace",
    label: "Regex Replace",
    description: `
Live regex-based string find/replace node.

- Runs a JavaScript regular expression on every input update.
- Useful for cleanup, format fixes, redacting, token extraction.
- Configurable flags, replacement text, and enable toggle.
- Handles errors gracefully, shows live preview in the sidebar.
`.trim(),
    content: "Perform regex replacement on incoming string",
    component: RegexReplaceNode,
    config: [
        {
            key: "pattern",
            label: "Regex Pattern",
            type: "text",
            defaultValue: "\\d+"
        },
        {
            key: "replacement",
            label: "Replacement",
            type: "text",
            defaultValue: ""
        },
        {
            key: "flags",
            label: "Regex Flags",
            type: "text",
            defaultValue: "g"
        },
        {
            key: "enabled",
            label: "Enable Replacement",
            type: "select",
            options: ["true", "false"],
            defaultValue: "true"
        }
    ],
    usage_documentation: `
### Regex Replace Node

**Purpose:**  
Perform flexible find-and-replace on strings using JavaScript-style regular expressions.

#### Typical Use Cases
- Clean up text, numbers, or IDs in a data stream
- Mask or redact sensitive info (emails, credit cards, etc)
- Extract tokens, words, or reformat content

#### Configuration (see "Config" tab):
- **Regex Pattern**: The search pattern (supports capture groups)
- **Replacement**: The replacement string. You can use \`$1, $2\` for capture groups.
- **Regex Flags**: Default \`g\` (global). Add \`i\` (case-insensitive), \`m\` (multiline), etc.
- **Enable Replacement**: On/Off toggle (for easy debugging)

#### Behavior
- Upstream value is live-updated.
- When enabled, node applies the regex and emits the result downstream.
- Shows both input and output in the sidebar for debugging.
- If the regex is invalid, error is displayed as output.

#### Output
- Emits the transformed string to all downstream nodes.
- Updates in real time at the global Borealis update rate.

#### Example
Pattern: \`(\\d+)\`  
Replacement: \`[number:$1]\`  
Input: \`abc 123 def 456\`  
Output: \`abc [number:123] def [number:456]\`

---
**Tips:**  
- Use double backslashes (\\) in patterns when needed (e.g. \`\\\\d+\`).
- Flags can be any combination (e.g. \`gi\`).

  `.trim()
};
