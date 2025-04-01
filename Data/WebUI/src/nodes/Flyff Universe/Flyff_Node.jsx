import React from "react";
import { Handle, Position } from "reactflow";

const flyffNode = ({ data }) => {
    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <Handle type="source" position={Position.Right} className="borealis-handle" />
            <div className="borealis-node-header">{data.label || "Flyff Node"}</div>
            <div className="borealis-node-content">{data.content || "Placeholder Flyff Content"}</div>
        </div>
    );
};

export default {
    type: "flyffNode",
    label: "Flyff Node",
    defaultContent: "Placeholder Node",
    component: flyffNode
};
