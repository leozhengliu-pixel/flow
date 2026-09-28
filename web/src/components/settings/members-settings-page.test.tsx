import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";

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
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap, teammate, viewer } from "@/test/fixtures";
import type { User, WorkspaceMember } from "@/types/flow";
import { SettingsPage } from "./settings-page";

const agent = {
  id: "app-1",
  name: "helper-bot",
  displayName: "Helper bot",
  email: "",
  active: true,
  app: true,
} as User;
const guest = {
  id: "user-3",
  name: "guest",
  displayName: "Guest person",
  email: "guest@example.test",
  active: true,
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
      teamMembers: [
        { teamId: "team-1", userId: viewer.id, role: "member", joinedAt: "" },
      ],
      invitations: [
        {
          id: "invite-1",
          workspaceId: "workspace-1",
          email: "pending@example.test",
          role: "member",
          teamIds: [],
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
    <I18nProvider>
      <SettingsPage {...input} />
    </I18nProvider>,
  );
  return input;
}

function groups() {
  return [...document.querySelectorAll(".settings-members-group")].map(
    (group) => group.textContent,
  );
}

beforeEach(() => {
  localStorage.setItem("flow:locale", "en");
  Object.values(api).forEach((mock) => mock.mockClear());
});

it("renders the members table with Linear's groups, headers and status badges", () => {
  renderMembers();

  expect(groups()).toEqual([
    "Active2",
    "Application1",
    "Pending invites1",
    "Suspended1",
  ]);
  expect(
    screen.getByRole("button", { name: "Order by Name, sorted ascending" }),
  ).toBeVisible();
  expect(screen.getByRole("button", { name: "Order by Email" })).toBeVisible();
  expect(screen.queryByRole("button", { name: /Order by Teams/ })).toBeNull();
  expect(screen.getByRole("button", { name: "Member filter" })).toHaveTextContent(
    "All",
  );

  const viewerRow = screen
    .getByRole("checkbox", { name: "Select viewer" })
    .closest(".settings-member-directory-row") as HTMLElement;
  expect(within(viewerRow).getByText("Admin")).toHaveClass(
    "settings-member-role",
    "is-admin",
  );
  expect(within(viewerRow).getByText("Online")).toBeVisible();
  expect(within(viewerRow).getByRole("link", { name: "Viewer" })).toHaveAttribute(
    "href",
    "/workspace/profiles/viewer",
  );
  const appRow = screen
    .getByRole("checkbox", { name: "Select helper-bot" })
    .closest(".settings-member-directory-row") as HTMLElement;
  expect(within(appRow).getByText("Application")).not.toHaveClass(
    "settings-member-role",
  );
});

it("filters members from the 142px filter menu", async () => {
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
  expect(options[0]).toHaveAttribute("aria-checked", "true");

  await user.click(screen.getByRole("menuitemradio", { name: "Admins" }));
  expect(groups()).toEqual(["Active1"]);
  expect(screen.getByRole("button", { name: "Member filter" })).toHaveTextContent(
    "Admins",
  );

  await user.click(screen.getByRole("button", { name: "Member filter" }));
  await user.click(screen.getByRole("menuitemradio", { name: "Left workspace" }));
  expect(screen.getByText("No members found")).toBeVisible();
});

it("selects rows, shows the bulk bar and clears with Escape", async () => {
  const user = userEvent.setup();
  renderMembers();

  await user.click(screen.getByRole("checkbox", { name: "Select teammate" }));
  expect(
    screen
      .getByRole("checkbox", { name: "Select teammate" })
      .closest(".settings-member-directory-row"),
  ).toHaveClass("is-selected");
  expect(screen.getByRole("toolbar")).toHaveTextContent("1 selected");
  expect(screen.getByRole("button", { name: "Open command menu" })).toBeVisible();

  await user.keyboard("{Escape}");
  expect(screen.queryByRole("toolbar")).toBeNull();

  await user.click(screen.getByRole("checkbox", { name: "Select teammate" }));
  await user.click(screen.getByRole("button", { name: "Clear selected" }));
  expect(screen.queryByRole("toolbar")).toBeNull();
});

it("runs bulk actions on the selection after confirmation", async () => {
  const user = userEvent.setup();
  const input = renderMembers();

  await user.click(screen.getByRole("checkbox", { name: "Select teammate" }));
  await user.click(screen.getByRole("button", { name: "Open command menu" }));
  await user.click(screen.getByRole("menuitem", { name: "Suspend user…" }));
  const dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveTextContent("Suspend Teammate?");
  await user.click(within(dialog).getByRole("button", { name: "Suspend" }));

  await waitFor(() =>
    expect(api.suspendMember).toHaveBeenCalledWith("workspace", teammate.id),
  );
  expect(input.onReload).toHaveBeenCalled();
});

it("offers self only team actions and others role and removal actions", async () => {
  const user = userEvent.setup();
  renderMembers();

  await user.click(screen.getByRole("button", { name: "Open menu Viewer" }));
  expect(screen.getByRole("menuitem", { name: "Add to teams…" })).toHaveAttribute(
    "aria-disabled",
    "true",
  );
  expect(screen.queryByRole("menuitem", { name: "Remove from workspace…" })).toBeNull();
  await user.keyboard("{Escape}");

  await user.click(screen.getByRole("button", { name: "Open menu Teammate" }));
  expect(screen.getByRole("menuitem", { name: "Add to teams…" })).not.toHaveAttribute(
    "aria-disabled",
  );
  expect(screen.getByRole("menuitem", { name: "Change role" })).toBeVisible();
  await user.click(screen.getByRole("menuitem", { name: "Remove from workspace…" }));
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("button", { name: "Remove" }));
  await waitFor(() =>
    expect(api.removeMember).toHaveBeenCalledWith("workspace", teammate.id),
  );
});

it("adds a member to the teams they are missing", async () => {
  const user = userEvent.setup();
  renderMembers();

  await user.click(screen.getByRole("button", { name: "Open menu Teammate" }));
  await user.click(screen.getByRole("menuitem", { name: "Add to teams…" }));
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("checkbox"));
  await user.click(within(dialog).getByRole("button", { name: "Add" }));
  await waitFor(() =>
    expect(api.setTeamMembership).toHaveBeenCalledWith(
      "workspace",
      "team-1",
      teammate.id,
      true,
      "member",
    ),
  );
});
