import { describe, expect, it } from "vitest";
import { detectEditorLanguage } from "@/Devices/Tabs/Remote_File_Management.jsx";

describe("Remote File Management editor language detection", () => {
  it("recognizes INI and service-style configuration files", () => {
    expect(detectEditorLanguage("C:\\ProgramData\\Borealis\\agent.ini")).toBe("ini");
    expect(detectEditorLanguage("/etc/systemd/system/borealis.service")).toBe("ini");
    expect(detectEditorLanguage("/opt/app/.env.production")).toBe("ini");
  });

  it("keeps logs and basic text-like files editable as plaintext", () => {
    expect(detectEditorLanguage("/var/log/borealis/engine.log")).toBe("plaintext");
    expect(detectEditorLanguage("/tmp/script.out")).toBe("plaintext");
    expect(detectEditorLanguage("/tmp/table.tsv")).toBe("plaintext");
    expect(detectEditorLanguage("/opt/app/README")).toBe("plaintext");
  });
});
