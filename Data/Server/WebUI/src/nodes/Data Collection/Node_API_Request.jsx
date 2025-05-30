////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data Collection/Node_API_Request.jsx
import React, { useState, useEffect, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// API Request Node (Modern, Sidebar Config Enabled)
const APIRequestNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);

  if (!window.BorealisValueBus) window.BorealisValueBus = {};

  // Use config values, but coerce types
  const url = data?.url || "http://localhost:5000/health";
  // Note: Store useProxy as a string ("true"/"false"), convert to boolean for logic
  const useProxy = (data?.useProxy ?? "true") === "true";
  const body = data?.body || "";
  const intervalSec = parseInt(data?.intervalSec || "10", 10) || 10;

  // Status State
  const [error, setError] = useState(null);
  const [statusCode, setStatusCode] = useState(null);
  const [statusText, setStatusText] = useState("");
  const resultRef = useRef(null);

  useEffect(() => {
    let cancelled = false;

    const runNodeLogic = async () => {
      try {
        setError(null);

        // Allow dynamic URL override from upstream node (if present)
        const inputEdge = edges.find((e) => e.target === id);
        const upstreamUrl = inputEdge ? window.BorealisValueBus[inputEdge.source] : null;
        const resolvedUrl = upstreamUrl || url;
        let target = useProxy ? `/api/proxy?url=${encodeURIComponent(resolvedUrl)}` : resolvedUrl;

        const options = {};
        if (body.trim()) {
          options.method = "POST";
          options.headers = { "Content-Type": "application/json" };
          options.body = body;
        }

        const res = await fetch(target, options);
        setStatusCode(res.status);
        setStatusText(res.statusText);

        if (!res.ok) {
          resultRef.current = null;
          window.BorealisValueBus[id] = undefined;
          setNodes((nds) =>
            nds.map((n) =>
              n.id === id ? { ...n, data: { ...n.data, result: undefined } } : n
            )
          );
          throw new Error(`HTTP ${res.status}`);
        }

        const json = await res.json();
        const pretty = JSON.stringify(json, null, 2);
        if (!cancelled && resultRef.current !== pretty) {
          resultRef.current = pretty;
          window.BorealisValueBus[id] = json;
          setNodes((nds) =>
            nds.map((n) =>
              n.id === id ? { ...n, data: { ...n.data, result: pretty } } : n
            )
          );
        }
      } catch (err) {
        console.error("API Request node fetch error:", err);
        setError(err.message);
      }
    };

    runNodeLogic();
    const ms = Math.max(intervalSec, 1) * 1000;
    const iv = setInterval(runNodeLogic, ms);
    return () => {
      cancelled = true;
      clearInterval(iv);
    };
  }, [url, body, intervalSec, useProxy, id, setNodes, edges]);

  // Upstream disables direct editing of URL in the UI
  const inputEdge = edges.find((e) => e.target === id);
  const hasUpstream = Boolean(inputEdge && inputEdge.source);

  // -- Node Card Render (minimal: sidebar handles config) --
  return (
    <div className="borealis-node">
      <Handle type="target" position={Position.Left} className="borealis-handle" />

      <div className="borealis-node-header">
        {data?.label || "API Request"}
      </div>
      <div className="borealis-node-content" style={{ fontSize: "9px", color: "#ccc" }}>
        <div>
          <b>Status:</b>{" "}
          {error ? (
            <span style={{ color: "#f66" }}>{error}</span>
          ) : statusCode !== null ? (
            <span style={{ color: "#6f6" }}>{statusCode} {statusText}</span>
          ) : (
            "N/A"
          )}
        </div>
        <div style={{ marginTop: "4px" }}>
          <b>Result:</b>
          <pre style={{
            background: "#181818",
            color: "#b6ffb4",
            fontSize: "8px",
            maxHeight: 62,
            overflow: "auto",
            margin: 0,
            padding: "4px",
            borderRadius: "2px"
          }}>{data?.result ? String(data.result).slice(0, 350) : "No data"}</pre>
        </div>
      </div>
      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

// Node Registration Object with sidebar config + docs
export default {
  type: "API_Request",
  label: "API Request",
  description: "Fetch JSON from an API endpoint with optional POST body, polling, and proxy toggle. Accepts URL from upstream.",
  content: "Fetch JSON from HTTP or remote API endpoint, with CORS proxy option.",
  component: APIRequestNode,
  config: [
    {
      key: "url",
      label: "Request URL",
      type: "text",
      defaultValue: "http://localhost:5000/health"
    },
    {
      key: "useProxy",
      label: "Use Proxy (bypass CORS)",
      type: "select",
      options: ["true", "false"],
      defaultValue: "true"
    },
    {
      key: "body",
      label: "Request Body (JSON)",
      type: "textarea",
      defaultValue: ""
    },
    {
      key: "intervalSec",
      label: "Polling Interval (sec)",
      type: "text",
      defaultValue: "10"
    }
  ],
  usage_documentation: `
### API Request Node

Fetches JSON from an HTTP or HTTPS API endpoint, with an option to POST a JSON body and control polling interval.

**Features:**
- **URL**: You can set a static URL, or connect an upstream node to dynamically control the API endpoint.
- **Use Proxy**: When enabled, requests route through the Borealis backend proxy to bypass CORS/browser restrictions.
- **Request Body**: POST JSON data (leave blank for GET).
- **Polling Interval**: Set how often (in seconds) to re-fetch the API.

**Outputs:**
- The downstream value is the parsed JSON object from the API response.

**Typical Use Cases:**
- Poll external APIs (weather, status, data, etc)
- Connect to local/internal REST endpoints
- Build data pipelines with API triggers

**Input & UI Behavior:**
- If an upstream node is connected, its output value will override the Request URL.
- All config is handled in the right sidebar (Node Properties).

**Error Handling:**
- If the fetch fails, the node displays the error in the UI.
- Only 2xx status codes are considered successful.

**Security Note:**
- Use Proxy mode for APIs requiring CORS bypass or additional privacy.

  `.trim()
};
