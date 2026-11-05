import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import * as api from "@/lib/chat-integrations-api";
import { makeBootstrap } from "@/test/fixtures";
import type { ChatBinding, ChatIntegrationProvider } from "@/types/chat-integrations";

import { ChatBindingEditor } from "./chat-group-bindings";

vi.mock("@/lib/chat-integrations-api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/lib/chat-integrations-api")>(),
  fetchChatIntegrationChats: vi.fn(),
}));

const binding: ChatBinding = { id: "binding-1", connectionId: "c", chatId: "chat-1", chatName: "Engineering", teamId: "team-1", events: ["issue.created"], enabled: true, allowCreate: true, allowUpdate: true };

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.clearAllMocks();
  vi.mocked(api.fetchChatIntegrationChats).mockResolvedValue({ items: [{ id: "chat-1", name: "Engineering", external: false }], hasMore: false });
});

function editor(locale: "en-US" | "zh-CN", current: ChatBinding, provider: ChatIntegrationProvider = "feishu") {
  localStorage.setItem("flow:locale", locale);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<I18nProvider><ChatBindingEditor provider={provider} data={makeBootstrap()} bindableTeamIds={["team-1"]} binding={current} busy={false} onCancel={vi.fn()} onSave={onSave} /></I18nProvider>);
  return onSave;
}

async function save(form: HTMLElement) {
  const user = userEvent.setup();
  await user.click(within(form).getByRole("checkbox", { name: /I approve sharing|我同意/ }));
  await user.click(within(form).getByRole("button", { name: /Save group binding|保存群绑定/ }));
}

it("saves the card language and clears it back to the platform default", async () => {
  const onSave = editor("en-US", binding);
  const user = userEvent.setup();
  const form = await screen.findByRole("form", { name: "Edit group binding" });
  const select = within(form).getByRole("combobox", { name: "Card language" });
  expect(select).toHaveTextContent("Platform default (Chinese)");
  await user.click(select);
  await user.click(await screen.findByRole("option", { name: "English" }));
  await save(form);
  await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ language: "en-US" })));

  onSave.mockClear();
  document.body.innerHTML = "";
  const cleared = editor("en-US", { ...binding, language: "zh-CN" }, "lark");
  const again = await screen.findByRole("form", { name: "Edit group binding" });
  const current = within(again).getByRole("combobox", { name: "Card language" });
  expect(current).toHaveTextContent("简体中文");
  await user.click(current);
  await user.click(await screen.findByRole("option", { name: "Platform default (English)" }));
  await save(again);
  await waitFor(() => expect(cleared).toHaveBeenCalledWith(expect.objectContaining({ language: "" })));
});

it("names the card language row in Chinese", async () => {
  editor("zh-CN", binding);
  const form = await screen.findByRole("form", { name: "编辑群绑定" });
  expect(within(form).getByRole("combobox", { name: "卡片语言" })).toHaveTextContent("平台默认（中文）");
  expect(within(form).getByText("Flow 在此群中发送的卡片和消息所用的语言。")).toBeInTheDocument();
});
