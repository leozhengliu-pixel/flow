import { render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap, viewer } from "@/test/fixtures";
import type { AuditLogEntry } from "@/types/flow";
import { AuditLogSettings } from "./audit-log-settings";

afterEach(() => localStorage.clear());

const removal: AuditLogEntry = {
  id: "audit_2",
  actor: viewer,
  action: "removed",
  resourceType: "team_member",
  resourceId: "user-9",
  metadata: { teamId: "team-1", teamKey: "ENG", userId: "user-9", userName: "Ada", origin: "sync", source: "sync", roleBefore: "member" },
  createdAt: "2026-10-13T08:00:00.000Z",
};

function renderLog(entries: AuditLogEntry[]) {
  // Members (not admins) see the log they were given without a refetch.
  const data = makeBootstrap({ viewerRole: "member", auditLog: entries });
  return render(
    <I18nProvider>
      <AuditLogSettings data={data} />
    </I18nProvider>,
  );
}

it("names the member, team and source of team membership changes", () => {
  renderLog([removal, { ...removal, id: "audit_1", resourceType: "webhook", action: "created", metadata: {} }]);
  expect(screen.getByText("Ada · ENG")).toBeInTheDocument();
  expect(screen.getByText(/Via sync/)).toBeInTheDocument();
  expect(screen.getByText("team member")).toBeInTheDocument();
  expect(screen.getByText("webhook")).toBeInTheDocument();
});

it("names the member or group of SCIM provisioning entries with their source", () => {
  const scimActor = { id: "scim", name: "SCIM", displayName: "SCIM provisioning", email: "", active: true, emailVerified: false };
  renderLog([
    { id: "audit_4", actor: scimActor, action: "deprovisioned", resourceType: "workspace_member", resourceId: "user-9", metadata: { userName: "Grace", source: "scim", deprovisionAction: "remove" }, createdAt: "2026-10-13T09:00:00.000Z" },
    { id: "audit_3", actor: scimActor, action: "members_changed", resourceType: "scim_group", resourceId: "scimg_1", metadata: { scimGroupName: "Engineering", source: "scim" }, createdAt: "2026-10-13T08:30:00.000Z" },
  ] as AuditLogEntry[]);
  expect(screen.getByText("Grace")).toBeInTheDocument();
  expect(screen.getByText("Engineering")).toBeInTheDocument();
  expect(screen.getAllByText(/Via SCIM/)).toHaveLength(2);
  expect(screen.getByText("workspace member")).toBeInTheDocument();
  expect(screen.getByText("scim group")).toBeInTheDocument();
});

it("translates the change source in zh-CN", async () => {
  localStorage.setItem("flow:locale", "zh-CN");
  renderLog([{ ...removal, metadata: { ...removal.metadata, source: "scim" } }]);
  expect(await screen.findByText(/通过 SCIM/)).toBeInTheDocument();
});
