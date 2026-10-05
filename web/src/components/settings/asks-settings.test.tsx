
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import type { WorkspaceSettings } from "@/types/flow";
import {
  createEmailIntakeAddress,
  deleteEmailIntakeAddress,
  listEmailIntakeAddresses,
  updateEmailIntakeAddress,
  updateWorkspacePreferences,
} from "@/lib/api";
import { confirmAction } from "@/components/ui/action-dialog-service";
import {
  AsksEmailIntakeDetailPage,
  AsksSettingsPage,
  AsksSlackSettingsPage,
  NewAsksEmailIntakePage,
} from "./asks-settings";

vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  updateWorkspacePreferences: vi.fn(),
  createEmailIntakeAddress: vi.fn(),
  verifyEmailIntakeAddress: vi.fn(),
  updateEmailIntakeAddress: vi.fn(),
  listEmailIntakeAddresses: vi.fn().mockResolvedValue([]),
  deleteEmailIntakeAddress: vi.fn(),
  authorizeIntegration: vi.fn(),
  disconnectIntegration: vi.fn(),
}));

vi.mock("@/components/ui/action-dialog-service", async (original) => ({
  ...(await original<typeof import("@/components/ui/action-dialog-service")>()),
  confirmAction: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem("flow:locale", "en-US");
});

const settings = {
  sessionDurationDays: 30,
  featureFlags: { asks: true },
  featureSettings: { asksEmailAddresses: [], asksSlackChannels: [] },
} as unknown as WorkspaceSettings;

it("shows honest Coming soon for web forms and opens Slack deep settings", () => {
  const onOpenSlack = vi.fn();
  const data = makeBootstrap({
    viewerRole: "admin",
    workspaceSettings: settings,
    integrationConnections: [
      {
        id: "slack-1",
        provider: "slack",
        name: "Acme Slack",
        status: "connected",
        config: { scope: "asks" },
        scopes: [],
        channels: ["support"],
        linkbackEnabled: false,
        deliveryAttempts: 0,
        connectedBy: "user-1",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
      },
    ] as never,
    emailIntakeAddresses: [],
  });
  render(
    <I18nProvider>
      <AsksSettingsPage
        data={data}
        settings={settings}
        busy={false}
        setEnabled={vi.fn()}
        setFeature={vi.fn()}
        onReload={vi.fn()}
        onOpenSlack={onOpenSlack}
        onOpenEmailIntake={vi.fn()}
      />
    </I18nProvider>,
  );
  expect(screen.queryByText("Web forms")).toBeNull();
  fireEvent.click(screen.getByText("Acme Slack"));
  expect(onOpenSlack).toHaveBeenCalledWith("slack-1");
});

it("maps Slack channels to teams without billing upgrade copy", async () => {
  const user = userEvent.setup();
  vi.mocked(updateWorkspacePreferences).mockResolvedValue({
    ...settings,
    featureSettings: {
      ...settings.featureSettings,
      asksSlackChannels: [
        { channel: "support", teamId: "team-1", enabled: true },
      ],
    },
  } as WorkspaceSettings);
  const data = makeBootstrap({
    viewerRole: "admin",
    workspaceSettings: settings,
    teams: [{ id: "team-1", name: "Engineering", key: "ENG", color: "#5e6ad2" }],
    issueTemplates: [],
    integrationConnections: [
      {
        id: "slack-1",
        provider: "slack",
        name: "Acme Slack",
        status: "connected",
        channels: ["support"],
        scopes: [],
        linkbackEnabled: false,
        deliveryAttempts: 0,
        connectedBy: "user-1",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
      },
    ] as never,
  });
  render(
    <I18nProvider>
      <AsksSlackSettingsPage
        data={data}
        integrationId="slack-1"
        onBack={vi.fn()}
        onReload={vi.fn()}
      />
    </I18nProvider>,
  );
  expect(screen.getByText("Connected Slack channels")).toBeVisible();
  expect(screen.queryByText(/Upgrade to the/i)).toBeNull();
  expect(screen.getByText("Coming soon")).toBeVisible();
  await user.click(screen.getByRole("combobox", { name: "Team for support" }));
  await user.click(await screen.findByRole("menuitem", { name: "Engineering" }));
  await waitFor(() =>
    expect(updateWorkspacePreferences).toHaveBeenCalledWith(
      {
        featureSettings: {
          asksSlackChannels: [
            { channel: "support", teamId: "team-1", enabled: true },
          ],
        },
      },
      "workspace",
    ),
  );
});

const intake = {
  id: "addr-1",
  teamId: "team-1",
  localPart: "x7k2p9q4mz",
  domain: "intake.flow.app",
  address: "x7k2p9q4mz@intake.flow.app",
  verificationState: "verified" as const,
  aliases: [],
  enabled: true,
  type: "asks" as const,
  system: true,
  customerRequestsEnabled: true,
  dnsRecords: [],
  outboundFromEmail: "issues@flow.app",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

it("walks Linear's email intake wizard: team, address, then optional domain", async () => {
  const user = userEvent.setup();
  vi.mocked(createEmailIntakeAddress).mockResolvedValue({ address: intake, inboundToken: "token", dnsRecord: { type: "TXT", name: "_flow-intake.intake.flow.app", value: "flow-verification=abc" } });
  vi.mocked(updateEmailIntakeAddress)
    .mockResolvedValueOnce({ ...intake, senderName: "Helpdesk", forwardingEmailAddress: "helpdesk@acme.com", dnsRecords: [{ type: "TXT", name: "_flow-intake.acme.com", content: "flow-verification=abc", isVerified: false }] })
    .mockResolvedValueOnce({ ...intake, senderName: "Helpdesk", forwardingEmailAddress: "helpdesk@acme.com" });
  const data = makeBootstrap({
    viewerRole: "admin",
    workspaceSettings: settings,
    teams: [{ id: "team-1", name: "Engineering", key: "ENG", color: "#5e6ad2" }],
  });
  const onOpenAddress = vi.fn();
  const onReload = vi.fn().mockResolvedValue(undefined);
  render(
    <I18nProvider>
      <NewAsksEmailIntakePage data={data} onBack={vi.fn()} onReload={onReload} onOpenAddress={onOpenAddress} />
    </I18nProvider>,
  );
  expect(screen.getByRole("heading", { name: "Add email intake" })).toBeVisible();
  expect(screen.getByRole("heading", { name: "Connect to Flow team" })).toBeVisible();
  expect(screen.getByRole("heading", { name: "Configure email address" })).toBeVisible();
  expect(screen.getByRole("link", { name: "Asks" })).toHaveAttribute("href", "/workspace/settings/asks");
  // Like Linear, the team is required before the address is created.
  await user.click(screen.getByRole("button", { name: "Continue" }));
  expect(screen.getByRole("alert")).toHaveTextContent("Please select a team");
  expect(createEmailIntakeAddress).not.toHaveBeenCalled();
  await user.click(screen.getByRole("combobox", { name: "Team" }));
  await user.click(screen.getByRole("option", { name: "Engineering" }));
  await user.click(screen.getByRole("button", { name: "Continue" }));
  await waitFor(() => expect(createEmailIntakeAddress).toHaveBeenCalledWith("team-1", { type: "asks", templateId: undefined }));
  expect(await screen.findByText("x7k2p9q4mz@intake.flow.app")).toBeVisible();
  // Step 2 requires a sender name and validates the custom address.
  await user.click(screen.getByRole("button", { name: "Continue" }));
  expect(screen.getByRole("alert")).toHaveTextContent("Please enter a name");
  await user.type(screen.getByLabelText("Sender name"), "Helpdesk");
  await user.type(screen.getByPlaceholderText("e.g. helpdesk@acme.com"), "not-an-email");
  await user.click(screen.getByRole("button", { name: "Continue" }));
  expect(screen.getByRole("alert")).toHaveTextContent("Please enter a valid email address");
  await user.clear(screen.getByPlaceholderText("e.g. helpdesk@acme.com"));
  await user.type(screen.getByPlaceholderText("e.g. helpdesk@acme.com"), "helpdesk@acme.com");
  await user.click(screen.getByRole("button", { name: "Continue" }));
  await waitFor(() => expect(updateEmailIntakeAddress).toHaveBeenCalledWith("team-1", "addr-1", { teamId: "team-1", templateId: "", senderName: "Helpdesk", forwardingEmailAddress: "helpdesk@acme.com" }));
  // Step 3 shows the DNS records for the custom domain.
  expect(await screen.findByText("_flow-intake.acme.com")).toBeVisible();
  expect(screen.getByText("Unverified")).toBeVisible();
  expect(screen.getByText("issues@flow.app")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Done" }));
  await waitFor(() => expect(onOpenAddress).toHaveBeenCalledWith("addr-1"));
});

it("manages an Asks email address from its settings page", async () => {
  const user = userEvent.setup();
  const onBack = vi.fn();
  const onReload = vi.fn().mockResolvedValue(undefined);
  vi.mocked(listEmailIntakeAddresses).mockResolvedValue([{ ...intake, senderName: "Helpdesk", forwardingEmailAddress: "help@acme.dev", dnsRecords: [{ type: "TXT", name: "_flow-intake.acme.dev", content: "flow-verification=abc", isVerified: true }] }]);
  vi.mocked(updateEmailIntakeAddress).mockResolvedValue({ ...intake, customerRequestsEnabled: false });
  vi.mocked(deleteEmailIntakeAddress).mockResolvedValue(undefined);
  vi.mocked(confirmAction).mockResolvedValue(true);
  const data = makeBootstrap({
    viewerRole: "admin",
    workspaceSettings: { ...settings, featureFlags: { asks: true, "customer-requests": true } } as WorkspaceSettings,
    teams: [{ id: "team-1", name: "Engineering", key: "ENG", color: "#5e6ad2" }],
    emailIntakeAddresses: [{ ...intake, senderName: "Helpdesk", forwardingEmailAddress: "help@acme.dev", dnsRecords: undefined, outboundFromEmail: undefined }],
  } as never);
  render(
    <I18nProvider>
      <AsksEmailIntakeDetailPage data={data} addressId="addr-1" onBack={onBack} onReload={onReload} />
    </I18nProvider>,
  );
  expect(screen.getByRole("heading", { name: "Helpdesk" })).toBeVisible();
  expect(screen.getByText("help@acme.dev")).toBeVisible();
  expect(await screen.findByText("DNS Active")).toBeVisible();
  expect(screen.getByRole("combobox", { name: "Team" })).toHaveTextContent("Engineering");
  await user.click(screen.getByRole("checkbox", { name: "Link incoming emails as customer requests" }));
  await waitFor(() => expect(updateEmailIntakeAddress).toHaveBeenCalledWith("team-1", "addr-1", { customerRequestsEnabled: false }));
  await user.click(screen.getByRole("button", { name: "Open menu" }));
  await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
  await waitFor(() => expect(deleteEmailIntakeAddress).toHaveBeenCalledWith("team-1", "addr-1"));
  expect(confirmAction).toHaveBeenCalledWith("Delete Helpdesk (help@acme.dev)?", expect.objectContaining({ description: "You cannot undo this action.", danger: true }));
  await waitFor(() => expect(onBack).toHaveBeenCalled());
});

it("lists Asks email addresses like Linear and opens the wizard or an address", async () => {
  const user = userEvent.setup();
  const onOpenEmailIntake = vi.fn();
  const data = makeBootstrap({
    viewerRole: "admin",
    workspaceSettings: settings,
    integrationConnections: [],
    emailIntakeAddresses: [
      { ...intake, senderName: "Helpdesk", forwardingEmailAddress: "help@acme.dev" },
      { ...intake, id: "team-addr", type: "team", address: "team@intake.flow.app" },
    ],
  } as never);
  render(
    <I18nProvider>
      <AsksSettingsPage data={data} settings={settings} busy={false} setEnabled={vi.fn()} setFeature={vi.fn()} onReload={vi.fn()} onOpenEmailIntake={onOpenEmailIntake} />
    </I18nProvider>,
  );
  expect(screen.getByText("1 email")).toBeVisible();
  expect(screen.queryByText("team@intake.flow.app")).toBeNull();
  await user.click(screen.getByRole("button", { name: "Helpdesk settings" }));
  expect(onOpenEmailIntake).toHaveBeenCalledWith("addr-1");
  await user.click(screen.getByRole("button", { name: "Add email" }));
  expect(onOpenEmailIntake).toHaveBeenLastCalledWith();
});
