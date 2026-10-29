import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import * as api from "@/lib/chat-integrations-api";
import { makeBootstrap, project } from "@/test/fixtures";
import type { ChatBinding } from "@/types/chat-integrations";
import type { Project } from "@/types/flow";

import { ChatBindingEditor, ChatBindingRow } from "./chat-group-bindings";

vi.mock("@/lib/chat-integrations-api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/lib/chat-integrations-api")>(),
  fetchChatIntegrationChats: vi.fn(),
}));

const projects: Project[] = [
  { ...project, id: "project-a", name: "Alpha" },
  { ...project, id: "project-b", name: "Beta" },
  { ...project, id: "project-c", name: "Gamma" },
  { ...project, id: "project-shared", name: "Shared", teamIds: ["team-1", "team-private"] },
];
const data = () => makeBootstrap({ projects });

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.clearAllMocks();
  vi.mocked(api.fetchChatIntegrationChats).mockResolvedValue({ items: [{ id: "chat-1", name: "Engineering", external: false }], hasMore: false });
});

function editor(locale: "en-US" | "zh-CN", binding?: ChatBinding) {
  localStorage.setItem("flow:locale", locale);
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<I18nProvider><ChatBindingEditor provider="feishu" data={data()} bindableTeamIds={["team-1"]} privateTeamIds={["team-private"]} binding={binding} busy={false} onCancel={vi.fn()} onSave={onSave} /></I18nProvider>);
  return onSave;
}

it("binds a chosen set of the team's projects with the shared multi-select picker", async () => {
  const onSave = editor("en-US");
  const user = userEvent.setup();
  const form = await screen.findByRole("form", { name: "Add group binding" });
  await user.click(within(form).getByRole("combobox", { name: "Group" }));
  await user.click(await screen.findByRole("option", { name: "Engineering" }));
  await user.click(within(form).getByRole("combobox", { name: "Team" }));
  await user.click(await screen.findByRole("option", { name: "Test team" }));
  const picker = within(form).getByRole("combobox", { name: "Projects" });
  expect(picker).toHaveTextContent("All team projects");
  await user.click(picker);
  // A project shared with a private team is not offered.
  expect(screen.queryByRole("option", { name: "Shared" })).not.toBeInTheDocument();
  await user.click(await screen.findByRole("option", { name: "Alpha" }));
  await user.click(await screen.findByRole("option", { name: "Gamma" }));
  expect(picker).toHaveTextContent("2 projects");
  await user.keyboard("{Escape}");
  await user.click(within(form).getByRole("checkbox", { name: /I approve sharing/ }));
  await user.click(within(form).getByRole("button", { name: "Save group binding" }));
  await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ chatId: "chat-1", teamId: "team-1", projectIds: ["project-a", "project-c"], authorize: true })));
  expect(onSave.mock.calls[0][0]).not.toHaveProperty("projectId");
});

it("edits a single-project binding as a one-project set and can widen it to all projects", async () => {
  const binding: ChatBinding = { id: "binding-1", connectionId: "c", chatId: "chat-1", chatName: "Engineering", teamId: "team-1", projectId: "project-b", events: ["issue.created"], enabled: true, allowCreate: true, allowUpdate: true };
  const onSave = editor("en-US", binding);
  const user = userEvent.setup();
  const form = await screen.findByRole("form", { name: "Edit group binding" });
  const picker = within(form).getByRole("combobox", { name: "Projects" });
  expect(picker).toHaveTextContent("Beta");
  await user.click(picker);
  await user.click(await screen.findByRole("option", { name: "All team projects" }));
  expect(picker).toHaveTextContent("All team projects");
  await user.keyboard("{Escape}");
  await user.click(within(form).getByRole("checkbox", { name: /I approve sharing/ }));
  await user.click(within(form).getByRole("button", { name: "Save group binding" }));
  await waitFor(() => expect(onSave).toHaveBeenCalled());
  expect(onSave.mock.calls[0][0]).not.toHaveProperty("projectIds");
});

it("names the project picker and a project set in Chinese", async () => {
  editor("zh-CN");
  const user = userEvent.setup();
  const form = await screen.findByRole("form", { name: "添加群绑定" });
  await user.click(within(form).getByRole("combobox", { name: "团队" }));
  await user.click(await screen.findByRole("option", { name: "Test team" }));
  const picker = within(form).getByRole("combobox", { name: "项目" });
  expect(picker).toHaveTextContent("所有团队项目");
  expect(within(form).getByText("通知和群内操作仅涵盖这些项目的事项。")).toBeInTheDocument();
  await user.click(picker);
  await user.click(await screen.findByRole("option", { name: "Alpha" }));
  await user.click(await screen.findByRole("option", { name: "Beta" }));
  expect(picker).toHaveTextContent("2 个项目");
});

it("lists a binding's project set in its row", () => {
  localStorage.setItem("flow:locale", "en-US");
  const binding: ChatBinding = { id: "binding-2", connectionId: "c", chatId: "chat-1", chatName: "Engineering", teamId: "team-1", projectIds: ["project-a", "project-b"], events: ["issue.created"], enabled: true, allowCreate: false, allowUpdate: false };
  render(<I18nProvider><ChatBindingRow binding={binding} data={data()} busy="" canEdit onRevoke={vi.fn()} onRemove={vi.fn()} /></I18nProvider>);
  const set = screen.getByText("2 projects");
  expect(set).toHaveAttribute("title", "Alpha, Beta");
});
