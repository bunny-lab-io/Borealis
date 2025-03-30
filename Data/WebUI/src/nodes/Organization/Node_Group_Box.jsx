// src/nodes/General Purpose/CustomNode.jsx
import React from "react";
import { Handle, Position } from "reactflow";

const nodeGroupBox = ({ data }) => {
    return (
        <div
            style={{
                background: "#2c2c2c",
                border: "1px solid #3a3a3a",
                borderRadius: "6px",
                color: "#ccc",
                fontSize: "12px",
                minWidth: "160px",
                maxWidth: "260px",
                boxShadow: `
                    0 0 5px rgba(88, 166, 255, 0.1),
                    0 0 10px rgba(88, 166, 255, 0.1)
                `,
                position: "relative",
                transition: "box-shadow 0.3s ease-in-out"
            }}
        >
            <Handle
                type="target"
                position={Position.Left}
                style={{
                    background: "#58a6ff",
                    width: 10,
                    height: 10
                }}
            />
            <Handle
                type="source"
                position={Position.Right}
                style={{
                    background: "#58a6ff",
                    width: 10,
                    height: 10
                }}
            />
            <div
                style={{
                    background: "#232323",
                    padding: "6px 10px",
                    borderTopLeftRadius: "6px",
                    borderTopRightRadius: "6px",
                    fontWeight: "bold",
                    fontSize: "13px"
                }}
            >
                {data.label || "Node Group Box"}
            </div>
            <div style={{ padding: "10px" }}>
                {data.content || "Node Group Box"}
            </div>
        </div>
    );
};

export default {
    type: "nodeGroupBox", // Must match the type used in nodeTypes
    label: "Node Group Box",
    component: nodeGroupBox
};
