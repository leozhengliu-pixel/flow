import { describe, expect, it } from "vitest";

import { classifyDiffFile } from "./use-diff-files-query";
import type { ReviewFile } from "@/types/flow";

function file(partial: Partial<ReviewFile>): ReviewFile {
  return {
    path: "a.ts",
    status: "modified",
    additions: 1,
    deletions: 0,
    patch: "@@ -1 +1 @@\n+x\n",
    ...partial,
  };
}

describe("useDiffFilesQuery helpers (LS-0728)", () => {
  it("classifies text / pdf / binary / empty files", () => {
    expect(classifyDiffFile(file({ path: "a.ts" }))).toBe("text");
    expect(classifyDiffFile(file({ path: "doc.pdf", patch: "" }))).toBe("pdf");
    expect(classifyDiffFile(file({ path: "logo.png", patch: "", additions: 0, deletions: 0 }))).toBe(
      "binary",
    );
    expect(classifyDiffFile(file({ path: "empty.ts", patch: "", additions: 0, deletions: 0 }))).toBe(
      "empty",
    );
  });
});
