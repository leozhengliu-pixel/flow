import { describe, expect, it } from "vitest";
import {
  actionGroupPriority,
  ACTION_GROUP_BAND_PRIORITY,
  sortActionGroups,
} from "./action-groups";

describe("ActionGroups (LS-0009)", () => {
  it("uses model-band priorities instead of hard-coded insert order", () => {
    const unsorted = ["Other", "Issues", "Navigation", "Filter", "Search results"];
    expect(sortActionGroups(unsorted)).toEqual([
      "Issues",
      "Navigation",
      "Other",
      "Filter",
      "Search results",
    ]);
    expect(actionGroupPriority("Filter")).toBeGreaterThanOrEqual(
      ACTION_GROUP_BAND_PRIORITY.searchActions,
    );
    expect(actionGroupPriority("Issues")).toBeLessThan(
      actionGroupPriority("Navigation"),
    );
  });

  it("elevates Customer / Review / Asks / AI by path", () => {
    const groups = ["Navigation", "Customers", "Reviews", "Asks Page", "AI panel", "Other"];
    expect(
      sortActionGroups(groups, { pathname: "/acme/customers/cust-1" })[0],
    ).toBe("Customers");
    expect(
      sortActionGroups(groups, { pathname: "/acme/reviews/for-you" })[0],
    ).toBe("Reviews");
    expect(sortActionGroups(groups, { pathname: "/acme/asks" })[0]).toBe(
      "Asks Page",
    );
    expect(sortActionGroups(groups, { pathname: "/acme/ai" })[0]).toBe(
      "AI panel",
    );
  });

  it("respects allowedActionGroups allow-list from selection bridge", () => {
    expect(
      sortActionGroups(["Issues", "Projects", "Navigation"], {
        allowedActionGroups: ["Issues", "Projects"],
      }),
    ).toEqual(["Issues", "Projects"]);
  });
});
