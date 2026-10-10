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

  it("does not title the retired shortcuts route as a settings page", () => {
    expect(routeInfo(
      { kind: "settings", workspaceSlug: "acme", page: "shortcuts" },
      data,
    ).title).toBe("Not found");
  });
});

describe("routeInfo titles", () => {
  it("names settings and workspace pages in the viewer's language", async () => {
    const { translateToChinese } = await import("@/i18n/i18n");
    const zh = (route: Parameters<typeof routeInfo>[0]) => routeInfo(route, data, translateToChinese).title;
    expect(zh({ kind: "settings", workspaceSlug: "acme", page: "coding-sessions" })).toBe("编码会话");
    expect(zh({ kind: "settings", workspaceSlug: "acme", page: "account-security" })).toBe(translateToChinese("Security & access"));
    expect(zh({ kind: "workspace-teams", workspaceSlug: "acme" })).toBe(translateToChinese("Teams"));
    expect(zh({ kind: "inbox", workspaceSlug: "acme" })).toBe(`${translateToChinese("Inbox")} (1)`);
    // English keeps Linear's sentence-case page names.
    expect(routeInfo({ kind: "settings", workspaceSlug: "acme", page: "coding-sessions" }, data).title).toBe("Coding sessions");
    expect(routeInfo({ kind: "team-cycles", workspaceSlug: "acme", teamKey: "ACM" }, data).title).toBe("Acme Team › Cycles");
  });
});

describe("custom view titles", () => {
  it("uses the view name and Linear's \"<name> > Edit\" while editing", () => {
    const savedViews = [{ id: "view-1", slugId: "urgent-view-1", name: "Urgent" }] as BootstrapData["savedViews"];
    expect(routeInfo({ kind: "workspace-saved-view", workspaceSlug: "w", viewId: "urgent-view-1" }, { savedViews }).title).toBe("Urgent");
    expect(routeInfo({ kind: "team-saved-view", workspaceSlug: "w", teamKey: "T", viewId: "view-1", editing: true }, { savedViews }).title).toBe("Urgent > Edit");
  });
});

describe("customer page titles", () => {
  it("names the browser tab after the customer, like the reference", () => {
    const customers = [{ id: "customer_1791431475874274000", name: "Acme Corp" }] as BootstrapData["customers"];
    expect(routeInfo({ kind: "customer", workspaceSlug: "w", customerSlugId: "acme-corp-475874274000" }, { customers }).title).toBe("Acme Corp");
    expect(routeInfo({ kind: "customer", workspaceSlug: "w", customerSlugId: "missing-000000000000" }, { customers }).title).toBe("Customers");
  });
});

describe("routeInfo documents", () => {
  const documents = [
    { id: "d1", slugId: "plan-abc", title: "Plan" },
    { id: "d2", slugId: "untitled-def", title: "  " },
  ];
  const withDocuments = { ...data, documents } as unknown as BootstrapData;

  it("uses the document title for the tab and falls back to Untitled when it is empty", () => {
    expect(routeInfo({ kind: "document", workspaceSlug: "acme", documentSlugId: "plan-abc" }, withDocuments).title).toBe("Plan");
    expect(routeInfo({ kind: "document", workspaceSlug: "acme", documentSlugId: "d2" }, withDocuments).title).toBe("Untitled");
    expect(routeInfo({ kind: "document", workspaceSlug: "acme", documentSlugId: "missing" }, withDocuments).title).toBe("Documents");
  });

  it("keeps the title of a deleted document that is still in the trash", () => {
    const trash = [{ id: "t1", resourceType: "document", resourceId: "d9", title: "Gone", payload: { id: "d9", slugId: "gone-abc", title: "Gone doc" }, deletedBy: {}, deletedAt: "2026-01-01T00:00:00Z", expiresAt: "2026-02-01T00:00:00Z" }];
    expect(routeInfo({ kind: "document", workspaceSlug: "acme", documentSlugId: "gone-abc" }, { ...withDocuments, trash } as unknown as BootstrapData).title).toBe("Gone doc");
  });
});
