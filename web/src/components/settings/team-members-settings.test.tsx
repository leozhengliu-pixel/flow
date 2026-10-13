import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  setTeamMembership: vi.fn().mockResolvedValue(undefined),
}));

import { I18nProvider } from "@/i18n/i18n";
import { zhCN } from "@/i18n/translations";
import { makeBootstrap, teammate, viewer } from "@/test/fixtures";
import type { TeamMember, User, WorkspaceMember } from "@/types/flow";
import { TeamMembersSettings } from "./team-members-settings";

const person = (id: string, displayName: string) =>
  ({ id, name: displayName.toLowerCase(), displayName, email: `${id}@example.test`, active: true }) as User;
const synced = person("user-sync", "Synced person");
const provisioned = person("user-scim", "Provisioned person");
const integration = person("user-api", "Api person");
const invited = person("user-invite", "Invited person");

function member(user: User): WorkspaceMember {
  return { user, role: "member", status: "active", joinedAt: "2026-09-27T18:25:59.000Z" };
}

function membership(userId: string, extra: Partial<TeamMember> = {}): TeamMember {
  return { teamId: "team-1", userId, role: "member", joinedAt: "2026-09-27T18:25:59.000Z", ...extra };
}

function renderMembers(teamMembers: TeamMember[]) {
  const users = [viewer, teammate, synced, provisioned, integration, invited];
  const data = makeBootstrap({
    viewerRole: "admin",
    users,
    members: users.map(member),
    teamMembers,
  });
  return render(
    <I18nProvider>
      <TeamMembersSettings data={data} team={data.teams[0]} onReload={vi.fn().mockResolvedValue(undefined)} />
    </I18nProvider>,
  );
}

function row(name: string) {
  const strong = screen.getByText(name);
  const element = strong.closest(".settings-member-directory-row");
  if (!(element instanceof HTMLElement)) throw new Error(`row for ${name} missing`);
  return element;
}

describe("team member origins", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it("notes members a sync, SCIM, the API or an invitation added, and not manual ones", () => {
    renderMembers([
      membership(viewer.id, { role: "owner" }),
      membership(teammate.id),
      membership(synced.id, { origin: "sync" }),
      // SCIM-managed rows read as SCIM even without a stored origin.
      membership(provisioned.id, { managed: true, managedSource: "scim" }),
      membership(integration.id, { origin: "api" }),
      membership(invited.id, { origin: "invitation" }),
    ]);
    expect(within(row("Synced person")).getByText("Added by sync")).toHaveAttribute("data-origin", "sync");
    expect(within(row("Provisioned person")).getByText("Synced from SCIM")).toHaveAttribute("data-origin", "scim");
    expect(within(row("Api person")).getByText("Added via API")).toBeInTheDocument();
    expect(within(row("Invited person")).getByText("Joined by invitation")).toBeInTheDocument();
    expect(row("Teammate").querySelector(".settings-member-origin")).toBeNull();
    expect(row("Viewer").querySelector(".settings-member-origin")).toBeNull();

    const note = screen.getByRole("note");
    expect(within(note).getByText("Who a sync can remove")).toBeInTheDocument();
    expect(note).toHaveTextContent("Team owners and members added in Flow by hand or by invitation are always kept");
  });

  it("explains on hover which syncs may remove the member", async () => {
    const user = userEvent.setup();
    renderMembers([membership(viewer.id, { role: "owner" }), membership(synced.id, { origin: "sync" })]);
    await user.hover(screen.getByText("Added by sync"));
    expect(
      (await screen.findAllByText("Added by a directory sync through the API. That sync may remove this member again; team owners are kept."))[0],
    ).toBeInTheDocument();
  });

  it("shows no sync rules on a team only people manage", () => {
    renderMembers([membership(viewer.id, { role: "owner" }), membership(teammate.id, { origin: "manual" }), membership(invited.id, { origin: "invitation" })]);
    expect(screen.queryByText("Who a sync can remove")).toBeNull();
    expect(screen.getByText("Joined by invitation")).toBeInTheDocument();
  });

  it("is localized in zh-CN", async () => {
    localStorage.setItem("flow:locale", "zh-CN");
    renderMembers([membership(viewer.id, { role: "owner" }), membership(synced.id, { origin: "sync" }), membership(provisioned.id, { origin: "scim" })]);
    expect(await screen.findByText("由同步添加")).toBeInTheDocument();
    expect(screen.getByText("由 SCIM 同步")).toBeInTheDocument();
    expect(screen.getByText("同步可以移除哪些成员")).toBeInTheDocument();
    for (const key of [
      "Synced from SCIM",
      "Added by sync",
      "Added via API",
      "Joined by invitation",
      "Added by the SCIM group mapped to this team. SCIM removes this member when they leave the group, unless they are a team owner.",
      "Added by a directory sync through the API. That sync may remove this member again; team owners are kept.",
      "Added by an integration through the API. Syncs that remove only their own members keep this member.",
      "Joined by accepting an invitation to this team. Syncs keep this member.",
      "By hand",
      "Via API",
      "Via agent",
      "Via sync",
      "Via invitation",
      "Via SCIM",
    ]) {
      expect(zhCN[key], key).toBeTruthy();
    }
  });
});
