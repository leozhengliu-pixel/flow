import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import * as api from "@/lib/chat-integrations-api";
import { toast } from "@/lib/toast";
import { makeBootstrap } from "@/test/fixtures";
import type { ChatIntegrationSettings } from "@/types/chat-integrations";
import { ChatConnectionRows } from "./chat-connection-rows";

vi.mock("@/lib/chat-integrations-api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/lib/chat-integrations-api")>(),
  fetchChatIntegration: vi.fn(),
  startChatIdentityBinding: vi.fn(),
  removeChatIdentity: vi.fn(),
}));
vi.mock("@/lib/toast", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function settings(overrides: Partial<ChatIntegrationSettings> = {}): ChatIntegrationSettings {
  return {
    connection: { id: "connection-1", provider: "feishu", status: "connected", config: { tenantKey: "tenant-1" } } as never,
    identity: null, bindings: [], credentialsConfigured: true, webhookURL: "", canManage: false,
    ...overrides,
  };
}

function show(overrides: Parameters<typeof makeBootstrap>[0] = {}) {
  return render(<MemoryRouter><I18nProvider><ChatConnectionRows data={makeBootstrap({ integrationConnections: [], ...overrides })} /></I18nProvider></MemoryRouter>);
}

beforeEach(() => {
  localStorage.setItem("flow:locale", "en-US");
  window.history.replaceState(null, "", "/workspace/settings/account/connections");
  vi.clearAllMocks();
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings());
  vi.mocked(api.removeChatIdentity).mockResolvedValue(undefined);
});

it("offers both personal chat accounts for members in the current workspace", async () => {
  show();
  for (const provider of ["Feishu", "Lark"]) {
    const row = screen.getByRole("region", { name: `${provider} account` });
    expect(await within(row).findByRole("button", { name: "Connect" })).toBeEnabled();
    expect(within(row).getByText("Create and update issues from group chats in Workspace")).toBeInTheDocument();
  }
  expect(screen.queryByRole("textbox", { name: "App ID" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Add group" })).not.toBeInTheDocument();
  expect(api.fetchChatIntegration).toHaveBeenCalledWith("feishu");
  expect(api.fetchChatIntegration).toHaveBeenCalledWith("lark");
});

it.each(["feishu", "lark"] as const)("starts %s identity authorization and safely surfaces a rejected URL", async (provider) => {
  vi.mocked(api.startChatIdentityBinding).mockResolvedValue({ authorizeUrl: "https://untrusted.example/oauth" });
  show();
  const row = screen.getByRole("region", { name: `${provider === "feishu" ? "Feishu" : "Lark"} account` });
  await userEvent.setup().click(await within(row).findByRole("button", { name: "Connect" }));
  expect(api.startChatIdentityBinding).toHaveBeenCalledWith(provider);
  expect(await within(row).findByRole("alert")).toHaveTextContent("Invalid authorization address");
  expect(within(row).getByRole("button", { name: "Connect" })).toBeEnabled();
});

it("waits for workspace setup for members and lets administrators verify a pending tenant", async () => {
  const pending = settings({ connection: { id: "connection-1", status: "pending_authorization" } as never });
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(pending);
  const member = show();
  expect(await screen.findAllByText("Waiting for workspace setup")).toHaveLength(2);
  expect(screen.queryByRole("button", { name: "Connect" })).not.toBeInTheDocument();
  member.unmount();
  vi.mocked(api.fetchChatIntegration).mockResolvedValue({ ...pending, canManage: true });
  show({ viewerRole: "admin" });
  expect(await screen.findAllByRole("button", { name: "Connect" })).toHaveLength(2);
});

it("confirms personal unlink without disconnecting the workspace application", async () => {
  const linked = settings({ identity: { userId: "user-1", openId: "open-1", tenantKey: "tenant-1", name: "Alex" } });
  vi.mocked(api.fetchChatIntegration).mockImplementation(async (provider) => provider === "feishu" ? linked : settings());
  show();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Feishu account options" }));
  await user.click(await screen.findByRole("menuitem", { name: "Disconnect…" }));
  expect(await screen.findByRole("dialog", { name: "Disconnect personal Feishu account?" })).toBeInTheDocument();
  expect(api.removeChatIdentity).not.toHaveBeenCalled();
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings());
  await user.click(screen.getByRole("button", { name: "Disconnect" }));
  await waitFor(() => expect(api.removeChatIdentity).toHaveBeenCalledWith("feishu"));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Feishu account options" })).not.toBeInTheDocument());
});

it("keeps a linked account available when unlink fails", async () => {
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings({ identity: { userId: "user-1", openId: "open-1", tenantKey: "tenant-1" } }));
  vi.mocked(api.removeChatIdentity).mockRejectedValue(new Error("Could not unlink now"));
  show();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Lark account options" }));
  await user.click(await screen.findByRole("menuitem", { name: "Disconnect…" }));
  await user.click(await screen.findByRole("button", { name: "Disconnect" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Could not unlink now");
  expect(screen.getByRole("button", { name: "Lark account options" })).toBeEnabled();
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("retries an unavailable provider without hiding the other account", async () => {
  vi.mocked(api.fetchChatIntegration).mockRejectedValueOnce(new Error("Feishu unavailable"));
  show();
  const user = userEvent.setup();
  const row = screen.getByRole("region", { name: "Feishu account" });
  expect(await within(row).findByRole("alert")).toHaveTextContent("Feishu unavailable");
  expect(await within(screen.getByRole("region", { name: "Lark account" })).findByRole("button", { name: "Connect" })).toBeEnabled();
  await user.click(within(row).getByRole("button", { name: "Retry" }));
  expect(await within(row).findByRole("button", { name: "Connect" })).toBeEnabled();
});

it("verifies the selected provider after OAuth and removes callback parameters", async () => {
  window.history.replaceState(null, "", "/workspace/settings/account/connections?chat_provider=lark&chat_auth=success&chat_auth_error=ignored");
  vi.mocked(api.fetchChatIntegration).mockImplementation(async (provider) => settings({ identity: provider === "lark" ? { userId: "user-1", openId: "open-1", tenantKey: "tenant-1" } : null }));
  show();
  await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Account linked"));
  expect(window.location.search).toBe("");
});

it("does not show a false OAuth success when the matching identity is absent", async () => {
  window.history.replaceState(null, "", "/workspace/settings/account/connections?chat_provider=feishu&chat_auth=success");
  show();
  expect(await screen.findByRole("alert")).toHaveTextContent("Could not link account. Please try again.");
  expect(toast.success).not.toHaveBeenCalled();
});

it("avoids workspace integration identity requests for guests", () => {
  show({ viewerRole: "guest" });
  expect(api.fetchChatIntegration).not.toHaveBeenCalled();
});
