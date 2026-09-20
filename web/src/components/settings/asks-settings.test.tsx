
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import type { WorkspaceSettings } from "@/types/flow";
import {
  createEmailIntakeAddress,
  updateWorkspacePreferences,
  verifyEmailIntakeAddress,
} from "@/lib/api";
import {
  AsksSettingsPage,
  AsksSlackSettingsPage,
  NewAsksEmailIntakePage,
} from "./asks-settings";

vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  updateWorkspacePreferences: vi.fn(),
  createEmailIntakeAddress: vi.fn(),
  verifyEmailIntakeAddress: vi.fn(),
  authorizeIntegration: vi.fn(),
  disconnectIntegration: vi.fn(),
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
  expect(screen.getByText("Coming soon")).toBeVisible();
  expect(
    screen.getByText(
      "Custom web forms, domains, and SAML for Asks are not available yet.",
    ),
  ).toBeVisible();
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

it("walks the email intake wizard through DNS verification", async () => {
  vi.mocked(createEmailIntakeAddress).mockResolvedValue({
    address: {
      id: "addr-1",
      teamId: "team-1",
      localPart: "asks",
      domain: "mail.example.com",
      address: "asks@mail.example.com",
      verificationState: "pending",
      aliases: [],
      enabled: true,
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:00:00Z",
    },
    inboundToken: "token",
    dnsRecord: {
      type: "TXT",
      name: "_flow-intake.mail.example.com",
      value: "flow-verification=abc",
    },
  });
  vi.mocked(verifyEmailIntakeAddress).mockResolvedValue({
    id: "addr-1",
    teamId: "team-1",
    localPart: "asks",
    domain: "mail.example.com",
    address: "asks@mail.example.com",
    verificationState: "verified",
    aliases: [],
    enabled: true,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  });
  vi.mocked(updateWorkspacePreferences).mockResolvedValue(settings);
  const data = makeBootstrap({
    viewerRole: "admin",
    workspaceSettings: settings,
    teams: [{ id: "team-1", name: "Engineering", key: "ENG", color: "#5e6ad2" }],
  });
  const onBack = vi.fn();
  const onReload = vi.fn().mockResolvedValue(undefined);
  render(
    <I18nProvider>
      <NewAsksEmailIntakePage data={data} onBack={onBack} onReload={onReload} />
    </I18nProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  fireEvent.change(screen.getByLabelText("Email domain"), {
    target: { value: "mail.example.com" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create address" }));
  await waitFor(() =>
    expect(screen.getByText("_flow-intake.mail.example.com")).toBeVisible(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Verify domain" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Finish" })).toBeVisible(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Finish" }));
  await waitFor(() => expect(onBack).toHaveBeenCalled());
  expect(updateWorkspacePreferences).toHaveBeenCalledWith(
    { featureSettings: { asksEmailAddresses: ["asks@mail.example.com"] } },
    "workspace",
  );
});
