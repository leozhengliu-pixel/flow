import { describe, expect, it } from "vitest";

import {
  DiffComputer,
  buildComputedFileDiff,
  computeStructuralLines,
  parseDiff,
  parsePatchHunks,
  pairDiffLines,
  languageFromFilename,
} from "./diff-computer";

const samplePatch = `diff --git a/web/src/a.ts b/web/src/a.ts
--- a/web/src/a.ts
+++ b/web/src/a.ts
@@ -1,3 +1,4 @@
 line one
-line two
+line two changed
 line three
+line four
`;

describe("DiffComputer (LS-0202)", () => {
  it("parses unified patches into hunks and lines", () => {
    const hunks = parsePatchHunks(samplePatch);
    expect(hunks).toHaveLength(1);
    expect(hunks[0].oldStart).toBe(1);
    expect(hunks[0].newStart).toBe(1);
    const lines = parseDiff(samplePatch);
    expect(lines.map((l) => l.kind)).toEqual(["context", "remove", "add", "context", "add"]);
    expect(pairDiffLines(lines)).toHaveLength(4);
  });

  it("computes structural LCS lines from original/modified content", () => {
    const lines = computeStructuralLines("a\nb\nc\n", "a\nx\nc\n");
    expect(lines.map((l) => [l.kind, l.content])).toEqual([
      ["context", "a"],
      ["remove", "b"],
      ["add", "x"],
      ["context", "c"],
    ]);
  });

  it("caches ready results and yields progressive states", async () => {
    const computer = new DiffComputer();
    computer.setDebugEnabled(true);
    const snapshots: string[] = [];
    for await (const snap of computer.computeFileDiff(
      { filename: "web/src/a.ts", patch: samplePatch },
      { variant: "basic", priority: "high" },
    )) {
      snapshots.push(snap.state);
      if (snap.state === "ready") {
        expect(snap.additions).toBe(2);
        expect(snap.deletions).toBe(1);
        expect(snap.language).toBe("TypeScript");
        expect(snap.commentableLines.modified.length).toBeGreaterThan(0);
      }
    }
    expect(snapshots[0]).toBe("pending");
    expect(snapshots).toContain("computing");
    expect(snapshots.at(-1)).toBe("ready");

    const cached: string[] = [];
    for await (const snap of computer.computeFileDiff(
      { filename: "web/src/a.ts", patch: samplePatch },
      { variant: "basic" },
    )) {
      cached.push(snap.state);
      expect(snap.fromCache).toBe(true);
    }
    expect(cached).toEqual(["ready"]);
    expect(computer.getDebugState().cacheSize).toBeGreaterThan(0);
  });

  it("builds structural enrichment from patch when contents are absent", () => {
    const diff = buildComputedFileDiff(
      { filename: "main.go", patch: samplePatch },
      "structural",
    );
    expect(diff.variant).toBe("structural");
    expect(diff.state).toBe("ready");
    expect(languageFromFilename("main.go")).toBe("Go");
    expect(diff.lines.length).toBeGreaterThan(0);
  });
});
