import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import type { IntegrationConnection, JiraLink } from "@/types/flow";

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  authorizeIntegration: vi.fn(),
  connectIntegration: vi.fn(),
  disconnectIntegration: vi.fn(),
  startIntegrationOAuth: vi.fn(),
}));

import { JiraSettingsPage } from "./jira-settings";

const connection = {
  id: "jira-1",
  provider: "jira",
  name: "Jira Cloud",
  status: "configured",
  config: { mode: "cloud" },
  connectedBy: "user",
  createdAt: "2026-09-20T00:00:00Z",
  updatedAt: "2026-09-20T00:00:00Z",
  scopes: [],
  channels: [],
  linkbackEnabled: false,
  deliveryAttempts: 0,
} as IntegrationConnection;

it("shows honest secret guidance before OAuth and opens sync when connected", () => {
  const onOpenSyncNew = vi.fn();
  const { rerender } = render(
    <I18nProvider>
      <JiraSettingsPage
        data={makeBootstrap({ integrationConnections: [] })}
        onBack={vi.fn()}
        onReload={vi.fn()}
        onOpenSyncNew={onOpenSyncNew}
        onOpenSyncEdit={vi.fn()}
      />
    </I18nProvider>,
  );
  expect(screen.getByText(/Deployment secrets required/i)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Connect with OAuth/i })).toBeInTheDocument();
  expect(screen.getByText(/Linear pixel follow-up/i)).toBeInTheDocument();

  rerender(
    <I18nProvider>
      <JiraSettingsPage
        data={makeBootstrap({
          integrationConnections: [{ ...connection, status: "connected" }],
          jiraLinks: [],
        })}
        onBack={vi.fn()}
        onReload={vi.fn()}
        onOpenSyncNew={onOpenSyncNew}
        onOpenSyncEdit={vi.fn()}
      />
    </I18nProvider>,
  );
  expect(screen.getByText(/No Jira project links/i)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /Create Jira link/i }));
  expect(onOpenSyncNew).toHaveBeenCalled();
});

it("lists existing links for edit", () => {
  const onOpenSyncEdit = vi.fn();
  const link = {
    id: "link-1",
    jiraProjectId: "10000",
    jiraProjectKey: "OPS",
    jiraProjectName: "Operations",
    teamId: "team-1",
    syncDirection: "bidirectional",
    statusMap: {},
    createdAt: "2026-09-20T00:00:00Z",
    updatedAt: "2026-09-20T00:00:00Z",
  } as JiraLink;
  render(
    <I18nProvider>
      <JiraSettingsPage
        data={makeBootstrap({
          integrationConnections: [{ ...connection, status: "connected" }],
          jiraLinks: [link],
          teams: [{ id: "team-1", key: "OPS", name: "Ops", color: "#5e6ad2" } as never],
        })}
        onBack={vi.fn()}
        onReload={vi.fn()}
        onOpenSyncNew={vi.fn()}
        onOpenSyncEdit={onOpenSyncEdit}
      />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: /Operations/i }));
  expect(onOpenSyncEdit).toHaveBeenCalledWith("10000");
});
