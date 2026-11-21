import React, { useEffect, useRef, useState } from "react";
import { Alert, Button, MenuItem, Stack, TextField, Typography } from "@mui/material";
import { DIALOG_BUTTON_SX, DIALOG_INPUT_SX, DIALOG_SELECT_SX } from "../DialogStyles.jsx";
import { clearPendingSSHPreparation, createSSHPreparationRequest, preparationProgressBinding, preparationProgressEligible, preparationProgressFresh,
  readPendingSSHPreparation, retainPendingSSHPreparation,
  sameSSHPreparationPlan, SSH_PREPARATION_CONFIRMATION, SSH_PREPARATION_UI_FRESH_MS, validSSHPreparationPlan, validSSHPreparationReceipt } from "./clusterSSHPreparation.js";

const unavailable = "Preparation plan is unavailable or changed. Refresh inspection status, then review the plan again.";
const uncertainMessage = "Confirmation could not be verified. Review the plan to check the recorded request; no confirmation will be resent.";
const storageMessage = "The retained confirmation request is unavailable. Confirmation is disabled; review server status before taking another action.";
const pendingIntent = (operationID) => {
  try { return readPendingSSHPreparation(operationID); }
  catch { return { unavailable: true }; }
};
const rejected = new Set([400, 401, 403, 404, 409, 413, 422, 429]);
const errorMessage = (status) => status === 401 || status === 403 ? "An active administrator session is required to review or confirm preparation." : unavailable;

export default function ClusterSSHPreparation({ operationID, progress, receivedAt, cancelling, acquireMutation, onConfirmed }) {
  const [review, setReview] = useState(null);
  const [sourceID, setSourceID] = useState("");
  const [phrase, setPhrase] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(() => pendingIntent(operationID));
  const [, tick] = useState(0);
  const mounted = useRef(false);
  const active = useRef(null);
  const binding = preparationProgressBinding(progress, operationID);
  const eligible = preparationProgressEligible(progress, operationID);
  const fresh = preparationProgressFresh(receivedAt);
  const current = useRef(null);
  current.current = { binding, eligible, receivedAt, cancelling, progress, operationID };
  const callbacks = useRef(null);
  callbacks.current = { acquireMutation, onConfirmed };

  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; active.current?.controller?.abort(); };
  }, []);
  useEffect(() => {
    if (!receivedAt) return undefined;
    const remaining = Math.max(0, receivedAt + SSH_PREPARATION_UI_FRESH_MS + 1 - Date.now());
    const timer = setTimeout(() => tick((value) => value + 1), remaining);
    return () => clearTimeout(timer);
  }, [receivedAt]);
  useEffect(() => {
    if (active.current && (active.current.binding !== binding || !fresh || !eligible || cancelling)) {
      active.current.obsolete = true;
      active.current.controller?.abort();
    }
    setReview((value) => value && (value.binding !== binding || !fresh || !eligible || cancelling) ? { ...value, usable: false } : value);
    setSourceID(""); setPhrase("");
  }, [binding, fresh, eligible, cancelling]);

  const isCurrent = (action) => mounted.current && active.current === action && !action.obsolete
    && current.current.binding === action.binding && current.current.eligible && !current.current.cancelling
    && preparationProgressFresh(current.current.receivedAt);
  const request = async (action, suffix, init = {}) => {
    action.controller = new AbortController();
    const timer = setTimeout(() => action.controller.abort(), init.method === "POST" ? 15000 : 10000);
    try {
      const response = await fetch(`/api/server/cluster/onboarding/operations/${action.operationID}/${suffix}`, {
        credentials: "include", redirect: "error", cache: "no-store", ...init, signal: action.controller.signal,
      });
      const payload = await response.json();
      if (action.controller.signal.aborted) throw new Error("request stopped");
      return { response, payload };
    } finally { clearTimeout(timer); }
  };
  const start = (release) => {
    if (active.current || !current.current.eligible || current.current.cancelling || !preparationProgressFresh(current.current.receivedAt)) return null;
    const action = { binding: current.current.binding, operationID: current.current.operationID, release };
    active.current = action; setBusy(true); setError("");
    return action;
  };
  const finish = (action) => {
    action.release?.();
    if (active.current === action) { active.current = null; if (mounted.current) setBusy(false); }
  };
  const acceptReview = (plan, action) => {
    setReview({ plan, binding: action.binding, usable: true }); setSourceID(""); setPhrase("");
  };
  const load = async () => {
    const action = start();
    if (!action) return;
    setReview((value) => value ? { ...value, usable: false } : value);
    setSourceID(""); setPhrase("");
    try {
      const { response, payload } = await request(action, "preparation-plan");
      if (!isCurrent(action)) return;
      if (response.status !== 200 || !validSSHPreparationPlan(payload, current.current.progress, action.operationID)) {
        setError(errorMessage(response.status)); return;
      }
      acceptReview(payload, action);
      const pending = pendingIntent(action.operationID);
      setUncertain(pending);
      if (pending) {
        if (!pending.unavailable && payload.attempt === pending.attempt && payload.plan_sha256 === pending.plan_sha256
          && payload.confirmation?.request_id === pending.request_id && payload.confirmation.source_id === pending.source_id) {
          try { clearPendingSSHPreparation(action.operationID, pending); setUncertain(null); }
          catch { setError(storageMessage); }
        }
        else if (pending.unavailable) setError(storageMessage);
        else setError(payload.confirmation ? "A different confirmation is recorded. Review its selected identity source; no request has been resent." : uncertainMessage);
      }
    } catch { if (isCurrent(action)) setError(unavailable); }
    finally { finish(action); }
  };
  const submit = async () => {
    if (!review?.usable || review.binding !== current.current.binding || review.plan.confirmation || uncertain || active.current) return;
    if (!current.current.eligible || current.current.cancelling || !preparationProgressFresh(current.current.receivedAt)) {
      setReview((value) => value ? { ...value, usable: false } : value); setError(unavailable); return;
    }
    const existing = pendingIntent(operationID);
    if (existing) { setUncertain(existing); setError(existing.unavailable ? storageMessage : uncertainMessage); return; }
    let body;
    try { body = createSSHPreparationRequest(review.plan, sourceID, phrase); }
    catch { return; }
    const release = callbacks.current.acquireMutation();
    if (!release) return;
    const action = start(release);
    if (!action) { release(); return; }
    action.body = body;
    const reviewed = review.plan;
    try {
      // One explicit confirmation click rechecks the reviewed immutable plan.
      // A changed plan is displayed for a new review; it never changes the POST.
      const latest = await request(action, "preparation-plan");
      if (!isCurrent(action)) return;
      if (latest.response.status !== 200 || !validSSHPreparationPlan(latest.payload, current.current.progress, action.operationID)) {
        setReview((value) => value ? { ...value, usable: false } : value);
        setError(errorMessage(latest.response.status)); return;
      }
      if (!sameSSHPreparationPlan(reviewed, latest.payload)) {
        acceptReview(latest.payload, action);
        setReview((value) => ({ ...value, usable: false }));
        setError("Preparation plan changed. Review it again and confirm the current plan."); return;
      }
      if (latest.payload.confirmation) {
        acceptReview(latest.payload, action);
        setError("Confirmation is already recorded. Its identity source cannot be changed."); return;
      }
      try { action.pending = retainPendingSSHPreparation(action.operationID, reviewed.attempt, body); }
      catch { setUncertain(pendingIntent(action.operationID) || { unavailable: true }); setError(storageMessage); return; }
      action.sent = true;
      const result = await request(action, "prepare", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      // Public pending identity survives obsolete callbacks. Only current UI
      // may consume a receipt; another operation/attempt never adopts it.
      if (!isCurrent(action)) return;
      if (result.response.status === 202 && validSSHPreparationReceipt(result.payload, action.operationID, body)) {
        try { clearPendingSSHPreparation(action.operationID, action.pending); setUncertain(null); }
        catch { setUncertain(action.pending); setError(storageMessage); }
        acceptReview({ ...latest.payload, confirmation: { request_id: result.payload.request_id, confirmed_at: result.payload.confirmed_at, source_id: result.payload.source_id } }, action);
        callbacks.current.onConfirmed?.();
      } else {
        setReview((value) => value ? { ...value, usable: false } : value);
        if (!rejected.has(result.response.status)) { setUncertain(action.pending); setError(uncertainMessage); }
        else {
          try { clearPendingSSHPreparation(action.operationID, action.pending); }
          catch { setUncertain(action.pending); }
          setError(errorMessage(result.response.status));
        }
      }
    } catch {
      if (isCurrent(action)) {
        if (action.sent) setUncertain(action.pending);
        setReview((value) => value ? { ...value, usable: false } : value); setError(action.sent ? uncertainMessage : unavailable);
      }
    } finally { finish(action); }
  };

  const usable = review?.usable && review.binding === binding && eligible && fresh && !cancelling;
  const plan = review?.plan;
  const confirmation = plan?.confirmation;
  const selected = plan?.identity_sources.find((source) => source.source_id === confirmation?.source_id);
  const canSubmit = usable && !confirmation && !uncertain && !busy && sourceID && phrase === SSH_PREPARATION_CONFIRMATION;
  return <Stack spacing={1.5} aria-label="Preparation confirmation">
    <Typography sx={{ fontWeight: 600 }}>Preparation confirmation</Typography>
    <Typography variant="body2">Review the exact hosts and choose the original Engine whose shared identity they will inherit. Confirmation records intent. Preparation and joining require separate readiness approval.</Typography>
    {!eligible || !fresh || cancelling ? <Alert severity="warning">Preparation confirmation needs current, completed inspection checks and available credentials. Retained readiness results above remain observations.</Alert> : null}
    {error ? <Alert severity="warning">{error}</Alert> : null}
    <Button sx={DIALOG_BUTTON_SX} disabled={!eligible || !fresh || cancelling || busy} onClick={() => void load()}>Review preparation</Button>
    {plan ? <Stack spacing={1}>
      {!usable ? <Typography variant="body2">Retained plan needs a fresh review before confirmation.</Typography> : null}
      <Typography variant="body2">Release {plan.baseline_release}</Typography>
      <Typography variant="caption" sx={{ overflowWrap: "anywhere" }}>Source commit: {plan.source_sha}<br />Plan: {plan.plan_sha256}</Typography>
      {plan.targets.map((target) => <Typography key={target.target_id} variant="body2" sx={{ overflowWrap: "anywhere" }}>{target.hostname} · {target.address}:{target.port}<br />{target.host_key_fingerprint}</Typography>)}
      {confirmation ? <Alert severity="info">Confirmation recorded for identity source {selected?.hostname}. Confirmation alone does not authorize preparation or joining.<br />Request: {confirmation.request_id}</Alert> : <>
        <TextField select label="Original identity source" value={sourceID} onChange={(event) => setSourceID(event.target.value)}
          disabled={!usable || busy || Boolean(uncertain)} fullWidth sx={DIALOG_SELECT_SX}>
          {plan.identity_sources.map((source) => <MenuItem key={source.source_id} value={source.source_id}>{source.hostname}</MenuItem>)}
        </TextField>
        <TextField label={`Type ${SSH_PREPARATION_CONFIRMATION}`} value={phrase} onChange={(event) => setPhrase(event.target.value)}
          disabled={!usable || busy || Boolean(uncertain)} fullWidth sx={DIALOG_INPUT_SX} inputProps={{ maxLength: 32 }} autoComplete="off" />
        <Button sx={DIALOG_BUTTON_SX} disabled={!canSubmit} onClick={() => void submit()}>Record preparation confirmation</Button>
      </>}
      {uncertain ? <Typography variant="body2">{uncertain.unavailable ? storageMessage : uncertainMessage}</Typography> : null}
    </Stack> : null}
  </Stack>;
}
