////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data Analysis/Node_Regex_Search.jsx
import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// Modern Regex Search Node: Config via Sidebar

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const RegexSearchNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);

  // Pattern/flags always come from sidebar config (with defaults)
  const pattern = data?.pattern ?? "";
  const flags = data?.flags ?? "i";

  const valueRef = useRef("0");
  const [matched, setMatched] = useState("0");

  useEffect(() => {
    let intervalId = null;
    let currentRate = window.BorealisUpdateRate;

    const runNodeLogic = () => {
      const inputEdge = edges.find((e) => e.target === id);
      const inputVal = inputEdge ? window.BorealisValueBus[inputEdge.source] || "" : "";

      let matchResult = false;
      try {
        if (pattern) {
          const regex = new RegExp(pattern, flags);
          matchResult = regex.test(inputVal);
        }
      } catch {
        matchResult = false;
      }

      const result = matchResult ? "1" : "0";

      if (result !== valueRef.current) {
        valueRef.current = result;
        setMatched(result);
        window.BorealisValueBus[id] = result;
        setNodes((nds) =>
          nds.map((n) =>
            n.id === id ? { ...n, data: { ...n.data, match: result } } : n
          )
        );
      }
    };

    intervalId = setInterval(runNodeLogic, currentRate);

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
  }, [id, edges, pattern, flags, setNodes]);

  return (
    <div className="borealis-node">
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <div className="borealis-node-header">
        {data?.label || "Regex Search"}
      </div>
      <div className="borealis-node-content" style={{ fontSize: "9px", color: "#ccc" }}>
        Match: {matched}
      </div>
      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

export default {
  type: "RegexSearch",
  label: "Regex Search",
  description: `
Test for text matches with a regular expression pattern.

- Accepts a regex pattern and flags (e.g. "i", "g", "m")
- Connect any node to the input to test its value.
- Outputs "1" if the regex matches, otherwise "0".
- Useful for input validation, filtering, or text triggers.
  `.trim(),
  content: "Outputs '1' if regex matches input, otherwise '0'",
  component: RegexSearchNode,
  config: [
    {
      key: "pattern",
      label: "Regex Pattern",
      type: "text",
      defaultValue: "",
      placeholder: "e.g. World"
    },
    {
      key: "flags",
      label: "Regex Flags",
      type: "text",
      defaultValue: "i",
      placeholder: "e.g. i"
    }
  ],
  usage_documentation: `
### Regex Search Node

This node tests its input value against a user-supplied regular expression pattern.

**Configuration (Sidebar):**
- **Regex Pattern**: Standard JavaScript regex pattern.
- **Regex Flags**: Any combination of \`i\` (ignore case), \`g\` (global), \`m\` (multiline), etc.

**Input:**
- Accepts a string from any upstream node.

**Output:**
- Emits "1" if the pattern matches the input string.
- Emits "0" if there is no match or the pattern/flags are invalid.

**Common Uses:**
- Search for words/phrases in extracted text.
- Filter values using custom patterns.
- Create triggers based on input structure (e.g. validate an email, detect phone numbers, etc).

#### Example:
- **Pattern:** \`World\`
- **Flags:** \`i\`
- **Input:** \`Hello world!\`
- **Output:** \`1\` (matched, case-insensitive)
  `.trim()
};
