////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Device_Details.jsx

import React, { useState, useEffect } from "react";
import {
  Paper,
  Box,
  Tabs,
  Tab,
  Typography,
  Table,
  TableHead,
  TableRow,
  TableCell,
  TableBody,
  Button
} from "@mui/material";

export default function DeviceDetails({ device, onBack }) {
  const [tab, setTab] = useState(0);
  const [agent, setAgent] = useState(device || {});

  useEffect(() => {
    if (!device || !device.id) return;
    const fetchAgent = async () => {
      try {
        const res = await fetch("/api/agents");
        const data = await res.json();
        if (data && data[device.id]) {
          setAgent({ id: device.id, ...data[device.id] });
        }
      } catch (e) {
        console.warn("Failed to load agent", e);
      }
    };
    fetchAgent();
  }, [device]);

  const summaryItems = [
    { label: "Device Name", value: agent.hostname || device?.hostname || "unknown" },
    { label: "Description", value: "unknown" },
    { label: "Operating System", value: agent.agent_operating_system || "unknown" },
    { label: "Last User", value: "unknown" },
    { label: "Internal IP", value: agent.internal_ip || "unknown" },
    { label: "External IP", value: "unknown" },
    { label: "Last Reboot", value: agent.last_reboot || "unknown" },
    { label: "Created", value: agent.created || "unknown" }
  ];

  const renderSummary = () => (
    <Table size="small">
      <TableBody>
        {summaryItems.map((item) => (
          <TableRow key={item.label}>
            <TableCell sx={{ fontWeight: 500 }}>{item.label}</TableCell>
            <TableCell>{item.value}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );

  const placeholderTable = (headers) => (
    <Table size="small">
      <TableHead>
        <TableRow>
          {headers.map((h) => (
            <TableCell key={h}>{h}</TableCell>
          ))}
        </TableRow>
      </TableHead>
      <TableBody>
        <TableRow>
          <TableCell colSpan={headers.length} sx={{ color: "#888" }}>
            No data available.
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  );

  const tabs = [
    { label: "Summary", content: renderSummary() },
    {
      label: "Monitors",
      content: placeholderTable([
        "Type",
        "Description",
        "Latest Value",
        "Policy",
        "Latest 10 Days of Alerts",
        "Enabled/Disabled Status"
      ])
    },
    {
      label: "Software",
      content: placeholderTable(["Software Name", "Version", "Action"])
    },
    {
      label: "Memory",
      content: placeholderTable(["Slot", "Speed", "Serial Number", "Capacity"])
    },
    {
      label: "Storage",
      content: placeholderTable([
        "Drive Letter",
        "Disk Type",
        "Usage",
        "Total Size",
        "Free %"
      ])
    },
    {
      label: "Network",
      content: placeholderTable(["Adapter", "IP Address", "MAC Address"])
    }
  ];

  return (
    <Paper sx={{ m: 2, p: 2, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ mb: 2, display: "flex", alignItems: "center" }}>
        {onBack && (
          <Button variant="outlined" size="small" onClick={onBack} sx={{ mr: 2 }}>
            Back
          </Button>
        )}
        <Typography variant="h6" sx={{ color: "#58a6ff" }}>
          {agent.hostname || "Device Details"}
        </Typography>
      </Box>
      <Tabs
        value={tab}
        onChange={(e, v) => setTab(v)}
        sx={{ borderBottom: 1, borderColor: "#333" }}
      >
        {tabs.map((t) => (
          <Tab key={t.label} label={t.label} />
        ))}
      </Tabs>
      <Box sx={{ mt: 2 }}>{tabs[tab].content}</Box>
    </Paper>
  );
}

