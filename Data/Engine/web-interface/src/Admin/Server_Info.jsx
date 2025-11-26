import React, { useEffect, useState } from "react";
import { Paper, Box, Typography, Button } from "@mui/material";
import { GitHub as GitHubIcon, InfoOutlined as InfoIcon } from "@mui/icons-material";
import { CreditsDialog } from "../Dialogs.jsx";

const gradientButtonSx = {
  backgroundImage: "linear-gradient(135deg,#7dd3fc,#c084fc)",
  color: "#0b1220",
  borderRadius: 999,
  textTransform: "none",
  boxShadow: "0 10px 26px rgba(124,58,237,0.28)",
  "&:hover": {
    backgroundImage: "linear-gradient(135deg,#86e1ff,#d1a6ff)",
    boxShadow: "0 12px 34px rgba(124,58,237,0.38)",
    filter: "none",
  },
};

export default function ServerInfo({ isAdmin = false, onPageMetaChange }) {
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

  useEffect(() => {
    onPageMetaChange?.({
      page_title: "Server Info",
      page_subtitle: "Basic server information and project links for debugging and support.",
      page_icon: InfoIcon,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange]);

  if (!isAdmin) return null;

  return (
    <Paper sx={{ m: 0, p: 0, bgcolor: "transparent", border: "none", boxShadow: "none" }} elevation={0}>
      <Box sx={{ p: 2 }}>
        <Typography sx={{ color: '#aaa', mb: 1 }}>
          Basic server information for debug and support. Server time updates automatically every minute.
        </Typography>
        <Box sx={{ display: 'flex', gap: 2, alignItems: 'baseline' }}>
          <Typography sx={{ color: '#ccc', fontWeight: 600, minWidth: 120 }}>Server Time</Typography>
          <Typography sx={{ color: error ? '#ff6b6b' : '#ddd', fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace' }}>
            {error ? `Error: ${error}` : (serverTime || 'Loading...')}
          </Typography>
        </Box>

        <Box sx={{ mt: 3 }}>
          <Typography variant="subtitle1" sx={{ color: "#e2e8f0", mb: 1, fontWeight: 600 }}>Project Links</Typography>
          <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
            <Button
              variant="outlined"
              startIcon={<GitHubIcon />}
              onClick={() => window.open("https://github.com/bunny-lab-io/Borealis", "_blank")}
              sx={{
                borderColor: "rgba(148,163,184,0.35)",
                color: "#e2e8f0",
                textTransform: "none",
                borderRadius: 999,
                px: 1.7,
                minWidth: 86,
                "&:hover": { borderColor: "rgba(148,163,184,0.55)" },
              }}
            >
              GitHub Project
            </Button>
            <Button
              variant="contained"
              startIcon={<InfoIcon />}
              onClick={() => setAboutOpen(true)}
              sx={gradientButtonSx}
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
