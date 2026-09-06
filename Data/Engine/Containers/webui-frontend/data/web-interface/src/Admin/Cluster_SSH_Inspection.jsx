import React, { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, MenuItem, Stack, TextField, Typography } from "@mui/material";
import { DIALOG_ACTIONS_SX, DIALOG_BUTTON_SX, DIALOG_CONTENT_SX, DIALOG_INPUT_SX, DIALOG_PAPER_SX, DIALOG_PRIMARY_BUTTON_SX, DIALOG_TITLE_SX } from "../DialogStyles.jsx";
import { FIELD_CLASS, validateInputValue } from "../app/utils/inputValidation.js";

const emptySecrets = () => ({ password: "", private_key: "", passphrase: "" });
const byteLength = (value) => new TextEncoder().encode(value).length;

export function validateSSHInspectionTarget(address, port) {
  if (validateInputValue("address", address, FIELD_CLASS.HOST)) return "Enter a private IPv4 address.";
  const octets = address.split(".");
  if (octets.length !== 4 || octets.some((part) => !/^(0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255)) return "Enter a private IPv4 address.";
  const [first, second] = octets.map(Number);
  if (!(first === 10 || (first === 172 && second >= 16 && second <= 31) || (first === 192 && second === 168))) return "Enter a private IPv4 address.";
  if (!/^\d{1,5}$/.test(String(port)) || Number(port) < 1 || Number(port) > 65535) return "SSH port must be between 1 and 65535.";
  return "";
}

export function validateSSHInspectionCredential(username, method, secrets) {
  if (!/^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/.test(username)) return "Enter a valid Linux username, up to 64 characters.";
  if (!["password", "private_key"].includes(method)) return "Choose password or private key authentication.";
  for (const field of method === "password" ? ["password"] : ["private_key", "passphrase"]) {
    const value = secrets[field];
    if (typeof value !== "string" || (field !== "passphrase" && !value) || byteLength(value) > (field === "private_key" ? 65536 : 4096) || validateInputValue(field, value, FIELD_CLASS.SECRET)) return `Enter a valid ${field.replaceAll("_", " ")} within its size limit.`;
  }
  if (method === "private_key" && !/^-----BEGIN (OPENSSH |RSA |EC |ENCRYPTED )?PRIVATE KEY-----\r?\n/.test(secrets.private_key)) return "Paste a supported SSH private key. The Engine verifies its encoding and passphrase.";
  return "";
}

function validObservedKey(payload, address, port) {
  return payload?.address === address && payload?.port === Number(port)
    && typeof payload?.host_key_algorithm === "string" && /^[A-Za-z0-9@._+-]{1,64}$/.test(payload.host_key_algorithm)
    && typeof payload?.host_key_fingerprint === "string" && /^SHA256:[A-Za-z0-9+/]{43}$/.test(payload.host_key_fingerprint)
    && typeof payload?.host_key_base64 === "string" && payload.host_key_base64.length <= 5500 && /^[A-Za-z0-9+/]+={0,2}$/.test(payload.host_key_base64);
}

// Mounted only while open. No credentials enter storage, URLs, notifications or
// parent page state; unmount aborts the request and drops the complete form.
export default function ClusterSSHInspection({ onClose }) {
  const [address, setAddress] = useState("");
  const [port, setPort] = useState("22");
  const [key, setKey] = useState(null);
  const [approved, setApproved] = useState(false);
  const [username, setUsername] = useState("");
  const [method, setMethod] = useState("password");
  const [secrets, setSecrets] = useState(emptySecrets);
  const [facts, setFacts] = useState(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef(null);
  useEffect(() => () => pending.current?.abort(), []);

  const invalidateTarget = () => {
    pending.current?.abort();
    setKey(null);
    setApproved(false);
    setSecrets(emptySecrets());
    setFacts(null);
    setError("");
  };
  const close = () => {
    pending.current?.abort();
    setSecrets(emptySecrets());
    onClose();
  };
  const request = async (inspect) => {
    if (pending.current) return;
    const validation = validateSSHInspectionTarget(address, port)
      || (inspect && (!key || !approved) ? "Approve the host key before authenticating." : "")
      || (inspect ? validateSSHInspectionCredential(username, method, secrets) : "");
    if (validation) { setError(validation); return; }
    const controller = new AbortController();
    pending.current = controller;
    const timeout = setTimeout(() => controller.abort(), 35000);
    const body = { address, port: Number(port) };
    if (inspect) Object.assign(body, {
      host_key_algorithm: key.host_key_algorithm,
      host_key_fingerprint: key.host_key_fingerprint,
      host_key_base64: key.host_key_base64,
      host_key_approved: true,
      username, auth_method: method,
      ...(method === "password" ? { password: secrets.password } : { private_key: secrets.private_key, passphrase: secrets.passphrase }),
    });
    setBusy(true);
    setError("");
    setFacts(null);
    if (!inspect) { setKey(null); setApproved(false); }
    try {
      const encoded = JSON.stringify(body);
      setSecrets(emptySecrets());
      const response = await fetch(`/api/server/cluster/onboarding/${inspect ? "inspect" : "host-key"}`, {
        method: "POST", credentials: "include", redirect: "error", cache: "no-store",
        headers: { "Content-Type": "application/json" }, body: encoded, signal: controller.signal,
      });
      const payload = await response.json();
      if (controller.signal.aborted) return;
      if (!response.ok) {
        if (payload?.error === "ssh_host_key_changed") {
          setKey(null); setApproved(false);
          setError("SSH host key changed. Verify the target and discover its key again before sending credentials.");
        } else if (response.status === 401 || response.status === 403) setError("An active Admin session is required.");
        else if (response.status === 503) setError("SSH inspection is busy. Try again shortly.");
        else setError("SSH check failed. Verify the target, approved key and Linux credentials, then retry.");
        return;
      }
      if (!inspect) {
        if (!validObservedKey(payload, address, port)) throw new Error("invalid host key response");
        setKey(payload);
      } else {
        if (payload?.address !== address || payload?.port !== Number(port) || payload?.host_key_fingerprint !== key.host_key_fingerprint
          || ![payload?.hostname, payload?.kernel, payload?.architecture].every((value) => typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9_.-]{0,252}$/.test(value))
          || !Number.isInteger(payload?.uid) || payload.uid < 0 || payload.uid > 4294967295) throw new Error("invalid inspection response");
        setFacts(payload);
      }
    } catch {
      if (!controller.signal.aborted) setError("SSH check failed. Verify connectivity and retry.");
    } finally {
      clearTimeout(timeout);
      if (pending.current === controller) {
        pending.current = null;
        setBusy(false);
        if (controller.signal.aborted) setError("SSH check stopped or timed out.");
      }
      delete body.password; delete body.private_key; delete body.passphrase;
    }
  };

  return <Dialog open onClose={close} maxWidth="sm" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
    <DialogTitle sx={DIALOG_TITLE_SX}>Connect joining host</DialogTitle>
    <DialogContent sx={DIALOG_CONTENT_SX}>
      <Stack spacing={2} sx={{ pt: 1.25 }}>
        <Typography>Check SSH access from this Engine before preparing a joining host.</Typography>
        {error ? <Alert severity="error">{error}</Alert> : null}
        <TextField sx={DIALOG_INPUT_SX} label="Private IPv4 address" value={address} disabled={busy} inputProps={{ maxLength: 15 }} onChange={(event) => { invalidateTarget(); setAddress(event.target.value); }} />
        <TextField sx={DIALOG_INPUT_SX} label="SSH port" value={port} disabled={busy} inputProps={{ maxLength: 5, inputMode: "numeric" }} onChange={(event) => { invalidateTarget(); setPort(event.target.value); }} />
        {key ? <>
          <Typography component="div" sx={{ overflowWrap: "anywhere" }}>SSH host key: {key.host_key_algorithm}<br />{key.host_key_fingerprint}</Typography>
          <FormControlLabel control={<Checkbox checked={approved} disabled={busy} onChange={(event) => { setApproved(event.target.checked); setSecrets(emptySecrets()); setFacts(null); }} />} label="I verified and approve this host key" />
        </> : null}
        {approved ? <>
          <TextField sx={DIALOG_INPUT_SX} label="Linux admin username" autoComplete="off" value={username} disabled={busy} inputProps={{ maxLength: 64 }} onChange={(event) => { setUsername(event.target.value); setFacts(null); }} />
          <TextField sx={DIALOG_INPUT_SX} select label="SSH authentication" value={method} disabled={busy} onChange={(event) => { setMethod(event.target.value); setSecrets(emptySecrets()); setFacts(null); }}>
            <MenuItem value="password">Password</MenuItem><MenuItem value="private_key">Private key</MenuItem>
          </TextField>
          {method === "password" ? <TextField sx={DIALOG_INPUT_SX} type="password" label="Linux password" autoComplete="new-password" value={secrets.password} disabled={busy} inputProps={{ maxLength: 4096 }} onChange={(event) => setSecrets({ ...secrets, password: event.target.value })} /> : <>
            <TextField sx={DIALOG_INPUT_SX} multiline minRows={4} label="SSH private key" autoComplete="off" value={secrets.private_key} disabled={busy} inputProps={{ maxLength: 65536, spellCheck: false }} onChange={(event) => setSecrets({ ...secrets, private_key: event.target.value })} />
            <TextField sx={DIALOG_INPUT_SX} type="password" label="Key passphrase (if encrypted)" autoComplete="new-password" value={secrets.passphrase} disabled={busy} inputProps={{ maxLength: 4096 }} onChange={(event) => setSecrets({ ...secrets, passphrase: event.target.value })} />
          </>}
        </> : null}
        {facts ? <Alert severity="success">SSH connected to {facts.hostname} ({facts.kernel}, {facts.architecture}), user ID {facts.uid}. Connection check complete; host has not been joined.</Alert> : null}
      </Stack>
    </DialogContent>
    <DialogActions sx={DIALOG_ACTIONS_SX}>
      <Button sx={DIALOG_BUTTON_SX} onClick={close}>{busy ? "Cancel check" : "Close"}</Button>
      {!key ? <Button sx={DIALOG_PRIMARY_BUTTON_SX} disabled={busy} onClick={() => void request(false)}>Discover host key</Button> : <Button sx={DIALOG_PRIMARY_BUTTON_SX} disabled={busy || !approved} onClick={() => void request(true)}>Check SSH access</Button>}
    </DialogActions>
  </Dialog>;
}
