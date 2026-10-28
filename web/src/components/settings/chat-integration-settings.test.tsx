import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

import { I18nProvider } from "@/i18n/i18n";
import { ApiError } from "@/lib/api-client";
import * as api from "@/lib/chat-integrations-api";
import { makeBootstrap, viewer } from "@/test/fixtures";
import type { ChatBinding, ChatIntegrationSettings, ChatPermissions } from "@/types/chat-integrations";
import { ChatIntegrationSettings as ChatSettings } from "./chat-integration-settings";
import { IntegrationSettingsPage } from "./integration-settings-page";

vi.mock("@/lib/chat-integrations-api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/lib/chat-integrations-api")>(),
  fetchChatIntegration: vi.fn(),
  saveChatIntegration: vi.fn(),
  testChatIntegration: vi.fn(),
  disconnectChatIntegration: vi.fn(),
  fetchChatIntegrationChats: vi.fn(),
  saveChatBinding: vi.fn(),
  removeChatBinding: vi.fn(),
  startChatIdentityBinding: vi.fn(),
  removeChatIdentity: vi.fn(),
  revokeChatBinding: vi.fn(),
}));
vi.mock("@/components/ui/action-dialog-service", () => ({ confirmAction: vi.fn().mockResolvedValue(true) }));
vi.mock("@/lib/toast", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const binding: ChatBinding = {
  id: "binding-1", connectionId: "connection-1", chatId: "chat-1", chatName: "Engineering",
  teamId: "team-1", events: ["issue.created"], enabled: true, allowCreate: true, allowUpdate: true,
};
const settings = (overrides: Partial<ChatIntegrationSettings> = {}): ChatIntegrationSettings => ({
  connection: {
    id: "connection-1", provider: "feishu", name: "Feishu", status: "connected", config: { appId: "app-1", tenantKey: "tenant-1" },
    scopes: [], channels: [], linkbackEnabled: false, deliveryAttempts: 0, connectedBy: viewer.id,
    createdAt: "2026-10-26T00:00:00Z", updatedAt: "2026-10-26T00:00:00Z",
  },
  identity: { userId: viewer.id, openId: "open-1", tenantKey: "tenant-1", name: "Alex" },
  bindings: [], bindableTeamIds: ["team-1"], credentialsConfigured: true, webhookURL: "https://flow.example/api/integrations/feishu/chat/events/connection-1", canManage: true,
  ...overrides,
});

beforeEach(() => {
  localStorage.setItem("flow:locale", "en-US");
  vi.clearAllMocks();
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings());
  vi.mocked(api.saveChatIntegration).mockResolvedValue({});
  vi.mocked(api.testChatIntegration).mockResolvedValue({});
  vi.mocked(api.saveChatBinding).mockResolvedValue({});
  vi.mocked(api.removeChatIdentity).mockResolvedValue(undefined);
  vi.mocked(api.removeChatBinding).mockResolvedValue(undefined);
  vi.mocked(api.disconnectChatIntegration).mockResolvedValue(undefined);
  vi.mocked(api.fetchChatIntegrationChats).mockResolvedValue({ items: [{ id: "chat-1", name: "Engineering", external: false }], hasMore: false });
});

function show(overrides: Parameters<typeof makeBootstrap>[0] = {}) {
  return render(<MemoryRouter><I18nProvider><ChatSettings provider="feishu" data={makeBootstrap({ viewerRole: "admin", integrationConnections: [], ...overrides })} onBack={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} /></I18nProvider></MemoryRouter>);
}

it.each(["feishu", "lark"] as const)("routes %s to its dedicated settings and loads the live connection", async (provider) => {
  render(<MemoryRouter><I18nProvider><IntegrationSettingsPage slug={provider} data={makeBootstrap({ viewerRole: "admin", integrationConnections: [] })} onBack={vi.fn()} onReload={vi.fn()} /></I18nProvider></MemoryRouter>);
  expect(await screen.findByRole("textbox", { name: "App ID" })).toHaveValue("app-1");
  expect(api.fetchChatIntegration).toHaveBeenCalledWith(provider);
});

it("preserves stored credentials when submitting blank secret fields", async () => {
  show();
  await screen.findByRole("textbox", { name: "App ID" });
  for (const label of ["App Secret", "Encrypt Key", "Verification Token"]) expect(screen.getByLabelText(label)).toHaveValue("");
  await userEvent.setup().click(screen.getByRole("button", { name: "Save settings" }));
  await waitFor(() => expect(api.saveChatIntegration).toHaveBeenCalledWith("feishu", { name: "Feishu", appId: "app-1" }));
});

it("recovers the read-only application ID after the initial settings request fails", async () => {
  vi.mocked(api.fetchChatIntegration).mockRejectedValueOnce(new Error("Connection unavailable"));
  show();
  const user = userEvent.setup();
  expect(await screen.findByRole("alert")).toHaveTextContent("Could not load integration");
  await user.click(screen.getByRole("button", { name: "Retry" }));
  expect(await screen.findByRole("textbox", { name: "App ID" })).toHaveValue("app-1");
});

it("keeps unsaved credentials and group editor intact when saving fails", async () => {
  vi.mocked(api.saveChatIntegration).mockRejectedValue(new ApiError("Chat application check failed; verify app credentials and bot publication", 502, "chat_check_failed"));
  show();
  await screen.findByRole("textbox", { name: "App ID" });
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("App Secret"), "new-secret");
  await user.click(screen.getByRole("button", { name: "Save settings" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("The application check failed. Verify the app credentials and that the bot is published.");
  expect(screen.getByLabelText("App Secret")).toHaveValue("new-secret");
  expect(api.fetchChatIntegration).toHaveBeenCalledTimes(1);
});

it("keeps personal account controls out of workspace integration settings", async () => {
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings({ canManage: false }));
  show({ viewerRole: "member" });
  expect(await screen.findByRole("region", { name: "Connection" })).toBeInTheDocument();
  expect(screen.queryByRole("region", { name: "Your account" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Unlink account" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Link account" })).not.toBeInTheDocument();
  expect(screen.queryByRole("textbox", { name: "App ID" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Add group" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Disconnect" })).not.toBeInTheDocument();
  expect(api.removeChatIdentity).not.toHaveBeenCalled();
});

const tenantPending = (overrides: Partial<ChatPermissions["tenant"]> = {}, workspace = true, teams: Partial<ChatPermissions["teams"]> = {}): ChatPermissions => ({
  tenant: { status: "pending", ...overrides },
  workspace: { canManage: workspace },
  teams: { manageableTeamIds: workspace ? ["team-1"] : [], privateTeamIds: [], ...teams },
});

it.each(["feishu", "lark"] as const)("shows the %s tenant as automatic, with no sign-in or designated authorizer", async (provider) => {
  const pending = settings({ identity: null, permissions: tenantPending() });
  pending.connection!.status = "pending_authorization";
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(pending);
  render(<MemoryRouter><I18nProvider><ChatSettings provider={provider} data={makeBootstrap({ viewerRole: "admin", integrationConnections: [] })} onBack={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)} /></I18nProvider></MemoryRouter>);
  const permissions = await screen.findByRole("region", { name: "Permissions" });
  expect(within(permissions).getByText("Tenant")).toBeInTheDocument();
  expect(within(permissions).getByTestId("chat-tenant-status")).toHaveTextContent("Not checked yet. Flow checks automatically once the app is approved and published. Select Test connection to check now.");
  expect(within(permissions).getByText(`Automatic: once your ${provider === "feishu" ? "Feishu" : "Lark"} admin approves and publishes the app version in the admin console`, { exact: false })).toBeInTheDocument();
  expect(within(permissions).getByText("Workspace integration")).toBeInTheDocument();
  expect(within(permissions).getByText("Team group bindings")).toBeInTheDocument();
  expect(within(permissions).queryByRole("button")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Authorize tenant" })).not.toBeInTheDocument();
  expect(screen.queryByRole("combobox", { name: "Tenant authorizer" })).not.toBeInTheDocument();
  expect(screen.queryByText(/completed before tenant authorization/i)).not.toBeInTheDocument();
});

it("shows the not-approved state with its reason after a failed check", async () => {
  const failed = settings({ identity: null, lastErrorCode: "chat_tenant_not_approved", permissions: tenantPending({ status: "not_approved", errorCode: "chat_tenant_not_approved", checkedAt: "2026-10-27T00:00:00Z" }) });
  failed.connection!.status = "error";
  failed.connection!.lastError = "not approved";
  vi.mocked(api.fetchChatIntegration).mockResolvedValueOnce(settings({ identity: null, permissions: tenantPending() })).mockResolvedValue(failed);
  vi.mocked(api.testChatIntegration).mockRejectedValue(new ApiError("The app isn't approved", 502, "chat_tenant_not_approved"));
  show();
  await screen.findByRole("region", { name: "Permissions" });
  await userEvent.setup().click(screen.getByRole("button", { name: "Test connection" }));
  const permissions = await screen.findByRole("region", { name: "Permissions" });
  await waitFor(() => expect(within(permissions).getByTestId("chat-tenant-status")).toHaveTextContent("The app isn't approved or published in your organization yet, or its credentials are wrong. Ask your chat platform admin to approve the app version in the admin console."));
  expect(within(permissions).getByTestId("chat-tenant-permission")).toHaveAttribute("data-granted", "false");
  expect(screen.getByRole("button", { name: "Add group" })).toBeDisabled();
});

it("shows the bot-unavailable reason separately", async () => {
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings({ permissions: tenantPending({ status: "not_approved", errorCode: "chat_bot_unpublished" }) }));
  show();
  const permissions = await screen.findByRole("region", { name: "Permissions" });
  expect(within(permissions).getByTestId("chat-tenant-status")).toHaveTextContent("Enable and publish the application's bot, then try again.");
});

it("shows the automatically authorized tenant and who configured it", async () => {
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings({ permissions: { ...tenantPending(), tenant: { status: "authorized", tenantKey: "tenant-1", authorization: { tenantKey: "tenant-1", authorizedAt: "2026-10-20T00:00:00Z", configuredBy: "user-2" } } } }));
  show();
  const permissions = await screen.findByRole("region", { name: "Permissions" });
  expect(within(permissions).getByTestId("chat-tenant-status")).toHaveTextContent("Connected to tenant tenant-1.");
  expect(within(permissions).getByText(/Detected automatically on .*Configured by Teammate\./)).toBeInTheDocument();
  expect(within(permissions).getByTestId("chat-tenant-permission")).toHaveAttribute("data-granted", "true");
  expect(screen.getByRole("button", { name: "Add group" })).toBeEnabled();
});

it("says when the tenant key is not known yet", async () => {
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings({ permissions: { ...tenantPending(), tenant: { status: "authorized", authorization: { authorizedAt: "2026-10-20T00:00:00Z" } } } }));
  show();
  const permissions = await screen.findByRole("region", { name: "Permissions" });
  expect(within(permissions).getByTestId("chat-tenant-status")).toHaveTextContent("Its tenant key is recorded once the bot joins an internal group or a member links an account.");
});

it("explains which permission a member lacks and who can grant it", async () => {
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings({ canManage: false, permissions: tenantPending({}, false) }));
  show({ viewerRole: "member" });
  const permissions = await screen.findByRole("region", { name: "Permissions" });
  expect(within(permissions).getByText("Ask a workspace administrator to test the connection.", { exact: false })).toBeInTheDocument();
  expect(within(permissions).getByTestId("chat-tenant-status")).not.toHaveTextContent("Select Test connection");
  expect(within(permissions).getByText("Only workspace administrators manage the application settings.")).toBeInTheDocument();
  expect(within(permissions).getByText("A team's owners bind its groups. Ask a team owner or a workspace administrator.")).toBeInTheDocument();
  expect(screen.queryByRole("region", { name: "Group bindings" })).not.toBeInTheDocument();
});

it("shows the tenant states in Chinese with 飞书 for Feishu", async () => {
  localStorage.setItem("flow:locale", "zh-CN");
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings({ permissions: tenantPending({ status: "not_approved", errorCode: "chat_tenant_not_approved" }) }));
  show();
  const permissions = await screen.findByRole("region", { name: "权限" });
  expect(within(permissions).getByText("租户")).toBeInTheDocument();
  expect(within(permissions).getByTestId("chat-tenant-status")).toHaveTextContent("应用尚未在你的组织中审批或发布");
  expect(within(permissions).getByText(/飞书 管理员在管理后台审批并发布应用版本后/)).toBeInTheDocument();
});

it("lets a private team owner bind only their team, with explicit authorization", async () => {
  const privateBinding: ChatBinding = { ...binding, id: "binding-private", teamId: "private-team", chatName: "Vault room", authorization: { userId: viewer.id, role: "team_owner", authorizedAt: "2026-10-21T00:00:00Z", privateTeam: true } };
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings({
    canManage: false, bindings: [privateBinding], bindableTeamIds: ["private-team"],
    permissions: { ...tenantPending({}, false, { manageableTeamIds: ["private-team"], privateTeamIds: ["private-team"] }), tenant: { status: "authorized", tenantKey: "tenant-1" } },
  }));
  show({ viewerRole: "member", teams: [makeBootstrap().teams[0], { id: "private-team", name: "Private team", key: "PVT", color: "", private: true }] });
  const bindings = await screen.findByRole("region", { name: "Group bindings" });
  expect(screen.queryByRole("region", { name: "Application settings" })).not.toBeInTheDocument();
  expect(within(bindings).getByText("Private team", { selector: "small" })).toBeInTheDocument();
  expect(within(bindings).getByText(/Authorized by Viewer on/)).toBeInTheDocument();
  expect(screen.getByText("You can bind groups for 1 team you own.")).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Add group" }));
  await user.click(await screen.findByRole("combobox", { name: "Group" }));
  await user.click(screen.getByRole("option", { name: "Engineering" }));
  await user.click(screen.getByRole("combobox", { name: "Team" }));
  expect(screen.queryByRole("option", { name: "Test team" })).not.toBeInTheDocument();
  await user.click(screen.getByRole("option", { name: "Private team" }));
  expect(screen.getByRole("note")).toHaveTextContent("Binding doesn't add anyone to the team");
  await user.click(screen.getByRole("checkbox", { name: /I approve sharing/ }));
  await user.click(screen.getByRole("button", { name: "Save group binding" }));
  await waitFor(() => expect(api.saveChatBinding).toHaveBeenCalledWith("feishu", expect.objectContaining({ teamId: "private-team", chatId: "chat-1", authorize: true })));
});

it("revokes a binding and keeps it listed as revoked", async () => {
  const revoked: ChatBinding = { ...binding, enabled: false, revokedAt: "2026-10-22T00:00:00Z", revokedBy: viewer.id };
  vi.mocked(api.fetchChatIntegration).mockResolvedValueOnce(settings({ bindings: [binding] })).mockResolvedValue(settings({ bindings: [revoked] }));
  vi.mocked(api.revokeChatBinding).mockResolvedValue(revoked);
  show();
  await userEvent.setup().click(await screen.findByRole("button", { name: "Revoke group binding: Engineering" }));
  await waitFor(() => expect(api.revokeChatBinding).toHaveBeenCalledWith("feishu", "binding-1"));
  expect(await screen.findByText("Revoked")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Revoke group binding: Engineering" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Remove group binding: Engineering" })).toBeInTheDocument();
});

it("translates the product name, connection errors and unknown failures in Chinese", async () => {
  localStorage.setItem("flow:locale", "zh-CN");
  const failing = settings({ lastErrorCode: "chat_delivery_failed" });
  failing.connection!.lastError = "Chat delivery: Chat platform request failed (HTTP 400, code 230002)";
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(failing);
  vi.mocked(api.testChatIntegration).mockRejectedValue(new Error("socket hang up"));
  show();
  expect(await screen.findByRole("heading", { level: 1, name: "飞书" })).toBeInTheDocument();
  expect(screen.getByText("消息无法送达。请检查机器人权限、群访问权限和应用凭据。")).toBeInTheDocument();
  expect(screen.queryByText(/HTTP 400/)).not.toBeInTheDocument();
  await userEvent.setup().click(screen.getByRole("button", { name: "测试连接" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("无法更新集成");
});

it("offers only the teams the server reports as bindable", async () => {
  show({ teams: [makeBootstrap().teams[0], { id: "team-2", name: "Second team", key: "SEC", color: "" }] });
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Add group" }));
  await user.click(await screen.findByRole("combobox", { name: "Team" }));
  expect(screen.getByRole("option", { name: "Test team" })).toBeInTheDocument();
  expect(screen.queryByRole("option", { name: "Second team" })).not.toBeInTheDocument();
});

it("paginates groups, excludes external groups, and saves the public scope after approval", async () => {
  vi.mocked(api.fetchChatIntegrationChats)
    .mockResolvedValueOnce({ items: [{ id: "external", name: "Customers", external: true }], hasMore: true, pageToken: "page-2" })
    .mockResolvedValueOnce({ items: [{ id: "chat-2", name: "Delivery", external: false }], hasMore: false });
  show({
    teams: [makeBootstrap().teams[0], { id: "private-team", name: "Private team", key: "PVT", color: "", private: true }, { id: "restricted-team", name: "Restricted team", key: "RST", color: "" }, { id: "private-child", name: "Private child", key: "CHD", color: "" }, { id: "members-only", name: "Members only", key: "MEM", color: "" }],
    teamSettings: { "restricted-team": { access: "restricted" }, "private-child": { parentTeamId: "private-team" }, "members-only": { membershipRestriction: "members" } } as never,
  });
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Add group" }));
  await user.click(await screen.findByRole("button", { name: "Load more groups" }));
  await waitFor(() => expect(api.fetchChatIntegrationChats).toHaveBeenLastCalledWith("feishu", "page-2"));
  await user.click(screen.getByRole("combobox", { name: "Group" }));
  expect(screen.getByRole("option", { name: "Customers (External group)" })).toHaveAttribute("data-disabled");
  await user.click(screen.getByRole("option", { name: "Delivery" }));
  await user.click(screen.getByRole("combobox", { name: "Team" }));
  expect(screen.queryByRole("option", { name: "Private team" })).not.toBeInTheDocument();
  expect(screen.queryByRole("option", { name: "Restricted team" })).not.toBeInTheDocument();
  expect(screen.queryByRole("option", { name: "Private child" })).not.toBeInTheDocument();
  expect(screen.queryByRole("option", { name: "Members only" })).not.toBeInTheDocument();
  await user.click(screen.getByRole("option", { name: "Test team" }));
  expect(screen.getByRole("button", { name: "Save group binding" })).toBeDisabled();
  await user.click(screen.getByRole("checkbox", { name: /I approve sharing/ }));
  await user.click(screen.getByRole("button", { name: "Save group binding" }));
  await waitFor(() => expect(api.saveChatBinding).toHaveBeenCalledWith("feishu", expect.objectContaining({ chatId: "chat-2", chatName: "Delivery", teamId: "team-1", enabled: true, allowCreate: true, allowUpdate: true })));
});

it("keeps existing group values after load failure, retries, and preserves edits on save failure", async () => {
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings({ bindings: [binding] }));
  vi.mocked(api.fetchChatIntegrationChats).mockRejectedValueOnce(new ApiError("The chat platform request failed; check the bot's permissions and retry", 502, "chat_platform_failed"));
  vi.mocked(api.saveChatBinding).mockRejectedValue(new ApiError("Could not save binding; the group may already be bound", 409, "chat_binding_conflict"));
  show();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Edit group binding: Engineering" }));
  const form = await screen.findByRole("form", { name: "Edit group binding" });
  expect(await within(form).findByRole("alert")).toHaveTextContent("The chat platform request failed. Check the bot's permissions and try again.");
  await user.click(within(form).getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(api.fetchChatIntegrationChats).toHaveBeenCalledTimes(2));
  await user.click(within(form).getByRole("checkbox", { name: "Status changed" }));
  await user.click(within(form).getByRole("checkbox", { name: /I approve sharing/ }));
  await user.click(within(form).getByRole("button", { name: "Save group binding" }));
  expect(await screen.findByText("This group is already bound.")).toBeInTheDocument();
  expect(within(form).getByRole("checkbox", { name: "Status changed" })).toBeChecked();
});

it("clears binding and identity controls after disconnect and uses the dedicated removal API", async () => {
  vi.mocked(api.fetchChatIntegration).mockResolvedValueOnce(settings({ bindings: [binding] })).mockResolvedValue(settings({ connection: null, bindings: [], identity: null, credentialsConfigured: false }));
  show();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Disconnect" }));
  await waitFor(() => expect(api.disconnectChatIntegration).toHaveBeenCalledWith("feishu"));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Edit group binding: Engineering" })).not.toBeInTheDocument());
  expect(screen.getByRole("textbox", { name: "App ID" })).toHaveValue("");
  expect(screen.getByRole("button", { name: "Add group" })).toBeDisabled();
});

it("removes only the selected group binding after confirmation", async () => {
  vi.mocked(api.fetchChatIntegration).mockResolvedValueOnce(settings({ bindings: [binding] })).mockResolvedValue(settings());
  show();
  await userEvent.setup().click(await screen.findByRole("button", { name: "Remove group binding: Engineering" }));
  await waitFor(() => expect(api.removeChatBinding).toHaveBeenCalledWith("feishu", "binding-1"));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Remove group binding: Engineering" })).not.toBeInTheDocument());
});

it("requires disconnecting before changing an existing application", async () => {
  show();
  const id = await screen.findByRole("textbox", { name: "App ID" });
  expect(id).toHaveAttribute("readonly");
  expect(screen.getByText("To use another application, disconnect this integration first.")).toBeInTheDocument();
});

it("keeps sharing controls unavailable when workspace policy blocks external processing", async () => {
  show({ workspaceSettings: { ...makeBootstrap().workspaceSettings, hipaaCompliance: true } });
  expect(await screen.findByRole("button", { name: "Add group" })).toBeDisabled();
  expect(screen.getByText("Workspace policy prevents sharing updates with chat applications.")).toBeInTheDocument();
});
