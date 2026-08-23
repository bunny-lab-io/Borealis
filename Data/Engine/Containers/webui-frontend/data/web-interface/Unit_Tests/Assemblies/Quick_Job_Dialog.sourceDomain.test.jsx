import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import QuickJobDialog from "@/Assemblies/Quick_Job_Dialog.jsx";

vi.mock("@/Assemblies/Assembly_Picker", () => ({
  default: ({ records, onChoose }) => (
    <button type="button" disabled={!records.length} onClick={() => onChoose(records[0])}>
      Choose Aurora Assembly
    </button>
  ),
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("QuickJobDialog Assembly source", () => {
  it("persists Aurora domain metadata in scheduled Quick Job component", async () => {
    let scheduledJobPayload = null;
    vi.stubGlobal("fetch", vi.fn(async (input, init = {}) => {
      const url = String(input);
      if (url === "/api/assemblies") {
        return {
          ok: true,
          json: async () => ({
            items: [{
              assembly_guid: "aurora-guid",
              display_name: "Aurora Maintenance",
              source: "official",
              assembly_type: "script",
              assembly_subtype: "powershell",
              source_path: "Scripts/windows/aurora-maintenance.json",
            }],
            queue: [],
          }),
        };
      }
      if (url === "/api/credentials") {
        return { ok: true, json: async () => ({ credentials: [] }) };
      }
      if (url === "/api/assemblies/aurora-guid/export") {
        return {
          ok: true,
          json: async () => ({
            assembly_guid: "aurora-guid",
            assembly_type: "script",
            assembly_subtype: "powershell",
            domain: "official",
            payload: {
              name: "Aurora Maintenance",
              type: "powershell",
              script: "Write-Output 'ok'",
              variables: [],
            },
          }),
        };
      }
      if (url === "/api/scheduled_jobs" && init.method === "POST") {
        scheduledJobPayload = JSON.parse(init.body);
        return { ok: true, json: async () => ({ id: 326 }) };
      }
      throw new Error(`Unexpected fetch: ${url}`);
    }));

    const onClose = vi.fn();
    render(
      <QuickJobDialog
        open
        onClose={onClose}
        hostnames={["LAB-DEVICE-01"]}
        targetRecords={[{ hostname: "LAB-DEVICE-01", device_guid: "device-guid" }]}
      />
    );

    const chooseButton = await screen.findByRole("button", { name: "Choose Aurora Assembly" });
    await waitFor(() => expect(chooseButton).toBeEnabled());
    fireEvent.click(chooseButton);
    fireEvent.click(screen.getByRole("button", { name: "Next" }));

    await waitFor(() => expect(scheduledJobPayload).not.toBeNull());
    expect(scheduledJobPayload.components).toHaveLength(1);
    expect(scheduledJobPayload.components[0]).toMatchObject({
      assembly_guid: "aurora-guid",
      domain: "official",
      domainLabel: "Aurora",
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
