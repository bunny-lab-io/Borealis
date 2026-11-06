import React from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ClusterSSHPreparation from "@/Admin/Cluster_SSH_Preparation.jsx";
import { createSSHPreparationRequest, preparationProgressEligible, readPendingSSHPreparation, sameSSHPreparationPlan, validSSHPreparationPlan, validSSHPreparationReceipt } from "@/Admin/clusterSSHPreparation.js";
import { validateBorealisFetchRequest } from "@/app/utils/inputValidation.js";

const operationID = "11111111-1111-4111-8111-111111111111";
const requestID = "22222222-2222-4222-8222-222222222222";
const sourceID = "33333333-3333-4333-8333-333333333333";
const secondSourceID = "44444444-4444-4444-8444-444444444444";
const response = (payload, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => payload });
function fixture(count = 2) {
  const now = Math.floor(Date.now() / 1000);
  const progress = { operation_id: operationID, attempt: 2, state: "waiting", current_step: "qualify_ssh_targets",
    qualification: { version: 1, attempt: 2, observed_at: now, ready: false, storage: [],
      checks: ["source_inputs", "host_profile", "replica_capacity", "network_boot", "network_arp", "remaining_prerequisites"]
        .map((code, index) => ({ code, state: index === 5 ? "pending" : "passed" })) },
    targets: Array.from({ length: count }, (_, index) => ({ id: `${index + 5}5555555-5555-4555-8555-555555555555`, address: `192.168.90.${21 + index}`, port: 22,
      host_key_fingerprint: `SHA256:${String(index + 1).repeat(43)}`, state: "queued", current_step: "qualification_complete", credentials_available: true,
      inspected_at: now, inspected_attempt: 2, inspected_generation: 3, report: { hostname: `engine-${index + 2}`, cpu_count: 4, memory_kib: 8388608 } })) };
  const plan = { operation_id: operationID, attempt: 2, plan_sha256: "a".repeat(64), baseline_release: "2026.9.5.1", source_sha: "b".repeat(40),
    targets: progress.targets.map((target) => ({ target_id: target.id, address: target.address, port: target.port, hostname: target.report.hostname, host_key_fingerprint: target.host_key_fingerprint })),
    identity_sources: count === 2 ? [{ source_id: sourceID, hostname: "engine-1" }] : [{ source_id: sourceID, hostname: "engine-1" }, { source_id: secondSourceID, hostname: "engine-3" }],
    confirmation: null, preparation_authorized: false, state: "awaiting_readiness" };
  const release = vi.fn(), acquireMutation = vi.fn(() => release), onConfirmed = vi.fn();
  const props = { operationID, progress, receivedAt: Date.now(), cancelling: false, acquireMutation, onConfirmed };
  return { props, plan, release };
}
const receipt = (body) => ({ operation_id: operationID, request_id: body.request_id, plan_sha256: body.plan_sha256, source_id: body.source_id,
  confirmed_at: Math.floor(Date.now() / 1000), state: "awaiting_readiness", preparation_authorized: false });
async function reviewPlan() {
  fireEvent.click(screen.getByRole("button", { name: "Review preparation" }));
  await screen.findByLabelText("Original identity source");
}
async function chooseSource(name = "engine-1") {
  fireEvent.mouseDown(screen.getByLabelText("Original identity source"));
  fireEvent.click(await screen.findByRole("option", { name }));
}
async function confirmFields(name = "engine-1") {
  await chooseSource(name);
  fireEvent.change(screen.getByLabelText("Type PREPARE ENGINE NODES"), { target: { value: "PREPARE ENGINE NODES" } });
}
const postCalls = (fetch) => fetch.mock.calls.filter(([, init]) => init.method === "POST");

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.useRealTimers(); window.sessionStorage.clear(); });

describe("preparation public contract", () => {
  it.each([1, 2])("accepts existing %i-target topology and shared fetch validation", (count) => {
    const { props, plan } = fixture(count);
    expect(validSSHPreparationPlan(plan, props.progress, operationID)).toBe(true);
    const body = createSSHPreparationRequest(plan, plan.identity_sources.at(-1).source_id, "PREPARE ENGINE NODES", requestID);
    expect(Object.keys(body)).toEqual(["request_id", "plan_sha256", "source_id", "confirmation"]);
    expect(validateBorealisFetchRequest(`/api/server/cluster/onboarding/operations/${operationID}/prepare`, { method: "POST", body: JSON.stringify(body) }, "https://borealis.test").errors).toEqual([]);
    expect(validSSHPreparationReceipt(receipt(body), operationID, body)).toBe(true);
    const reordered = { ...plan, targets: plan.targets.map((target) => Object.fromEntries(Object.entries(target).reverse())) };
    expect(sameSSHPreparationPlan(plan, reordered)).toBe(true);
  });

  it.each(["operation", "attempt", "zero digest", "source sha", "release size", "release syntax", "target count", "target ID", "target order", "address", "port", "hostname", "fingerprint", "target duplicate", "source duplicate", "source hostname", "source count", "target as source", "authorized", "state", "extra", "confirmation source", "confirmation time"])("rejects malformed or mismatched plan: %s", (mode) => {
    const { props, plan } = fixture();
    if (mode === "operation") plan.operation_id = requestID;
    if (mode === "attempt") plan.attempt++;
    if (mode === "zero digest") plan.plan_sha256 = "0".repeat(64);
    if (mode === "source sha") plan.source_sha = "A".repeat(40);
    if (mode === "release size") plan.baseline_release = `2026.1.${"1".repeat(60)}`;
    if (mode === "release syntax") plan.baseline_release = "<script>";
    if (mode === "target count") plan.targets.pop();
    if (mode === "target ID") plan.targets[0].target_id = requestID;
    if (mode === "target order") plan.targets.reverse();
    if (mode === "address") plan.targets[0].address = "192.168.90.99";
    if (mode === "port") plan.targets[0].port = 2222;
    if (mode === "hostname") plan.targets[0].hostname = "replacement";
    if (mode === "fingerprint") plan.targets[0].host_key_fingerprint = `SHA256:${"3".repeat(43)}`;
    if (mode === "target duplicate") plan.targets[1] = plan.targets[0];
    if (mode === "source duplicate") plan.identity_sources.push(plan.identity_sources[0]);
    if (mode === "source hostname") plan.identity_sources[0].hostname = "a".repeat(64);
    if (mode === "source count") plan.identity_sources = [];
    if (mode === "target as source") plan.identity_sources[0].source_id = plan.targets[0].target_id;
    if (mode === "authorized") plan.preparation_authorized = true;
    if (mode === "state") plan.state = "ready";
    if (mode === "extra") plan.credentials = "private";
    if (mode === "confirmation source") plan.confirmation = { request_id: requestID, confirmed_at: 1, source_id: secondSourceID };
    if (mode === "confirmation time") plan.confirmation = { request_id: requestID, confirmed_at: Number.MAX_SAFE_INTEGER, source_id: sourceID };
    expect(validSSHPreparationPlan(plan, props.progress, operationID)).toBe(false);
  });

  it.each(["", " PREPARE ENGINE NODES", "PREPARE ENGINE NODES ", "prepare engine nodes", "PREPARE  ENGINE NODES", "PREPARE ENGINE NODES\n", "PREPARE ENGINE NODES\0", "x".repeat(33)])("requires exact unnormalized phrase %j", (phrase) => {
    const { plan } = fixture();
    expect(() => createSSHPreparationRequest(plan, sourceID, phrase, requestID)).toThrow();
  });

  it.each(["blocked", "pending", "missing", "credentials", "attempt", "phase", "target phase"])("keeps required evidence fail-closed: %s", (mode) => {
    const { props } = fixture();
    if (mode === "blocked" || mode === "pending") props.progress.qualification.checks[0].state = mode;
    if (mode === "missing") props.progress.qualification = null;
    if (mode === "credentials") props.progress.targets[0].credentials_available = false;
    if (mode === "attempt") props.progress.targets[0].inspected_attempt--;
    if (mode === "phase") props.progress.current_step = "prepare_ssh_targets";
    if (mode === "target phase") props.progress.targets[0].state = "prepared";
    expect(preparationProgressEligible(props.progress, operationID)).toBeFalsy();
    const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHPreparation {...props} />);
    expect(screen.getByRole("button", { name: "Review preparation" })).toBeDisabled();
    expect(fetch).not.toHaveBeenCalled();
  });
});

describe("explicit preparation intent", () => {
  it("does no mount calls, requires deliberate source/phrase and sends one captured request after recheck", async () => {
    const { props, plan, release } = fixture(1);
    vi.spyOn(crypto, "randomUUID").mockReturnValue(requestID);
    const fetch = vi.fn((path, init) => Promise.resolve(response(init.method === "POST" ? receipt(JSON.parse(init.body)) : plan, init.method === "POST" ? 202 : 200)));
    vi.stubGlobal("fetch", fetch);
    render(<React.StrictMode><ClusterSSHPreparation {...props} /></React.StrictMode>);
    expect(fetch).not.toHaveBeenCalled();
    await reviewPlan();
    expect(screen.getByRole("button", { name: "Record preparation confirmation" })).toBeDisabled();
    expect(screen.getByLabelText("Type PREPARE ENGINE NODES")).toHaveAttribute("maxlength", "32");
    await confirmFields("engine-3");
    fireEvent.click(screen.getByRole("button", { name: "Record preparation confirmation" }));
    await screen.findByText(/Confirmation recorded for identity source engine-3/);
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(postCalls(fetch)).toHaveLength(1);
    const [path, init] = postCalls(fetch)[0];
    expect(path).toBe(`/api/server/cluster/onboarding/operations/${operationID}/prepare`);
    expect(JSON.parse(init.body)).toEqual({ request_id: requestID, plan_sha256: plan.plan_sha256, source_id: secondSourceID, confirmation: "PREPARE ENGINE NODES" });
    expect(init).toMatchObject({ credentials: "include", redirect: "error", cache: "no-store" });
    expect(release).toHaveBeenCalledTimes(1);
    expect(props.onConfirmed).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Record preparation confirmation" })).toBeNull();
    expect(screen.getByText(/Confirmation alone does not authorize/)).toBeInTheDocument();
  });

  it("joins duplicate clicks behind one pre-submit read and never races another mutation", async () => {
    const { props, plan } = fixture();
    let complete;
    const fetch = vi.fn().mockResolvedValueOnce(response(plan)).mockImplementationOnce(() => new Promise((resolve) => { complete = resolve; }))
      .mockImplementationOnce((path, init) => Promise.resolve(response(receipt(JSON.parse(init.body)), 202)));
    vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHPreparation {...props} />);
    await reviewPlan(); await confirmFields();
    const button = screen.getByRole("button", { name: "Record preparation confirmation" });
    fireEvent.click(button); fireEvent.click(button);
    expect(props.acquireMutation).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledTimes(2);
    await act(async () => complete(response(plan)));
    await screen.findByText(/Confirmation recorded for identity source/);
    expect(postCalls(fetch)).toHaveLength(1);
  });

  it.each(["digest", "source", "confirmation"])("rejects changed pre-submit plan %s without POST", async (mode) => {
    const { props, plan, release } = fixture();
    const changed = structuredClone(plan);
    if (mode === "digest") changed.plan_sha256 = "c".repeat(64);
    if (mode === "source") changed.identity_sources[0].source_id = secondSourceID;
    if (mode === "confirmation") changed.confirmation = { request_id: requestID, confirmed_at: 1, source_id: sourceID };
    const fetch = vi.fn().mockResolvedValueOnce(response(plan)).mockResolvedValueOnce(response(changed));
    vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHPreparation {...props} />);
    await reviewPlan(); await confirmFields();
    fireEvent.click(screen.getByRole("button", { name: "Record preparation confirmation" }));
    await screen.findByText(mode === "confirmation" ? /Confirmation is already recorded/ : /Preparation plan changed/);
    expect(postCalls(fetch)).toHaveLength(0);
    expect(release).toHaveBeenCalledTimes(1);
  });

  it.each([401, 403, 409, 503])("keeps rejected plan %i unavailable without rendering diagnostics", async (status) => {
    const { props } = fixture(); const fetch = vi.fn().mockResolvedValue(response({ error: "private contents" }, status));
    vi.stubGlobal("fetch", fetch); render(<ClusterSSHPreparation {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "Review preparation" }));
    await screen.findByText(status < 409 ? /administrator session/ : /plan is unavailable or changed/);
    expect(screen.queryByText(/private contents/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Record preparation confirmation" })).toBeNull();
  });

  it("recovers a lost POST receipt only through GET, keeping one request identity", async () => {
    const { props, plan } = fixture(); vi.spyOn(crypto, "randomUUID").mockReturnValue(requestID);
    const saved = { ...plan, confirmation: { request_id: requestID, confirmed_at: 1, source_id: sourceID } };
    const fetch = vi.fn().mockResolvedValueOnce(response(plan)).mockResolvedValueOnce(response(plan)).mockRejectedValueOnce(new Error("private transport"))
      .mockResolvedValueOnce(response(plan)).mockResolvedValueOnce(response(saved));
    vi.stubGlobal("fetch", fetch); render(<ClusterSSHPreparation {...props} />);
    await reviewPlan(); await confirmFields(); fireEvent.click(screen.getByRole("button", { name: "Record preparation confirmation" }));
    await waitFor(() => expect(screen.getAllByText(/Confirmation could not be verified/).length).toBeGreaterThan(0));
    fireEvent.click(screen.getByRole("button", { name: "Review preparation" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(4));
    expect(screen.getByRole("button", { name: "Record preparation confirmation" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Review preparation" }));
    await screen.findByText(/Confirmation recorded for identity source/);
    expect(postCalls(fetch)).toHaveLength(1);
    expect(crypto.randomUUID).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/private transport/)).toBeNull();
  });

  it("reopens existing confirmation as read-only without reselecting or resending", async () => {
    const { props, plan } = fixture(1);
    plan.confirmation = { request_id: requestID, confirmed_at: 1, source_id: secondSourceID };
    const fetch = vi.fn().mockResolvedValue(response(plan)); vi.stubGlobal("fetch", fetch);
    const view = render(<ClusterSSHPreparation {...props} />);
    view.unmount(); render(<ClusterSSHPreparation {...props} />);
    expect(fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Review preparation" }));
    await screen.findByText(/Confirmation recorded for identity source engine-3/);
    expect(screen.queryByLabelText("Original identity source")).toBeNull();
    expect(postCalls(fetch)).toHaveLength(0);
  });

  it.each(["operation", "attempt", "generation", "credentials", "cancel", "stale"])("aborts and discards late review after %s changes", async (mode) => {
    const { props, plan } = fixture(); let complete;
    const fetch = vi.fn(() => new Promise((resolve) => { complete = resolve; })); vi.stubGlobal("fetch", fetch);
    const view = render(<ClusterSSHPreparation {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "Review preparation" }));
    const changed = structuredClone(props.progress);
    const next = { ...props, progress: changed };
    if (mode === "operation") next.operationID = requestID;
    if (mode === "attempt") changed.attempt++;
    if (mode === "generation") changed.targets[0].inspected_generation++;
    if (mode === "credentials") changed.targets[0].credentials_available = false;
    if (mode === "cancel") next.cancelling = true;
    if (mode === "stale") next.receivedAt = 0;
    view.rerender(<ClusterSSHPreparation {...next} />);
    expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
    await act(async () => complete(response(plan)));
    expect(screen.queryByLabelText("Original identity source")).toBeNull();
    expect(postCalls(fetch)).toHaveLength(0);
  });

  it("invalidates reviewed input when receipt ages without inventing a proposal countdown", async () => {
    const { props, plan } = fixture(); const fetch = vi.fn().mockResolvedValue(response(plan)); vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHPreparation {...props} />); await reviewPlan(); await confirmFields();
    vi.spyOn(Date, "now").mockReturnValue(props.receivedAt + 15001);
    fireEvent.click(screen.getByRole("button", { name: "Record preparation confirmation" }));
    expect(postCalls(fetch)).toHaveLength(0);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("aborts pending POST on unmount and ignores late accepted receipt", async () => {
    const { props, plan, release } = fixture(); let complete;
    const fetch = vi.fn().mockResolvedValueOnce(response(plan)).mockResolvedValueOnce(response(plan))
      .mockImplementationOnce(() => new Promise((resolve) => { complete = resolve; }));
    vi.stubGlobal("fetch", fetch); const view = render(<ClusterSSHPreparation {...props} />);
    await reviewPlan(); await confirmFields(); fireEvent.click(screen.getByRole("button", { name: "Record preparation confirmation" }));
    await waitFor(() => expect(postCalls(fetch)).toHaveLength(1));
    view.unmount(); expect(postCalls(fetch)[0][1].signal.aborted).toBe(true);
    await act(async () => complete(response(receipt(JSON.parse(postCalls(fetch)[0][1].body)), 202)));
    expect(props.onConfirmed).not.toHaveBeenCalled(); expect(release).toHaveBeenCalledTimes(1);
  });

  it("retains unresolved POST identity across close/reopen and page reload without a second UUID", async () => {
    const { props, plan } = fixture(); vi.spyOn(crypto, "randomUUID").mockReturnValue(requestID);
    let complete;
    const saved = { ...plan, confirmation: { request_id: requestID, confirmed_at: 1, source_id: sourceID } };
    const fetch = vi.fn().mockResolvedValueOnce(response(plan)).mockResolvedValueOnce(response(plan))
      .mockImplementationOnce(() => new Promise((resolve) => { complete = resolve; }))
      .mockResolvedValueOnce(response(plan)).mockResolvedValueOnce(response(saved));
    vi.stubGlobal("fetch", fetch);
    const view = render(<ClusterSSHPreparation {...props} />);
    await reviewPlan(); await confirmFields(); fireEvent.click(screen.getByRole("button", { name: "Record preparation confirmation" }));
    await waitFor(() => expect(postCalls(fetch)).toHaveLength(1));
    const pending = readPendingSSHPreparation(operationID);
    expect(pending).toEqual({ operation_id: operationID, attempt: 2, plan_sha256: plan.plan_sha256, request_id: requestID, source_id: sourceID });
    expect(window.sessionStorage.length).toBe(1);
    view.unmount();
    // New component has no old React state; only serialized public identity.
    render(<ClusterSSHPreparation {...props} />);
    await reviewPlan();
    expect(screen.getByRole("button", { name: "Record preparation confirmation" })).toBeDisabled();
    await act(async () => complete(response(receipt(JSON.parse(postCalls(fetch)[0][1].body)), 202)));
    expect(props.onConfirmed).not.toHaveBeenCalled();
    expect(readPendingSSHPreparation(operationID)).toEqual(pending);
    fireEvent.click(screen.getByRole("button", { name: "Review preparation" }));
    await screen.findByText(/Confirmation recorded for identity source/);
    expect(readPendingSSHPreparation(operationID)).toBeNull();
    expect(postCalls(fetch)).toHaveLength(1);
    expect(crypto.randomUUID).toHaveBeenCalledTimes(1);
  });

  it.each(["rejected", "accepted"])("keeps late old-attempt POST %s away from current UI", async (outcome) => {
    const { props, plan } = fixture(); let complete, reject;
    const fetch = vi.fn().mockResolvedValueOnce(response(plan)).mockResolvedValueOnce(response(plan))
      .mockImplementationOnce(() => new Promise((resolve, failure) => { complete = resolve; reject = failure; }));
    vi.stubGlobal("fetch", fetch); const view = render(<ClusterSSHPreparation {...props} />);
    await reviewPlan(); await confirmFields(); fireEvent.click(screen.getByRole("button", { name: "Record preparation confirmation" }));
    await waitFor(() => expect(postCalls(fetch)).toHaveLength(1));
    const next = structuredClone(props.progress); next.attempt = 3; next.qualification.attempt = 3;
    next.targets.forEach((target) => { target.inspected_attempt = 3; target.inspected_generation++; });
    view.rerender(<ClusterSSHPreparation {...props} progress={next} />);
    expect(postCalls(fetch)[0][1].signal.aborted).toBe(true);
    await act(async () => outcome === "accepted" ? complete(response(receipt(JSON.parse(postCalls(fetch)[0][1].body)), 202)) : reject(new Error("old failure")));
    expect(props.onConfirmed).not.toHaveBeenCalled();
    expect(screen.queryByText(/Confirmation recorded for identity source/)).toBeNull();
    expect(screen.queryByText(/Confirmation could not be verified/)).toBeNull();
    expect(readPendingSSHPreparation(operationID).attempt).toBe(2);
  });

  it.each(["corrupt", "read denied", "write denied", "write ignored"])("fails closed when retained intent storage is %s", async (mode) => {
    const { props, plan } = fixture();
    if (mode === "corrupt") window.sessionStorage.setItem(`borealis:ssh-preparation-intent:v1:${operationID}`, "broken");
    if (mode === "read denied") vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("private storage"); });
    if (mode === "write denied") vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("private storage"); });
    if (mode === "write ignored") vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {});
    const fetch = vi.fn().mockResolvedValue(response(plan)); vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHPreparation {...props} />); await reviewPlan();
    if (mode === "write denied" || mode === "write ignored") {
      await confirmFields(); fireEvent.click(screen.getByRole("button", { name: "Record preparation confirmation" }));
    }
    await waitFor(() => expect(screen.getAllByText(/retained confirmation request is unavailable/).length).toBeGreaterThan(0));
    expect(postCalls(fetch)).toHaveLength(0);
    expect(screen.getByRole("button", { name: "Record preparation confirmation" })).toBeDisabled();
    expect(screen.queryByText(/private storage/)).toBeNull();
  });

  it.each(["request", "digest", "source", "authorized", "state"])("retains uncertainty for substituted POST receipt %s", async (mode) => {
    const { props, plan } = fixture();
    const fetch = vi.fn().mockResolvedValueOnce(response(plan)).mockResolvedValueOnce(response(plan))
      .mockImplementationOnce((path, init) => {
        const value = receipt(JSON.parse(init.body));
        if (mode === "request") value.request_id = sourceID;
        if (mode === "digest") value.plan_sha256 = "c".repeat(64);
        if (mode === "source") value.source_id = secondSourceID;
        if (mode === "authorized") value.preparation_authorized = true;
        if (mode === "state") value.state = "ready";
        return Promise.resolve(response(value, 202));
      });
    vi.stubGlobal("fetch", fetch); render(<ClusterSSHPreparation {...props} />);
    await reviewPlan(); await confirmFields(); fireEvent.click(screen.getByRole("button", { name: "Record preparation confirmation" }));
    await waitFor(() => expect(screen.getAllByText(/Confirmation could not be verified/).length).toBeGreaterThan(0));
    expect(props.onConfirmed).not.toHaveBeenCalled();
    expect(readPendingSSHPreparation(operationID)).not.toBeNull();
    expect(postCalls(fetch)).toHaveLength(1);
  });
});
