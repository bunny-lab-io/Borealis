import React from "react";
import { Handle, Position } from "reactflow";

const dataNode = ({ data }) => {
    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <Handle type="source" position={Position.Right} className="borealis-handle" />
            <div className="borealis-node-header">{data.label || "Data Node"}</div>
            <div className="borealis-node-content">{data.content || "Placeholder Data Content"}</div>
        </div>
    );
};

export default {
    type: "dataNode",
    label: "Data Node",
    description: "This node acts as a baseline foundational example for all nodes to follow.",
    defaultContent: "Placeholder Node",
    component: dataNode
};
