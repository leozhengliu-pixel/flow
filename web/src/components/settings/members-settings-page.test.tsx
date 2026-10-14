import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import { MemoryRouter } from "react-router-dom";

vi.hoisted(() => {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  });
});

const api = vi.hoisted(() => ({
  removeMember: vi.fn().mockResolvedValue(undefined),
  suspendMember: vi.fn().mockResolvedValue(undefined),
  resumeMember: vi.fn().mockResolvedValue(undefined),
  updateMemberRole: vi.fn().mockResolvedValue(undefined),
  setTeamMembership: vi.fn().mockResolvedValue(undefined),
  resendInvitation: vi.fn().mockResolvedValue(undefined),
  revokeInvitation: vi.fn().mockResolvedValue(undefined),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));
const apps = vi.hoisted(() => ({ uninstallApplication: vi.fn().mockResolvedValue(undefined) }));
vi.mock("@/lib/application-agents", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/application-agents")>()),
  ...apps,
}));

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap, teammate, viewer } from "@/test/fixtures";
import type { User, WorkspaceMember } from "@/types/flow";
import { SettingsPage } from "./settings-page";

const agent = {
  id: "app_0123456789abcdef0123456789abcdef",
  name: "helper-bot",
  displayName: "Helper bot",
  email: "",
  active: true,
  app: true,
  oauthClientId: "client-helper",
} as User;
const guest = {
  id: "user-3",
  name: "guest",
  displayName: "Guest person",
  email: "guest@example.test",
  active: true,
} as User;
const departedApp = {
  id: "app_975ccd3a6df34a109a93521534c74ff7",
  name: "QA OAuth – install flow",
  displayName: "QA OAuth – install flow",
  username: "qaoauthinstallflow",
  email: "975ccd3a-6df3-4a10-9a93-521534c74ff7@oauthapp.localhost",
  active: false,
} as User;

function member(
  user: User,
  role: WorkspaceMember["role"],
  status: WorkspaceMember["status"] = "active",
): WorkspaceMember {
  return { user, role, status, joinedAt: "2026-09-27T18:25:59.000Z" };
}

function props(): ComponentProps<typeof SettingsPage> {
  return {
    data: makeBootstrap({
      viewerRole: "admin",
      users: [viewer, teammate, guest, agent],
      members: [
        member(viewer, "admin"),
        member(teammate, "member"),
        member(guest, "guest", "suspended"),
        member(agent, "app"),
      ],
      departedMembers: [
        { ...member(departedApp, "app", "departed"), lastSeenAt: "2026-10-13T09:00:00.000Z" },
      ],
      teamMembers: [
        { teamId: "team-1", userId: viewer.id, role: "member", joinedAt: "" },
      ],
      invitations: [
        {
          id: "invite-1",
          workspaceId: "workspace-1",
          email: "pending@example.test",
          role: "member",
          teamIds: ["team-1"],
          status: "pending",
          inviterId: viewer.id,
          expiresAt: "2026-10-05T00:00:00.000Z",
          createdAt: "2026-09-28T00:00:00.000Z",
        },
      ],
      oauthApplications: [],
      workspaceSettings: {},
      userSettings: {},
    } as never),
    page: "members" as const,
    onBack: vi.fn(),
    onNavigate: vi.fn(),
    onReload: vi.fn().mockResolvedValue(undefined),
  } as unknown as ComponentProps<typeof SettingsPage>;
}

function renderMembers(input = props()) {
  render(
    <MemoryRouter>
      <I18nProvider>
        <SettingsPage {...input} />
      </I18nProvider>
    </MemoryRouter>,
  );
  return input;
}

function groups() {
  return [...document.querySelectorAll(".settings-members-group")].map(
    (group) => group.textContent,
  );
}

function row(text: string) {
  return screen
    .getAllByText(text)[0]
    .closest(".settings-member-directory-row") as HTMLElement;
}

async function menuItems(user: ReturnType<typeof userEvent.setup>, label: string) {
  await user.click(screen.getByRole("button", { name: `Open menu ${label}` }));
  const items = screen.getAllByRole("menuitem").map((item) => item.textContent);
  await user.keyboard("{Escape}");
  return items;
}

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("flow:locale", "en");
  Object.values(api).forEach((mock) => mock.mockClear());
  apps.uninstallApplication.mockClear();
});

it("groups rows: Active, Invited, Application, Inactive", () => {
  renderMembers();

  expect(groups()).toEqual(["Active2", "Invited1", "Application1", "Inactive2"]);
  expect(
    screen.getByRole("button", { name: "Order by Name, sorted ascending" }),
  ).toBeVisible();
  expect(screen.queryByRole("button", { name: /Order by Teams/ })).toBeNull();

  const viewerRow = row("Viewer");
  expect(within(viewerRow).getByText("Admin")).toHaveClass("settings-member-role", "is-admin");
  expect(within(viewerRow).getByText("Online")).toBeVisible();
  expect(within(viewerRow).getByRole("link", { name: "Viewer" })).toHaveAttribute(
    "href",
    "/workspace/profiles/viewer",
  );
  // Members get the neutral pill; the app user shows "Application" without an email or a checkbox.
  expect(within(row("Teammate")).getByText("Member")).toHaveClass("settings-member-role");
  expect(within(row("Teammate")).getByText("Member")).not.toHaveClass("is-admin");
  const appRow = row("Helper bot");
  expect(within(appRow).getByText("Application")).not.toHaveClass("settings-member-role");
  expect(within(appRow).queryByRole("checkbox")).toBeNull();
  expect(within(appRow).queryByText("@")).toBeNull();

  // An invitation shows its email once in the name column and "<Role> (Invited)".
  const invited = row("pending@example.test");
  expect(within(invited).getByText("Member (Invited)")).toBeVisible();
  expect(within(invited).getByText("1 team")).toBeVisible();
  expect(within(invited).queryByRole("checkbox")).toBeNull();

  // Inactive: the suspended guest and the departed app user, faint, "Left workspace" with its generated email.
  const suspended = row("Guest person");
  expect(suspended).toHaveClass("is-inactive");
  expect(within(suspended).getByText("Suspended")).not.toHaveClass("settings-member-role");
  const departed = row("QA OAuth – install flow");
  expect(departed).toHaveClass("is-inactive");
  expect(within(departed).getByText("Left workspace")).toBeVisible();
  expect(within(departed).getByText("qaoauthinstallflow")).toBeVisible();
  expect(within(departed).getByText(departedApp.email)).toBeVisible();
  expect(within(departed).getByText("Oct 13")).toBeVisible();
  expect(within(departed).queryByRole("checkbox")).toBeNull();
  expect(within(departed).queryByRole("button", { name: /Open menu/ })).toBeNull();
  expect(within(departed).queryByRole("link")).toBeNull();
});

it("offers the reference app's filters and its empty state", async () => {
  const user = userEvent.setup();
  renderMembers();

  await user.click(screen.getByRole("button", { name: "Member filter" }));
  const options = screen.getAllByRole("menuitemradio");
  expect(options.map((option) => option.textContent)).toEqual([
    "All",
    "Admins",
    "Members",
    "Guests",
    "Applications",
    "Pending invites",
    "Suspended",
    "Left workspace",
  ]);
  await user.click(screen.getByRole("menuitemradio", { name: "Admins" }));
  expect(groups()).toEqual(["Active1"]);

  await user.click(screen.getByRole("button", { name: "Member filter" }));
  await user.click(screen.getByRole("menuitemradio", { name: "Left workspace" }));
  expect(groups()).toEqual(["Inactive1"]);
  expect(row("QA OAuth – install flow")).toBeTruthy();

  await user.click(screen.getByRole("button", { name: "Member filter" }));
  await user.click(screen.getByRole("menuitemradio", { name: "Applications" }));
  expect(groups()).toEqual(["Application1"]);

  await user.type(screen.getByLabelText("Search by name or email"), "zz");
  expect(screen.getByText("No matching applications")).toBeVisible();
  expect(screen.queryByRole("button", { name: /Order by/ })).toBeNull();
  await user.click(screen.getByRole("button", { name: "Clear filter" }));
  expect(groups()).toEqual(["Active2", "Invited1", "Application1", "Inactive2"]);
  expect(screen.getByRole("button", { name: "Member filter" })).toHaveTextContent("All");
});

it("ignores one-character searches", async () => {
  const user = userEvent.setup();
  renderMembers();
  await user.type(screen.getByLabelText("Search by name or email"), "q");
  expect(groups()).toEqual(["Active2", "Invited1", "Application1", "Inactive2"]);
  await user.type(screen.getByLabelText("Search by name or email"), "a");
  expect(groups()).toEqual(["Inactive1"]);
});

it("selects active or suspended people, never both, and clears with Escape", async () => {
  const user = userEvent.setup();
  renderMembers();

  await user.click(screen.getByRole("checkbox", { name: "Select teammate" }));
  expect(row("Teammate")).toHaveClass("is-selected");
  expect(screen.getByRole("toolbar")).toHaveTextContent("1 selected");
  // A suspended user cannot join an active selection.
  expect(within(row("Guest person")).queryByRole("checkbox")).toBeNull();

  await user.keyboard("{Escape}");
  expect(screen.queryByRole("toolbar")).toBeNull();
  await user.click(screen.getByRole("checkbox", { name: "Select guest" }));
  expect(within(row("Teammate")).queryByRole("checkbox")).toBeNull();
  await user.click(screen.getByRole("button", { name: "Clear selected" }));
  expect(screen.queryByRole("toolbar")).toBeNull();
});

it("runs the reference app's bulk actions on the selection after confirmation", async () => {
  const user = userEvent.setup();
  const input = renderMembers();

  await user.click(screen.getByRole("checkbox", { name: "Select teammate" }));
  await user.click(screen.getByRole("button", { name: "Open command menu" }));
  expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
    "Add to teams…",
    "Change roles…",
    "Suspend users…",
  ]);
  await user.click(screen.getByRole("menuitem", { name: "Suspend users…" }));
  const dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent("Suspend Teammate?");
  await user.click(within(dialog).getByRole("button", { name: "Suspend" }));

  await waitFor(() => expect(api.suspendMember).toHaveBeenCalledWith("workspace", teammate.id));
  expect(input.onReload).toHaveBeenCalled();
});

it("gives each row the actions the reference app offers for its state", async () => {
  const user = userEvent.setup();
  renderMembers();

  // Your own row has no menu.
  expect(screen.queryByRole("button", { name: "Open menu Viewer" })).toBeNull();
  expect(await menuItems(user, "Teammate")).toEqual([
    "Change role…",
    "Update name…",
    "Update username…",
    "Update email…",
    "Suspend user…",
    "Manage teams…",
    "Remove from workspace…",
  ]);
  expect(await menuItems(user, "Guest person")).toEqual([
    "Activate user…",
    "Update name…",
    "Update username…",
    "Remove from workspace…",
  ]);
  expect(await menuItems(user, "Helper bot")).toEqual(["Manage team access…", "Revoke access…"]);
  expect(await menuItems(user, "pending@example.test")).toEqual(["Resend invite", "Revoke invite"]);
});

it("activates a suspended user and revokes an application's access", async () => {
  const user = userEvent.setup();
  renderMembers();

  await user.click(screen.getByRole("button", { name: "Open menu Guest person" }));
  await user.click(screen.getByRole("menuitem", { name: "Activate user…" }));
  let dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent("Activate Guest person?");
  await user.click(within(dialog).getByRole("button", { name: "Confirm" }));
  await waitFor(() => expect(api.resumeMember).toHaveBeenCalledWith("workspace", guest.id));

  await user.click(screen.getByRole("button", { name: "Open menu Helper bot" }));
  await user.click(screen.getByRole("menuitem", { name: "Revoke access…" }));
  dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent("Revoke access for Helper bot?");
  await user.click(within(dialog).getByRole("button", { name: "Confirm" }));
  await waitFor(() =>
    expect(apps.uninstallApplication).toHaveBeenCalledWith("workspace", "installation_0123456789abcdef0123456789abcdef"),
  );
});

it("manages a member's teams from the row menu", async () => {
  const user = userEvent.setup();
  renderMembers();

  await user.click(screen.getByRole("button", { name: "Open menu Teammate" }));
  await user.click(screen.getByRole("menuitem", { name: "Manage teams…" }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByText("Manage teams")).toBeVisible();
  const checkbox = within(dialog).getByRole("checkbox");
  expect(checkbox).not.toBeChecked();
  await user.click(checkbox);
  expect(dialog).toHaveTextContent("Teammate has full access to these teams");
  await user.click(within(dialog).getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(api.setTeamMembership).toHaveBeenCalledWith("workspace", "team-1", teammate.id, true, "member"),
  );
});

it("removes a member from the workspace", async () => {
  const user = userEvent.setup();
  renderMembers();

  await user.click(screen.getByRole("button", { name: "Open menu Teammate" }));
  await user.click(screen.getByRole("menuitem", { name: "Remove from workspace…" }));
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("button", { name: "Remove" }));
  await waitFor(() => expect(api.removeMember).toHaveBeenCalledWith("workspace", teammate.id));
});

