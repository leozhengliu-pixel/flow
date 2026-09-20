import { describe, expect, it } from "vitest";
import { routeInfo } from "./route-info";
import type { BootstrapData } from "@/types/flow";

const data = {
  workspace: { id: "w1", name: "Acme", urlKey: "acme" },
  issues: [
    {
      id: "i1",
      identifier: "ACM-1",
      title: "Ship welcome",
      team: { id: "t1", key: "ACM", name: "Acme" },
    },
  ],
  projects: [
    { id: "p1", slugId: "roadmap", name: "Roadmap" },
  ],
  teams: [{ id: "t1", key: "ACM", name: "Acme Team" }],
  notifications: [{ id: "n1" }, { id: "n2", readAt: "2026-01-01" }],
} as unknown as BootstrapData;

describe("routeInfo", () => {
  it("resolves inbox, my-issues, issue, project, settings", () => {
    expect(routeInfo({ kind: "inbox", workspaceSlug: "acme" }, data)).toEqual(
      expect.objectContaining({ title: "Inbox (1)", pinnedTitle: "Inbox" }),
    );
    expect(
      routeInfo(
        { kind: "my-issues", workspaceSlug: "acme", view: "assigned" },
        data,
      ),
    ).toEqual(expect.objectContaining({ title: "My issues" }));
    expect(
      routeInfo(
        { kind: "my-issues", workspaceSlug: "acme", view: "created" },
        data,
      ).title,
    ).toBe("My issues › Created");
    expect(
      routeInfo(
        { kind: "issue", workspaceSlug: "acme", identifier: "ACM-1" },
        data,
      ).title,
    ).toBe("ACM-1 Ship welcome");
    expect(
      routeInfo(
        {
          kind: "project",
          workspaceSlug: "acme",
          projectSlugId: "roadmap",
          tab: "overview",
        },
        data,
      ).title,
    ).toBe("Roadmap");
    expect(
      routeInfo(
        { kind: "settings", workspaceSlug: "acme", page: "profile" },
        data,
      ).title,
    ).toBe("Profile");
  });

  it("skips billing settings titles", () => {
    expect(
      routeInfo(
        { kind: "settings", workspaceSlug: "acme", page: "billing" as never },
        data,
      ).title,
    ).toBe("Settings");
  });
});
