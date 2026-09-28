import { describe, expect, it } from "vitest";

import { canRenderStoredDiff, canViewReviewsWithoutFullAcl } from "./code-access";
import { makeBootstrap } from "@/test/fixtures";

describe("code access for Reviews depth", () => {
  it("allows stored diffs without blocking on full ACL", () => {
    const data = makeBootstrap({
      integrationConnections: [
        {
          id: "github",
          provider: "github",
          status: "connected",
          scopes: ["read:org"],
          config: {},
        } as never,
      ],
    });
    expect(canViewReviewsWithoutFullAcl(data)).toBe(true);
    expect(canRenderStoredDiff(data)).toBe(true);
  });
});
