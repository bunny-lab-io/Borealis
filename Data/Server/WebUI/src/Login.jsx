import React, { useState } from "react";
import { Box, TextField, Button, Typography } from "@mui/material";

export default function Login({ onLogin }) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  const sha512 = async (text) => {
    try {
      if (window.crypto && window.crypto.subtle && window.isSecureContext) {
        const encoder = new TextEncoder();
        const data = encoder.encode(text);
        const hashBuffer = await window.crypto.subtle.digest("SHA-512", data);
        const hashArray = Array.from(new Uint8Array(hashBuffer));
        return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
      }
    } catch (_) {
      // fall through to return null
    }
    // Not a secure context or subtle crypto unavailable
    return null;
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    try {
      const hash = await sha512(password);
      const body = hash
        ? { username, password_sha512: hash }
        : { username, password };
      const resp = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify(body)
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      // Persist token via cookie as a proxy-friendly fallback
      if (data?.token) {
        try {
          // Set cookie for current host; SameSite=Lax for dev
          document.cookie = `borealis_auth=${data.token}; Path=/; SameSite=Lax`;
        } catch (_) {}
      }
      setError("");
      onLogin({ username: data.username, role: data.role });
    } catch (err) {
      setError("Invalid username or password");
    }
  };

  return (
    <Box
      sx={{
        display: "flex",
        justifyContent: "center",
        alignItems: "center",
        height: "100vh",
        backgroundColor: "#2b2b2b",
      }}
    >
      <Box
        component="form"
        onSubmit={handleSubmit}
        sx={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          width: 300,
        }}
      >
        <img
          src="/Borealis_Logo.png"
          alt="Borealis Logo"
          style={{ width: "120px", marginBottom: "16px" }}
        />
        <Typography variant="h6" sx={{ mb: 3 }}>
          Borealis - Automation Platform
        </Typography>
        <TextField
          label="Username"
          variant="outlined"
          fullWidth
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          margin="normal"
        />
        <TextField
          label="Password"
          type="password"
          variant="outlined"
          fullWidth
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          margin="normal"
        />
        {error && (
          <Typography color="error" sx={{ mt: 1 }}>
            {error}
          </Typography>
        )}
        <Button
          type="submit"
          variant="contained"
          fullWidth
          sx={{ mt: 2, bgcolor: "#58a6ff", "&:hover": { bgcolor: "#1d82d3" } }}
        >
          Login
        </Button>
      </Box>
    </Box>
  );
}
