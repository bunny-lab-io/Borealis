import React, { useRef, useState } from "react";
import { Handle, Position } from "reactflow";
import { Button, Snackbar } from "@mui/material";

/**
 * ExportToCSVNode
 * ----------------
 * Simplified version:
 * - No output connector
 * - Removed "Export to Disk" checkbox
 * - Only function is export to disk (manual trigger)
 */
const ExportToCSVNode = ({ data }) => {
    const [exportPath, setExportPath] = useState("");
    const [appendMode, setAppendMode] = useState(false);
    const [snackbarOpen, setSnackbarOpen] = useState(false);
    const fileInputRef = useRef(null);

    const handleExportClick = () => setSnackbarOpen(true);
    const handleSnackbarClose = () => setSnackbarOpen(false);

    const handlePathClick = async () => {
        if (window.showDirectoryPicker) {
            try {
                const dirHandle = await window.showDirectoryPicker();
                setExportPath(dirHandle.name || "Selected Directory");
            } catch (err) {
                console.warn("Directory Selection Cancelled:", err);
            }
        } else {
            fileInputRef.current?.click();
        }
    };

    const handleFakePicker = (event) => {
        const files = event.target.files;
        if (files.length > 0) {
            const fakePath = files[0].webkitRelativePath?.split("/")[0];
            setExportPath(fakePath || "Selected Folder");
        }
    };

    return (
        <div className="borealis-node">
            <Handle type="target" position={Position.Left} className="borealis-handle" />

            <div className="borealis-node-header">
                {data.label || "Export to CSV"}
            </div>

            <div className="borealis-node-content">
                <div style={{ marginBottom: "8px" }}>
                    {data.content || "Export Input Data to CSV File"}
                </div>

                <label style={{ fontSize: "9px", display: "block", marginTop: "6px" }}>
                    Export Path:
                </label>
                <div style={{ display: "flex", gap: "4px", alignItems: "center", marginBottom: "6px" }}>
                    <input
                        type="text"
                        readOnly
                        value={exportPath}
                        placeholder="Click to Select Folder"
                        onClick={handlePathClick}
                        style={{
                            flex: 1,
                            fontSize: "9px",
                            padding: "3px",
                            background: "#1e1e1e",
                            color: "#ccc",
                            border: "1px solid #444",
                            borderRadius: "2px",
                            cursor: "pointer"
                        }}
                    />
                    <Button
                        variant="outlined"
                        size="small"
                        onClick={handleExportClick}
                        sx={{
                            fontSize: "9px",
                            padding: "2px 8px",
                            minWidth: "unset",
                            borderColor: "#58a6ff",
                            color: "#58a6ff"
                        }}
                    >
                        Export
                    </Button>
                </div>

                <label style={{ fontSize: "9px", display: "block", marginTop: "4px" }}>
                    <input
                        type="checkbox"
                        checked={appendMode}
                        onChange={(e) => setAppendMode(e.target.checked)}
                        style={{ marginRight: "4px" }}
                    />
                    Append CSV Data if Headers Match
                </label>
            </div>

            <input
                ref={fileInputRef}
                type="file"
                webkitdirectory="true"
                directory=""
                multiple
                style={{ display: "none" }}
                onChange={handleFakePicker}
            />

            <Snackbar
                open={snackbarOpen}
                autoHideDuration={1000}
                onClose={handleSnackbarClose}
                message="Feature Coming Soon..."
                anchorOrigin={{ vertical: "bottom", horizontal: "center" }}
            />
        </div>
    );
};

export default {
    type: "ExportToCSVNode",
    label: "Export to CSV",
    description: `
Reporting Node  
This node lets the user choose a folder to export CSV data to disk.

When the "Export" button is clicked, CSV content (from upstream logic) is intended to be saved
to the selected directory. This is a placeholder for future file system interaction.

Inputs:
- Structured Table Data (via upstream node)

Outputs:
- None (writes directly to disk in future)
`.trim(),
    defaultContent: "Export Input Data to CSV File",
    component: ExportToCSVNode
};
