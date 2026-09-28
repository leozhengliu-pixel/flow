import { afterEach, describe, expect, it } from "vitest";

import {
  BackgroundReviewProcessor,
  diffStoreKey,
} from "./background-review-processor";
import { DiffComputer } from "./diff-computer";
import type { CodeReview, ReviewFile, User } from "@/types/flow";

const actor: User = {
  id: "user-1",
  name: "Ada",
  email: "ada@flow.dev",
  displayName: "Ada",
  avatarUrl: "",
  active: true,
  emailVerified: true,
};

function makeFile(path: string, patch: string): ReviewFile {
  return { path, status: "modified", additions: 1, deletions: 1, patch };
}

function makeReview(files: ReviewFile[]): CodeReview {
  return {
    id: "review-1",
    slugId: "review-one",
    provider: "github",
    externalId: "1",
    number: 1,
    title: "Diff engine",
    description: "",
    status: "open",
    repositoryOwner: "flow",
    repositoryName: "flow",
    url: "https://example.com",
    author: actor,
    reviewerIds: [],
    teamReviewers: [],
    issueIds: [],
    baseBranch: "main",
    headBranch: "feature",
    branchState: "upToDate",
    additions: 1,
    deletions: 1,
    commitCount: 1,
    checks: [],
    files,
    events: [],
    favorite: false,
    draft: false,
    quickToReview: false,
    createdAt: "2026-09-20T00:00:00.000Z",
    updatedAt: "2026-09-20T00:00:00.000Z",
  };
}

afterEach(() => {
  BackgroundReviewProcessor.resetForTests();
});

describe("BackgroundReviewProcessor (LS-0097)", () => {
  it("requires initialize before getInstance", () => {
    expect(() => BackgroundReviewProcessor.getInstance()).toThrow(/initialized/);
  });

  it("prefetches multi-file basic diffs into the store", async () => {
    const computer = new DiffComputer();
    const processor = BackgroundReviewProcessor.initialize({
      computer,
      concurrency: 2,
      preferStructural: false,
    });
    const review = makeReview([
      makeFile(
        "a.ts",
        "@@ -1,1 +1,1 @@\n-old\n+new\n",
      ),
      makeFile(
        "b.ts",
        "@@ -1,1 +1,2 @@\n context\n+added\n",
      ),
    ]);

    let notifies = 0;
    const stop = processor.subscribe(() => {
      notifies += 1;
    });

    processor.engage(review.id);
    await processor.prefetchReview(review, { variants: ["basic"] }).promise;
    stop();

    expect(processor.areAllDiffsReady(review, "basic")).toBe(true);
    const a = processor.getComputedDiff(review.id, "a.ts", "basic");
    const b = processor.getComputedDiff(review.id, "b.ts", "basic");
    expect(a?.state).toBe("ready");
    expect(a?.additions).toBe(1);
    expect(b?.state).toBe("ready");
    expect(b?.lines.some((line) => line.kind === "add")).toBe(true);
    expect(notifies).toBeGreaterThan(0);
    expect(diffStoreKey({ reviewId: review.id, path: "a.ts", variant: "basic" })).toContain(
      "review-1",
    );
  });

  it("computes structural variant when contentResolver supplies both sides", async () => {
    const processor = BackgroundReviewProcessor.initialize({
      computer: new DiffComputer(),
      preferStructural: true,
      contentResolver: async ({ side }) => (side === "original" ? "one\ntwo\n" : "one\nthree\n"),
    });
    const review = makeReview([makeFile("c.ts", "@@ -1,2 +1,2 @@\n one\n-two\n+three\n")]);
    await processor.ensureFileProcessed(review, review.files[0], "structural");
    const structural = processor.getComputedDiff(review.id, "c.ts", "structural");
    expect(structural?.state).toBe("ready");
    expect(structural?.variant).toBe("structural");
    expect(structural?.lines.map((l) => l.kind)).toContain("remove");
    expect(structural?.lines.map((l) => l.kind)).toContain("add");
  });
});
