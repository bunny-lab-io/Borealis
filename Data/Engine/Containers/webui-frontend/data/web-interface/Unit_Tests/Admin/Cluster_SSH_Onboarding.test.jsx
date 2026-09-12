import React from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ClusterSSHOnboarding, { validSSHInspectionProgress } from "@/Admin/Cluster_SSH_Onboarding.jsx";
import { createSSHInspectionSubmission, validateSSHInspectionSubmission } from "@/Admin/clusterSSHOnboarding.js";
import { validateBorealisFetchRequest } from "@/app/utils/inputValidation.js";

const requestID = "5bdb3c22-6cf3-4c18-a22a-c020e47bd286";
const targets = () => [1, 2].map((n) => ({ address: `192.168.90.${20 + n}`, port: 22, username: "operator", auth_method: "password",
  password: "  <password> $ exact  ", sudo_password: "  <sudo> $ exact  ", host_key_approved: true,
  host_key_algorithm: "ssh-ed25519", host_key_fingerprint: `SHA256:${String(n).repeat(43)}`, host_key_base64: "onerror=" }));

describe("SSH inspection submission contract", () => {
  it("keeps one request identity and exact nested secret/wire values through shared fetch guard", () => {
    const input = targets();
    input[1].auth_method = "private_key";
    delete input[1].password;
    input[1].private_key = "-----BEGIN OPENSSH PRIVATE KEY-----\nfixture\n-----END OPENSSH PRIVATE KEY-----";
    input[1].passphrase = "  <passphrase> $ exact  ";
    const body = createSSHInspectionSubmission(input, requestID);
    const checked = validateBorealisFetchRequest("/api/server/cluster/onboarding/operations", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }, "https://borealis.test");
    expect(checked.errors).toEqual([]);
    expect(JSON.parse(checked.init.body)).toEqual(body);
    expect(body.request_id).toBe(requestID);
    expect(body.confirmation).toBe("INSPECT ENGINE NODES");
    expect(validateSSHInspectionSubmission(createSSHInspectionSubmission(targets().slice(0, 1), requestID))).toBe("");
  });

  it.each(["unapproved", "duplicate address", "duplicate key", "invalid port", "sudo newline", "sudo byte limit", "sudo NUL", "mixed credentials", "null passphrase", "extra field", "invalid request ID", "too many targets"])("rejects %s before submission", (mode) => {
    const body = createSSHInspectionSubmission(targets(), requestID);
    const target = body.targets[1];
    if (mode === "unapproved") target.host_key_approved = false;
    if (mode === "duplicate address") target.address = body.targets[0].address;
    if (mode === "duplicate key") target.host_key_fingerprint = body.targets[0].host_key_fingerprint;
    if (mode === "invalid port") target.port = 22.5;
    if (mode === "sudo newline") target.sudo_password = "line1\nline2";
    if (mode === "sudo byte limit") target.sudo_password = "é".repeat(2049);
    if (mode === "sudo NUL") target.sudo_password = "before\0after";
    if (mode === "mixed credentials") target.private_key = "key";
    if (mode === "null passphrase") target.passphrase = null;
    if (mode === "extra field") target.command = "anything";
    if (mode === "invalid request ID") body.request_id = "../operations";
    if (mode === "too many targets") body.targets.push({ ...target });
    expect(validateSSHInspectionSubmission(body)).not.toBe("");
  });
});


const response = (payload, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => payload });
const progress = (state = "waiting") => ({ operation_id: requestID, state, current_step: "qualify_ssh_targets", attempt: 1,
  targets: targets().map((target, index) => ({ id: `${index + 1}1111111-1111-4111-8111-111111111111`, address: target.address, port: target.port,
    host_key_fingerprint: target.host_key_fingerprint, state: "running", current_step: "inspection_complete", credentials_available: state === "waiting",
    inspected_at: 1788920000, inspected_attempt: 1, inspected_generation: 2, report: { hostname: `engine-${index + 2}`, cpu_count: 4, memory_kib: 8388608 } })) });

async function fillHost(index, address = targets()[index].address) {
  fireEvent.change(screen.getByLabelText("Private IPv4 address"), { target: { value: address } });
  fireEvent.click(screen.getByRole("button", { name: "Discover host key" }));
  fireEvent.click(await screen.findByLabelText("I verified and approve this host key"));
  fireEvent.change(screen.getByLabelText("Linux admin username"), { target: { value: "operator" } });
  fireEvent.change(screen.getByLabelText("Linux password"), { target: { value: targets()[index].password } });
  fireEvent.change(screen.getByLabelText("Sudo password (if required)"), { target: { value: targets()[index].sudo_password } });
}

function mockDiscovery(path, init) {
  if (path.endsWith("/host-key")) {
    const { address, port } = JSON.parse(init.body);
    const target = targets().find((target) => target.address === address) || targets()[0];
    return Promise.resolve(response({ address, port, host_key_algorithm: target.host_key_algorithm, host_key_fingerprint: target.host_key_fingerprint, host_key_base64: target.host_key_base64 }));
  }
  throw new Error("Unexpected request");
}

describe("SSH cohort inspection dialog", () => {
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  it("collects both approved hosts before one POST, preserves secret syntax, clears forms and shows durable ownership", async () => {
    const saved = vi.fn();
    vi.spyOn(crypto, "randomUUID").mockReturnValue(requestID);
    const fetch = vi.fn((path, init) => {
      if (path.endsWith("/host-key")) return mockDiscovery(path, init);
      if (init.method === "POST") return Promise.resolve(response({ operation_id: requestID }, 202));
      return Promise.resolve(response(progress()));
    });
    vi.stubGlobal("fetch", fetch);
    render(<React.StrictMode><ClusterSSHOnboarding onClose={vi.fn()} onOperation={saved} /></React.StrictMode>);
    await fillHost(0);
    fireEvent.click(screen.getByRole("button", { name: "Next host" }));
    expect(await screen.findByText("Inspect Engine host 2 of 2")).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText("Linux password")).toBeNull();
    await fillHost(1);
    fireEvent.click(screen.getByRole("button", { name: "Inspect hosts" }));
    await screen.findByText(/Inspection complete/);
    const submissions = fetch.mock.calls.filter(([path]) => path.endsWith("/operations"));
    expect(submissions).toHaveLength(1);
    expect(JSON.parse(submissions[0][1].body)).toEqual(createSSHInspectionSubmission(targets(), requestID));
    expect(saved).toHaveBeenCalledExactlyOnceWith(requestID);
    expect(screen.queryByLabelText("Linux password")).toBeNull();
    expect(screen.getAllByText(/attempt 1, generation 2/)).toHaveLength(2);
    expect(screen.getByText(/Joining requires further readiness checks/)).toBeInTheDocument();
    expect(screen.queryByText(targets()[0].password)).toBeNull();
  });

  it("rejects a duplicate second host before operation submission", async () => {
    const fetch = vi.fn(mockDiscovery); vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHOnboarding onClose={vi.fn()} />);
    await fillHost(0);
    fireEvent.click(screen.getByRole("button", { name: "Next host" }));
    await screen.findByText("Inspect Engine host 2 of 2");
    await fillHost(1, targets()[0].address);
    fireEvent.click(screen.getByRole("button", { name: "Inspect hosts" }));
    await screen.findByText(/distinct addresses and SSH host keys/);
    expect(fetch.mock.calls.every(([path]) => path.endsWith("/host-key"))).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Start over" }));
    expect(screen.getByLabelText("Private IPv4 address")).toHaveValue("");
    expect(screen.queryByLabelText("Linux password")).toBeNull();
  });

  it("drops collected credentials on close and starts with an empty first target on reopen", async () => {
    const fetch = vi.fn(mockDiscovery); vi.stubGlobal("fetch", fetch);
    function Harness() { const [open, setOpen] = React.useState(true); return open ? <ClusterSSHOnboarding onClose={() => setOpen(false)} /> : <button onClick={() => setOpen(true)}>Reopen</button>; }
    render(<Harness />);
    await fillHost(0);
    fireEvent.click(screen.getByRole("button", { name: "Next host" }));
    await screen.findByText("Inspect Engine host 2 of 2");
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    fireEvent.click(screen.getByRole("button", { name: "Reopen" }));
    expect(screen.getByText("Inspect Engine host 1 of 2")).toBeInTheDocument();
    expect(screen.getByLabelText("Private IPv4 address")).toHaveValue("");
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("recovers a lost submission response using the same UUID without POST replay", async () => {
    vi.spyOn(crypto, "randomUUID").mockReturnValue(requestID);
    const saved = vi.fn();
    const fetch = vi.fn((path, init) => {
      if (path.endsWith("/host-key")) return mockDiscovery(path, init);
      if (init.method === "POST") return Promise.reject(new Error("private transport details"));
      return Promise.resolve(response({ ...progress(), targets: progress().targets.slice(0, 1) }));
    }); vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHOnboarding targetCount={1} onClose={vi.fn()} onOperation={saved} />);
    await fillHost(0);
    fireEvent.click(screen.getByRole("button", { name: "Inspect hosts" }));
    await screen.findByText(/Inspection complete/);
    expect(saved).toHaveBeenCalledExactlyOnceWith(requestID);
    expect(fetch.mock.calls.filter(([, init]) => init.method === "POST" && !JSON.parse(init.body).address)).toHaveLength(1);
    expect(fetch.mock.calls.at(-1)[0]).toBe(`/api/server/cluster/onboarding/operations/${requestID}`);
    expect(screen.queryByText(/private transport details/)).toBeNull();
  });

  it("keeps submission alive when its public UUID is echoed by navigation, then aborts on close", async () => {
    vi.spyOn(crypto, "randomUUID").mockReturnValue(requestID);
    const fetch = vi.fn((path, init) => path.endsWith("/host-key") ? mockDiscovery(path, init) : new Promise(() => {}));
    vi.stubGlobal("fetch", fetch);
    function Harness() {
      const [id, setID] = React.useState("");
      return <ClusterSSHOnboarding targetCount={1} initialOperationID={id} onOperation={setID} onClose={vi.fn()} />;
    }
    const view = render(<Harness />);
    await fillHost(0);
    fireEvent.click(screen.getByRole("button", { name: "Inspect hosts" }));
    await screen.findByText("Submitting approved hosts…");
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(fetch.mock.calls[1][1].signal.aborted).toBe(false);
    expect(screen.getByText(`Request: ${requestID}`)).toBeInTheDocument();
    view.unmount();
    expect(fetch.mock.calls[1][1].signal.aborted).toBe(true);
  });

  it("keeps rejected submissions distinct from uncertain receipts without displaying diagnostics", async () => {
    vi.spyOn(crypto, "randomUUID").mockReturnValue(requestID);
    const fetch = vi.fn((path, init) => {
      if (path.endsWith("/host-key")) return mockDiscovery(path, init);
      return Promise.resolve(response({ message: "private server details" }, init.method === "POST" ? 409 : 404));
    }); vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHOnboarding targetCount={1} onClose={vi.fn()} />);
    await fillHost(0);
    fireEvent.click(screen.getByRole("button", { name: "Inspect hosts" }));
    await screen.findByText(/Request was rejected and no inspection is recorded/);
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(screen.queryByText(/private server details/)).toBeNull();
    expect(screen.getByRole("button", { name: "End inspection" })).toBeDisabled();
  });

  it("invalidates cancellation on failed status and ignores an aborted older response", async () => {
    let finishOld;
    const fetch = vi.fn().mockResolvedValueOnce(response(progress()))
      .mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve; }))
      .mockRejectedValueOnce(new Error("private status failure"));
    vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHOnboarding initialOperationID={requestID} onClose={vi.fn()} />);
    await screen.findByText(/Inspection complete/);
    expect(screen.getByRole("button", { name: "End inspection" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "Refresh status" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("button", { name: "End inspection" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Refresh status" }));
    await screen.findByText(/Retained results may be stale/);
    expect(fetch.mock.calls[1][1].signal.aborted).toBe(true);
    await act(async () => { finishOld(response(progress())); });
    expect(screen.getByRole("button", { name: "End inspection" })).toBeDisabled();
    expect(screen.getAllByText(/attempt 1, generation 2/)).toHaveLength(2);
    expect(screen.queryByText(/private status failure/)).toBeNull();
  });

  it("cancels read-only inspection and reloads retained reports without resubmitting credentials", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(response(progress())).mockResolvedValueOnce(response({ state: "cancelled" }))
      .mockResolvedValueOnce(response({ ...progress("cancelled"), targets: progress("cancelled").targets.map((target) => ({ ...target, state: "cancelled" })) }));
    vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHOnboarding initialOperationID={requestID} onClose={vi.fn()} />);
    await screen.findByText(/Inspection complete/);
    fireEvent.click(screen.getByRole("button", { name: "End inspection" }));
    await screen.findByText(/Inspection ended/);
    expect(fetch.mock.calls[1][0]).toBe(`/api/server/cluster/operations/${requestID}/cancel`);
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ confirmation: "CANCEL OPERATION" });
    expect(screen.getByRole("button", { name: "End inspection" })).toBeDisabled();
    expect(screen.getAllByText("Temporary credentials unavailable")).toHaveLength(2);
  });

  it.each(["parent preparation", "target joined", "stale receipt"])("blocks cancellation after %s", async (mode) => {
    const value = progress();
    if (mode === "parent preparation") value.current_step = "prepare_ssh_targets";
    if (mode === "target joined") value.targets[0].state = "joined";
    const fetch = vi.fn().mockResolvedValue(response(value)); vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHOnboarding initialOperationID={requestID} onClose={vi.fn()} />);
    await screen.findByText(/Inspection complete/);
    if (mode === "stale receipt") {
      vi.spyOn(Date, "now").mockReturnValue(Date.now() + 16000);
      fireEvent.click(screen.getByRole("button", { name: "End inspection" }));
    }
    expect(screen.getByRole("button", { name: "End inspection" })).toBeDisabled();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("aborts status requests on unmount and never sends malformed URL identity", async () => {
    const fetch = vi.fn(() => new Promise(() => {})); vi.stubGlobal("fetch", fetch);
    const view = render(<ClusterSSHOnboarding initialOperationID={requestID} onClose={vi.fn()} />);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    view.unmount(); expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
    render(<ClusterSSHOnboarding initialOperationID="../secret" onClose={vi.fn()} />);
    expect(screen.getByText(/Invalid inspection request ID/)).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "End inspection" })).toBeDisabled();
  });

  it.each(["wrong operation", "duplicate target", "missing hostname", "invalid capacity", "missing ownership", "future attempt", "report absent"])("rejects malformed public progress: %s", (mode) => {
    const value = progress();
    if (mode === "wrong operation") value.operation_id = value.targets[0].id;
    if (mode === "duplicate target") value.targets[1].id = value.targets[0].id;
    if (mode === "missing hostname") delete value.targets[0].report.hostname;
    if (mode === "invalid capacity") value.targets[0].report.memory_kib = -1;
    if (mode === "missing ownership") value.targets[0].inspected_generation = 0;
    if (mode === "future attempt") value.targets[0].inspected_attempt = 2;
    if (mode === "report absent") delete value.targets[0].report;
    expect(validSSHInspectionProgress(value, requestID)).toBe(false);
  });
});
