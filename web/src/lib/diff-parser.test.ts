import { describe, expect, it } from "vitest";

import { hasParseableDiff, parseDiffFile } from "./diff-parser";

describe("DiffParser (LS-0205)", () => {
  it("parses hunk headers into DiffContent-ready files", () => {
    const parsed = parseDiffFile(
      "web/src/a.ts",
      "@@ -1,2 +1,3 @@\n context\n-old\n+new\n+extra\n",
    );
    expect(parsed.language).toBe("TypeScript");
    expect(parsed.hunks).toHaveLength(1);
    expect(parsed.additions).toBe(2);
    expect(parsed.deletions).toBe(1);
    expect(hasParseableDiff(parsed.hunks[0]?.header && "@@ -1 +1 @@\n+x\n")).toBe(true);
  });

  it("treats empty patches as non-parseable", () => {
    expect(hasParseableDiff("")).toBe(false);
    expect(hasParseableDiff(undefined)).toBe(false);
  });
});
