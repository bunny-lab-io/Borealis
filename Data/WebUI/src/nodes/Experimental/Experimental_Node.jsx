import React from "react";
import { Handle, Position } from "reactflow";

const experimentalNode = ({ data }) => {
    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <Handle type="source" position={Position.Right} className="borealis-handle" />
            <div className="borealis-node-header">{data.label || "Experimental Node"}</div>
            <div className="borealis-node-content">{data.content || "Placeholder Experimental Content"}</div>
        </div>
    );
};

export default {
    type: "experimentalNode",
    label: "Experimental Node",
    defaultContent: "Placeholder Node",
    component: experimentalNode
};
