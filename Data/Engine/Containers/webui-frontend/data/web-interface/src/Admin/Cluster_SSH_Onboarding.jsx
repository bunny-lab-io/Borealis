import React, { useEffect, useRef, useState } from "react";
import { Alert, Button, Dialog, DialogActions, DialogContent, DialogTitle, Stack, Typography } from "@mui/material";
import ClusterSSHInspection, { validateSSHInspectionTarget } from "./Cluster_SSH_Inspection.jsx";
import { createSSHInspectionSubmission } from "./clusterSSHOnboarding.js";
import { DIALOG_ACTIONS_SX, DIALOG_BUTTON_SX, DIALOG_CONTENT_SX, DIALOG_DANGER_BUTTON_SX, DIALOG_PAPER_SX, DIALOG_TITLE_SX } from "../DialogStyles.jsx";

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const phases = new Set(["preflight", "inspect_ssh_targets", "qualify_ssh_targets"]);
const terminalStates = new Set(["failed", "cancelled", "succeeded"]);
const statusUnavailable = "Inspection status is unavailable. Retained results may be stale.";
export const validSSHInspectionID = (value) => typeof value === "string" && uuid.test(value);
const clearCredentials = (targets) => targets.forEach((target) => {
  for (const field of ["password", "private_key", "passphrase", "sudo_password"]) delete target[field];
});

export function validSSHInspectionProgress(value, id) {
  if (!value || value.operation_id !== id || !validSSHInspectionID(id) || !["queued", "running", "waiting", "failed", "cancelled", "succeeded"].includes(value.state)
    || typeof value.current_step !== "string" || !/^[a-z_]{1,64}$/.test(value.current_step) || !Number.isSafeInteger(value.attempt) || value.attempt < 1
    || !Array.isArray(value.targets) || value.targets.length < 1 || value.targets.length > 2) return false;
  const ids = new Set();
  const addresses = new Set();
  for (const target of value.targets) {
    if (!target || !validSSHInspectionID(target.id) || ids.has(target.id) || typeof target.address !== "string" || addresses.has(target.address)
      || !Number.isInteger(target.port) || validateSSHInspectionTarget(target.address, target.port)
      || typeof target.host_key_fingerprint !== "string" || !/^SHA256:[A-Za-z0-9+/]{43}$/.test(target.host_key_fingerprint)
      || !["queued", "running", "recovery_required", "prepared", "joined", "completed", "failed", "cancelled"].includes(target.state)
      || typeof target.current_step !== "string" || !/^[a-z_]{1,64}$/.test(target.current_step)
      || typeof target.credentials_available !== "boolean"
      || ![target.inspected_at, target.inspected_attempt, target.inspected_generation].every((n) => Number.isSafeInteger(n) && n >= 0)) return false;
    ids.add(target.id); addresses.add(target.address);
    if (target.report != null) {
      if (typeof target.report !== "object" || Array.isArray(target.report) || typeof target.report.hostname !== "string"
        || !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(target.report.hostname)
        || ![target.report.cpu_count, target.report.memory_kib, target.inspected_at, target.inspected_attempt, target.inspected_generation].every((n) => Number.isSafeInteger(n) && n > 0)
        || target.inspected_at > 8640000000000 || target.inspected_attempt > value.attempt) return false;
    } else if (target.inspected_at || target.inspected_attempt || target.inspected_generation) return false;
  }
  return true;
}

export default function ClusterSSHOnboarding({ targetCount = 2, initialOperationID = "", onClose, onOperation, onChanged }) {
  const [index, setIndex] = useState(0);
  const [operationID, setOperationID] = useState(initialOperationID);
  const [submittingID, setSubmittingID] = useState("");
  const [progress, setProgress] = useState(null);
  const [error, setError] = useState("");
  const [receivedAt, setReceivedAt] = useState(0);
  const [cancelling, setCancelling] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [submissionRejected, setSubmissionRejected] = useState(false);
  const drafts = useRef([]);
  const requests = useRef(new Set());
  const mounted = useRef(true);
  const callbacks = useRef({ onClose, onOperation, onChanged });
  callbacks.current = { onClose, onOperation, onChanged };
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      requests.current.forEach((controller) => controller.abort());
      clearCredentials(drafts.current); drafts.current = [];
    };
  }, []);

  const request = async (path, init, timeout, controller = new AbortController()) => {
    requests.current.add(controller);
    const timer = setTimeout(() => controller.abort(), timeout);
    try {
      const response = await fetch(path, { credentials: "include", redirect: "error", cache: "no-store", ...init, signal: controller.signal });
      const payload = await response.json();
      if (controller.signal.aborted) throw new Error("request stopped");
      return { response, payload };
    } finally { clearTimeout(timer); requests.current.delete(controller); }
  };

  useEffect(() => {
    if (initialOperationID && initialOperationID !== submittingID && initialOperationID !== operationID) {
      requests.current.forEach((controller) => controller.abort());
      clearCredentials(drafts.current); drafts.current = [];
      setProgress(null); setReceivedAt(0); setSubmissionRejected(false);
      setOperationID(initialOperationID);
    }
  }, [initialOperationID, submittingID, operationID]);

  useEffect(() => {
    if (!validSSHInspectionID(operationID)) return undefined;
    let stopped = false;
    let timer;
    let activeRequest;
    const poll = async () => {
      let terminal = false;
      try {
        activeRequest = new AbortController();
        const { response, payload } = await request(`/api/server/cluster/onboarding/operations/${operationID}`, {}, 10000, activeRequest);
        if (stopped || !mounted.current) return;
        if (!response.ok || !validSSHInspectionProgress(payload, operationID)) {
          setReceivedAt(0);
          setError(response.status === 404 ? (submissionRejected ? "Request was rejected and no inspection is recorded. Close, refresh cluster state and check the source, host details and Admin session before starting over." : "Submission is not recorded yet. Check status before starting another inspection.") : statusUnavailable);
        } else {
          setProgress(payload); setReceivedAt(Date.now()); setError("");
          terminal = terminalStates.has(payload.state);
        }
      } catch { if (!stopped && mounted.current) { setReceivedAt(0); setError(statusUnavailable); } }
      if (!stopped && !terminal) timer = setTimeout(poll, 5000);
    };
    void poll();
    return () => { stopped = true; clearTimeout(timer); activeRequest?.abort(); };
  }, [operationID, refresh, submissionRejected]);

  const selectTarget = async (target) => {
    const targets = [...drafts.current, target];
    if (![1, 2].includes(targetCount) || targets.length > targetCount) { clearCredentials([target]); return "Choose one replacement or two joining Engine hosts."; }
    let body;
    try { body = createSSHInspectionSubmission(targets); }
    catch (failure) { clearCredentials([target]); return failure.message; }
    if (targets.length < targetCount) { drafts.current = targets; setIndex(index + 1); return ""; }
    const id = body.request_id;
    const encoded = JSON.stringify(body);
    clearCredentials(targets); drafts.current = [];
    setSubmittingID(id); callbacks.current.onOperation?.(id); setError("");
    try {
      const { response, payload } = await request("/api/server/cluster/onboarding/operations", { method: "POST", headers: { "Content-Type": "application/json" }, body: encoded }, 25000);
      if (mounted.current && (response.status !== 202 || payload?.operation_id !== id)) {
        setError("Submission needs status verification. No request has been replayed.");
        // Only definitive rejection plus a later 404 permits a new request.
        // A timeout, lost receipt or server failure can hide a committed queue.
        setSubmissionRejected([400, 401, 403, 409, 413, 422, 429].includes(response.status));
      }
    } catch { if (mounted.current) setError("Submission response was lost. Checking the original request's status."); }
    finally {
      if (mounted.current) { setSubmittingID(""); setOperationID(id); callbacks.current.onChanged?.(); }
    }
    return "";
  };

  const cancel = async () => {
    if (!canCancel) return;
    if (Date.now() - receivedAt > 15000) { setReceivedAt(0); setError(statusUnavailable); return; }
    setCancelling(true);
    try {
      const { response, payload } = await request(`/api/server/cluster/operations/${operationID}/cancel`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ confirmation: "CANCEL OPERATION" }) }, 15000);
      if (!response.ok || payload?.state !== "cancelled") throw new Error("cancellation not confirmed");
      if (mounted.current) { setReceivedAt(0); setRefresh((n) => n + 1); callbacks.current.onChanged?.(); }
    } catch { if (mounted.current) { setReceivedAt(0); setError("Cancellation is not confirmed. Refresh status before taking another action."); } }
    finally { if (mounted.current) setCancelling(false); }
  };

  if (!operationID && !submittingID) return <ClusterSSHInspection key={index} title={`Inspect Engine host ${index + 1} of ${targetCount}`}
    submitLabel={index + 1 < targetCount ? "Next host" : "Inspect hosts"} onApprovedTarget={selectTarget} onClose={onClose}
    onBack={index ? () => { clearCredentials(drafts.current); drafts.current = []; setIndex(0); } : undefined} />;

  const canCancel = !submittingID && !cancelling && receivedAt > 0 && Date.now() - receivedAt <= 15000 && ["queued", "running", "waiting"].includes(progress?.state) && phases.has(progress?.current_step)
    && progress.targets.every((target) => ["queued", "running", "recovery_required"].includes(target.state) && ["inspect", "inspection_complete"].includes(target.current_step));
  return <Dialog open onClose={onClose} maxWidth="sm" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
    <DialogTitle sx={DIALOG_TITLE_SX}>Engine host inspection</DialogTitle>
    <DialogContent sx={{ ...DIALOG_CONTENT_SX, overflowY: "auto" }}><Stack spacing={2} sx={{ pt: 1.25 }}>
      {operationID && !validSSHInspectionID(operationID) ? <Alert severity="error">Invalid inspection request ID. Open an inspection from operation history.</Alert> : error ? <Alert severity="warning">{error}</Alert> : null}
      <Typography>{submittingID ? "Submitting approved hosts…" : progress?.state === "waiting" ? "Inspection complete. Review collected host results." : progress?.state === "cancelled" ? "Inspection ended. Temporary credentials and host reservations released." : progress?.state === "failed" ? "Inspection stopped. Review retained host results before starting over." : progress?.state === "succeeded" ? "Operation finished. Review recorded results and cluster membership." : "Checking approved hosts from this Engine…"}</Typography>
      {progress?.state === "waiting" ? <Typography>Joining requires further readiness checks. End inspection to release hosts and temporary credentials.</Typography> : null}
      {progress?.targets.map((target) => <Stack key={target.id} spacing={0.5} sx={{ p: 1.5, border: "1px solid rgba(148,163,184,0.2)", borderRadius: 2 }}>
        <Typography sx={{ fontWeight: 600 }}>{target.address}:{target.port}</Typography>
        <Typography variant="caption" sx={{ overflowWrap: "anywhere" }}>{target.host_key_fingerprint}</Typography>
        <Typography>{target.current_step === "inspection_complete" ? "Host inspection recorded" : target.state.replaceAll("_", " ")}</Typography>
        <Typography variant="body2">{target.credentials_available ? "Temporary credentials available" : "Temporary credentials unavailable"}</Typography>
        {target.report ? <Typography variant="body2">{target.report.hostname} · {target.report.cpu_count} CPUs · {(target.report.memory_kib / 1048576).toFixed(1)} GiB RAM<br />Recorded {new Date(target.inspected_at * 1000).toLocaleString()} · attempt {target.inspected_attempt}, generation {target.inspected_generation}</Typography> : null}
      </Stack>)}
      <Typography variant="caption" sx={{ overflowWrap: "anywhere" }}>Request: {operationID || submittingID}</Typography>
    </Stack></DialogContent>
    <DialogActions sx={DIALOG_ACTIONS_SX}>
      <Button sx={DIALOG_BUTTON_SX} onClick={onClose}>Close</Button>
      <Button sx={DIALOG_BUTTON_SX} disabled={!validSSHInspectionID(operationID) || Boolean(submittingID) || cancelling} onClick={() => { setReceivedAt(0); setRefresh((n) => n + 1); }}>Refresh status</Button>
      <Button sx={DIALOG_DANGER_BUTTON_SX} disabled={!canCancel} onClick={() => void cancel()}>End inspection</Button>
    </DialogActions>
  </Dialog>;
}
