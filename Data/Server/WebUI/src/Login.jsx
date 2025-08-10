import React, { useState, useEffect } from "react";
import { Box, TextField, Button, Typography } from "@mui/material";

export default function Login({ onLogin }) {
  const [users, setUsers] = useState([]);
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    fetch("/api/users")
      .then((res) => res.json())
      .then((data) => setUsers(data.users || []))
      .catch(() => setUsers([]));
  }, []);

  const sha512 = async (text) => {
    const encoder = new TextEncoder();
    const data = encoder.encode(text);
    const hashBuffer = await crypto.subtle.digest("SHA-512", data);
    const hashArray = Array.from(new Uint8Array(hashBuffer));
    return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    const user = users.find((u) => u.username === username);
    if (!user) {
      setError("Invalid username or password");
      return;
    }
    const hash = await sha512(password);
    if (hash.toLowerCase() === (user.password || "").toLowerCase()) {
      setError("");
      onLogin(username);
    } else {
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

