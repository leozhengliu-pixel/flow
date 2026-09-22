import { describe, expect, it } from "vitest";

import { resolvePreferredMergeMethod } from "./pull-request-header";

describe("PullRequestHeader merge methods (LS-0505)", () => {
  it("maps personal merge strategy preference", () => {
    expect(resolvePreferredMergeMethod("Squash and merge")).toBe("squash");
    expect(resolvePreferredMergeMethod("Create a merge commit")).toBe("merge");
    expect(resolvePreferredMergeMethod("Rebase and merge")).toBe("rebase");
    expect(resolvePreferredMergeMethod(undefined)).toBe("squash");
  });
});
