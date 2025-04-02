/**
 * ===========================================
 * Borealis - Backdrop Group Box Node
 * ===========================================
 *
 * COMPONENT ROLE:
 * This node functions as a backdrop or grouping box.
 * It's resizable and can be renamed by clicking its title.
 * It doesn't connect to other nodes or pass data—it's purely visual.
 *
 * BEHAVIOR:
 * - Allows renaming via single-click on the header text.
 * - Can be resized by dragging from the bottom-right corner.
 *
 * NOTE:
 * - No inputs/outputs: purely cosmetic for grouping and labeling.
 */

import React, { useState, useEffect, useRef } from "react";
import { Handle, Position } from "reactflow";
import { ResizableBox } from "react-resizable";
import "react-resizable/css/styles.css";

const BackdropGroupBoxNode = ({ id, data }) => {
    const [title, setTitle] = useState(data?.label || "Backdrop Group Box");
    const [isEditing, setIsEditing] = useState(false);
    const inputRef = useRef(null);

    useEffect(() => {
        if (isEditing && inputRef.current) {
            inputRef.current.focus();
        }
    }, [isEditing]);

    const handleTitleClick = () => {
        setIsEditing(true);
    };

    const handleTitleChange = (e) => {
        const newTitle = e.target.value;
        setTitle(newTitle);
        window.BorealisValueBus[id] = newTitle;
    };

    const handleBlur = () => {
        setIsEditing(false);
    };

    return (
        <div style={{ pointerEvents: "auto", zIndex: -1 }}> {/* Prevent blocking other nodes */}
            <ResizableBox
                width={200}
                height={120}
                minConstraints={[120, 80]}
                maxConstraints={[600, 600]}
                resizeHandles={["se"]}
                style={{
                    backgroundColor: "rgba(44, 44, 44, 0.5)",
                    border: "1px solid #3a3a3a",
                    borderRadius: "4px",
                    boxShadow: "0 0 5px rgba(88, 166, 255, 0.15)",
                    overflow: "hidden",
                    position: "relative"
                }}
                onClick={(e) => e.stopPropagation()} // prevent drag on resize
            >
                <div
                    onClick={handleTitleClick}
                    style={{
                        backgroundColor: "rgba(35, 35, 35, 0.5)",
                        padding: "6px 10px",
                        fontWeight: "bold",
                        fontSize: "10px",
                        cursor: "pointer",
                        userSelect: "none"
                    }}
                >
                    {isEditing ? (
                        <input
                            ref={inputRef}
                            type="text"
                            value={title}
                            onChange={handleTitleChange}
                            onBlur={handleBlur}
                            style={{
                                fontSize: "10px",
                                padding: "2px",
                                background: "#1e1e1e",
                                color: "#ccc",
                                border: "1px solid #444",
                                borderRadius: "2px",
                                width: "100%"
                            }}
                        />
                    ) : (
                        <span>{title}</span>
                    )}
                </div>
                <div style={{ padding: "10px", fontSize: "9px", height: "100%" }}>
                    {/* Empty space for grouping */}
                </div>
            </ResizableBox>
        </div>
    );
};

export default {
    type: "BackdropGroupBoxNode",
    label: "Backdrop Group Box",
    description: `
Resizable Grouping Node

- Purely cosmetic, for grouping related nodes
- Resizable by dragging bottom-right corner
- Rename by clicking on title bar
`.trim(),
    content: "Use as a visual group label",
    component: BackdropGroupBoxNode
};
