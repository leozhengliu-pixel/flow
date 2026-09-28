import { describe, expect, it } from "vitest";

import {
  buildAgentDispatchPrompt,
  formatReviewCommentMarkdown,
} from "./pull-request-comment-actions";
import { teammate } from "@/test/fixtures";

describe("PullRequestCommentActions (LS-0503)", () => {
  it("formats markdown and agent dispatch prompts", () => {
    const event = {
      id: "c1",
      type: "commented",
      body: "Please rename this",
      path: "web/a.ts",
      line: 4,
      actor: teammate,
      createdAt: "2026-09-01T00:00:00.000Z",
    };
    expect(formatReviewCommentMarkdown(event)).toContain("`web/a.ts:4`");
    expect(buildAgentDispatchPrompt(event, "review-1")).toContain("review review-1");
  });
});
