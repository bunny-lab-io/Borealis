import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import RemoteFileManagement, {
  buildRemoteChildPath,
  validateNewTextFileName,
} from "@/Devices/Tabs/Remote_File_Management.jsx";

const { notify } = vi.hoisted(() => ({ notify: vi.fn(async () => {}) }));
vi.mock("@/app/hooks/useAppNotifications.js", () => ({ useAppNotifications: () => notify }));
vi.mock("ag-grid-react", async () => {
  const React = await import("react");
  return {
    AgGridReact: React.forwardRef(function FileGrid({ rowData, onSelectionChanged, onCellContextMenu }, ref) {
      const selected = React.useRef([]);
      React.useImperativeHandle(ref, () => ({ api: {
        getSelectedRows: () => selected.current,
        deselectAll: () => { selected.current = []; },
      } }), []);
      return <div>{rowData.map((row) => <button key={row.path} onContextMenu={(event) => {
        selected.current = [row];
        onSelectionChanged();
        onCellContextMenu({ event, data: row, node: { isSelected: () => true } });
      }}>{row.name}</button>)}</div>;
    }),
  };
});

describe("Remote File Management helpers", () => {
  it("validates new text file names for target platform", () => {
    expect(validateNewTextFileName("script.ps1", "windows")).toBe("");
    expect(validateNewTextFileName("notes.log", "linux")).toBe("");
    expect(validateNewTextFileName("../script.ps1", "linux")).toContain("path separators");
    expect(validateNewTextFileName("CON", "windows")).toContain("reserved");
    expect(validateNewTextFileName("script.ps1.", "windows")).toContain("space or period");
  });

  it("builds child file paths using remote platform separators", () => {
    expect(buildRemoteChildPath("C:\\Temp\\", "script.ps1", "windows")).toBe("C:\\Temp\\script.ps1");
    expect(buildRemoteChildPath("/opt/Borealis/", "script.sh", "linux")).toBe("/opt/Borealis/script.sh");
    expect(buildRemoteChildPath("/", "agent.log", "linux")).toBe("/agent.log");
  });
});

describe("Remote File Management transfer delivery", () => {
  const entry = { path: "/image.png", name: "image.png", kind: "file", size_bytes: 5 };
  let fetchMock;
  let contentBlocked;
  let anchorRequests;
  let direction;
  let transferCount;

  beforeEach(() => {
    notify.mockClear();
    contentBlocked = true;
    anchorRequests = [];
    direction = "download";
    transferCount = 0;
    const NativeURL = URL;
    vi.stubGlobal("URL", class extends NativeURL {
      static createObjectURL = vi.fn(() => "blob:preview-fixture");
      static revokeObjectURL = vi.fn();
    });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function () {
      // Native anchor delivery has no JS response acknowledgement. Model the
      // browser's first 503 and later successful retry without buffering it.
      anchorRequests.push({ url: this.href, download: this.download, status: contentBlocked ? 503 : 200 });
    });
    const json = (body) => ({ ok: true, json: async () => body });
    fetchMock = vi.fn(async (url) => {
      if (url.endsWith("/roots")) return json({ platform: "linux", current_path: "/", entries: [entry] });
      if (url.includes("/children?")) return json({ entries: [entry] });
      if (url.endsWith("/upload/conflicts")) return json({ conflicts: [] });
      if (url.endsWith("/upload") || url.endsWith("/download")) {
        direction = url.endsWith("/upload") ? "upload" : "download";
        return json({ transfer_id: `prepared-${++transferCount}`, direction, status: "pending" });
      }
      if (url.endsWith("/status")) return json({ transfer_id: url.split("/").at(-2), direction, status: "completed", download_ready: direction === "download", result_name: "image.png", item_count: 1, target_path: "/" });
      if (url.endsWith("/content")) {
        if (contentBlocked) return { ok: false, status: 503, text: async () => "worker_capacity_exhausted" };
        return { ok: true, blob: async () => new Blob(["image"], { type: "image/png" }) };
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  async function openAction(name) {
    const view = render(<MemoryRouter><RemoteFileManagement device={{ hostname: "LAB-ONE" }} /></MemoryRouter>);
    fireEvent.contextMenu(await screen.findByRole("button", { name: "image.png" }));
    fireEvent.click(await screen.findByRole("menuitem", { name }));
    return view;
  }

  const starts = () => fetchMock.mock.calls.filter(([url]) => url.endsWith("/download"));
  const contents = () => fetchMock.mock.calls.filter(([url]) => url.endsWith("/content"));

  it("keeps a capacity-blocked native download retryable until explicit dismissal", async () => {
    await openAction(/^Download$/);
    await screen.findByText("Download prepared");
    await waitFor(() => expect(anchorRequests).toHaveLength(1));
    expect(anchorRequests[0]).toMatchObject({ status: 503, download: "image.png" });
    expect(starts()).toHaveLength(1);
    expect(contents()).toHaveLength(0);
    contentBlocked = false;
    fireEvent.click(screen.getByRole("button", { name: "Retry download" }));
    expect(anchorRequests).toHaveLength(2);
    expect(anchorRequests[1]).toMatchObject({ url: anchorRequests[0].url, status: 200 });
    expect(screen.getByText("Download prepared")).toBeInTheDocument();
    expect(starts()).toHaveLength(1);
    const requestsBeforeDismiss = fetchMock.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText("Download prepared")).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(requestsBeforeDismiss);
    expect(anchorRequests).toHaveLength(2);
    expect(notify.mock.calls.some(([notice]) => /download complete|download saved/i.test(notice.title))).toBe(false);
  });

  it("retries a blocked preview on its original transfer and saves its existing object URL", async () => {
    await openAction(/^Preview Image$/);
    const retry = await screen.findByRole("button", { name: "Retry preview" });
    expect(screen.getByText("worker_capacity_exhausted")).toBeInTheDocument();
    expect(starts()).toHaveLength(1);
    expect(contents()).toHaveLength(1);
    expect(screen.getByText("Download prepared")).toBeInTheDocument();
    contentBlocked = false;
    fireEvent.click(retry);
    const dialog = await screen.findByRole("dialog");
    const download = await within(dialog).findByRole("button", { name: "Download" });
    expect(contents()).toHaveLength(2);
    expect(contents()[1][0]).toBe(contents()[0][0]);
    expect(starts()).toHaveLength(1);
    expect(screen.queryByText("Download prepared")).not.toBeInTheDocument();
    expect(within(dialog).getByRole("img", { name: "image.png" })).toHaveAttribute("src", "blob:preview-fixture");
    fireEvent.click(download);
    expect(anchorRequests).toEqual([{ url: "blob:preview-fixture", download: "image.png", status: 200 }]);
    expect(contents()).toHaveLength(2);
    expect(starts()).toHaveLength(1);
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:preview-fixture");
  });

  it("preserves upload completion without download retry controls", async () => {
    const { container } = render(<MemoryRouter><RemoteFileManagement device={{ hostname: "LAB-ONE" }} /></MemoryRouter>);
    await screen.findByRole("button", { name: "image.png" });
    fireEvent.change(container.querySelector('input[type="file"]'), { target: { files: [new File(["payload"], "upload.txt", { type: "text/plain" })] } });
    await waitFor(() => expect(notify.mock.calls.some(([notice]) => notice.title === "Upload Complete")).toBe(true));
    expect(fetchMock.mock.calls.filter(([url]) => url.endsWith("/upload"))).toHaveLength(1);
    expect(starts()).toHaveLength(0);
    expect(anchorRequests).toHaveLength(0);
    expect(screen.queryByRole("button", { name: "Retry download" })).not.toBeInTheDocument();
    expect(screen.queryByText("Upload in progress")).not.toBeInTheDocument();
  });

  it("serializes duplicate preview retries before a render commits", async () => {
    await openAction(/^Preview Image$/);
    const retry = await screen.findByRole("button", { name: "Retry preview" });
    let resolve;
    const blocked = new Promise((done) => { resolve = done; });
    fetchMock.mockImplementationOnce(() => blocked);
    act(() => { retry.click(); retry.click(); });
    expect(contents()).toHaveLength(2);
    await act(async () => resolve({ ok: true, blob: async () => new Blob(["image"], { type: "image/png" }) }));
    await screen.findByRole("button", { name: "Download" });
    expect(starts()).toHaveLength(1);
    expect(URL.createObjectURL).toHaveBeenCalledTimes(1);
  });

  it.each(["close", "device", "unmount"])("ignores blocked preview fetch after %s", async (action) => {
    const view = await openAction(/^Preview Image$/);
    const retry = await screen.findByRole("button", { name: "Retry preview" });
    let resolve;
    const blocked = new Promise((done) => { resolve = done; });
    fetchMock.mockImplementationOnce(() => blocked);
    fireEvent.click(retry);
    expect(contents()).toHaveLength(2);
    if (action === "close") fireEvent.click(screen.getByRole("button", { name: "Close" }));
    else if (action === "device") view.rerender(<MemoryRouter><RemoteFileManagement device={{ hostname: "LAB-TWO" }} /></MemoryRouter>);
    else view.unmount();
    const blob = vi.fn(async () => new Blob(["stale"], { type: "image/png" }));
    const cancel = vi.fn(async () => {});
    await act(async () => resolve({ ok: true, blob, body: { cancel } }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(blob).not.toHaveBeenCalled();
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(URL.createObjectURL).not.toHaveBeenCalled();
    expect(starts()).toHaveLength(1);
  });

  it("does not let an old preview blob replace or revoke a newer preview", async () => {
    await openAction(/^Preview Image$/);
    const retry = await screen.findByRole("button", { name: "Retry preview" });
    let resolve;
    const blockedBlob = new Promise((done) => { resolve = done; });
    const oldBlob = vi.fn(() => blockedBlob);
    fetchMock.mockImplementationOnce(async () => ({ ok: true, blob: oldBlob }));
    fireEvent.click(retry);
    await waitFor(() => expect(oldBlob).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    contentBlocked = false;
    URL.createObjectURL.mockReturnValue("blob:new-preview");
    fireEvent.contextMenu(screen.getByRole("button", { name: "image.png" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /^Preview Image$/ }));
    await screen.findByRole("button", { name: "Download" });
    expect(starts()).toHaveLength(2);
    expect(contents().at(-1)[0]).toContain("prepared-2/content");
    await act(async () => resolve(new Blob(["stale"], { type: "image/png" })));
    expect(screen.getByRole("img", { name: "image.png" })).toHaveAttribute("src", "blob:new-preview");
    expect(URL.createObjectURL).toHaveBeenCalledTimes(1);
    expect(URL.revokeObjectURL).not.toHaveBeenCalledWith("blob:new-preview");
  });

  it("ignores transfer creation that finishes after its preview closes", async () => {
    let resolve;
    const blockedStart = new Promise((done) => { resolve = done; });
    const originalFetch = fetchMock.getMockImplementation();
    fetchMock.mockImplementation((url, ...args) => url.endsWith("/download") ? blockedStart : originalFetch(url, ...args));
    await openAction(/^Preview Image$/);
    fireEvent.click(await screen.findByRole("button", { name: "Close" }));
    await act(async () => resolve({ ok: true, json: async () => ({ transfer_id: "late-start", direction: "download", status: "pending" }) }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(starts()).toHaveLength(1);
    expect(contents()).toHaveLength(0);
    expect(fetchMock.mock.calls.some(([url]) => url.includes("/late-start/"))).toBe(false);
    expect(notify.mock.calls.some(([notice]) => notice.title === "Preview Started")).toBe(false);
  });
});
