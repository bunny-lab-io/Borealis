import React, { useEffect, useMemo, useState } from "react";
import { Paper, Box } from "@mui/material";
import { GitHub as GitHubIcon, InfoOutlined as InfoIcon } from "@mui/icons-material";
import { CreditsDialog } from "../Dialogs.jsx";

export default function ServerInfo({ isAdmin = false, onPageMetaChange }) {
  const [aboutOpen, setAboutOpen] = useState(false);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "server-github",
        label: "Aurora Repository",
        icon: <GitHubIcon />,
        tone: "secondary",
        onClick: () => window.open("https://github.com/bunny-lab-io/Aurora", "_blank"),
      },
      {
        id: "server-github",
        label: "GitHub Project",
        icon: <GitHubIcon />,
        tone: "secondary",
        onClick: () => window.open("https://github.com/bunny-lab-io/Borealis", "_blank"),
      },
      {
        id: "server-about",
        label: "About Borealis",
        icon: <InfoIcon />,
        tone: "primary",
        onClick: () => setAboutOpen(true),
      },
    ],
    []
  );

  useEffect(() => {
    onPageMetaChange?.({
      page_title: "Server Info",
      page_subtitle: "Basic server information and project links for debugging and support.",
      page_icon: InfoIcon,
      page_header_actions: pageHeaderActions,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions]);

  if (!isAdmin) return null;

  return (
    <Paper sx={{ m: 0, p: 0, bgcolor: "transparent", border: "none", boxShadow: "none" }} elevation={0}>
      <Box
        sx={{
          px: { xs: 2, md: 3 },
          pb: 3,
          display: "flex",
          flexDirection: "column",
          minHeight: "calc(100vh - 120px)",
        }}
      />
      <CreditsDialog open={aboutOpen} onClose={() => setAboutOpen(false)} />
    </Paper>
  );
}
