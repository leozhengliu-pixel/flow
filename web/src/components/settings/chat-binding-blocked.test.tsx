import { render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import * as api from "@/lib/chat-integrations-api";
import { makeBootstrap } from "@/test/fixtures";
import type { ChatBinding, ChatBindingBlockedReason } from "@/types/chat-integrations";

import { ChatBindingEditor, ChatBindingRow } from "./chat-group-bindings";
import { chatBindingBlockedText } from "./chat-binding-blocked-text";

vi.mock("@/lib/chat-integrations-api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/lib/chat-integrations-api")>(),
  fetchChatIntegrationChats: vi.fn(),
}));

const base: ChatBinding = { id: "binding-1", connectionId: "c", chatId: "chat-1", chatName: "Engineering", teamId: "team-1", events: ["issue.created"], enabled: true, allowCreate: true, allowUpdate: true };

beforeEach(() => {
  localStorage.setItem("flow:locale", "en-US");
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.mocked(api.fetchChatIntegrationChats).mockResolvedValue({ items: [{ id: "chat-1", name: "Engineering", external: false }], hasMore: false });
});

function row(binding: ChatBinding, locale: "en-US" | "zh-CN" = "en-US") {
  localStorage.setItem("flow:locale", locale);
  return render(<I18nProvider><ChatBindingRow binding={binding} data={makeBootstrap()} busy="" canEdit onEdit={vi.fn()} onRevoke={vi.fn()} onRemove={vi.fn()} /></I18nProvider>);
}

it("shows nothing extra for a binding that delivers", () => {
  const { container } = row(base);
  expect(container.querySelector(".chat-binding-blocked")).toBeNull();
});

it("shows the blocked reason as a sentence next to a blocked binding", () => {
  row({ ...base, blockedReason: "team_private_needs_authorization" });
  const status = screen.getByRole("status");
  expect(status).toHaveTextContent("Not sending updates: this team is private. Edit the binding and approve sharing it again.");
  expect(status).toHaveAttribute("data-blocked", "team_private_needs_authorization");
});

it("says a members-only team needs sharing approval", () => {
  row({ ...base, blockedReason: "team_members_only_needs_authorization" });
  expect(screen.getByRole("status")).toHaveTextContent("Not sending updates: this team is limited to its members, so sharing it with a group needs approval. Edit the binding and approve sharing it.");
});

it("asks for the private-team approval when binding a members-only team, and says why", async () => {
  render(<I18nProvider><ChatBindingEditor provider="feishu" data={makeBootstrap()} bindableTeamIds={["team-1"]} privateTeamIds={["team-1"]} membersOnlyTeamIds={["team-1"]} fixedTeamId="team-1" busy={false} onCancel={vi.fn()} onSave={vi.fn()} /></I18nProvider>);
  const note = await screen.findByRole("note");
  expect(note).toHaveTextContent("This team is limited to its members");
  expect(note).not.toHaveTextContent("This team is private");
});

it("keeps the private-team note for a private team", async () => {
  render(<I18nProvider><ChatBindingEditor provider="feishu" data={makeBootstrap()} bindableTeamIds={["team-1"]} privateTeamIds={["team-1"]} fixedTeamId="team-1" busy={false} onCancel={vi.fn()} onSave={vi.fn()} /></I18nProvider>);
  expect(await screen.findByRole("note")).toHaveTextContent("This team is private");
});

it("shows when the last update was skipped and why", () => {
  row({ ...base, blockedReason: "hipaa", lastSkipped: { reason: "hipaa", eventId: "evt", eventType: "issue.created", at: "2026-11-17T08:30:00Z" } });
  const status = screen.getByRole("status");
  expect(status).toHaveTextContent(/^Not sending updates: group notifications are off while HIPAA compliance is on\. Last update skipped on .*2026.*: HIPAA compliance is on\.$/);
});

it("shows a past skip of a binding that delivers again without a warning tone", () => {
  row({ ...base, lastSkipped: { reason: "issue_restricted", eventId: "evt", eventType: "issue.created", at: "2026-11-17T08:30:00Z" } });
  const status = screen.getByRole("status");
  expect(status).not.toHaveAttribute("data-blocked");
  expect(status).toHaveTextContent(/Last update skipped on .*: the issue has its own access restrictions\./);
});

it("leaves a revoked binding to its Revoked badge", () => {
  const { container } = row({ ...base, revokedAt: "2026-11-17T08:30:00Z", blockedReason: "revoked" });
  expect(container.querySelector(".chat-binding-blocked")).toBeNull();
  expect(screen.getByText("Revoked")).toBeInTheDocument();
});

it("translates the reason", () => {
  row({ ...base, blockedReason: "team_inactive" }, "zh-CN");
  expect(screen.getByRole("status")).toHaveTextContent("未发送更新：团队已归档或不可用。");
});

it("has a sentence for every reason", () => {
  const reasons: ChatBindingBlockedReason[] = ["hipaa", "revoked", "team_inactive", "team_private_needs_authorization", "team_members_only_needs_authorization", "authorizer_lost_access", "project_out_of_scope", "template_not_permitted", "issue_restricted", "initiative_out_of_scope"];
  for (const reason of reasons) expect(chatBindingBlockedText(reason, (key) => key)).toMatch(/^Not sending updates: .+\.$/);
});
