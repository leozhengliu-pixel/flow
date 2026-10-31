import { describe, expect, it } from "vitest";
import type { User, WorkspaceMember } from "@/types/flow";
import {
  availableMemberFilters,
  emptyMembersMessage,
  memberGroup,
  memberMatchesFilter,
  memberSearchQuery,
  memberSelectable,
  memberStatusLabel,
  sortMemberEntries,
  type MemberEntry,
} from "./workspace-members";

const user = (id: string, displayName: string, extra: Partial<User> = {}) => ({ id, name: id, displayName, email: `${id}@example.test`, active: true, ...extra }) as User;
const member = (input: Partial<WorkspaceMember> & { user: User }): WorkspaceMember => ({ role: "member", status: "active", joinedAt: "2026-09-27T00:00:00.000Z", ...input });

const admin = member({ user: user("admin", "Ada"), role: "admin" });
const person = member({ user: user("person", "Bob") });
const suspendedGuest = member({ user: user("guest", "Cy"), role: "guest", status: "suspended" });
const app = member({ user: user("app_1", "Bot", { app: true, email: "" }), role: "app" });
const suspendedApp = member({ user: user("app_2", "Old bot", { app: true, email: "" }), role: "app", status: "suspended" });
const departedApp = member({ user: user("app_3", "Gone bot"), role: "app", status: "departed", lastSeenAt: "2026-10-13T00:00:00.000Z" });

describe("workspace members", () => {
  it("groups and labels rows", () => {
    expect([admin, person, suspendedGuest, app, suspendedApp, departedApp].map(memberGroup)).toEqual([
      "Active", "Active", "Inactive", "Application", "Application", "Inactive",
    ]);
    expect([admin, person, suspendedGuest, app, suspendedApp, departedApp].map(memberStatusLabel)).toEqual([
      "Admin", "Member", "Suspended", "Application", "Application (Suspended)", "Left workspace",
    ]);
  });

  it("filters, with departed app users no longer counted as applications", () => {
    const all = [admin, person, suspendedGuest, app, suspendedApp, departedApp];
    expect(all.filter((item) => memberMatchesFilter(item, "Applications"))).toEqual([app, suspendedApp]);
    expect(all.filter((item) => memberMatchesFilter(item, "Suspended"))).toEqual([suspendedGuest, suspendedApp]);
    expect(all.filter((item) => memberMatchesFilter(item, "Left workspace"))).toEqual([departedApp]);
    expect(availableMemberFilters(false, true)).toEqual(["All", "Admins", "Members", "Guests"]);
    expect(availableMemberFilters(true, false)).not.toContain("Applications");
    expect(emptyMembersMessage("Left workspace", true)).toBe("No matching departed users");
    expect(emptyMembersMessage("All", false)).toBe("No members");
    expect(memberSearchQuery(" Q ")).toBe("");
    expect(memberSearchQuery(" QA ")).toBe("qa");
  });

  it("lets admins select active or suspended people, one kind at a time", () => {
    expect(memberSelectable(person, true)).toBe(true);
    expect(memberSelectable(suspendedGuest, true)).toBe(true);
    expect(memberSelectable(app, true)).toBe(false);
    expect(memberSelectable(departedApp, true)).toBe(false);
    expect(memberSelectable(person, false)).toBe(false);
    expect(memberSelectable(suspendedGuest, true, true)).toBe(false);
    expect(memberSelectable(person, true, false)).toBe(false);
  });

  it("sorts by status priority and puts online people last when ordering by last seen", () => {
    const entries: MemberEntry[] = [suspendedGuest, person, admin].map((item) => ({ kind: "member", key: item.user.id, member: item }));
    const context = { descending: false, isOnline: (item: WorkspaceMember) => item === admin, now: Date.parse("2026-10-14T00:00:00.000Z") };
    expect(sortMemberEntries(entries, { ...context, sort: "status" }).map((entry) => entry.key)).toEqual(["admin", "person", "guest"]);
    expect(sortMemberEntries(entries, { ...context, sort: "role" }).map((entry) => entry.key)).toEqual(["admin", "guest", "person"]);
    expect(sortMemberEntries(entries, { ...context, sort: "lastSeen" }).map((entry) => entry.key)).toEqual(["guest", "person", "admin"]);
    expect(sortMemberEntries(entries, { ...context, sort: "name", descending: true }).map((entry) => entry.key)).toEqual(["guest", "person", "admin"]);
  });
});
