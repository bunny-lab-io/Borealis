////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Scheduled_Jobs_List.jsx

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
  TableSortLabel,
  Switch
} from "@mui/material";
import { Edit as EditIcon } from "@mui/icons-material";

export default function ScheduledJobsList() {
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

  const resultColor = (r) =>
    r === "Success" ? "#00d18c" : r === "Warning" ? "#ff8c00" : "#ff4f4f";

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1 }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
          Scheduled Jobs
        </Typography>
        <Typography variant="body2" sx={{ color: "#aaa" }}>
          List of automation jobs with schedules, results, and actions.
        </Typography>
      </Box>
      <Table size="small" sx={{ minWidth: 900 }}>
        <TableHead>
          <TableRow>
            {[
              ["name", "Name"],
              ["scriptWorkflow", "Script / Workflow"],
              ["target", "Target"],
              ["occurrence", "Schedule Occurrence"],
              ["lastRun", "Last Run"],
              ["nextRun", "Next Run"],
              ["result", "Result"],
              ["enabled", "Enabled"],
              ["edit", "Edit Job"]
            ].map(([key, label]) => (
              <TableCell key={key} sortDirection={orderBy === key ? order : false}>
                {key !== "edit" ? (
                  <TableSortLabel
                    active={orderBy === key}
                    direction={orderBy === key ? order : "asc"}
                    onClick={() => handleSort(key)}
                  >
                    {label}
                  </TableSortLabel>
                ) : (
                  label
                )}
              </TableCell>
            ))}
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((r, i) => (
            <TableRow key={i} hover>
              <TableCell>{r.name}</TableCell>
              <TableCell>{r.scriptWorkflow}</TableCell>
              <TableCell>{r.target}</TableCell>
              <TableCell>{r.occurrence}</TableCell>
              <TableCell>{r.lastRun}</TableCell>
              <TableCell>{r.nextRun}</TableCell>
              <TableCell>
                <span
                  style={{
                    display: "inline-block",
                    width: 10,
                    height: 10,
                    borderRadius: 10,
                    background: resultColor(r.result),
                    marginRight: 8,
                    verticalAlign: "middle"
                  }}
                />
                {r.result}
              </TableCell>
              <TableCell>
                <Switch
                  checked={r.enabled}
                  onChange={() => {
                    setRows((prev) =>
                      prev.map((job, idx) =>
                        idx === i ? { ...job, enabled: !job.enabled } : job
                      )
                    );
                  }}
                  size="small"
                />
              </TableCell>
              <TableCell>
                <Button
                  variant="outlined"
                  size="small"
                  startIcon={<EditIcon />}
                  sx={{
                    color: "#58a6ff",
                    borderColor: "#58a6ff",
                    textTransform: "none"
                  }}
                  onClick={() => alert(`Edit job: ${r.name}`)}
                >
                  Edit
                </Button>
              </TableCell>
            </TableRow>
          ))}
          {sorted.length === 0 && (
            <TableRow>
              <TableCell colSpan={9} sx={{ color: "#888" }}>
                No scheduled jobs found.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Paper>
  );
}
