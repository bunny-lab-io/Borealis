import React, { useEffect, useState } from "react";
import { Paper, Box, Typography, Button } from "@mui/material";
import { GitHub as GitHubIcon, InfoOutlined as InfoIcon } from "@mui/icons-material";
import { CreditsDialog } from "../Dialogs.jsx";

export default function ServerInfo({ isAdmin = false }) {
  const [serverTime, setServerTime] = useState(null);
  const [error, setError] = useState(null);
  const [aboutOpen, setAboutOpen] = useState(false);

  useEffect(() => {
    if (!isAdmin) return;
    let isMounted = true;
    const fetchTime = async () => {
      try {
        const resp = await fetch('/api/server/time');
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        if (isMounted) {
          setServerTime(data?.display || data?.iso || null);
          setError(null);
        }
      } catch (e) {
        if (isMounted) setError(String(e));
      }
    };
    fetchTime();
    const id = setInterval(fetchTime, 60000); // update once per minute
    return () => { isMounted = false; clearInterval(id); };
  }, [isAdmin]);

  if (!isAdmin) return null;

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "transparent" }} elevation={2}>
      <Box sx={{ p: 2 }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 1 }}>Server Info</Typography>
        <Typography sx={{ color: '#aaa', mb: 1 }}>Basic server information will appear here for informative and debug purposes.</Typography>
        <Box sx={{ display: 'flex', gap: 2, alignItems: 'baseline' }}>
          <Typography sx={{ color: '#ccc', fontWeight: 600, minWidth: 120 }}>Server Time</Typography>
          <Typography sx={{ color: error ? '#ff6b6b' : '#ddd', fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace' }}>
            {error ? `Error: ${error}` : (serverTime || 'Loading...')}
          </Typography>
        </Box>

        <Box sx={{ mt: 3 }}>
          <Typography variant="subtitle1" sx={{ color: "#58a6ff", mb: 1 }}>Project Links</Typography>
          <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
            <Button
              variant="outlined"
              color="primary"
              startIcon={<GitHubIcon />}
              onClick={() => window.open("https://github.com/bunny-lab-io/Borealis", "_blank")}
              sx={{ borderColor: '#3a3a3a', color: '#7db7ff' }}
            >
              GitHub Project
            </Button>
            <Button
              variant="outlined"
              color="inherit"
              startIcon={<InfoIcon />}
              onClick={() => setAboutOpen(true)}
              sx={{ borderColor: '#3a3a3a', color: '#ddd' }}
            >
              About Borealis
            </Button>
          </Box>
        </Box>
      </Box>
      <CreditsDialog open={aboutOpen} onClose={() => setAboutOpen(false)} />
    </Paper>
  );
}
