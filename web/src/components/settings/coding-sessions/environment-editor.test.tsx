import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  addCodingEnvironmentSecrets,
  applyCodingEnvironmentProposal,
  archiveCodingEnvironment,
  cancelCodingSnapshot,
  configureCodingEnvironmentWithAgent,
  createCodingEnvironment,
  deleteCodingEnvironmentSecret,
  fetchCodingEnvironment,
  fetchCodingEnvironments,
  fetchCodingModels,
  fetchCodingRepositories,
  fetchCodingSnapshotLogs,
  fetchCodingSnapshots,
  fetchCodingStatus,
  fixCodingEnvironmentWithAgent,
  importCodingEnvironmentFile,
  refreshCodingSnapshot,
  updateCodingEnvironment,
} from "@/lib/api";
import { ApiError } from "@/lib/api-client";
import { toast } from "@/lib/toast";
import type { CodingEnvironmentSnapshot } from "@/types/flow";
import { ROOT, makeEnvironment, makeStatus, renderCodingSettings } from "./coding-sessions-test-kit";

vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  fetchCodingStatus: vi.fn(),
  fetchCodingModels: vi.fn(),
  fetchCodingEnvironments: vi.fn(),
  fetchCodingEnvironment: vi.fn(),
  fetchCodingRepositories: vi.fn(),
  createCodingEnvironment: vi.fn(),
  updateCodingEnvironment: vi.fn(),
  archiveCodingEnvironment: vi.fn(),
  addCodingEnvironmentSecrets: vi.fn(),
  deleteCodingEnvironmentSecret: vi.fn(),
  importCodingEnvironmentFile: vi.fn(),
  fetchCodingSnapshots: vi.fn(),
  refreshCodingSnapshot: vi.fn(),
  cancelCodingSnapshot: vi.fn(),
  fetchCodingSnapshotLogs: vi.fn(),
  configureCodingEnvironmentWithAgent: vi.fn(),
  fixCodingEnvironmentWithAgent: vi.fn(),
  applyCodingEnvironmentProposal: vi.fn(),
}));
vi.mock("@/lib/toast", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

function snapshot(overrides: Partial<CodingEnvironmentSnapshot> = {}): CodingEnvironmentSnapshot {
  return { id: "snap1", environmentId: "env1", status: "ready", strategy: "archive", configHash: "h", image: "img", baseRef: "main", baseSha: "abcdef1234", sizeBytes: 1024 * 1024 * 3, createdAt: "2026-10-01T00:00:00Z", ...overrides };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem("flow:locale", "en-US");
  vi.mocked(fetchCodingStatus).mockResolvedValue(makeStatus());
  vi.mocked(fetchCodingModels).mockResolvedValue([]);
  vi.mocked(fetchCodingEnvironments).mockResolvedValue([]);
  vi.mocked(fetchCodingSnapshots).mockResolvedValue([]);
  vi.mocked(fetchCodingRepositories).mockResolvedValue({ repositories: [
    { codeHost: "github", hostName: "github.com", fullName: "acme/web", id: "1", defaultBranch: "main", webUrl: "", private: true, archived: false },
    { codeHost: "github", hostName: "github.com", fullName: "acme/old", id: "2", defaultBranch: "main", webUrl: "", private: false, archived: true },
  ] });
});

const NEW = `${ROOT}/environments/new`;
const EDIT = `${ROOT}/environments/env1`;

async function choose(user: ReturnType<typeof userEvent.setup>, combobox: string, option: RegExp | string) {
  await user.click(screen.getByRole("combobox", { name: combobox }));
  await user.click(await screen.findByRole("menuitem", { name: option }));
}

async function addFile(user: ReturnType<typeof userEvent.setup>, path: string) {
  await user.click(within(screen.getByTestId("files-editor")).getByRole("button", { name: "Add file" }));
  const dialog = await screen.findByRole("dialog", { name: "Add file" });
  await user.type(within(dialog).getByLabelText("File path"), path);
  await user.click(within(dialog).getByRole("button", { name: "Add file" }));
}

describe("environment editor: create", () => {
  it("creates an environment with tools, files, variables and secrets", async () => {
    const user = userEvent.setup();
    vi.mocked(createCodingEnvironment).mockResolvedValue(makeEnvironment({ id: "env-new" }));
    renderCodingSettings({ path: NEW });
    expect(await screen.findByRole("heading", { level: 1, name: "New environment" })).toBeVisible();
    expect(screen.getByText("Configure this coding environment.")).toBeVisible();
    // Like the reference app there is no Name field and no Configure with agent before the environment exists.
    expect(screen.queryByLabelText("Name")).toBeNull();
    expect(screen.queryByRole("button", { name: "Configure with agent" })).toBeNull();
    const save = screen.getByRole("button", { name: "Create environment" });
    expect(save).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Repository" }));
    const option = await screen.findByRole("button", { name: /acme\/web/ });
    expect(screen.queryByRole("button", { name: /acme\/old/ })).toBeNull();
    expect(screen.getByPlaceholderText("Search repositories...")).toBeVisible();
    await user.click(option);
    expect(fetchCodingRepositories).toHaveBeenCalledWith({ codeHost: "github", hostName: "github.com", q: undefined });
    expect(screen.getByRole("button", { name: "Repository" })).toHaveTextContent("acme/web");

    await choose(user, "Add tool", "Node.js");
    await choose(user, "Node.js version", "22");
    await addFile(user, ".npmrc");
    fireEvent.change(screen.getByLabelText("File content"), { target: { value: "registry=x" } });
    await user.click(screen.getByRole("button", { name: "Add variable" }));
    await user.type(screen.getByLabelText("Variable name"), "PORT");
    await user.type(screen.getByLabelText("Variable value"), "3000");
    await user.click(screen.getByRole("button", { name: "Add variable" }));
    await user.type(screen.getAllByLabelText("Variable name")[1], "NPM_TOKEN");
    await user.click(screen.getAllByRole("checkbox", { name: "Secret" })[1]);
    const secretValue = screen.getByLabelText("Secret value");
    expect(secretValue).toHaveAttribute("type", "password");
    expect(screen.getByLabelText("Secret name")).toHaveValue("NPM_TOKEN");
    await user.type(secretValue, "s3cret");
    fireEvent.change(screen.getByLabelText("Prepare script"), { target: { value: "npm ci" } });
    fireEvent.change(screen.getByLabelText("Custom guidance"), { target: { value: "Run npm test" } });

    await choose(user, "Sandbox size", /^Large/);
    await choose(user, "Network access", /^Extended/);
    fireEvent.change(screen.getByLabelText("Allowed domains"), { target: { value: "example.com" } });
    await choose(user, "Dependency management", /^Custom only/);
    await choose(user, "Environment agent", /^Claude Code/);

    await user.click(save);
    await waitFor(() => expect(createCodingEnvironment).toHaveBeenCalledTimes(1));
    expect(createCodingEnvironment).toHaveBeenCalledWith({
      name: "web",
      codeHost: "github",
      hostName: "github.com",
      repository: "acme/web",
      repositoryId: "1",
      defaultBranch: "main",
      harness: "claudeCode",
      model: "auto",
      sandboxSize: "large",
      network: "extended",
      allowedDomains: ["example.com"],
      mise: { tools: { node: "22" }, files: [{ path: ".npmrc", content: "registry=x" }], environmentVariables: [{ name: "PORT", value: "3000" }] },
      prepareScript: "npm ci",
      customGuidance: "Run npm test",
      dependencyManagement: "custom-only",
      secrets: [{ name: "NPM_TOKEN", value: "s3cret" }],
    });
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(`${ROOT}/environments/env-new`));
  });

  it("shows two-line sandbox sizes from the server", async () => {
    const user = userEvent.setup();
    renderCodingSettings({ path: NEW });
    await user.click(await screen.findByRole("combobox", { name: "Sandbox size" }));
    expect(await screen.findByRole("menuitem", { name: /Medium\s*2 vCPU · 16 GB memory/ })).toBeVisible();
  });

  it("accepts a typed repository when the listing has no match", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingRepositories).mockResolvedValue({ repositories: [] });
    renderCodingSettings({ path: NEW });
    await user.click(await screen.findByRole("button", { name: "Repository" }));
    await user.type(screen.getByLabelText("Search repositories"), "acme/mobile");
    await user.click(await screen.findByRole("button", { name: "Use acme/mobile" }));
    expect(screen.getByRole("button", { name: "Repository" })).toHaveTextContent("acme/mobile");
  });

  it("loads more repositories with the cursor", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingRepositories)
      .mockResolvedValueOnce({ repositories: [{ codeHost: "github", hostName: "github.com", fullName: "acme/a", defaultBranch: "main", webUrl: "", private: false, archived: false }], nextCursor: "c2" })
      .mockResolvedValueOnce({ repositories: [{ codeHost: "github", hostName: "github.com", fullName: "acme/b", defaultBranch: "main", webUrl: "", private: false, archived: false }] });
    renderCodingSettings({ path: NEW });
    await user.click(await screen.findByRole("button", { name: "Repository" }));
    await user.click(await screen.findByRole("button", { name: "Load more" }));
    expect(await screen.findByRole("button", { name: "acme/b" })).toBeVisible();
    expect(fetchCodingRepositories).toHaveBeenLastCalledWith({ codeHost: "github", hostName: "github.com", q: undefined, cursor: "c2" });
  });

  it("switches the code host to GitLab when both are connected", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingStatus).mockResolvedValue(makeStatus({ codeHosts: [{ codeHost: "github", hostName: "github.com", connected: true }, { codeHost: "gitlab", hostName: "gitlab.acme.dev", connected: true }] }));
    renderCodingSettings({ path: NEW });
    await user.click(await screen.findByRole("combobox", { name: "Code host" }));
    expect(screen.queryByTestId("gitlab-credential-note")).toBeNull();
    await user.click(await screen.findByRole("menuitem", { name: "GitLab" }));
    expect(screen.getByTestId("gitlab-credential-note")).toHaveTextContent(/api scope and the Maintainer role.*Premium or Ultimate/);
    await user.click(screen.getByRole("button", { name: "Repository" }));
    await waitFor(() => expect(fetchCodingRepositories).toHaveBeenLastCalledWith({ codeHost: "gitlab", hostName: "gitlab.acme.dev", q: undefined }));
  });

  it("hides the code host select with a single connected host", async () => {
    renderCodingSettings({ path: NEW });
    await screen.findByRole("button", { name: "Repository" });
    expect(screen.queryByRole("combobox", { name: "Code host" })).toBeNull();
  });

  it("explains the configured GitLab credential mode", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingStatus).mockResolvedValue(makeStatus({ codeHosts: [{ codeHost: "github", hostName: "github.com", connected: true }, { codeHost: "gitlab", hostName: "gitlab.com", connected: true, credentialMode: "connection" }] }));
    renderCodingSettings({ path: NEW });
    await user.click(await screen.findByRole("combobox", { name: "Code host" }));
    await user.click(await screen.findByRole("menuitem", { name: "GitLab" }));
    expect(screen.getByTestId("gitlab-credential-note")).toHaveTextContent(/connection token itself.*Prefer project access tokens/);
  });

  it("blocks a second active environment for the same repository", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingEnvironments).mockResolvedValue([makeEnvironment()]);
    renderCodingSettings({ path: NEW });
    await user.click(await screen.findByRole("button", { name: "Repository" }));
    await user.click(await screen.findByRole("button", { name: /acme\/web/ }));
    expect(await screen.findByText(/Web already uses this repository/)).toBeVisible();
    expect(screen.getByRole("button", { name: "Create environment" })).toBeDisabled();
  });

  it("shows the first validation problem", async () => {
    const user = userEvent.setup();
    renderCodingSettings({ path: NEW });
    await user.click(await screen.findByRole("button", { name: "Add variable" }));
    await user.type(screen.getByLabelText("Variable name"), "X");
    expect(await screen.findByRole("alert")).toHaveTextContent("Choose a repository");
  });
});

describe("environment editor: tools and files", () => {
  it("adds, versions and removes preset tools and keeps custom versions", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ mise: { tools: { node: "20.11", ruby: "3.4" }, files: [], environmentVariables: [] } }));
    vi.mocked(updateCodingEnvironment).mockResolvedValue(makeEnvironment());
    renderCodingSettings({ path: EDIT });
    const node = await screen.findByTestId("tool-node");
    expect(within(node).getByText("Node.js")).toBeVisible();
    expect(within(node).getByRole("combobox", { name: "Node.js version" })).toHaveTextContent("20.11");
    await user.click(within(node).getByRole("combobox", { name: "Node.js version" }));
    expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual(["latest", "26", "24", "22", "20.11"]);
    await user.keyboard("{Escape}");

    await user.click(screen.getByRole("combobox", { name: "Add tool" }));
    const options = (await screen.findAllByRole("menuitem")).map((item) => item.textContent);
    expect(options).toEqual(["Bun", "Deno", "Python", "Go", "Rust", "Java", ".NET", "PostgreSQL", "Redis", "MongoDB", "ClickHouse"]);
    await user.click(screen.getByRole("menuitem", { name: "PostgreSQL" }));
    expect(screen.getByRole("combobox", { name: "PostgreSQL version" })).toHaveTextContent("latest");
    await user.click(screen.getByRole("button", { name: "Remove Ruby" }));
    expect(screen.queryByTestId("tool-ruby")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(updateCodingEnvironment).toHaveBeenCalledWith("env1", expect.objectContaining({ mise: expect.objectContaining({ tools: { node: "20.11", postgres: "latest" } }) })));
  });

  it("adds, renames and removes files through the modal", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    renderCodingSettings({ path: EDIT });
    const files = await screen.findByTestId("files-editor");
    expect(within(files).getByText("No files added. Add a file to set its path and contents.")).toBeVisible();

    await user.click(within(files).getByRole("button", { name: "Add file" }));
    const dialog = await screen.findByRole("dialog", { name: "Add file" });
    expect(within(dialog).getByText("Add files to the sandbox’s home directory")).toBeVisible();
    const input = within(dialog).getByPlaceholderText(".npmrc");
    expect(within(dialog).getByRole("button", { name: "Add file" })).toBeDisabled();
    await user.type(input, ".npmrc{Enter}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(within(files).getByRole("tab", { name: ".npmrc" })).toHaveAttribute("aria-selected", "true");
    expect(within(files).getByLabelText("File content")).toHaveAttribute("placeholder", "File contents");

    await user.click(within(files).getByRole("button", { name: "Rename file" }));
    const rename = await screen.findByRole("dialog", { name: "Rename file" });
    const field = within(rename).getByLabelText("File path");
    expect(field).toHaveValue(".npmrc");
    await user.clear(field);
    await user.type(field, ".config/app.json");
    await user.click(within(rename).getByRole("button", { name: "Rename" }));
    expect(await within(files).findByRole("tab", { name: ".config/app.json" })).toBeVisible();

    await user.click(within(files).getByRole("button", { name: "Remove file" }));
    expect(within(files).queryByRole("tab")).toBeNull();
    expect(within(files).getByText("No files added. Add a file to set its path and contents.")).toBeVisible();
  });
});

describe("environment editor: edit", () => {
  it("saves with the version and adds rows marked Secret as secrets", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    vi.mocked(updateCodingEnvironment).mockResolvedValue(makeEnvironment({ version: 4, prepareScript: "pnpm i" }));
    vi.mocked(addCodingEnvironmentSecrets).mockResolvedValue(makeEnvironment({ version: 5, prepareScript: "pnpm i", secrets: [{ name: "NPM_TOKEN", createdAt: "" }, { name: "API_KEY", createdAt: "" }] }));
    renderCodingSettings({ path: EDIT });
    expect(await screen.findByRole("heading", { level: 1, name: "Edit environment" })).toBeVisible();
    expect(screen.getByTestId("environment-repository")).toHaveTextContent("acme/web");
    expect(screen.getByText("Create a new environment to use a different repository")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Repository" })).toBeNull();
    const save = screen.getByRole("button", { name: "Save changes" });
    expect(save).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Prepare script"), { target: { value: "pnpm i" } });
    // Configure with agent works on saved settings only.
    expect(screen.getByRole("button", { name: "Configure with agent" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Add variable" }));
    const names = screen.getAllByLabelText("Variable name");
    await user.type(names[names.length - 1], "API_KEY");
    const checks = screen.getAllByRole("checkbox", { name: "Secret" });
    await user.click(checks[checks.length - 1]);
    await user.type(screen.getByLabelText("Secret value"), "k");
    await user.click(save);
    await waitFor(() => expect(addCodingEnvironmentSecrets).toHaveBeenCalledWith("env1", [{ name: "API_KEY", value: "k" }]));
    expect(updateCodingEnvironment).toHaveBeenCalledWith("env1", expect.objectContaining({ version: 3, prepareScript: "pnpm i", mise: expect.objectContaining({ environmentVariables: [{ name: "NODE_ENV", value: "test" }] }) }));
    expect(updateCodingEnvironment).toHaveBeenCalledWith("env1", expect.not.objectContaining({ ownerIds: expect.anything() }));
    expect(await screen.findByTestId("secret-API_KEY")).toBeVisible();
    expect(screen.queryByLabelText("Secret value")).toBeNull();
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Configure with agent" })).toBeEnabled();
  });

  it("reports a concurrent edit and reloads", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    vi.mocked(updateCodingEnvironment).mockRejectedValue(new ApiError("version mismatch", 409, "version_conflict"));
    renderCodingSettings({ path: EDIT });
    fireEvent.change(await screen.findByLabelText("Prepare script"), { target: { value: "x" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("This environment was changed by someone else.")).toBeVisible();
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ version: 9, prepareScript: "remote" }));
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(screen.getByLabelText("Prepare script")).toHaveValue("remote"));
    expect(screen.queryByText("This environment was changed by someone else.")).toBeNull();
  });

  it("maps a repository conflict from the server", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    vi.mocked(updateCodingEnvironment).mockRejectedValue(new ApiError("repository already has an active environment", 409, "repo_in_use"));
    renderCodingSettings({ path: EDIT });
    fireEvent.change(await screen.findByLabelText("Prepare script"), { target: { value: "x" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Another active environment already uses this repository"));
  });

  it("shows server validation errors inline", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    vi.mocked(updateCodingEnvironment).mockRejectedValue(new ApiError("allowedDomains: invalid domain", 400));
    renderCodingSettings({ path: EDIT });
    fireEvent.change(await screen.findByLabelText("Prepare script"), { target: { value: "x" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("allowedDomains: invalid domain");
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("masks saved secrets and deletes one after confirming", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    vi.mocked(deleteCodingEnvironmentSecret).mockResolvedValue(makeEnvironment({ secrets: [] }));
    renderCodingSettings({ path: EDIT });
    const row = await screen.findByTestId("secret-NPM_TOKEN");
    expect(within(row).getByLabelText("Hidden value")).toHaveTextContent("••••");
    const name = within(row).getByLabelText("Secret name");
    expect(name).toHaveAttribute("readonly");
    const secret = within(row).getByRole("checkbox", { name: "Secret" });
    expect(secret).toBeChecked();
    expect(secret).toBeDisabled();
    expect(within(row).queryByLabelText("Secret value")).toBeNull();
    expect(document.body.textContent).not.toContain("s3cret");
    fireEvent.click(within(row).getByRole("button", { name: "Delete secret NPM_TOKEN" }));
    fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
    await waitFor(() => expect(deleteCodingEnvironmentSecret).toHaveBeenCalledWith("env1", "NPM_TOKEN"));
    await waitFor(() => expect(screen.queryByTestId("secret-NPM_TOKEN")).toBeNull());
  });

  it("shows only the footer when there are no variables", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ secrets: [], mise: { tools: {}, files: [], environmentVariables: [] } }));
    renderCodingSettings({ path: EDIT });
    const card = await screen.findByTestId("variables-editor");
    expect(within(card).queryByText("Name")).toBeNull();
    expect(within(card).getByRole("button", { name: "Import from .env" })).toBeVisible();
    expect(within(card).getByRole("button", { name: "Add variable" })).toBeVisible();
  });

  it("asks before discarding unsaved changes", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    renderCodingSettings({ path: EDIT });
    fireEvent.change(await screen.findByLabelText("Prepare script"), { target: { value: "changed" } });
    fireEvent.click(screen.getByRole("button", { name: "Coding sessions" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Discard changes?")).toBeVisible();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByTestId("location")).toHaveTextContent(EDIT);
    fireEvent.click(screen.getByRole("button", { name: "Coding sessions" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(new RegExp(`^${ROOT}$`)));
  });

  it("leaves without asking when nothing changed", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    renderCodingSettings({ path: EDIT });
    await screen.findByLabelText("Prepare script");
    fireEvent.click(screen.getByRole("button", { name: "Coding sessions" }));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(new RegExp(`^${ROOT}$`)));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("lists the creator as owner and lets admins add owners", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ createdBy: "user-1" }));
    vi.mocked(updateCodingEnvironment).mockResolvedValue(makeEnvironment({ ownerIds: ["user-1", "user-2"] }));
    renderCodingSettings({ path: EDIT });
    const owners = await screen.findByTestId("owners-editor");
    expect(within(owners).getByTestId("owner-user-1")).toHaveTextContent(/Viewer\s*viewer@example\.test/);
    await user.click(within(owners).getByRole("button", { name: "Add owner" }));
    await user.click(await screen.findByRole("menuitem", { name: /Teammate/ }));
    expect(within(owners).getByTestId("owner-user-2")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(updateCodingEnvironment).toHaveBeenCalledWith("env1", expect.objectContaining({ ownerIds: ["user-1", "user-2"] })));
  });

  it("does not let members change owners", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ ownerIds: ["user-2"] }));
    renderCodingSettings({ path: EDIT, viewerRole: "member" });
    const owners = await screen.findByTestId("owners-editor");
    expect(within(owners).getByRole("button", { name: "Add owner" })).toBeDisabled();
    expect(within(owners).queryByRole("button", { name: /^Remove/ })).toBeNull();
  });

  it("archives after confirming", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    vi.mocked(archiveCodingEnvironment).mockResolvedValue(makeEnvironment({ archivedAt: "now" }));
    renderCodingSettings({ path: EDIT });
    fireEvent.click(await screen.findByRole("button", { name: "Archive" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Archive" }));
    await waitFor(() => expect(archiveCodingEnvironment).toHaveBeenCalledWith("env1"));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(new RegExp(`^${ROOT}$`)));
  });

  it("is read-only when archived", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ archivedAt: "2026-10-02" }));
    renderCodingSettings({ path: EDIT });
    expect(await screen.findByText(/This environment is archived/)).toBeVisible();
    expect(screen.getByLabelText("Prepare script")).toBeDisabled();
    expect(screen.queryByTestId("debug-terminal")).toBeNull();
  });

  it("shows the debug section", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    renderCodingSettings({ path: EDIT });
    expect(await screen.findByText(/destroyed after 20 minutes\. Billed at the same rate as sandbox runtime\./)).toBeVisible();
    expect(within(screen.getByTestId("debug-terminal")).getByText("No terminal is running.")).toBeVisible();
  });

  it("shows a load error", async () => {
    vi.mocked(fetchCodingEnvironment).mockRejectedValue(new Error("Not found"));
    renderCodingSettings({ path: EDIT });
    expect(await screen.findByRole("alert")).toHaveTextContent("Not found");
  });
});

describe("Import from .env", () => {
  it("parses locally for a new environment and lets the user reclassify keys", async () => {
    const user = userEvent.setup();
    renderCodingSettings({ path: NEW });
    await user.click(await screen.findByRole("button", { name: "Import from .env" }));
    fireEvent.change(screen.getByLabelText(".env contents"), { target: { value: "# app\nexport PORT=3000\nAPI_TOKEN=\"abc\"\nREGION=us" } });
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(importCodingEnvironmentFile).not.toHaveBeenCalled();
    expect(screen.getByRole("checkbox", { name: "API_TOKEN is a secret" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "PORT is a secret" })).not.toBeChecked();
    expect(screen.queryByText("abc")).toBeNull();
    await user.click(screen.getByRole("checkbox", { name: "REGION is a secret" }));
    await user.click(screen.getByRole("checkbox", { name: "Import PORT" }));
    await user.click(screen.getByRole("button", { name: "Import 2" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    const secretNames = screen.getAllByLabelText("Secret name").map((input) => (input as HTMLInputElement).value);
    expect(secretNames).toEqual(["REGION", "API_TOKEN"]);
    expect(screen.queryAllByLabelText("Variable name")).toHaveLength(0);
  });

  it("parses on the server for an existing environment and skips existing names", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    vi.mocked(importCodingEnvironmentFile).mockResolvedValue({ variables: [{ name: "NODE_ENV", value: "dev" }, { name: "PORT", value: "1" }], secrets: [{ name: "NPM_TOKEN", value: "x" }, { name: "DB_PASSWORD", value: "p" }] });
    renderCodingSettings({ path: EDIT });
    await user.click(await screen.findByRole("button", { name: "Import from .env" }));
    fireEvent.change(screen.getByLabelText(".env contents"), { target: { value: "PORT=1" } });
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(importCodingEnvironmentFile).toHaveBeenCalledWith("env1", "PORT=1");
    await user.click(await screen.findByRole("button", { name: "Import 4" }));
    const variables = screen.getAllByLabelText("Variable name").map((input) => (input as HTMLInputElement).value);
    expect(variables).toEqual(["NODE_ENV", "PORT"]);
    expect(screen.getAllByLabelText("Variable value").map((input) => (input as HTMLInputElement).value)).toEqual(["test", "1"]);
    expect(screen.getAllByLabelText("Secret value")).toHaveLength(1);
  });

  it("reads a dropped or chosen file and reports empty files", async () => {
    const user = userEvent.setup();
    renderCodingSettings({ path: NEW });
    await user.click(await screen.findByRole("button", { name: "Import from .env" }));
    const file = new File(["# nothing here\n"], ".env", { type: "text/plain" });
    await user.upload(screen.getByLabelText("Choose .env file"), file);
    await waitFor(() => expect(screen.getByLabelText(".env contents")).toHaveValue("# nothing here\n"));
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByText("No variables found in this file")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("snapshots", () => {
  it("lists snapshots newest first and refreshes the environment", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ latestSnapshot: snapshot() }));
    vi.mocked(fetchCodingSnapshots).mockResolvedValue([
      snapshot({ id: "old", createdAt: "2026-09-01T00:00:00Z", startedAt: "2026-09-01T00:00:00Z", finishedAt: "2026-09-01T00:01:12Z" }),
      snapshot(),
    ]);
    let resolve: (value: CodingEnvironmentSnapshot) => void = () => undefined;
    vi.mocked(refreshCodingSnapshot).mockReturnValue(new Promise((done) => (resolve = done)));
    renderCodingSettings({ path: EDIT });
    const panel = await screen.findByTestId("snapshot-panel");
    await waitFor(() => expect(within(panel).getAllByTestId("snapshot-row")).toHaveLength(2));
    const statuses = within(panel).getAllByTestId("snapshot-status");
    expect(statuses[0]).toHaveTextContent("Ready");
    expect(statuses[1]).toHaveTextContent("1m 12s");
    const refresh = screen.getByRole("button", { name: "Refresh environment" });
    expect(refresh).toHaveAttribute("title", "Prepare a new snapshot from saved settings");
    fireEvent.click(refresh);
    expect(await screen.findByRole("button", { name: "Refreshing environment…" })).toBeDisabled();
    await act(async () => resolve(snapshot({ id: "snap2", status: "queued", createdAt: "2026-10-02T00:00:00Z" })));
    await waitFor(() => expect(within(panel).getAllByTestId("snapshot-status")[0]).toHaveTextContent("Queued"));
    expect(refreshCodingSnapshot).toHaveBeenCalledWith("env1");
  });

  it("shows an empty state", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment());
    renderCodingSettings({ path: EDIT });
    expect(await within(await screen.findByTestId("snapshot-panel")).findByText("No snapshots yet.")).toBeVisible();
  });

  it("opens a building snapshot's logs, streams them and cancels", async () => {
    const building = snapshot({ status: "building" });
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ latestSnapshot: building }));
    vi.mocked(fetchCodingSnapshots).mockResolvedValue([building]);
    vi.mocked(fetchCodingSnapshotLogs).mockResolvedValueOnce({ logs: ["cloning…", "mise install"], complete: false } as never);
    vi.mocked(cancelCodingSnapshot).mockResolvedValue(snapshot({ status: "canceled" }));
    renderCodingSettings({ path: EDIT });
    const panel = await screen.findByTestId("snapshot-panel");
    fireEvent.click(within(panel).getByTestId("snapshot-row"));
    const dialog = await screen.findByRole("dialog", { name: "Snapshot logs" });
    expect(await within(dialog).findByTestId("snapshot-logs")).toHaveTextContent(/cloning…\s*mise install/);
    expect(fetchCodingSnapshotLogs).toHaveBeenCalledWith("snap1");
    expect(within(dialog).getByTestId("snapshot-summary")).toHaveTextContent(/^Building · started/);
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(within(panel).getByTestId("snapshot-status")).toHaveTextContent("Canceled"));
    expect(cancelCodingSnapshot).toHaveBeenCalledWith("snap1");
  });

  it("summarizes a finished build and copies its logs", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    const done = snapshot({ startedAt: "2026-10-01T00:00:00Z", finishedAt: "2026-10-01T00:00:09Z" });
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ latestSnapshot: done }));
    vi.mocked(fetchCodingSnapshots).mockResolvedValue([done]);
    vi.mocked(fetchCodingSnapshotLogs).mockResolvedValue({ logs: "ok", complete: true } as never);
    renderCodingSettings({ path: EDIT });
    const panel = await screen.findByTestId("snapshot-panel");
    await waitFor(() => expect(within(panel).getByTestId("snapshot-status")).toHaveTextContent("9s"));
    await user.click(within(panel).getByTestId("snapshot-row"));
    const dialog = await screen.findByRole("dialog", { name: "Snapshot logs" });
    expect(within(dialog).getByTestId("snapshot-summary")).toHaveTextContent(/^Succeeded · started .+ · took 9s$/);
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Copy logs" })).toBeEnabled());
    await user.click(within(dialog).getByRole("button", { name: "Copy logs" }));
    expect(writeText).toHaveBeenCalledWith("ok");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Logs copied"));
  });

  it("polls an in-progress snapshot until it finishes", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const building = snapshot({ status: "building" });
      vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ latestSnapshot: building }));
      vi.mocked(fetchCodingSnapshots).mockResolvedValueOnce([building]).mockResolvedValue([snapshot({ status: "ready" })]);
      renderCodingSettings({ path: EDIT });
      const panel = await screen.findByTestId("snapshot-panel");
      await act(async () => {
        await vi.advanceTimersByTimeAsync(3100);
      });
      await waitFor(() => expect(within(panel).getByTestId("snapshot-status")).toHaveTextContent("Ready"));
    } finally {
      vi.useRealTimers();
    }
  });

  it("offers Fix with agent for a failed snapshot", async () => {
    const failed = snapshot({ status: "failed", error: "npm ci exited 1" });
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ latestSnapshot: failed }));
    vi.mocked(fetchCodingSnapshots).mockResolvedValue([failed, snapshot({ id: "old", createdAt: "2026-09-01T00:00:00Z" })]);
    vi.mocked(fetchCodingSnapshotLogs).mockResolvedValue({ logs: "npm ERR!", complete: true } as never);
    vi.mocked(fixCodingEnvironmentWithAgent).mockResolvedValue({} as never);
    renderCodingSettings({ path: EDIT });
    const panel = await screen.findByTestId("snapshot-panel");
    await waitFor(() => expect(within(panel).getAllByTestId("snapshot-row")).toHaveLength(2));
    expect(within(panel).getAllByTestId("snapshot-status")[0]).toHaveTextContent("Failed");
    fireEvent.click(within(panel).getAllByTestId("snapshot-row")[0]);
    const dialog = await screen.findByRole("dialog", { name: "Snapshot logs" });
    expect(within(dialog).getByText("npm ci exited 1")).toBeVisible();
    fireEvent.click(within(dialog).getByRole("button", { name: "Fix with agent" }));
    await waitFor(() => expect(fixCodingEnvironmentWithAgent).toHaveBeenCalledWith("env1", "snap1"));
  });
});

describe("agent-assisted setup", () => {
  it("starts Configure with agent from the header and shows the proposal when it arrives", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.mocked(fetchCodingEnvironment).mockResolvedValueOnce(makeEnvironment()).mockResolvedValue(makeEnvironment({ pendingProposal: { sessionId: "s1", explanation: "Use pnpm", verified: true, createdAt: "", prepareScript: "pnpm i" } }));
      vi.mocked(configureCodingEnvironmentWithAgent).mockResolvedValue({} as never);
      renderCodingSettings({ path: EDIT });
      fireEvent.click(await screen.findByRole("button", { name: "Configure with agent" }));
      await waitFor(() => expect(configureCodingEnvironmentWithAgent).toHaveBeenCalledWith("env1"));
      expect(await screen.findByText("Flow Agent is working. Its proposal will appear here.")).toBeVisible();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5100);
      });
      expect(await screen.findByTestId("proposal-panel")).toBeVisible();
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the proposal diff and applies it", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ pendingProposal: { sessionId: "s1", explanation: "Use pnpm", verified: false, createdAt: "", prepareScript: "pnpm i", missingSecrets: ["DB_URL"] } }));
    vi.mocked(applyCodingEnvironmentProposal).mockResolvedValue(makeEnvironment({ prepareScript: "pnpm i", version: 4 }));
    renderCodingSettings({ path: EDIT });
    const panel = await screen.findByTestId("proposal-panel");
    expect(within(panel).getByText("Use pnpm")).toBeVisible();
    expect(within(panel).getByText("Not verified")).toBeVisible();
    expect(within(panel).getByText("DB_URL")).toBeVisible();
    const diff = within(panel).getByLabelText("Changes to Prepare script");
    expect(diff).toHaveTextContent("- npm ci");
    expect(diff).toHaveTextContent("+ pnpm i");
    fireEvent.click(within(panel).getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(applyCodingEnvironmentProposal).toHaveBeenCalledWith("env1", false));
    await waitFor(() => expect(screen.getByLabelText("Prepare script")).toHaveValue("pnpm i"));
    expect(screen.queryByTestId("proposal-panel")).toBeNull();
  });

  it("discards a proposal", async () => {
    vi.mocked(fetchCodingEnvironment).mockResolvedValue(makeEnvironment({ pendingProposal: { sessionId: "s1", explanation: "", verified: true, createdAt: "" } }));
    vi.mocked(applyCodingEnvironmentProposal).mockResolvedValue(makeEnvironment());
    renderCodingSettings({ path: EDIT });
    const panel = await screen.findByTestId("proposal-panel");
    expect(within(panel).getByText("The proposal matches the current configuration.")).toBeVisible();
    fireEvent.click(within(panel).getByRole("button", { name: "Discard" }));
    await waitFor(() => expect(applyCodingEnvironmentProposal).toHaveBeenCalledWith("env1", true));
  });
});
