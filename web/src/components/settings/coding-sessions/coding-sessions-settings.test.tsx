import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  createCodingEnvironment,
  fetchCodingEnvironments,
  fetchCodingRepositories,
  fetchCodingModels,
  fetchCodingSettings,
  fetchCodingStatus,
  fetchCommitSigningKey,
  unarchiveCodingEnvironment,
  updateCodingSettings,
} from "@/lib/api";
import { ApiError } from "@/lib/api-client";
import { toast } from "@/lib/toast";
import type { CodingSettings } from "@/types/flow";
import { ROOT, makeEnvironment, makeSettings, makeStatus, renderCodingSettings } from "./coding-sessions-test-kit";

vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  fetchCodingStatus: vi.fn(),
  fetchCodingSettings: vi.fn(),
  updateCodingSettings: vi.fn(),
  fetchCodingModels: vi.fn(),
  fetchCodingEnvironments: vi.fn(),
  fetchCodingRepositories: vi.fn(),
  createCodingEnvironment: vi.fn(),
  unarchiveCodingEnvironment: vi.fn(),
  fetchCommitSigningKey: vi.fn(),
}));
vi.mock("@/lib/toast", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem("flow:locale", "en-US");
  vi.mocked(fetchCodingStatus).mockResolvedValue(makeStatus());
  vi.mocked(fetchCodingSettings).mockResolvedValue(makeSettings());
  vi.mocked(updateCodingSettings).mockImplementation(async (patch) => makeSettings(patch as Partial<CodingSettings>));
  vi.mocked(fetchCodingModels).mockResolvedValue([
    { id: "auto", label: "Auto", harness: "codex", auto: true },
    { id: "gpt-5-codex", label: "GPT-5 Codex", harness: "codex", reasoningEfforts: ["low", "medium", "high"] },
    { id: "claude-sonnet-4-5", label: "Claude Sonnet 4.5", harness: "claudeCode" },
  ]);
  vi.mocked(fetchCodingEnvironments).mockResolvedValue([]);
  vi.mocked(fetchCommitSigningKey).mockResolvedValue({ id: "k" } as never);
  vi.mocked(fetchCodingRepositories).mockResolvedValue({ repositories: [
    { codeHost: "github", hostName: "github.com", fullName: "acme/web", id: "1", defaultBranch: "main", webUrl: "", private: false, archived: false },
    { codeHost: "github", hostName: "github.com", fullName: "acme/api", id: "2", defaultBranch: "trunk", webUrl: "", private: false, archived: false },
  ] });
});

async function choose(name: string, option: string) {
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox", { name }));
  await user.click(await screen.findByRole("menuitem", { name: new RegExp(`^${option}`) }));
}

describe("Coding sessions settings page", () => {
  it("renders the reference layout with crumb, docs and Manage credits", async () => {
    const { onNavigateSettings } = renderCodingSettings();
    expect(screen.getByRole("heading", { level: 1, name: "Coding sessions" })).toBeVisible();
    expect(screen.getByText(/Let Flow Agent write code and open pull requests\./)).toBeVisible();
    expect(screen.getByRole("link", { name: /Docs/ })).toHaveAttribute("href", "https://flow.app/docs/coding-sessions");
    fireEvent.click(screen.getByRole("button", { name: /Manage credits/ }));
    expect(onNavigateSettings).toHaveBeenCalledWith("usage");
    fireEvent.click(screen.getByRole("button", { name: "AI & agents" }));
    expect(onNavigateSettings).toHaveBeenCalledWith("ai");
    expect(await screen.findByText("No environments configured")).toBeVisible();
    for (const title of ["Agent", "Model", "Require signed commits", "Pin sandbox region", "Environments", "Trusted external sources", "Repository access for Loops"])
      expect(screen.getAllByText(title).length).toBeGreaterThan(0);
  });

  it("toggles the coding-sessions feature flag", async () => {
    const { onToggleEnabled } = renderCodingSettings();
    const toggle = await screen.findByRole("checkbox", { name: "Enable coding sessions" });
    expect(toggle).toBeChecked();
    fireEvent.click(toggle);
    expect(onToggleEnabled).toHaveBeenCalledWith(false);
  });

  it("disables the policy controls while coding sessions are off", async () => {
    renderCodingSettings({ flags: { "coding-sessions": false } });
    expect(await screen.findByRole("checkbox", { name: "Enable coding sessions" })).not.toBeChecked();
    expect(screen.getByRole("combobox", { name: "Agent" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Model" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "Require signed commits" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "New environment" })).toBeDisabled();
  });

  it("asks for code access and links to the GitHub integration's App card", async () => {
    vi.mocked(fetchCodingStatus).mockResolvedValue(makeStatus({ codeHosts: [{ codeHost: "github", hostName: "github.com", connected: false }] }));
    const { onOpenIntegration } = renderCodingSettings();
    expect(await screen.findByText("Needs code access via GitHub integration")).toBeVisible();
    expect(screen.queryByRole("checkbox", { name: "Enable coding sessions" })).toBeNull();
    expect(screen.getByRole("combobox", { name: "Agent" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /Enable integration/ }));
    expect(onOpenIntegration).toHaveBeenCalledWith("github");
  });

  it("shows server validation errors inline", async () => {
    vi.mocked(updateCodingSettings).mockRejectedValueOnce(new ApiError("pinnedRegion must be one of FLOW_CODING_REGIONS", 400));
    renderCodingSettings();
    const toggle = await screen.findByRole("checkbox", { name: "Require signed commits" });
    fireEvent.click(toggle);
    expect(await screen.findByRole("alert")).toHaveTextContent("pinnedRegion must be one of FLOW_CODING_REGIONS");
    expect(toast.error).not.toHaveBeenCalled();
    expect(toggle).not.toBeChecked();
    fireEvent.click(toggle);
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });

  it("changes the agent and resets a model the new agent does not offer", async () => {
    vi.mocked(fetchCodingSettings).mockResolvedValue(makeSettings({ model: "gpt-5-codex" }));
    renderCodingSettings();
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Model" })).toHaveTextContent("GPT-5 Codex"));
    await choose("Agent", "Claude Code");
    expect(updateCodingSettings).toHaveBeenCalledWith({ harness: "claudeCode", model: "auto" });
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Agent" })).toHaveTextContent("Claude Code"));
    expect(screen.getByRole("combobox", { name: "Model" })).toHaveTextContent("Auto");
  });

  it("picks a model and a reasoning effort", async () => {
    renderCodingSettings();
    await screen.findByRole("combobox", { name: "Model" });
    // Like the reference app, Auto has no reasoning-effort row.
    expect(screen.queryByRole("combobox", { name: "Reasoning effort" })).toBeNull();
    await choose("Model", "GPT-5 Codex");
    await screen.findByRole("combobox", { name: "Reasoning effort" });
    expect(updateCodingSettings).toHaveBeenCalledWith({ model: "gpt-5-codex" });
    await choose("Reasoning effort", "High");
    expect(updateCodingSettings).toHaveBeenCalledWith({ reasoningEffort: "high" });
  });

  it("groups the model menu under Frontier and Other models with descriptions", async () => {
    vi.mocked(fetchCodingModels).mockResolvedValue([
      { id: "auto", label: "Auto (gpt-5-codex)", harness: "codex", auto: true, group: "auto", description: "Uses adaptive routing to pick the best model" },
      { id: "gpt-5-codex", label: "GPT-5 Codex", harness: "codex", group: "frontier", description: "Strong reasoning for multi-file changes" },
      { id: "gpt-4.1", label: "GPT-4.1", harness: "codex", group: "other" },
    ]);
    const user = userEvent.setup();
    renderCodingSettings();
    await user.click(await screen.findByRole("combobox", { name: "Model" }));
    const menu = await screen.findByRole("menu");
    expect(menu).toHaveClass("cs-model-menu");
    const labels = Array.from(menu.querySelectorAll(".cs-select-group, [role=menuitem]")).map((node) => node.textContent);
    expect(labels).toEqual(["AutoUses adaptive routing to pick the best model", "Frontier", "GPT-5 CodexStrong reasoning for multi-file changes", "Other models", "GPT-4.1"]);
    await user.click(screen.getByRole("menuitem", { name: /^GPT-4\.1/ }));
    expect(updateCodingSettings).toHaveBeenCalledWith({ model: "gpt-4.1" });
  });

  it("saves Require signed commits and reverts on failure", async () => {
    renderCodingSettings();
    const toggle = await screen.findByRole("checkbox", { name: "Require signed commits" });
    fireEvent.click(toggle);
    expect(updateCodingSettings).toHaveBeenCalledWith({ requireSignedCommits: true });
    await waitFor(() => expect(toggle).toBeChecked());
    vi.mocked(updateCodingSettings).mockRejectedValueOnce(new Error("Denied"));
    fireEvent.click(toggle);
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Denied"));
    expect(toggle).toBeChecked();
  });

  it("pins the sandbox region and switches between regions", async () => {
    vi.mocked(fetchCodingStatus).mockResolvedValue(makeStatus({ regions: ["us", "eu"] }));
    renderCodingSettings();
    const toggle = await screen.findByRole("checkbox", { name: "Pin sandbox region" });
    await waitFor(() => expect(toggle).toBeEnabled());
    expect(screen.getByText("Pin new coding session sandboxes to us. Region pinning raises the sandbox compute price.")).toBeVisible();
    fireEvent.click(toggle);
    expect(updateCodingSettings).toHaveBeenCalledWith({ pinnedRegion: "us" });
    await choose("Sandbox region", "eu");
    expect(updateCodingSettings).toHaveBeenCalledWith({ pinnedRegion: "eu" });
    fireEvent.click(screen.getByRole("checkbox", { name: "Pin sandbox region" }));
    expect(updateCodingSettings).toHaveBeenLastCalledWith({ pinnedRegion: "" });
  });

  it("disables region pinning when the server has no regions", async () => {
    renderCodingSettings();
    expect(await screen.findByText("No sandbox regions are configured on this server")).toBeVisible();
    expect(screen.getByRole("checkbox", { name: "Pin sandbox region" })).toBeDisabled();
  });

  it("lists environments and opens the editor", async () => {
    vi.mocked(fetchCodingEnvironments).mockResolvedValue([makeEnvironment({ latestSnapshot: { id: "s1", environmentId: "env1", status: "ready", strategy: "archive", configHash: "", image: "", baseRef: "main", createdAt: "" } })]);
    renderCodingSettings();
    const row = await screen.findByRole("button", { name: "Web" });
    expect(within(row.closest(".cs-environment-row") as HTMLElement).getByText("Ready")).toBeVisible();
    fireEvent.click(row);
    expect(screen.getByTestId("location")).toHaveTextContent(`${ROOT}/environments/env1`);
  });

  it("creates a new environment from the repository popover and opens it", async () => {
    const user = userEvent.setup();
    vi.mocked(createCodingEnvironment).mockResolvedValue(makeEnvironment({ id: "env-api", name: "api", repository: "acme/api" }));
    renderCodingSettings();
    await user.click(await screen.findByRole("button", { name: "New environment" }));
    const picker = await screen.findByTestId("repository-picker");
    expect(within(picker).getByPlaceholderText("Search repositories...")).toHaveFocus();
    await user.click(await within(picker).findByRole("button", { name: "acme/api" }));
    await waitFor(() => expect(createCodingEnvironment).toHaveBeenCalledWith({ name: "acme/api", codeHost: "github", hostName: "github.com", repository: "acme/api", repositoryId: "2", defaultBranch: "trunk" }));
    expect(fetchCodingRepositories).toHaveBeenCalledWith({ codeHost: "github", hostName: "github.com", q: undefined });
    expect(toast.success).toHaveBeenCalledWith("Environment created");
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(`${ROOT}/environments/env-api`));
  });

  it("opens the existing environment when the repository already has one", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchCodingEnvironments).mockImplementation(async (archived) => (archived ? [] : [makeEnvironment()]));
    renderCodingSettings();
    await screen.findByRole("button", { name: "Web" });
    await user.click(screen.getByRole("button", { name: "New environment" }));
    await user.click(await screen.findByRole("button", { name: "acme/web" }));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(`${ROOT}/environments/env1`));
    expect(createCodingEnvironment).not.toHaveBeenCalled();
  });

  it("searches repositories from the popover and picks with Enter", async () => {
    const user = userEvent.setup();
    vi.mocked(createCodingEnvironment).mockResolvedValue(makeEnvironment({ id: "env-web" }));
    renderCodingSettings();
    await user.click(await screen.findByRole("button", { name: "New environment" }));
    await screen.findByRole("button", { name: "acme/web" });
    await user.type(screen.getByLabelText("Search repositories"), "web");
    await waitFor(() => expect(fetchCodingRepositories).toHaveBeenLastCalledWith({ codeHost: "github", hostName: "github.com", q: "web" }));
    await user.keyboard("{Enter}");
    await waitFor(() => expect(createCodingEnvironment).toHaveBeenCalledWith(expect.objectContaining({ repository: "acme/web" })));
  });

  it("keeps the direct /environments/new route working", async () => {
    renderCodingSettings({ path: `${ROOT}/environments/new` });
    expect(await screen.findByRole("heading", { level: 1, name: "New environment" })).toBeVisible();
  });

  it("shows archived environments and unarchives one", async () => {
    vi.mocked(fetchCodingEnvironments).mockImplementation(async (archived) => (archived ? [makeEnvironment({ id: "old", name: "Old", archivedAt: "2026-09-01" })] : []));
    vi.mocked(unarchiveCodingEnvironment).mockResolvedValue(makeEnvironment({ id: "old" }));
    renderCodingSettings();
    fireEvent.click(await screen.findByRole("button", { name: "Show archived environments" }));
    expect(await screen.findByText("Old")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Unarchive" }));
    await waitFor(() => expect(unarchiveCodingEnvironment).toHaveBeenCalledWith("old"));
  });

  it("adds and removes trusted external sources", async () => {
    const user = userEvent.setup();
    renderCodingSettings();
    expect(await screen.findByText(/No trusted sources\./)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Add trusted source" }));
    await user.click(await screen.findByRole("menuitem", { name: "Slack" }));
    expect(updateCodingSettings).toHaveBeenCalledWith({ trustedExternalSources: ["slack"] });
    fireEvent.click(await screen.findByRole("button", { name: "Remove Slack" }));
    expect(updateCodingSettings).toHaveBeenLastCalledWith({ trustedExternalSources: [] });
  });

  it("opens Code access from the trusted sources description", async () => {
    const { onOpenIntegration } = renderCodingSettings();
    fireEvent.click(await screen.findByRole("button", { name: "Code access" }));
    expect(onOpenIntegration).toHaveBeenCalledWith("github");
  });

  it("links Repository access for Loops to the Loops settings", async () => {
    const { onNavigateSettings } = renderCodingSettings();
    fireEvent.click(await screen.findByRole("button", { name: /Repository access for Loops/ }));
    expect(onNavigateSettings).toHaveBeenCalledWith("loops");
    expect(screen.queryByRole("combobox", { name: "Repository access for Loops" })).toBeNull();
  });

  it("hides the archived environments link when nothing is archived", async () => {
    renderCodingSettings();
    expect(await screen.findByText("No environments configured")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Show archived environments" })).toBeNull();
  });

  it("is read-only for members", async () => {
    renderCodingSettings({ viewerRole: "member" });
    expect(await screen.findByRole("checkbox", { name: "Enable coding sessions" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Agent" })).toBeDisabled();
    expect(screen.getByText("Only workspace admins can change coding session settings.")).toBeVisible();
  });

  it("falls back to the legacy workspace settings when the coding API is unavailable", async () => {
    vi.mocked(fetchCodingSettings).mockRejectedValue(new Error("Not implemented"));
    vi.mocked(fetchCodingStatus).mockRejectedValue(new Error("Not implemented"));
    renderCodingSettings();
    expect(await screen.findByRole("checkbox", { name: "Enable coding sessions" })).toBeEnabled();
    expect(screen.getByRole("combobox", { name: "Agent" })).toHaveTextContent("Codex");
  });

  it("redirects the legacy environments page to Coding sessions", async () => {
    renderCodingSettings({ path: `${ROOT}/environments`, page: "coding-environments" });
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(new RegExp(`^${ROOT}$`)));
    // The app re-parses the URL into the coding-sessions page; this harness only checks the redirect.
  });

  it("asks to re-upload a signing key without a private key when signed commits are required", async () => {
    vi.mocked(fetchCodingSettings).mockResolvedValue(makeSettings({ requireSignedCommits: true }));
    vi.mocked(fetchCommitSigningKey).mockResolvedValue({ id: "k", needsReupload: true } as never);
    const { onNavigateSettings } = renderCodingSettings();
    expect(await screen.findByTestId("signing-key-notice")).toHaveTextContent(/Re-upload your commit signing key/);
    fireEvent.click(screen.getByRole("button", { name: "Re-upload key" }));
    expect(onNavigateSettings).toHaveBeenCalledWith("account-security");
  });

  it("asks for a signing key when none is uploaded and hides the notice when one is usable", async () => {
    vi.mocked(fetchCodingSettings).mockResolvedValue(makeSettings({ requireSignedCommits: true }));
    vi.mocked(fetchCommitSigningKey).mockResolvedValueOnce(null);
    const first = renderCodingSettings();
    expect(await screen.findByTestId("signing-key-notice")).toHaveTextContent(/Upload a commit signing key/);
    first.unmount();
    renderCodingSettings();
    await screen.findByRole("checkbox", { name: "Require signed commits" });
    await waitFor(() => expect(fetchCommitSigningKey).toHaveBeenCalledTimes(2));
    expect(screen.queryByTestId("signing-key-notice")).toBeNull();
  });
});
