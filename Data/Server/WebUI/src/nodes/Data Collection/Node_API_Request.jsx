////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data Collection/Node_API_Request.jsx

import React, { useState, useEffect, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

const APIRequestNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);

  if (!window.BorealisValueBus) window.BorealisValueBus = {};

  const [url, setUrl] = useState(data?.url || "http://localhost:5000/health");
  const [editUrl, setEditUrl] = useState(data?.url || "http://localhost:5000/health");
  const [useProxy, setUseProxy] = useState(data?.useProxy ?? true);
  const [body, setBody] = useState(data?.body || "");
  const [intervalSec, setIntervalSec] = useState(data?.intervalSec ?? 10);
  const [error, setError] = useState(null);
  const [statusCode, setStatusCode] = useState(null);
  const [statusText, setStatusText] = useState("");
  const resultRef = useRef(null);

  const handleUrlInputChange = (e) => setEditUrl(e.target.value);
  const handleToggleProxy = (e) => {
    const flag = e.target.checked;
    setUseProxy(flag);
    setNodes((nds) =>
      nds.map((n) => (n.id === id ? { ...n, data: { ...n.data, useProxy: flag } } : n))
    );
  };

  const handleUpdateUrl = () => {
    setUrl(editUrl);
    setError(null);
    setStatusCode(null);
    setStatusText("");
    resultRef.current = null;
    window.BorealisValueBus[id] = undefined;
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id
          ? { ...n, data: { ...n.data, url: editUrl, result: undefined } }
          : n
      )
    );
  };

  const handleBodyChange = (e) => {
    const newBody = e.target.value;
    setBody(newBody);
    setNodes((nds) =>
      nds.map((n) => (n.id === id ? { ...n, data: { ...n.data, body: newBody } } : n))
    );
  };

  const handleIntervalChange = (e) => {
    const sec = parseInt(e.target.value, 10) || 1;
    setIntervalSec(sec);
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id ? { ...n, data: { ...n.data, intervalSec: sec } } : n
      )
    );
  };

  useEffect(() => {
    let cancelled = false;

    const runNodeLogic = async () => {
      try {
        setError(null);

        const inputEdge = edges.find((e) => e.target === id);
        const upstreamUrl = inputEdge ? window.BorealisValueBus[inputEdge.source] : null;
        if (upstreamUrl && upstreamUrl !== editUrl) {
          setEditUrl(upstreamUrl);
        }

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

  const inputEdge = edges.find((e) => e.target === id);
  const hasUpstream = Boolean(inputEdge && inputEdge.source);

  return (
    <div className="borealis-node">
      <Handle type="target" position={Position.Left} className="borealis-handle" />

      <div className="borealis-node-header">API Request</div>
      <div className="borealis-node-content">
        <label style={{ fontSize: "9px", display: "block", marginBottom: "4px" }}>Request URL:</label>
        <input
          type="text"
          value={editUrl}
          onChange={handleUrlInputChange}
          disabled={hasUpstream}
          style={{
            fontSize: "9px",
            padding: "4px",
            width: "100%",
            background: hasUpstream ? "#2a2a2a" : "#1e1e1e",
            color: "#ccc",
            border: "1px solid #444",
            borderRadius: "2px"
          }}
        />
        <button
          onClick={handleUpdateUrl}
          disabled={hasUpstream}
          style={{
            fontSize: "9px",
            marginTop: "6px",
            padding: "2px 6px",
            background: "#333",
            color: "#ccc",
            border: "1px solid #444",
            borderRadius: "2px",
            cursor: hasUpstream ? "not-allowed" : "pointer"
          }}
        >
          Update URL
        </button>

        <div style={{ marginTop: "6px" }}>
          <input
            id={`${id}-proxy-toggle`}
            type="checkbox"
            checked={useProxy}
            onChange={handleToggleProxy}
          />
          <label
            htmlFor={`${id}-proxy-toggle`}
            title="Query a remote API server using internal Borealis mechanisms to bypass CORS limitations."
            style={{ fontSize: "8px", marginLeft: "4px", cursor: "help" }}
          >
            Remote API Endpoint
          </label>
        </div>

        <label style={{ fontSize: "9px", display: "block", margin: "8px 0 4px" }}>Request Body:</label>
        <textarea
          value={body}
          onChange={handleBodyChange}
          rows={4}
          style={{
            fontSize: "9px",
            padding: "4px",
            width: "100%",
            background: "#1e1e1e",
            color: "#ccc",
            border: "1px solid #444",
            borderRadius: "2px",
            resize: "vertical"
          }}
        />

        <label style={{ fontSize: "9px", display: "block", margin: "8px 0 4px" }}>Polling Interval (sec):</label>
        <input
          type="number"
          min="1"
          value={intervalSec}
          onChange={handleIntervalChange}
          style={{
            fontSize: "9px",
            padding: "4px",
            width: "100%",
            background: "#1e1e1e",
            color: "#ccc",
            border: "1px solid #444",
            borderRadius: "2px"
          }}
        />

        {statusCode !== null && statusCode >= 200 && statusCode < 300 && (
          <div style={{ color: "#6f6", fontSize: "8px", marginTop: "6px" }}>
            Status: {statusCode} {statusText}
          </div>
        )}
        {error && (
          <div style={{ color: "#f66", fontSize: "8px", marginTop: "6px" }}>
            Error: {error}
          </div>
        )}
      </div>

      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

export default {
  type: "API_Request",
  label: "API Request",
  description: "Fetch JSON from an API endpoint with optional body, proxy toggle, and polling interval.",
  content: "Fetch JSON from HTTP or remote API via internal proxy to bypass CORS.",
  component: APIRequestNode
};
