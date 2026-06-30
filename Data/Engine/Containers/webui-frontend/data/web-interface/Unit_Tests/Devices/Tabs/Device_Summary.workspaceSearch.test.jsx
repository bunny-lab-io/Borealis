import { describe, expect, it } from "vitest";
import {
  createDeviceWorkspaceSearch,
  pruneDeviceWorkspaceContextParams,
} from "@/Devices/Tabs/deviceWorkspaceUrlState.js";

describe("Device Summary workspace URL state", () => {
  it("keeps file working directory and removes registry path in file workspace", () => {
    expect(
      createDeviceWorkspaceSearch(
        "?tab=remote_ops&view=registry&registry_path=HKLM%5CSOFTWARE&working_directory=C%3A%5CUsers&debugSummaryGrid=1",
        "remote_ops",
        "files"
      )
    ).toBe("?tab=remote_ops&view=files&working_directory=C%3A%5CUsers&debugSummaryGrid=1");
  });

  it("keeps registry path and removes file working directory in registry workspace", () => {
    expect(
      createDeviceWorkspaceSearch(
        "?tab=remote_ops&view=files&working_directory=C%3A%5CUsers&registry_path=HKLM%5CSOFTWARE",
        "remote_ops",
        "registry"
      )
    ).toBe("?tab=remote_ops&view=registry&registry_path=HKLM%5CSOFTWARE");
  });

  it("removes editor paths outside file and registry workspaces", () => {
    expect(
      createDeviceWorkspaceSearch(
        "?tab=remote_ops&view=files&working_directory=C%3A%5CUsers&registry_path=HKLM%5CSOFTWARE",
        "remote_ops",
        "shell"
      )
    ).toBe("?tab=remote_ops&view=shell");

    expect(
      createDeviceWorkspaceSearch(
        "?tab=remote_ops&view=registry&working_directory=C%3A%5CUsers&registry_path=HKLM%5CSOFTWARE",
        "inventory",
        "software"
      )
    ).toBe("?tab=inventory&view=software");
  });

  it("cleans direct URL params against current workspace context", () => {
    const fileParams = new URLSearchParams(
      "tab=remote_ops&view=files&registry_path=HKLM%5CSOFTWARE&working_directory=C%3A%5CUsers"
    );
    pruneDeviceWorkspaceContextParams(fileParams, "remote_ops", "files");
    expect(fileParams.toString()).toBe("tab=remote_ops&view=files&working_directory=C%3A%5CUsers");

    const registryParams = new URLSearchParams(
      "tab=remote_ops&view=registry&registry_path=HKLM%5CSOFTWARE&working_directory=C%3A%5CUsers"
    );
    pruneDeviceWorkspaceContextParams(registryParams, "remote_ops", "registry");
    expect(registryParams.toString()).toBe("tab=remote_ops&view=registry&registry_path=HKLM%5CSOFTWARE");
  });
});
