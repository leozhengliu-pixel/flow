import { describe, expect, it } from "vitest";

import {
  commentIdFromHash,
  getAnchoredCommentFilePath,
  getAnchoredCommentRedirectPath,
  isCommentIdInHash,
  targetCommentHash,
} from "./review-comment-navigation";
import type { ReviewEvent } from "@/types/flow";
import { teammate } from "@/test/fixtures";

const events: ReviewEvent[] = [
  {
    id: "c1",
    type: "commented",
    body: "nits",
    path: "web/src/a.ts",
    line: 12,
    actor: teammate,
    createdAt: "2026-09-01T00:00:00.000Z",
  },
  {
    id: "opened",
    type: "opened",
    actor: teammate,
    createdAt: "2026-09-01T00:00:00.000Z",
  },
];

describe("ReviewCommentNavigation (LS-0535)", () => {
  it("builds and parses anchored comment hashes", () => {
    expect(targetCommentHash("c1")).toBe("comment-c1");
    expect(commentIdFromHash("#comment-c1")).toBe("c1");
    expect(isCommentIdInHash("#comment-c1", "c1")).toBe(true);
    expect(isCommentIdInHash("#comment-c1", "c2")).toBe(false);
  });

  it("resolves file path and redirect for Diff tab", () => {
    expect(getAnchoredCommentFilePath(events, "#comment-c1")).toBe("web/src/a.ts");
    expect(
      getAnchoredCommentRedirectPath({
        reviewPath: "/acme/review/r1/overview",
        events,
        targetCommentHash: "comment-c1",
      }),
    ).toBe("/acme/review/r1/changes#comment-c1");
  });
});
