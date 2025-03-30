// Data/WebUI/src/nodes/TestNode.jsx
import React from 'react';
import { Handle, Position } from 'reactflow';

export default function TestNode({ data }) {
    return (
        <div style={{ background: '#fff', padding: '8px', border: '1px solid #999' }}>
            <b>Test Node</b>
            <div>{data.label}</div>
            <Handle type="target" position={Position.Top} />
            <Handle type="source" position={Position.Bottom} />
        </div>
    );
}
