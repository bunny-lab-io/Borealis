import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  FormControl,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import {
  ArrowBack as ArrowBackIcon,
  Devices as DevicesIcon,
  PlayArrow as PlayArrowIcon,
  Save as SaveIcon,
} from "@mui/icons-material";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../app/routes/paths.js";

const PAGE_TITLE = "Automatic Device Onboarding";
const PAGE_SUBTITLE = "Enroll Linux devices over local-network SSH with stored machine credentials.";
const DEFAULT_BRANCH = "main";
const DEFAULT_SSH_PORT = 22;
const BOREALIS_GITHUB_REPO = "bunny-lab-io/Borealis";
const GITHUB_BRANCHES_API_URL = `https://api.github.com/repos/${BOREALIS_GITHUB_REPO}/branches`;

const PANEL_SX = {
  p: 2.5,
  borderRadius: 2,
  border: "1px solid rgba(148,163,184,0.28)",
  background: "linear-gradient(135deg, rgba(10,16,31,0.96), rgba(6,10,24,0.92))",
  color: "#e2e8f0",
};

function toScopeText(entries) {
  if (Array.isArray(entries)) {
    return entries.map((entry) => String(entry || "").trim()).filter(Boolean).join("\n");
  }
  return String(entries || "");
}

function splitScopeEntries(value) {
  return String(value || "")
    .split(/[\n\r,;]+/)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

function datetimeLocalValue(epochSeconds) {
  if (!epochSeconds) return "";
  const date = new Date(Number(epochSeconds) * 1000);
  if (Number.isNaN(date.getTime())) return "";
  const offsetMs = date.getTimezoneOffset() * 60000;
  return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16);
}

function isoFromDatetimeLocal(value) {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return date.toISOString();
}

function targetOutputSnippet(target) {
  const status = String(target?.status || "").trim().toLowerCase();
  const showOutput = status === "failed" || status === "ssh_unreachable";
  if (!showOutput) return "";
  const stderr = String(target?.stderr_snippet || "").trim();
  const stdout = String(target?.stdout_snippet || "").trim();
  return [
    stderr ? `stderr\n${stderr}` : "",
    stdout ? `stdout\n${stdout}` : "",
  ].filter(Boolean).join("\n\n");
}

function normalizeBranchName(value) {
  return String(value || DEFAULT_BRANCH).trim() || DEFAULT_BRANCH;
}

export default function CreateOnboardingJob() {
  const navigate = useNavigate();
  const params = useParams();
  const jobId = params?.jobId ? Number(params.jobId) : null;
  const editing = Number.isInteger(jobId) && jobId > 0;
  const [sites, setSites] = useState([]);
  const [credentials, setCredentials] = useState([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [targetRows, setTargetRows] = useState([]);
  const [branchRows, setBranchRows] = useState([]);
  const [branchesLoading, setBranchesLoading] = useState(false);
  const [branchLoadError, setBranchLoadError] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [form, setForm] = useState({
    name: "",
    siteId: "",
    scope: "",
    credentialId: "",
    branch: DEFAULT_BRANCH,
    sshPort: DEFAULT_SSH_PORT,
    scheduleType: "immediately",
    start: "",
    enabled: true,
  });

  const selectedSite = useMemo(
    () => sites.find((site) => String(site.id) === String(form.siteId)) || null,
    [form.siteId, sites]
  );

  const sshCredentials = useMemo(
    () => credentials.filter((credential) => String(credential.connection_type || "").toLowerCase() === "ssh"),
    [credentials]
  );

  const branchOptions = useMemo(() => {
    const currentBranch = normalizeBranchName(form.branch);
    const rows = Array.isArray(branchRows) ? branchRows : [];
    if (rows.some((branch) => branch.name === currentBranch)) {
      return rows;
    }
    return [
      {
        name: currentBranch,
        sha: "",
        protected: false,
        default: currentBranch === DEFAULT_BRANCH,
      },
      ...rows,
    ];
  }, [branchRows, form.branch]);

  const setField = useCallback((key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  }, []);

  const fetchInstallBranches = useCallback(async () => {
    setBranchesLoading(true);
    setBranchLoadError("");
    try {
      const tokenRes = await fetch("/api/github/token", {
        credentials: "include",
        cache: "no-store",
      });
      const tokenData = await tokenRes.json().catch(() => ({}));
      if (!tokenRes.ok) {
        const message = tokenData?.message || tokenData?.error || `GitHub token lookup failed (HTTP ${tokenRes.status}).`;
        throw new Error(message);
      }
      const githubToken = String(tokenData?.token || "").trim();
      if (!githubToken) {
        throw new Error(tokenData?.message || "GitHub API token is unavailable.");
      }

      const nextRows = [];
      for (let page = 1; page <= 10; page += 1) {
        const branchRes = await fetch(`${GITHUB_BRANCHES_API_URL}?per_page=100&page=${page}`, {
          cache: "no-store",
          headers: {
            Accept: "application/vnd.github+json",
            Authorization: `Bearer ${githubToken}`,
          },
        });
        if (!branchRes.ok) {
          const body = await branchRes.text().catch(() => "");
          throw new Error(`GitHub branch lookup failed (HTTP ${branchRes.status})${body ? `: ${body.slice(0, 180)}` : ""}`);
        }
        const pageRows = await branchRes.json().catch(() => []);
        if (!Array.isArray(pageRows)) {
          throw new Error("GitHub branch lookup returned an unexpected payload.");
        }
        pageRows.forEach((branch) => {
          const name = String(branch?.name || "").trim();
          if (!name) return;
          nextRows.push({
            name,
            sha: String(branch?.commit?.sha || "").trim(),
            protected: Boolean(branch?.protected),
            default: name === DEFAULT_BRANCH,
          });
        });
        if (pageRows.length < 100) {
          break;
        }
      }
      nextRows.sort((a, b) => {
        if (a.name === DEFAULT_BRANCH) return -1;
        if (b.name === DEFAULT_BRANCH) return 1;
        return a.name.localeCompare(b.name);
      });
      setBranchRows(nextRows);
      if (!nextRows.length) {
        setBranchLoadError("No GitHub branches returned.");
      }
    } catch (err) {
      setBranchRows([]);
      setBranchLoadError(err instanceof Error ? err.message : "GitHub branch lookup failed.");
    } finally {
      setBranchesLoading(false);
    }
  }, []);

  const loadData = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [sitesResp, credentialsResp, jobResp] = await Promise.all([
        fetch("/api/sites", { credentials: "include" }),
        fetch("/api/credentials", { credentials: "include" }),
        editing ? fetch(`/api/scheduled_jobs/${jobId}`, { credentials: "include" }) : Promise.resolve(null),
      ]);
      const sitesData = await sitesResp.json().catch(() => ({}));
      if (!sitesResp.ok) throw new Error(sitesData?.error || `Unable to load sites (${sitesResp.status})`);
      const credentialsData = await credentialsResp.json().catch(() => ({}));
      if (!credentialsResp.ok) throw new Error(credentialsData?.message || credentialsData?.error || `Unable to load credentials (${credentialsResp.status})`);
      setSites(Array.isArray(sitesData?.sites) ? sitesData.sites : []);
      setCredentials(Array.isArray(credentialsData?.credentials) ? credentialsData.credentials : []);

      if (editing && jobResp) {
        const jobData = await jobResp.json().catch(() => ({}));
        if (!jobResp.ok) throw new Error(jobData?.error || `Unable to load onboarding job (${jobResp.status})`);
        const job = jobData?.job || {};
        const firstTarget = Array.isArray(job.targets) ? job.targets.find((target) => target?.kind === "onboarding_scope") : null;
        const firstComponent = Array.isArray(job.components) ? job.components[0] || {} : {};
        setForm({
          name: job.name || "",
          siteId: firstTarget?.site_id ? String(firstTarget.site_id) : "",
          scope: toScopeText(firstTarget?.entries || []),
          credentialId: job.credential_id ? String(job.credential_id) : "",
          branch: firstComponent.install_branch || firstComponent.repo_branch || firstComponent.branch || DEFAULT_BRANCH,
          sshPort: Number(firstComponent.ssh_port || firstComponent.port || DEFAULT_SSH_PORT),
          scheduleType: job.schedule_type || "immediately",
          start: datetimeLocalValue(job.start_ts),
          enabled: Boolean(job.enabled),
        });
        setNameTouched(Boolean(job.name));
      }
    } catch (err) {
      setError(err?.message || "Unable to load onboarding form.");
    } finally {
      setLoading(false);
    }
  }, [editing, jobId]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  useEffect(() => {
    fetchInstallBranches();
  }, [fetchInstallBranches]);

  const loadTargets = useCallback(async () => {
    if (!editing || !jobId) return;
    try {
      const resp = await fetch(`/api/onboarding/jobs/${jobId}/targets`, { credentials: "include" });
      const data = await resp.json().catch(() => ({}));
      if (resp.ok) {
        setTargetRows(Array.isArray(data?.targets) ? data.targets : []);
      }
    } catch {
      /* status refresh is best effort */
    }
  }, [editing, jobId]);

  useEffect(() => {
    if (!editing) return undefined;
    loadTargets();
    const timer = setInterval(loadTargets, 5000);
    return () => clearInterval(timer);
  }, [editing, loadTargets]);

  useEffect(() => {
    if (nameTouched || !selectedSite || editing) return;
    setForm((prev) => ({
      ...prev,
      name: `Automatic Device Onboarding ${selectedSite.name || `Site ${selectedSite.id}`}`,
    }));
  }, [editing, nameTouched, selectedSite]);

  const submit = useCallback(async () => {
    setSaving(true);
    setError("");
    setNotice("");
    try {
      const entries = splitScopeEntries(form.scope);
      if (!form.siteId) throw new Error("Select site.");
      if (!entries.length) throw new Error("Enter at least one IP address, CIDR, range, or FQDN.");
      if (!form.credentialId) throw new Error("Select SSH credential.");
      const port = Number(form.sshPort || DEFAULT_SSH_PORT);
      if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error("SSH port must be 1-65535.");
      const siteName = selectedSite?.name || "";
      const payload = {
        job_kind: "onboarding",
        name: form.name || `Automatic Device Onboarding ${siteName || form.siteId}`,
        components: [
          {
            kind: "device_onboarding",
            name: "Linux SSH Device Onboarding",
            install_branch: form.branch || DEFAULT_BRANCH,
            ssh_port: port,
          },
        ],
        targets: [
          {
            kind: "onboarding_scope",
            site_id: Number(form.siteId),
            site_name: siteName,
            entries,
          },
        ],
        schedule: {
          type: form.scheduleType || "immediately",
          start: form.scheduleType === "immediately" ? null : isoFromDatetimeLocal(form.start),
        },
        duration: { expiration: "no_expire" },
        execution_context: "onboarding_linux_ssh",
        credential_id: Number(form.credentialId),
        use_service_account: false,
        enabled: Boolean(form.enabled),
      };
      const resp = await fetch(editing ? `/api/scheduled_jobs/${jobId}` : "/api/scheduled_jobs", {
        method: editing ? "PUT" : "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const body = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(body?.message || body?.error || `Save failed (${resp.status})`);
      }
      const savedId = body?.job?.id || jobId;
      setNotice("Onboarding job saved.");
      if (savedId && !editing) {
        navigate(APP_PATHS.jobOnboarding(savedId), { replace: true });
      }
    } catch (err) {
      setError(err?.message || "Unable to save onboarding job.");
    } finally {
      setSaving(false);
    }
  }, [editing, form, jobId, navigate, selectedSite]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "onboarding-back",
        label: "Back",
        icon: <ArrowBackIcon />,
        tone: "secondary",
        onClick: () => navigate(APP_PATHS.jobs),
      },
      {
        id: "onboarding-save",
        label: editing ? "Save" : "Create",
        icon: editing ? <SaveIcon /> : <PlayArrowIcon />,
        tone: "primary",
        loading: saving,
        onClick: submit,
      },
    ],
    [editing, navigate, saving, submit]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: DevicesIcon,
    actions: pageHeaderActions,
  });

  if (loading) {
    return (
      <Paper elevation={0} sx={{ p: 3, background: "transparent", color: "#e2e8f0" }}>
        <CircularProgress size={24} sx={{ color: "#7dd3fc" }} />
      </Paper>
    );
  }

  return (
    <Paper elevation={0} sx={{ m: 0, p: 0, background: "transparent", color: "#e2e8f0" }}>
      <PageBodyFrame
        variant="content_panel"
        fillHeight={false}
      >
          <Stack spacing={2}>
            {error ? <Alert severity="error">{error}</Alert> : null}
            {notice ? <Alert severity="success">{notice}</Alert> : null}
            <Box sx={PANEL_SX}>
              <Stack spacing={2.25}>
                <TextField
                  label="Job Name"
                  value={form.name}
                  onChange={(event) => {
                    setNameTouched(true);
                    setField("name", event.target.value);
                  }}
                  fullWidth
                />
                <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
                  <FormControl fullWidth>
                    <InputLabel id="onboarding-site-label">Site</InputLabel>
                    <Select
                      labelId="onboarding-site-label"
                      label="Site"
                      value={form.siteId}
                      onChange={(event) => setField("siteId", event.target.value)}
                    >
                      {sites.map((site) => (
                        <MenuItem key={site.id} value={String(site.id)}>
                          {site.name || `Site ${site.id}`}
                        </MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                  <FormControl fullWidth>
                    <InputLabel id="onboarding-credential-label">SSH Credential</InputLabel>
                    <Select
                      labelId="onboarding-credential-label"
                      label="SSH Credential"
                      value={form.credentialId}
                      onChange={(event) => setField("credentialId", event.target.value)}
                    >
                      {sshCredentials.map((credential) => (
                        <MenuItem
                          key={credential.id}
                          value={String(credential.id)}
                          disabled={Boolean(credential.secret_reset_required)}
                        >
                          {credential.name || `Credential ${credential.id}`}
                          {credential.secret_reset_required ? " (Secret Re-Entry Required)" : ""}
                        </MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                </Stack>
                <TextField
                  label="Discovery Scope"
                  value={form.scope}
                  onChange={(event) => setField("scope", event.target.value)}
                  multiline
                  minRows={7}
                  placeholder={"192.168.1.10\n192.168.1.20-192.168.1.30\n192.168.2.0/24\nserver01.local"}
                  fullWidth
                />
                <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
                  <FormControl fullWidth error={Boolean(branchLoadError)}>
                    <InputLabel id="onboarding-install-branch-label">Install Branch</InputLabel>
                    <Select
                      labelId="onboarding-install-branch-label"
                      label="Install Branch"
                      value={normalizeBranchName(form.branch)}
                      onChange={(event) => setField("branch", event.target.value)}
                      onOpen={() => {
                        if (!branchRows.length && !branchesLoading) {
                          void fetchInstallBranches();
                        }
                      }}
                    >
                      {branchOptions.map((branch) => (
                        <MenuItem key={branch.name} value={branch.name}>
                          {branch.name}
                          {branch.default ? " (default)" : ""}
                          {branch.sha ? ` - ${branch.sha.slice(0, 12)}` : ""}
                        </MenuItem>
                      ))}
                      {branchesLoading ? (
                        <MenuItem disabled value="__loading">
                          Loading branches...
                        </MenuItem>
                      ) : null}
                      {branchLoadError ? (
                        <MenuItem disabled value="__error">
                          Branch lookup failed
                        </MenuItem>
                      ) : null}
                    </Select>
                    {branchLoadError ? (
                      <Typography variant="caption" sx={{ mt: 0.75, color: "#fca5a5" }}>
                        {branchLoadError}
                      </Typography>
                    ) : null}
                  </FormControl>
                  <TextField
                    label="SSH Port"
                    type="number"
                    value={form.sshPort}
                    onChange={(event) => setField("sshPort", event.target.value)}
                    sx={{ minWidth: 180 }}
                  />
                </Stack>
                <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
                  <FormControl fullWidth>
                    <InputLabel id="onboarding-schedule-label">Schedule</InputLabel>
                    <Select
                      labelId="onboarding-schedule-label"
                      label="Schedule"
                      value={form.scheduleType}
                      onChange={(event) => setField("scheduleType", event.target.value)}
                    >
                      <MenuItem value="immediately">Immediately</MenuItem>
                      <MenuItem value="once">Once</MenuItem>
                      <MenuItem value="daily">Daily</MenuItem>
                      <MenuItem value="weekly">Weekly</MenuItem>
                      <MenuItem value="monthly">Monthly</MenuItem>
                    </Select>
                  </FormControl>
                  <TextField
                    label="Start"
                    type="datetime-local"
                    value={form.start}
                    onChange={(event) => setField("start", event.target.value)}
                    disabled={form.scheduleType === "immediately"}
                    fullWidth
                    InputLabelProps={{ shrink: true }}
                  />
                </Stack>
                <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                  Remote installer creates normal pending device approvals after SSH deploy succeeds.
                </Typography>
              </Stack>
            </Box>
            {saving ? (
              <Box sx={{ display: "flex", alignItems: "center", gap: 1, color: "#94a3b8" }}>
                <CircularProgress size={18} sx={{ color: "#7dd3fc" }} />
                <Typography variant="body2">Saving onboarding job...</Typography>
              </Box>
            ) : null}
            {editing ? (
              <Box sx={PANEL_SX}>
                <Stack spacing={1.25}>
                  <Typography variant="h6" sx={{ fontSize: "1rem", fontWeight: 700 }}>
                    Target Status
                  </Typography>
                  <Box
                    sx={{
                      display: "grid",
                      gridTemplateColumns: { xs: "1fr", md: "1.2fr 0.8fr 1.5fr 1fr" },
                      gap: 1,
                      color: "#94a3b8",
                      fontSize: 12,
                      fontWeight: 700,
                      textTransform: "uppercase",
                    }}
                  >
                    <Box>Target</Box>
                    <Box>Status</Box>
                    <Box>Detail</Box>
                    <Box>Approval</Box>
                  </Box>
                  {targetRows.length ? targetRows.map((target) => {
                    const outputSnippet = targetOutputSnippet(target);
                    return (
                    <Box
                      key={target.id || `${target.target_address}:${target.ssh_port}`}
                      sx={{
                        py: 1,
                        borderTop: "1px solid rgba(148,163,184,0.18)",
                      }}
                    >
                      <Box
                        sx={{
                          display: "grid",
                          gridTemplateColumns: { xs: "1fr", md: "1.2fr 0.8fr 1.5fr 1fr" },
                          gap: 1,
                          alignItems: "center",
                        }}
                      >
                        <Typography variant="body2" sx={{ color: "#e2e8f0" }}>
                          {target.target_hostname || target.target_address || target.target_input || "Target"}
                          {target.ssh_port ? `:${target.ssh_port}` : ""}
                        </Typography>
                        <Typography variant="body2" sx={{ color: "#7dd3fc", fontWeight: 700 }}>
                          {target.status || "pending"}
                        </Typography>
                        <Typography variant="body2" sx={{ color: "#cbd5e1" }}>
                          {target.detail || "—"}
                        </Typography>
                        <Typography variant="body2" sx={{ color: "#cbd5e1" }}>
                          {target.approval_reference || "—"}
                        </Typography>
                      </Box>
                      {outputSnippet ? (
                        <Box
                          component="pre"
                          sx={{
                            mt: 1,
                            mb: 0,
                            maxHeight: 220,
                            overflow: "auto",
                            whiteSpace: "pre-wrap",
                            wordBreak: "break-word",
                            borderRadius: 1,
                            border: "1px solid rgba(148,163,184,0.18)",
                            background: "rgba(2,6,23,0.58)",
                            color: "#cbd5e1",
                            fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
                            fontSize: 12,
                            lineHeight: 1.5,
                            p: 1.25,
                          }}
                        >
                          {outputSnippet}
                        </Box>
                      ) : null}
                    </Box>
                    );
                  }) : (
                    <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                      No target attempts recorded yet.
                    </Typography>
                  )}
                </Stack>
              </Box>
            ) : null}
          </Stack>
      </PageBodyFrame>
    </Paper>
  );
}
