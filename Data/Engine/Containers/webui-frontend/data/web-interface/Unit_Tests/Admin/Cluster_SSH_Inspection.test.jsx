import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ClusterSSHInspection, { validateSSHInspectionCredential, validateSSHInspectionTarget } from "@/Admin/Cluster_SSH_Inspection.jsx";
import { validateBorealisFetchRequest } from "@/app/utils/inputValidation.js";

const observed = {
  address: "192.168.3.251", port: 22, host_key_algorithm: "ssh-ed25519",
  host_key_fingerprint: `SHA256:${"a".repeat(43)}`, host_key_base64: "AAAA",
};
const response = (payload, status = 200) => ({ ok: status === 200, status, json: async () => payload });
const secrets = { password: "  <punctuation> $ intact  ", private_key: "", passphrase: "" };

async function discover() {
  fireEvent.change(screen.getByLabelText("Private IPv4 address"), { target: { value: observed.address } });
  fireEvent.click(screen.getByRole("button", { name: "Discover host key" }));
  await screen.findByLabelText("I verified and approve this host key");
}

async function approveAndFill() {
  await discover();
  fireEvent.click(screen.getByLabelText("I verified and approve this host key"));
  fireEvent.change(screen.getByLabelText("Linux admin username"), { target: { value: "nicole" } });
  fireEvent.change(screen.getByLabelText("Linux password"), { target: { value: secrets.password } });
}

describe("central SSH inspection", () => {
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  it("keeps discovery credential-free, then sends only explicitly approved identity and clears the password", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(response(observed))
      .mockResolvedValueOnce(response({ ...observed, hostname: "engine-2", kernel: "Linux", architecture: "x86_64", uid: 1000 }));
    vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHInspection onClose={vi.fn()} />);
    await discover();
    expect(screen.queryByLabelText("Linux password")).toBeNull();
    expect(screen.getByRole("button", { name: "Check SSH access" })).toBeDisabled();
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ address: observed.address, port: 22 });
    fireEvent.click(screen.getByLabelText("I verified and approve this host key"));
    fireEvent.change(screen.getByLabelText("Linux admin username"), { target: { value: "nicole" } });
    fireEvent.change(screen.getByLabelText("Linux password"), { target: { value: secrets.password } });
    fireEvent.click(screen.getByRole("button", { name: "Check SSH access" }));
    await screen.findByText(/SSH connected to engine-2/);
    expect(screen.getByLabelText("Linux password")).toHaveValue("");
    const [url, init] = fetch.mock.calls[1];
    expect(url).toBe("/api/server/cluster/onboarding/inspect");
    expect(init.redirect).toBe("error");
    expect(init.credentials).toBe("include");
    expect(JSON.parse(init.body)).toEqual({ ...observed, host_key_approved: true, username: "nicole", auth_method: "password", password: secrets.password });
    expect(screen.queryByText(secrets.password)).toBeNull();
  });

  it("invalidates approval and drops credentials when the target changes", async () => {
    const fetch = vi.fn().mockResolvedValue(response(observed));
    vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHInspection onClose={vi.fn()} />);
    await approveAndFill();
    fireEvent.change(screen.getByLabelText("SSH port"), { target: { value: "2222" } });
    expect(screen.queryByLabelText("Linux password")).toBeNull();
    expect(screen.queryByLabelText("I verified and approve this host key")).toBeNull();
    expect(screen.getByRole("button", { name: "Discover host key" })).toBeEnabled();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("requires new discovery after a changed key and withholds server diagnostics", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(response(observed)).mockResolvedValueOnce(response({ error: "ssh_host_key_changed", message: secrets.password }, 409));
    vi.stubGlobal("fetch", fetch);
    render(<ClusterSSHInspection onClose={vi.fn()} />);
    await approveAndFill();
    fireEvent.click(screen.getByRole("button", { name: "Check SSH access" }));
    await screen.findByText(/SSH host key changed/);
    expect(screen.queryByLabelText("Linux password")).toBeNull();
    expect(screen.queryByText(secrets.password)).toBeNull();
    expect(screen.getByRole("button", { name: "Discover host key" })).toBeEnabled();
  });

  it("aborts an in-flight request when the dialog closes", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(response(observed)).mockImplementationOnce(() => new Promise(() => {}));
    const close = vi.fn();
    vi.stubGlobal("fetch", fetch);
    const view = render(<ClusterSSHInspection onClose={close} />);
    await approveAndFill();
    fireEvent.click(screen.getByRole("button", { name: "Check SSH access" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    const signal = fetch.mock.calls[1][1].signal;
    fireEvent.click(screen.getByRole("button", { name: "Cancel check" }));
    expect(signal.aborted).toBe(true);
    expect(close).toHaveBeenCalledTimes(1);
    view.unmount();
  });

  it("rejects a discovered key response for another target", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({ ...observed, address: "192.168.3.250" })));
    render(<ClusterSSHInspection onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Private IPv4 address"), { target: { value: observed.address } });
    fireEvent.click(screen.getByRole("button", { name: "Discover host key" }));
    await screen.findByText(/SSH check failed/);
    expect(screen.queryByLabelText("I verified and approve this host key")).toBeNull();
  });

  it("validates private IPv4, port, Linux identity and byte limits before network calls", () => {
    expect(validateSSHInspectionTarget("192.168.3.251", "22")).toBe("");
    for (const address of ["8.8.8.8", "127.0.0.1", "10.0.0.999", "010.0.0.1", "host.example", "::1"]) expect(validateSSHInspectionTarget(address, "22")).not.toBe("");
    for (const port of ["0", "65536", "22.5", "22abc"]) expect(validateSSHInspectionTarget(observed.address, port)).not.toBe("");
    expect(validateSSHInspectionCredential("nicole", "password", secrets)).toBe("");
    expect(validateSSHInspectionCredential("root; id", "password", secrets)).not.toBe("");
    expect(validateSSHInspectionCredential("nicole", "password", { ...secrets, password: "é".repeat(2049) })).not.toBe("");
    expect(validateSSHInspectionCredential("nicole", "password", { ...secrets, password: "x\0y" })).not.toBe("");
    expect(validateSSHInspectionCredential("nicole", "private_key", { ...secrets, private_key: "invalid" })).not.toBe("");
  });

  it("preserves encrypted key passphrases through the shared fetch guard", () => {
    const body = { auth_method: "private_key", private_key: "-----BEGIN OPENSSH PRIVATE KEY-----\nfixture\n-----END OPENSSH PRIVATE KEY-----", passphrase: "  <passphrase> $ stays exact  ", host_key_base64: "onerror=" };
    const checked = validateBorealisFetchRequest("/api/server/cluster/onboarding/inspect", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }, "https://borealis.test");
    expect(checked.errors).toEqual([]);
    expect(JSON.parse(checked.init.body)).toEqual(body);
  });
});
