import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import type { AsksWebSettings, WorkspaceSettings } from "@/types/flow";
import {
  createAsksWebSettings,
  createEmailIntakeAddress,
  updateAsksWebSettings,
  updateWorkspacePreferences,
  verifyAsksWebDNS,
  verifyEmailIntakeAddress,
} from "@/lib/api";
import {
  AsksSettingsPage,
  AsksSlackSettingsPage,
  NewAsksEmailIntakePage,
} from "./asks-settings";
import {
  AsksWebSettingsWizardPage,
  resolveAsksWebStatus,
} from "./asks-web-settings";

vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  updateWorkspacePreferences: vi.fn(),
  createEmailIntakeAddress: vi.fn(),
  verifyEmailIntakeAddress: vi.fn(),
  authorizeIntegration: vi.fn(),
  disconnectIntegration: vi.fn(),
  createAsksWebSettings: vi.fn(),
  updateAsksWebSettings: vi.fn(),
  verifyAsksWebDNS: vi.fn(),
  deleteAsksWebSettings: vi.fn(),
  createAsksWebPage: vi.fn(),
  updateAsksWebPage: vi.fn(),
  deleteAsksWebPage: vi.fn(),
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

const sampleWeb: AsksWebSettings = {
  id: "cfg-1",
  title: "Acme Asks",
  hostname: "asks.acme.test",
  asksUrl: "https://asks.acme.test",
  customDomainStatus: "none",
  emailAddress: "asks@acme.test",
  emailDomainConfigured: true,
  dnsVerified: true,
  hostingStatus: "configured",
  samlConfigured: false,
  pages: [],
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

it("shows web forms empty chrome and opens wizard", () => {
  const onOpenWebFormsWizard = vi.fn();
  const data = makeBootstrap({
    viewerRole: "admin",
    workspaceSettings: settings,
    asksWebSettings: [],
    integrationConnections: [],
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
        onOpenSlack={vi.fn()}
        onOpenEmailIntake={vi.fn()}
        onOpenWebFormsWizard={onOpenWebFormsWizard}
        onOpenWebFormsSettings={vi.fn()}
        onOpenWebFormsPage={vi.fn()}
      />
    </I18nProvider>,
  );
  expect(screen.getByText("No Asks web configuration")).toBeVisible();
  expect(screen.queryByText(/Upgrade to the/i)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add Asks Web forms" }));
  expect(onOpenWebFormsWizard).toHaveBeenCalled();
});

it("renders AsksWebStatus for configured web forms", () => {
  const onOpenWebFormsSettings = vi.fn();
  const data = makeBootstrap({
    viewerRole: "admin",
    workspaceSettings: settings,
    asksWebSettings: [sampleWeb],
    integrationConnections: [],
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
        onOpenWebFormsSettings={onOpenWebFormsSettings}
        onOpenWebFormsWizard={vi.fn()}
        onOpenWebFormsPage={vi.fn()}
      />
    </I18nProvider>,
  );
  expect(screen.getByText("Configured")).toBeVisible();
  expect(screen.getByText("SAML not configured")).toBeVisible();
  fireEvent.click(screen.getByText("asks.acme.test"));
  expect(onOpenWebFormsSettings).toHaveBeenCalledWith("cfg-1");
});

it("maps status machine kinds without billing limits", () => {
  expect(
    resolveAsksWebStatus({
      ...sampleWeb,
      hostingStatus: "pending",
      dnsVerified: false,
      emailDomainConfigured: false,
    }).kind,
  ).toBe("pending");
  expect(
    resolveAsksWebStatus({
      ...sampleWeb,
      customDomainStatus: "blocked",
    }).message,
  ).toBe("Domain activation blocked");
  expect(
    resolveAsksWebStatus({
      ...sampleWeb,
      emailDomainConfigured: false,
    }).message,
  ).toBe("Email domain not configured");
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

it("walks Asks web forms wizard host → email → DNS → SAML stub", async () => {
  vi.mocked(createAsksWebSettings).mockResolvedValue({
    settings: {
      ...sampleWeb,
      id: "cfg-new",
      dnsVerified: false,
      hostingStatus: "pending",
      emailDomainConfigured: false,
      emailAddress: "",
    },
    dnsRecord: {
      type: "TXT",
      name: "_flow-asks.asks.acme.test",
      value: "flow-asks-verification=abc",
    },
  });
  vi.mocked(verifyAsksWebDNS).mockResolvedValue({
    ...sampleWeb,
    id: "cfg-new",
  });
  const onComplete = vi.fn();
  const onReload = vi.fn().mockResolvedValue(undefined);
  const data = makeBootstrap({
    viewerRole: "admin",
    workspaceSettings: settings,
    asksWebSettings: [],
  });
  render(
    <I18nProvider>
      <AsksWebSettingsWizardPage
        data={data}
        onBack={vi.fn()}
        onComplete={onComplete}
        onReload={onReload}
      />
    </I18nProvider>,
  );
  fireEvent.change(screen.getByLabelText("Hostname"), {
    target: { value: "asks.acme.test" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await waitFor(() =>
    expect(screen.getByLabelText("Notification email")).toBeVisible(),
  );
  fireEvent.change(screen.getByLabelText("Notification email"), {
    target: { value: "asks@acme.test" },
  });
  vi.mocked(updateAsksWebSettings).mockResolvedValue({
    ...sampleWeb,
    id: "cfg-new",
    dnsVerified: false,
    hostingStatus: "pending",
  });
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await waitFor(() =>
    expect(screen.getByText("_flow-asks.asks.acme.test")).toBeVisible(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Verify domain" }));
  await waitFor(() =>
    expect(screen.getByText("Coming soon")).toBeVisible(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Finish" }));
  await waitFor(() => expect(onComplete).toHaveBeenCalledWith("cfg-new"));
});
