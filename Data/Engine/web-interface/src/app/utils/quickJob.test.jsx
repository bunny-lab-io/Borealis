import { describe, expect, it } from "vitest";
import { createQuickJobDraft, normalizeQuickJobTargets } from "./quickJob.js";

describe("createQuickJobDraft", () => {
  it("normalizes hostnames and creates one-shot navigation state", () => {
    const draft = createQuickJobDraft([" host-a ", "host-b", "host-a"]);
    expect(draft).toMatchObject({
      hostnames: ["host-a", "host-b"],
      deviceLabel: "host-a +1 more",
      initialTabKey: "components",
      scheduleType: "immediately",
      placeholderAssemblyLabel: "Choose Assembly",
    });
    expect(typeof draft.id).toBe("string");
  });

  it("drops excluded identifier values when building quick-job targets", () => {
    expect(
      normalizeQuickJobTargets(["LAB-OPERATOR-01", "5EF58209-4252-434A-B030-7E13DFEC211E"], {
        excludeValues: ["5ef58209-4252-434a-b030-7e13dfec211e"],
      })
    ).toEqual(["LAB-OPERATOR-01"]);

    const draft = createQuickJobDraft(
      ["LAB-OPERATOR-01", "5EF58209-4252-434A-B030-7E13DFEC211E"],
      {
        excludeValues: ["5ef58209-4252-434a-b030-7e13dfec211e"],
      }
    );

    expect(draft).toMatchObject({
      hostnames: ["LAB-OPERATOR-01"],
      deviceLabel: "LAB-OPERATOR-01",
    });
  });
});
