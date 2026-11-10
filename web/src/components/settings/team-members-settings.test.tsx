import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  setTeamMembership: vi.fn().mockResolvedValue(undefined),
}));

import { I18nProvider } from "@/i18n/i18n";
import { zhCN } from "@/i18n/translations";
import { avatarColor } from "@/lib/avatar-color";
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

  it("lets team members add members when Member management allows all team members", async () => {
    const users = [viewer, teammate, synced];
    const render_ = (memberPermission: "allMembers" | "owners") => {
      const data = makeBootstrap({ viewerRole: "member", users, members: users.map(member), teamMembers: [membership(viewer.id), membership(teammate.id, { role: "owner" })] });
      data.teamSettings = { "team-1": { ...data.teamSettings?.["team-1"], teamId: "team-1", memberPermission } } as typeof data.teamSettings;
      return render(<I18nProvider><TeamMembersSettings data={data} team={data.teams[0]} onReload={vi.fn().mockResolvedValue(undefined)} /></I18nProvider>);
    };
    const allowed = render_("allMembers");
    expect(screen.getByRole("button", { name: "Add team members" })).toBeEnabled();
    // Owner roles stay with team owners: a team member gets no menu on the owner's row.
    expect(within(row(teammate.displayName)).queryByRole("button", { name: /Open menu/ })).toBeNull();
    allowed.unmount();
    render_("owners");
    expect(screen.getByRole("button", { name: "Add team members" })).toBeDisabled();
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

describe("team member avatars", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  function avatarOf(name: string) {
    const avatar = row(name).querySelector<HTMLElement>(".settings-member-avatar");
    if (!avatar) throw new Error(`avatar for ${name} missing`);
    return avatar;
  }

  function renderWith(users: User[]) {
    const data = makeBootstrap({
      viewerRole: "admin",
      users,
      members: users.map(member),
      teamMembers: users.map((user) => membership(user.id)),
    });
    return render(
      <I18nProvider>
        <TeamMembersSettings data={data} team={data.teams[0]} onReload={vi.fn().mockResolvedValue(undefined)} />
      </I18nProvider>,
    );
  }

  it("draws a member's uploaded picture instead of initials", () => {
    renderWith([{ ...person("user-photo", "Photo person"), avatarUrl: "/uploads/photo.png" }]);
    const avatar = avatarOf("Photo person");
    expect(avatar.querySelector("img")).toHaveAttribute("src", "/uploads/photo.png");
    expect(avatar).not.toHaveTextContent("PP");
  });

  it("draws an OAuth app agent's application icon", () => {
    renderWith([{ ...person("app_1", "Review bot"), app: true, avatarUrl: "/uploads/app-icon.svg" }]);
    expect(avatarOf("Review bot").querySelector("img")).toHaveAttribute("src", "/uploads/app-icon.svg");
  });

  it("shows initials on the person's colour when there is no picture", () => {
    renderWith([person("user-plain", "Plain person")]);
    const avatar = avatarOf("Plain person");
    expect(avatar.querySelector("img")).toBeNull();
    expect(avatar).toHaveTextContent("PP");
    expect(avatar.style.getPropertyValue("--avatar")).toBe(avatarColor("user-plain"));
  });

  it("shows one character of a Chinese name", () => {
    renderWith([person("user-cjk", "张伟")]);
    const avatar = avatarOf("张伟");
    expect(avatar).toHaveTextContent(/^张$/);
    expect(avatar).toHaveAttribute("data-script", "cjk");
    expect(avatar.style.getPropertyValue("--avatar")).toBe(avatarColor("user-cjk"));
  });
});

describe("team members table", () => {
  beforeEach(() => localStorage.clear());

  it("sorts by a column header and lists the sub-teams a member also belongs to", async () => {
    const users = [viewer, teammate];
    const data = makeBootstrap({
      viewerRole: "admin",
      users,
      members: users.map(member),
      teams: [
        { id: "team-1", key: "TM1", name: "Team one", color: "#777777" },
        { id: "team-sub", key: "SUB", name: "Sub team", color: "#aa8800", icon: "Team" },
      ] as never,
      teamSettings: { "team-sub": { teamId: "team-sub", parentTeamId: "team-1" } } as never,
      teamMembers: [membership(viewer.id), membership(teammate.id), { ...membership(teammate.id), teamId: "team-sub" }],
    });
    render(
      <I18nProvider>
        <TeamMembersSettings data={data} team={data.teams[0]} onReload={vi.fn().mockResolvedValue(undefined)} />
      </I18nProvider>,
    );
    const names = () => Array.from(document.querySelectorAll(".settings-member-directory-row strong")).map((node) => node.textContent);
    const ascending = names();
    expect(ascending).toEqual([...ascending].sort((left, right) => (left ?? "").localeCompare(right ?? "")));
    await userEvent.setup().click(screen.getByRole("button", { name: /Order by Name/ }));
    expect(names()).toEqual([...ascending].reverse());
    expect(within(row(teammate.displayName)).getByRole("link", { name: "Open Sub team" })).toHaveAttribute("href", expect.stringContaining("/settings/teams/SUB/members"));
    expect(within(row(viewer.displayName)).queryByRole("link", { name: /Open/ })).toBeNull();
  });
});
