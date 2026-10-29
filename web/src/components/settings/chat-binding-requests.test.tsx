import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import * as api from "@/lib/chat-integrations-api";
import { makeBootstrap } from "@/test/fixtures";
import type { ChatBinding } from "@/types/chat-integrations";
import type { IssueTemplate } from "@/types/flow";

import { ChatBindingEditor, ChatBindingRow } from "./chat-group-bindings";

vi.mock("@/lib/chat-integrations-api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/lib/chat-integrations-api")>(),
  fetchChatIntegrationChats: vi.fn(),
}));

const template = { id: "template-bug", name: "Bug report", teamId: "team-1", title: "", body: "", priority: 0, labelIds: [] } as unknown as IssueTemplate;
const data = () => makeBootstrap({ issueTemplates: [template] });
const binding: ChatBinding = { id: "binding-1", connectionId: "c", chatId: "chat-1", chatName: "Engineering", teamId: "team-1", events: ["issue.created"], enabled: true, allowCreate: true, allowUpdate: true };

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.clearAllMocks();
  vi.mocked(api.fetchChatIntegrationChats).mockResolvedValue({ items: [{ id: "chat-1", name: "Engineering", external: false }], hasMore: false });
});

function editor(locale: "en-US" | "zh-CN", current?: ChatBinding) {
  localStorage.setItem("flow:locale", locale);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<I18nProvider><ChatBindingEditor provider="feishu" data={data()} bindableTeamIds={["team-1"]} binding={current} busy={false} onCancel={vi.fn()} onSave={onSave} /></I18nProvider>);
  return onSave;
}

async function choose(form: HTMLElement, name: string, option: string) {
  const user = userEvent.setup();
  await user.click(within(form).getByRole("combobox", { name }));
  await user.click(await screen.findByRole("option", { name: option }));
}

it("turns on requests with a reaction, template and priority", async () => {
  const onSave = editor("en-US", binding);
  const user = userEvent.setup();
  const form = await screen.findByRole("form", { name: "Edit group binding" });
  expect(within(form).queryByRole("combobox", { name: "Request reaction" })).not.toBeInTheDocument();
  await user.click(within(form).getByRole("checkbox", { name: "Take requests" }));
  await choose(form, "Request reaction", "✅ Done");
  await choose(form, "Request template", "Bug report");
  await choose(form, "Request priority", "High");
  await user.click(within(form).getByRole("checkbox", { name: /I approve sharing/ }));
  await user.click(within(form).getByRole("button", { name: "Save group binding" }));
  await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ requests: { enabled: true, emoji: "DONE", templateId: "template-bug", priority: 2 } })));
});

it("turns requests off and leaves bindings without them untouched", async () => {
  const onSave = editor("en-US", { ...binding, requests: { enabled: true, emoji: "OK", priority: 1 } });
  const user = userEvent.setup();
  const form = await screen.findByRole("form", { name: "Edit group binding" });
  expect(within(form).getByRole("combobox", { name: "Request reaction" })).toHaveTextContent("👌 OK");
  await user.click(within(form).getByRole("checkbox", { name: "Take requests" }));
  await user.click(within(form).getByRole("checkbox", { name: /I approve sharing/ }));
  await user.click(within(form).getByRole("button", { name: "Save group binding" }));
  await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ requests: { enabled: false, emoji: "OK", priority: 1 } })));

  onSave.mockClear();
  document.body.innerHTML = "";
  const untouched = editor("en-US", binding);
  const again = await screen.findByRole("form", { name: "Edit group binding" });
  await user.click(within(again).getByRole("checkbox", { name: /I approve sharing/ }));
  await user.click(within(again).getByRole("button", { name: "Save group binding" }));
  await waitFor(() => expect(untouched).toHaveBeenCalled());
  expect(untouched.mock.calls[0][0]).not.toHaveProperty("requests");
});

it("names the request settings in Chinese and lists them in the row", async () => {
  editor("zh-CN", { ...binding, requests: { enabled: true } });
  const form = await screen.findByRole("form", { name: "编辑群绑定" });
  expect(within(form).getByRole("checkbox", { name: "接收需求" })).toBeChecked();
  expect(within(form).getByRole("combobox", { name: "需求表情" })).toHaveTextContent("不使用表情");
  expect(within(form).getByRole("combobox", { name: "需求优先级" })).toHaveTextContent("团队默认");
  expect(within(form).getByText(/@ 机器人、添加下方的表情回复/)).toBeInTheDocument();

  document.body.innerHTML = "";
  render(<I18nProvider><ChatBindingRow binding={{ ...binding, requests: { enabled: true } }} data={data()} busy="" canEdit onRevoke={vi.fn()} onRemove={vi.fn()} /></I18nProvider>);
  expect(screen.getByText(/需求收集已开启/)).toBeInTheDocument();
});
