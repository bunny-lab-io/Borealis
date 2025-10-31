////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Image Processing/Node_Image_Viewer.jsx

import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useReactFlow } from "reactflow";

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const ImageViewerNode = ({ id }) => {
    const { getEdges } = useReactFlow();
    const [imageBase64, setImageBase64] = useState("");
    const canvasRef = useRef(null);
    const overlayDivRef = useRef(null);
    const [isZoomed, setIsZoomed] = useState(false);

    // Poll upstream for base64 image and propagate
    useEffect(() => {
        const interval = setInterval(() => {
            const edges = getEdges();
            const inp = edges.find(e => e.target === id);
            if (inp) {
                const val = window.BorealisValueBus[inp.source] || "";
                setImageBase64(val);
                window.BorealisValueBus[id] = val;
            } else {
                setImageBase64("");
                window.BorealisValueBus[id] = "";
            }
        }, window.BorealisUpdateRate);
        return () => clearInterval(interval);
    }, [getEdges, id]);

    // Draw the image into canvas for high performance
    useEffect(() => {
        const canvas = canvasRef.current;
        if (!canvas) return;
        const ctx = canvas.getContext("2d");
        if (imageBase64) {
            const img = new Image();
            img.onload = () => {
                canvas.width = img.width;
                canvas.height = img.height;
                ctx.clearRect(0, 0, canvas.width, canvas.height);
                ctx.drawImage(img, 0, 0);
            };
            img.src = "data:image/png;base64," + imageBase64;
        } else {
            ctx.clearRect(0, 0, canvas.width, canvas.height);
        }
    }, [imageBase64]);

    // Manage zoom overlay on image click
    useEffect(() => {
        if (!isZoomed || !imageBase64) return;
        const div = document.createElement("div");
        overlayDivRef.current = div;
        Object.assign(div.style, {
            position: "fixed",
            top: "0",
            left: "0",
            width: "100%",
            height: "100%",
            backgroundColor: "rgba(0,0,0,0.8)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: "1000",
            cursor: "zoom-out",
            transition: "opacity 0.3s ease"
        });
        const handleOverlayClick = () => setIsZoomed(false);
        div.addEventListener("click", handleOverlayClick);

        const ovCanvas = document.createElement("canvas");
        const ctx = ovCanvas.getContext("2d");
        const img = new Image();
        img.onload = () => {
            let w = img.width;
            let h = img.height;
            const maxW = window.innerWidth * 0.8;
            const maxH = window.innerHeight * 0.8;
            const scale = Math.min(1, maxW / w, maxH / h);
            ovCanvas.width = w;
            ovCanvas.height = h;
            ctx.clearRect(0, 0, w, h);
            ctx.drawImage(img, 0, 0);
            ovCanvas.style.width = `${w * scale}px`;
            ovCanvas.style.height = `${h * scale}px`;
            ovCanvas.style.transition = "transform 0.3s ease";
        };
        img.src = "data:image/png;base64," + imageBase64;
        div.appendChild(ovCanvas);
        document.body.appendChild(div);

        // Cleanup when unzooming
        return () => {
            div.removeEventListener("click", handleOverlayClick);
            if (overlayDivRef.current) {
                document.body.removeChild(overlayDivRef.current);
                overlayDivRef.current = null;
            }
        };
    }, [isZoomed, imageBase64]);

    const handleClick = () => {
        if (imageBase64) setIsZoomed(z => !z);
    };

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />
            <div className="borealis-node-header">Image Viewer</div>
            <div
                className="borealis-node-content"
                style={{ cursor: imageBase64 ? "zoom-in" : "default" }}
            >
                {imageBase64 ? (
                    <canvas
                        ref={canvasRef}
                        onClick={handleClick}
                        style={{ width: "100%", border: "1px solid #333", marginTop: "6px", marginBottom: "6px" }}
                    />
                ) : (
                    <div style={{ fontSize: "9px", color: "#888", marginTop: "6px" }}>
                        Waiting for image...
                    </div>
                )}
            </div>
            <Handle type="source" position={Position.Right} className="borealis-handle" />
        </div>
    );
};

export default {
    type: "Image_Viewer",
    label: "Image Viewer",
    description: `
Displays base64 image via canvas for high performance

- Accepts upstream base64 image
- Renders with canvas for speed
- Click to zoom/unzoom overlay with smooth transition
`.trim(),
    content: "Visual preview of base64 image with zoom overlay.",
    component: ImageViewerNode
};
