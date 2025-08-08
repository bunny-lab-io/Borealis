////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Device_List.jsx

import React, { useState, useEffect, useCallback, useMemo } from "react";
import {
  Paper,
  Box,
  Typography,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TableSortLabel
} from "@mui/material";

function timeSince(tsSec) {
  if (!tsSec) return "unknown";
  const now = Date.now() / 1000;
  const s = Math.max(0, Math.floor(now - tsSec));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

function statusFromHeartbeat(tsSec, offlineAfter = 15) {
  if (!tsSec) return "Offline";
  const now = Date.now() / 1000;
  return now - tsSec <= offlineAfter ? "Online" : "Offline";
}

export default function DeviceList() {
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("status");
  const [order, setOrder] = useState("desc");

  const fetchAgents = useCallback(async () => {
    try {
      const res = await fetch("/api/agents");
      const data = await res.json();
      const arr = Object.values(data || {}).map((a) => ({
        hostname: a.hostname || a.agent_id || "unknown",
        status: statusFromHeartbeat(a.last_seen),
        lastSeen: a.last_seen || 0,
        os: a.agent_operating_system || a.os || "-"
      }));
      setRows(arr);
    } catch (e) {
      console.warn("Failed to load agents:", e);
      setRows([]);
    }
  }, []);

  useEffect(() => {
    fetchAgents();
    const t = setInterval(fetchAgents, 5000);
    return () => clearInterval(t);
  }, [fetchAgents]);

  const sorted = useMemo(() => {
    const dir = order === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      const A = a[orderBy];
      const B = b[orderBy];
      if (orderBy === "lastSeen") return (A - B) * dir;
      return String(A).localeCompare(String(B)) * dir;
    });
  }, [rows, orderBy, order]);

  const handleSort = (col) => {
    if (orderBy === col) setOrder(order === "asc" ? "desc" : "asc");
    else {
      setOrderBy(col);
      setOrder("asc");
    }
  };

  const statusColor = (s) => (s === "Online" ? "#00d18c" : "#ff4f4f");

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1 }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
          Devices
        </Typography>
        <Typography variant="body2" sx={{ color: "#aaa" }}>
          Devices connected to Borealis via Agent and their recent heartbeats.
        </Typography>
      </Box>
      <Table size="small" sx={{ minWidth: 680 }}>
        <TableHead>
          <TableRow>
            <TableCell sortDirection={orderBy === "status" ? order : false}>
              <TableSortLabel
                active={orderBy === "status"}
                direction={orderBy === "status" ? order : "asc"}
                onClick={() => handleSort("status")}
              >
                Status
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "hostname" ? order : false}>
              <TableSortLabel
                active={orderBy === "hostname"}
                direction={orderBy === "hostname" ? order : "asc"}
                onClick={() => handleSort("hostname")}
              >
                Device Hostname
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "lastSeen" ? order : false}>
              <TableSortLabel
                active={orderBy === "lastSeen"}
                direction={orderBy === "lastSeen" ? order : "asc"}
                onClick={() => handleSort("lastSeen")}
              >
                Last Heartbeat
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "os" ? order : false}>
              <TableSortLabel
                active={orderBy === "os"}
                direction={orderBy === "os" ? order : "asc"}
                onClick={() => handleSort("os")}
              >
                OS
              </TableSortLabel>
            </TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((r, i) => (
            <TableRow key={i} hover>
              <TableCell>
                <span
                  style={{
                    display: "inline-block",
                    width: 10,
                    height: 10,
                    borderRadius: 10,
                    background: statusColor(r.status),
                    marginRight: 8,
                    verticalAlign: "middle"
                  }}
                />
                {r.status}
              </TableCell>
              <TableCell>{r.hostname}</TableCell>
              <TableCell>{timeSince(r.lastSeen)}</TableCell>
              <TableCell>{r.os}</TableCell>
            </TableRow>
          ))}
          {sorted.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} sx={{ color: "#888" }}>
                No agents connected.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Paper>
  );
}
