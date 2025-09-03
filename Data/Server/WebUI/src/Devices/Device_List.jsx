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
  TableSortLabel,
  IconButton,
  Menu,
  MenuItem
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import { DeleteDeviceDialog } from "../Dialogs.jsx";

function formatLastSeen(tsSec, offlineAfter = 15) {
  if (!tsSec) return "unknown";
  const now = Date.now() / 1000;
  if (now - tsSec <= offlineAfter) return "Currently Online";
  const d = new Date(tsSec * 1000);
  const date = d.toLocaleDateString("en-US", {
    month: "2-digit",
    day: "2-digit",
    year: "numeric",
  });
  const time = d.toLocaleTimeString("en-US", {
    hour: "numeric",
    minute: "2-digit",
  });
  return `${date} @ ${time}`;
}

function statusFromHeartbeat(tsSec, offlineAfter = 15) {
  if (!tsSec) return "Offline";
  const now = Date.now() / 1000;
  return now - tsSec <= offlineAfter ? "Online" : "Offline";
}

export default function DeviceList({ onSelectDevice }) {
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("status");
  const [order, setOrder] = useState("desc");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [selected, setSelected] = useState(null);
  const [confirmOpen, setConfirmOpen] = useState(false);

  const fetchAgents = useCallback(async () => {
    try {
      const res = await fetch("/api/agents");
      const data = await res.json();
      const arr = Object.entries(data || {}).map(([id, a]) => ({
        id,
        hostname: a.hostname || id || "unknown",
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

  const openMenu = (e, row) => {
    setMenuAnchor(e.currentTarget);
    setSelected(row);
  };

  const closeMenu = () => setMenuAnchor(null);

  const confirmDelete = () => {
    closeMenu();
    setConfirmOpen(true);
  };

  const handleDelete = async () => {
    if (!selected) return;
    try {
      await fetch(`/api/agent/${selected.id}`, { method: "DELETE" });
    } catch (e) {
      console.warn("Failed to remove agent", e);
    }
    setRows((r) => r.filter((x) => x.id !== selected.id));
    setConfirmOpen(false);
    setSelected(null);
  };

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1 }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
          Devices
        </Typography>
        <Typography variant="body2" sx={{ color: "#aaa" }}>
          Devices connected to Borealis via Agent and their last check-ins.
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
                Last Seen
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
            <TableCell />
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((r, i) => (
            <TableRow key={r.id || i} hover>
              <TableCell>
                <Box sx={{ display: "flex", alignItems: "center" }}>
                  <Box
                    component="span"
                    sx={{
                      display: "inline-block",
                      width: 10,
                      height: 10,
                      borderRadius: 10,
                      bgcolor: statusColor(r.status),
                      mr: 1,
                    }}
                  />
                  {r.status}
                </Box>
              </TableCell>
              <TableCell
                onClick={() => onSelectDevice && onSelectDevice(r)}
                sx={{
                  color: "#58a6ff",
                  "&:hover": {
                    cursor: onSelectDevice ? "pointer" : "default",
                    textDecoration: onSelectDevice ? "underline" : "none",
                  },
                }}
              >
                {r.hostname}
              </TableCell>
              <TableCell>{formatLastSeen(r.lastSeen)}</TableCell>
              <TableCell>{r.os}</TableCell>
              <TableCell align="right">
                <IconButton
                  size="small"
                  onClick={(e) => {
                    e.stopPropagation();
                    openMenu(e, r);
                  }}
                  sx={{ color: "#ccc" }}
                >
                  <MoreVertIcon fontSize="small" />
                </IconButton>
              </TableCell>
            </TableRow>
          ))}
          {sorted.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} sx={{ color: "#888" }}>
                No agents connected.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={closeMenu}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        <MenuItem onClick={confirmDelete}>Delete</MenuItem>
      </Menu>
      <DeleteDeviceDialog
        open={confirmOpen}
        onCancel={() => setConfirmOpen(false)}
        onConfirm={handleDelete}
      />
    </Paper>
  );
}
