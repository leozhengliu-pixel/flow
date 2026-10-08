import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ActionDialogHost } from "@/components/ui/action-dialogs";
import { I18nProvider } from "@/i18n/i18n";
import { ApiError } from "@/lib/api-client";
import { makeBootstrap } from "@/test/fixtures";
import type { IntegrationConnection } from "@/types/flow";

const api = vi.hoisted(() => ({
  connectGitLab: vi.fn(),
  disconnectIntegrationConnection: vi.fn(),
  rotateGitLabToken: vi.fn(),
  setGitLabTokenRotation: vi.fn(),
  testIntegrationConnection: vi.fn(),
  updateGitLabToken: vi.fn(),
  updateIntegrationConnection: vi.fn(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));
vi.mock("sonner", () => ({
  toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn(), info: vi.fn() }),
}));

import { CodeIntegrationSettings } from "./code-integration-settings";
import { gitlabBranchExample } from "@/lib/gitlab-branch-formats";

// Obviously fake token used only in these tests.
const FAKE_TOKEN = "glpat-fake-test-token";

function gitlabConnection(config: Record<string, string> = {}, overrides: Partial<IntegrationConnection> = {}): IntegrationConnection {
  return {
    id: "gitlab-connection",
    provider: "gitlab",
    name: "GitLab",
    status: "connected",
    config: { host: "https://gitlab.com", username: "flow-bot", tokenHint: "oken", readonly: "false", canSelfRotate: "true", ...config },
    connectedBy: "user-1",
    createdAt: "2026-09-10T00:00:00Z",
    updatedAt: "2026-09-10T00:00:00Z",
    scopes: ["api"],
    channels: [],
    linkbackEnabled: true,
    deliveryAttempts: 0,
    ...overrides,
  };
}

function renderPage(connections: IntegrationConnection[] = [], onReload = vi.fn().mockResolvedValue(undefined)) {
  const view = render(
    <I18nProvider>
      <CodeIntegrationSettings
        provider="gitlab"
        data={makeBootstrap({ integrationConnections: connections, integrationDeliveries: [] })}
        onBack={vi.fn()}
        onReload={onReload}
      />
      <ActionDialogHost />
    </I18nProvider>,
  );
  return { ...view, onReload };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem("flow:locale", "en");
});

describe("GitLab integration settings", () => {
  it("shows the not-connected page with Enable, overview and disabled settings", async () => {
    const user = userEvent.setup();
    renderPage();
    expect(screen.getByRole("heading", { name: "GitLab" })).toBeInTheDocument();
    expect(screen.getByText("Automate your Merge Request workflow")).toBeInTheDocument();
    expect(screen.getByText("Built by")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Docs" })).toHaveAttribute("target", "_blank");
    expect(screen.getByRole("heading", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByText("Branch format", { selector: "h3" }).closest(".gitlab-settings-sections")).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByText("Example:", { exact: false })).toHaveTextContent("Example: alex/eng-123-fix-login-error");
    await user.click(screen.getByRole("button", { name: "Read more" }));
    expect(screen.getByRole("heading", { name: "How it works" })).toBeInTheDocument();
  });

  it("enters the setup state with the grid loader, token help and Linear's fields", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Enable" }));
    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("Setting up GitLab…");
    expect(status.querySelector("svg.gli")).toHaveAttribute("data-variant", "pong");
    expect(screen.queryByRole("heading", { name: "Overview" })).not.toBeInTheDocument();
    const token = screen.getByLabelText("API access token");
    expect(token).toHaveAttribute("type", "password");
    expect(screen.getByRole("link", { name: "user settings" })).toHaveAttribute("href", "https://gitlab.com/-/user_settings/personal_access_tokens");
    expect(screen.getByText(/project access token, it requires/)).toHaveTextContent("If you use a project access token, it requires reporter role access.");
    expect(screen.getByText(/can be automatically renewed/)).toHaveTextContent("If the token has the self_rotate scope, it can be automatically renewed.");
    expect(screen.getByLabelText("Custom GitLab URL (optional, self-hosted only)")).toHaveAttribute("placeholder", "https://gitlab.your-company.com");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("button", { name: "Enable" })).toBeInTheDocument();
  });

  it("validates the token and HTTPS URL before calling the API", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Enable" }));
    await user.click(screen.getByRole("button", { name: "Connect" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Please enter a valid access token.");
    const url = screen.getByLabelText("Custom GitLab URL (optional, self-hosted only)");
    await user.type(url, "http://gitlab.example.com");
    await user.tab();
    expect(screen.getByText("Please enter a valid HTTPS URL.")).toBeInTheDocument();
    expect(api.connectGitLab).not.toHaveBeenCalled();
  });

  it("shows server errors with response details", async () => {
    const user = userEvent.setup();
    api.connectGitLab.mockRejectedValue(
      new ApiError("The access token is invalid, expired or revoked.", 422, undefined, {
        errorRequest: "GET https://gitlab.example.com/api/v4/personal_access_tokens/self",
        errorResponseBody: '{"message":"401 Unauthorized"}',
      }),
    );
    renderPage();
    await user.click(screen.getByRole("button", { name: "Enable" }));
    await user.type(screen.getByLabelText("API access token"), FAKE_TOKEN);
    await user.type(screen.getByLabelText("Custom GitLab URL (optional, self-hosted only)"), "https://gitlab.example.com");
    await user.click(screen.getByRole("button", { name: "Connect" }));
    expect(api.connectGitLab).toHaveBeenCalledWith({ token: FAKE_TOKEN, url: "https://gitlab.example.com" });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The access token is invalid, expired or revoked. See below for more details.");
    expect(within(alert).getByText("Response details")).toBeInTheDocument();
    expect(within(alert).getByText("GET https://gitlab.example.com/api/v4/personal_access_tokens/self")).toBeInTheDocument();
    expect(within(alert).getByText('{"message":"401 Unauthorized"}')).toBeInTheDocument();
  });

  it("shows the webhook step with URL, secret and triggers after connecting", async () => {
    const user = userEvent.setup();
    api.connectGitLab.mockResolvedValue({ ...gitlabConnection(), webhookSecret: "flow_glwh_fake", webhookPath: "/api/integrations/gitlab/webhook?workspace=workspace" });
    const { onReload } = renderPage();
    await user.click(screen.getByRole("button", { name: "Enable" }));
    await user.type(screen.getByLabelText("API access token"), FAKE_TOKEN);
    await user.click(screen.getByRole("button", { name: "Connect" }));
    expect(onReload).toHaveBeenCalled();
    expect(await screen.findByRole("heading", { name: "Set up webhook" })).toBeInTheDocument();
    expect(screen.getByLabelText("Webhook URL")).toHaveValue(`${window.location.origin}/api/integrations/gitlab/webhook?workspace=workspace`);
    expect(screen.getByLabelText("Webhook secret")).toHaveValue("flow_glwh_fake");
    for (const trigger of ["Push events", "Comments", "Merge request events", "Pipeline events"]) expect(screen.getByText(trigger)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByRole("heading", { name: "Set up webhook" })).not.toBeInTheDocument();
  });

  it("shows the connected state with token expiry, rotation and enabled settings", async () => {
    const user = userEvent.setup();
    api.setGitLabTokenRotation.mockResolvedValue(gitlabConnection({ rotationEnabled: "true" }));
    renderPage([gitlabConnection({ expiresAt: "2099-01-02T00:00:00Z" })]);
    expect(screen.getByText("Enabled by")).toBeInTheDocument();
    expect(screen.getByText("Support")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Disable" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Overview" })).not.toBeInTheDocument();
    expect(screen.getByText(/^Expires /)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Manage GitLab token" })).toHaveTextContent("Connected");
    expect(screen.getByText("Automatically renew the token before it expires.")).toBeInTheDocument();
    expect(screen.getByText("Branch format", { selector: "h3" }).closest(".gitlab-settings-sections")).not.toHaveAttribute("aria-disabled");
    await user.click(screen.getByRole("checkbox", { name: "Automatic rotation" }));
    expect(api.setGitLabTokenRotation).toHaveBeenCalledWith("gitlab-connection", true);
    await user.click(screen.getByRole("button", { name: "Show more information" }));
    expect(screen.getByRole("heading", { name: "Overview" })).toBeInTheDocument();
  });

  it("flags expired and non-rotatable tokens", () => {
    const { unmount } = renderPage([gitlabConnection({ expiresAt: "2020-01-01T00:00:00Z" })]);
    expect(screen.getByRole("button", { name: "Manage GitLab token" })).toHaveTextContent("Token expired");
    expect(screen.getByText(/^Expired .*Update the token to reconnect\.$/)).toBeInTheDocument();
    expect(screen.getByText("Update the expired token to enable automatic rotation.")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Automatic rotation" })).toBeDisabled();
    unmount();
    renderPage([gitlabConnection({ canSelfRotate: "false", readonly: "true" })]);
    expect(screen.getByText("Expiry unavailable")).toBeInTheDocument();
    expect(screen.getByText("This token cannot rotate itself. Update it with api or both read_api and self_rotate scopes.")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Private/Internal repositories" })).toBeDisabled();
    expect(screen.queryByRole("checkbox", { name: "Include issue descriptions in comments" })).not.toBeInTheDocument();
  });

  it("rotates and updates the token from the token menu", async () => {
    const user = userEvent.setup();
    api.rotateGitLabToken.mockResolvedValue(gitlabConnection());
    api.updateGitLabToken.mockResolvedValue(gitlabConnection());
    renderPage([gitlabConnection({ expiresAt: "2099-01-02T00:00:00Z" })]);
    await user.click(screen.getByRole("button", { name: "Manage GitLab token" }));
    await user.click(await screen.findByRole("menuitem", { name: "Rotate token" }));
    expect(api.rotateGitLabToken).toHaveBeenCalledWith("gitlab-connection");
    expect(toast.success).toHaveBeenCalledWith("GitLab token rotated");
    await user.click(screen.getByRole("button", { name: "Manage GitLab token" }));
    await user.click(await screen.findByRole("menuitem", { name: "Update token" }));
    const dialog = await screen.findByRole("dialog", { name: "Update GitLab token" });
    const submit = within(dialog).getByRole("button", { name: "Update token" });
    expect(submit).toBeDisabled();
    await user.type(within(dialog).getByLabelText("API access token"), FAKE_TOKEN);
    await user.click(submit);
    expect(api.updateGitLabToken).toHaveBeenCalledWith("gitlab-connection", FAKE_TOKEN);
    expect(toast.success).toHaveBeenCalledWith("GitLab token updated");
  });

  it("shows connectivity issues and restores the connection with a test", async () => {
    const user = userEvent.setup();
    api.testIntegrationConnection.mockResolvedValue({ provider: "gitlab", status: "ready", testedAt: new Date().toISOString() });
    renderPage([gitlabConnection({}, { lastTestStatus: "error", lastError: "The access token is invalid, expired or revoked." })]);
    expect(screen.getByText("GitLab connectivity issues")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Test connection" }));
    expect(api.testIntegrationConnection).toHaveBeenCalledWith("gitlab", "gitlab-connection");
    expect(toast.success).toHaveBeenCalledWith("Connection restored", { description: "Your GitLab integration is now active." });
  });

  it("confirms before disconnecting", async () => {
    const user = userEvent.setup();
    api.disconnectIntegrationConnection.mockResolvedValue(undefined);
    renderPage([gitlabConnection()]);
    await user.click(screen.getByRole("button", { name: "Disable" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Disconnect GitLab?");
    expect(dialog).toHaveTextContent("Integration functionality will stop and related settings will be deleted.");
    await act(async () => {
      await user.click(within(dialog).getByRole("button", { name: "Disconnect" }));
    });
    expect(api.disconnectIntegrationConnection).toHaveBeenCalledWith("gitlab", "gitlab-connection");
    expect(toast.info).toHaveBeenCalledWith("Disconnected GitLab integration");
  });

  it("builds branch name examples for every format", () => {
    expect(gitlabBranchExample("feature/identifier-title")).toBe("feature/eng-123-fix-login-error");
    expect(gitlabBranchExample("title-identifier")).toBe("fix-login-error-eng-123");
  });
});
