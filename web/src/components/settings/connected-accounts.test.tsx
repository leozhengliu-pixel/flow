import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import type { CodeHostAccount, CodeHostAccountsResponse } from "@/types/code-host-accounts";

const api = vi.hoisted(() => ({
  fetchCodeHostAccounts: vi.fn(),
  authorizeGitHubAccount: vi.fn(),
  authorizeGitLabAccount: vi.fn(),
  connectGitLabToken: vi.fn(),
  disconnectCodeHostAccount: vi.fn(),
}));
const dialogs = vi.hoisted(() => ({ confirmAction: vi.fn() }));
vi.mock("@/components/ui/action-dialog-service", () => dialogs);
vi.mock("@/lib/code-host-accounts-api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/code-host-accounts-api")>("@/lib/code-host-accounts-api");
  return { ...actual, ...api };
});

import { MissingScopesError } from "@/lib/code-host-accounts-api";
import { CodeHostConnectionRows, PersonalCodeHostRow } from "./code-host-accounts";

const account = (overrides: Partial<CodeHostAccount> = {}): CodeHostAccount => ({
  id: "cha_1",
  codeHost: "github",
  hostName: "github.com",
  baseUrl: "https://github.com",
  login: "alice",
  externalUserId: "1",
  authType: "oauth",
  scopes: [],
  status: "active",
  canAct: true,
  linkedAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  ...overrides,
});

const response = (overrides: Partial<CodeHostAccountsResponse> = {}): CodeHostAccountsResponse => ({
  accounts: [],
  github: { available: true, method: "oauth", hostName: "github.com" },
  gitlabInstances: [],
  ...overrides,
});

const instances = [
  { id: "gl_1", name: "GitLab", baseUrl: "https://gitlab.com", hostName: "gitlab.com", oauthAvailable: false },
  { id: "gl_2", name: "Corp", baseUrl: "https://gitlab.corp.test", hostName: "gitlab.corp.test", oauthAvailable: true },
];

function renderRows() {
  return render(
    <I18nProvider>
      <CodeHostConnectionRows githubDescription="Review code in Flow" />
    </I18nProvider>,
  );
}

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  dialogs.confirmAction.mockReset();
  dialogs.confirmAction.mockResolvedValue(true);
});

function openAccountMenu(name: string) {
  fireEvent.keyDown(screen.getByRole("button", { name: `${name} account options` }), { key: "Enter" });
}

it("shows a linked GitHub account as Connected and disconnects after confirming", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response({ accounts: [account()] }));
  api.disconnectCodeHostAccount.mockResolvedValue(undefined);
  renderRows();
  expect(await screen.findByText(/@alice/)).toBeInTheDocument();
  expect(screen.getByText("github.com")).toBeInTheDocument();
  openAccountMenu("GitHub");
  fireEvent.click(await screen.findByRole("menuitem", { name: "Disconnect…" }));
  await waitFor(() =>
    expect(dialogs.confirmAction).toHaveBeenCalledWith(
      "Disconnect personal GitHub account?",
      expect.objectContaining({ confirmLabel: "Disconnect" }),
    ),
  );
  await waitFor(() => expect(api.disconnectCodeHostAccount).toHaveBeenCalledWith("cha_1"));
});

it("keeps the account when the disconnect is cancelled", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response({ accounts: [account()] }));
  dialogs.confirmAction.mockResolvedValueOnce(false);
  renderRows();
  expect(await screen.findByText(/@alice/)).toBeInTheDocument();
  openAccountMenu("GitHub");
  fireEvent.click(await screen.findByRole("menuitem", { name: "Disconnect…" }));
  await waitFor(() => expect(dialogs.confirmAction).toHaveBeenCalled());
  expect(api.disconnectCodeHostAccount).not.toHaveBeenCalled();
});

it("shows the workspace organization on the linked GitHub row", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response({ accounts: [account()] }));
  render(
    <I18nProvider>
      <CodeHostConnectionRows githubDescription="Review code in Flow" githubOrganization="Acme" />
    </I18nProvider>,
  );
  expect(await screen.findByText("Acme")).toBeInTheDocument();
});

it("offers Reconnect when the token needs renewing", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(
    response({ accounts: [account({ status: "reconnect", statusReason: "Token revoked" })] }),
  );
  api.authorizeGitHubAccount.mockResolvedValue({ url: "https://github.com/login/oauth/authorize?x" });
  const assign = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, assign } as Location);
  renderRows();
  expect(await screen.findByText(/Token revoked/)).toBeInTheDocument();
  expect(screen.getByText("Reconnect needed")).toBeInTheDocument();
  openAccountMenu("GitHub");
  fireEvent.click(await screen.findByRole("menuitem", { name: "Reconnect" }));
  await waitFor(() => expect(api.authorizeGitHubAccount).toHaveBeenCalledWith("account"));
  await waitFor(() => expect(assign).toHaveBeenCalledWith("https://github.com/login/oauth/authorize?x"));
});

it("shows a linked GitLab account with the color logo and keeps self-managed hosts", async () => {
  const linked = account({ id: "cha_gl", codeHost: "gitlab", login: "bob", hostName: "gitlab.corp.test", instanceId: "gl_2" });
  api.fetchCodeHostAccounts.mockResolvedValue(response({ accounts: [linked] }));
  const { container } = renderRows();
  expect(await screen.findByText(/@bob/)).toBeInTheDocument();
  expect(screen.getByText("gitlab.corp.test")).toBeInTheDocument();
  expect(container.querySelector('[data-testid="gitlab-account-row"] path[fill="#FC6D26"]')).not.toBeNull();
  // A linked account with no other instance needs no "ask an admin" row.
  expect(screen.queryByText("Ask an admin to connect GitLab")).toBeNull();
});

it("lists only the provided GitLab instances and shows the token form for one without OAuth", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response({ gitlabInstances: instances }));
  renderRows();
  const select = await screen.findByLabelText("GitLab instance");
  const labels = Array.from((select as HTMLSelectElement).options).map((option) => option.textContent);
  expect(labels).toEqual(["Select an instance", "GitLab — gitlab.com", "Corp — gitlab.corp.test"]);
  expect(screen.queryByLabelText("Personal access token")).toBeNull();
  fireEvent.change(select, { target: { value: "gl_1" } });
  expect(screen.getByLabelText("Personal access token")).toHaveAttribute("type", "password");
  expect(screen.getByRole("link", { name: "Create a token" })).toHaveAttribute(
    "href",
    "https://gitlab.com/-/user_settings/personal_access_tokens?name=Flow&scopes=api,read_user",
  );
});

it("submits the token and shows missing scopes on a 422", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response({ gitlabInstances: [instances[0]] }));
  api.connectGitLabToken.mockRejectedValueOnce(new MissingScopesError("Token is missing scopes", ["api", "read_user"]));
  renderRows();
  fireEvent.change(await screen.findByLabelText("Personal access token"), { target: { value: "glpat-1" } });
  fireEvent.click(screen.getAllByRole("button", { name: "Connect" }).at(-1)!);
  await waitFor(() => expect(api.connectGitLabToken).toHaveBeenCalledWith("gl_1", "glpat-1"));
  expect(await screen.findByText("Token is missing scopes")).toBeInTheDocument();
  expect(screen.getByText("api, read_user")).toBeInTheDocument();
});

it("reloads accounts after a token is accepted", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response({ gitlabInstances: [instances[0]] }));
  api.connectGitLabToken.mockResolvedValue(account({ id: "cha_2", codeHost: "gitlab", login: "bob", hostName: "gitlab.com" }));
  renderRows();
  fireEvent.change(await screen.findByLabelText("Personal access token"), { target: { value: "glpat-2" } });
  fireEvent.click(screen.getAllByRole("button", { name: "Connect" }).at(-1)!);
  await waitFor(() => expect(api.fetchCodeHostAccounts).toHaveBeenCalledTimes(2));
});

it("starts OAuth for an instance with an OAuth application", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response({ gitlabInstances: instances }));
  api.authorizeGitLabAccount.mockResolvedValue({ url: "https://gitlab.corp.test/oauth/authorize?x" });
  const assign = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, assign } as Location);
  renderRows();
  fireEvent.change(await screen.findByLabelText("GitLab instance"), { target: { value: "gl_2" } });
  expect(screen.queryByLabelText("Personal access token")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Connect with GitLab" }));
  await waitFor(() => expect(api.authorizeGitLabAccount).toHaveBeenCalledWith("gl_2", "account"));
  await waitFor(() => expect(assign).toHaveBeenCalledWith("https://gitlab.corp.test/oauth/authorize?x"));
});

it("offers only GitLab instances the member has not linked yet", async () => {
  const linked = account({ id: "cha_gl", codeHost: "gitlab", login: "bob", hostName: "gitlab.corp.test", instanceId: "gl_2" });
  api.fetchCodeHostAccounts.mockResolvedValue(response({ accounts: [linked], gitlabInstances: instances }));
  const { unmount } = renderRows();
  expect(await screen.findByText(/@bob/)).toBeInTheDocument();
  // Only the unlinked instance is offered, straight to its token form.
  const select = screen.getByLabelText("GitLab instance") as HTMLSelectElement;
  expect(Array.from(select.options).map((option) => option.value)).toEqual(["gl_1"]);
  expect(screen.getByLabelText("Personal access token")).toBeInTheDocument();
  unmount();
  // Every instance linked: no connect row at all.
  api.fetchCodeHostAccounts.mockResolvedValue(response({ accounts: [linked], gitlabInstances: [instances[1]] }));
  renderRows();
  expect(await screen.findByText(/@bob/)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Connect with GitLab" })).toBeNull();
  expect(screen.queryByText("Ask an admin to connect GitLab")).toBeNull();
});

it("tells members to ask an admin when no GitLab instance exists", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response());
  renderRows();
  expect(await screen.findByText("Ask an admin to connect GitLab")).toBeInTheDocument();
  expect(screen.queryByLabelText("GitLab instance")).toBeNull();
});

it("shows the personal GitHub row as Connected with Manage", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response({ accounts: [account()] }));
  const onManage = vi.fn();
  render(<I18nProvider><PersonalCodeHostRow codeHost="github" onManage={onManage} /></I18nProvider>);
  expect(await screen.findByText("Your personal GitHub account")).toBeInTheDocument();
  expect(screen.getByText("@alice")).toBeInTheDocument();
  expect(screen.getByText(/Connected/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Manage" }));
  expect(onManage).toHaveBeenCalled();
});

it("shows Not connected and starts the integration flow when only classic OAuth is available", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(response());
  api.authorizeGitHubAccount.mockResolvedValue({ url: "https://github.com/login/oauth/authorize?y" });
  const assign = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, assign } as Location);
  render(<I18nProvider><PersonalCodeHostRow codeHost="github" /></I18nProvider>);
  expect(await screen.findByText("Not connected")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Connect" }));
  await waitFor(() => expect(api.authorizeGitHubAccount).toHaveBeenCalledWith("integration"));
});

it("shows Reconnect needed on the personal GitLab row", async () => {
  api.fetchCodeHostAccounts.mockResolvedValue(
    response({ accounts: [account({ codeHost: "gitlab", login: "bob", status: "reconnect", instanceId: "gl_1" })], gitlabInstances: [instances[0]] }),
  );
  render(<I18nProvider><PersonalCodeHostRow codeHost="gitlab" /></I18nProvider>);
  expect(await screen.findByText(/Reconnect needed/)).toBeInTheDocument();
  expect(screen.getByText("@bob")).toBeInTheDocument();
});
