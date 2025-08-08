////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Workflow_List.jsx

import React, { useState, useMemo } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TableSortLabel
} from "@mui/material";
import { PlayCircle as PlayCircleIcon } from "@mui/icons-material";

export default function WorkflowList({ onOpenWorkflow }) {
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("name");
  const [order, setOrder] = useState("asc");

  const handleSort = (col) => {
    if (orderBy === col) setOrder(order === "asc" ? "desc" : "asc");
    else {
      setOrderBy(col);
      setOrder("asc");
    }
  };

  const sorted = useMemo(() => {
    const dir = order === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      const A = a[orderBy] || "";
      const B = b[orderBy] || "";
      return String(A).localeCompare(String(B)) * dir;
    });
  }, [rows, orderBy, order]);

  const handleNewWorkflow = () => {
    if (onOpenWorkflow) {
      onOpenWorkflow(); // trigger App.jsx to open editor
    }
  };

  const handleRowClick = (workflow) => {
    if (onOpenWorkflow) {
      onOpenWorkflow(workflow);
    }
  };

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <Box>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
            Workflows
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            List of available workflows.
          </Typography>
        </Box>
        <Button
          startIcon={<PlayCircleIcon />}
          sx={{
            color: "#58a6ff",
            borderColor: "#58a6ff",
            textTransform: "none",
            border: "1px solid #58a6ff",
            backgroundColor: "#1e1e1e",
            "&:hover": { backgroundColor: "#1b1b1b" }
          }}
          onClick={handleNewWorkflow}
        >
          New Workflow
        </Button>
      </Box>
      <Table size="small" sx={{ minWidth: 680 }}>
        <TableHead>
          <TableRow>
            <TableCell sortDirection={orderBy === "name" ? order : false}>
              <TableSortLabel
                active={orderBy === "name"}
                direction={orderBy === "name" ? order : "asc"}
                onClick={() => handleSort("name")}
              >
                Name
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "description" ? order : false}>
              <TableSortLabel
                active={orderBy === "description"}
                direction={orderBy === "description" ? order : "asc"}
                onClick={() => handleSort("description")}
              >
                Description
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "category" ? order : false}>
              <TableSortLabel
                active={orderBy === "category"}
                direction={orderBy === "category" ? order : "asc"}
                onClick={() => handleSort("category")}
              >
                Category
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "lastEdited" ? order : false}>
              <TableSortLabel
                active={orderBy === "lastEdited"}
                direction={orderBy === "lastEdited" ? order : "asc"}
                onClick={() => handleSort("lastEdited")}
              >
                Last Edited
              </TableSortLabel>
            </TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((r, i) => (
            <TableRow
              key={i}
              hover
              sx={{ cursor: "pointer" }}
              onClick={() => handleRowClick(r)}
            >
              <TableCell>{r.name}</TableCell>
              <TableCell>{r.description}</TableCell>
              <TableCell>{r.category}</TableCell>
              <TableCell>{r.lastEdited}</TableCell>
            </TableRow>
          ))}
          {sorted.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} sx={{ color: "#888" }}>
                No workflows found.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Paper>
  );
}
