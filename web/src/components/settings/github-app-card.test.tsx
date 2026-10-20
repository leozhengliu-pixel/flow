import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";

const api = vi.hoisted(() => ({
  fetchGitHubApp: vi.fn(),
  startGitHubAppInstall: vi.fn(),
  removeGitHubAppInstallation: vi.fn(),
  fetchAvailableGitHubAppInstallations: vi.fn(),
  linkGitHubAppInstallation: vi.fn(),
}));
vi.mock("@/lib/coding-sessions-api", () => api);

import { GitHubAppCard } from "./github-app-card";

const status = {
  configured: true,
  hostName: "github.com",
  slug: "flow-dev",
  installations: [{ id: "chi_1", installationId: "42", accountLogin: "acme", repositorySelection: "selected" }],
};

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
});

it("renders nothing when the App is not configured", async () => {
  api.fetchGitHubApp.mockResolvedValue({ configured: false, hostName: "github.com", installations: [] });
  const { container } = render(<I18nProvider><GitHubAppCard isAdmin /></I18nProvider>);
  await waitFor(() => expect(api.fetchGitHubApp).toHaveBeenCalled());
  expect(container).toBeEmptyDOMElement();
});

it("lists installations and unlinks one", async () => {
  api.fetchGitHubApp.mockResolvedValue(status);
  api.removeGitHubAppInstallation.mockResolvedValue(undefined);
  render(<I18nProvider><GitHubAppCard isAdmin /></I18nProvider>);
  expect(await screen.findByText("acme")).toBeInTheDocument();
  expect(screen.getByText("Selected repositories")).toBeInTheDocument();
  expect(screen.queryByText("Your GitHub account")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Remove installation" }));
  await waitFor(() => expect(api.removeGitHubAppInstallation).toHaveBeenCalledWith("chi_1"));
});

it("hides admin actions from members", async () => {
  api.fetchGitHubApp.mockResolvedValue(status);
  render(<I18nProvider><GitHubAppCard isAdmin={false} /></I18nProvider>);
  expect(await screen.findByText("acme")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Install GitHub App" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove installation" })).toBeNull();
});

it("links an installation that already exists on GitHub through the admin's linked account", async () => {
  api.fetchGitHubApp.mockResolvedValue({ ...status, installations: [], linkedAccount: { login: "octo", linkedAt: "2026-01-01T00:00:00Z" } });
  api.fetchAvailableGitHubAppInstallations.mockResolvedValue({ installations: [{ installationId: "42", accountLogin: "acme", repositorySelection: "selected" }] });
  api.linkGitHubAppInstallation.mockResolvedValue({ id: "chi_1", installationId: "42", accountLogin: "acme" });
  render(<I18nProvider><GitHubAppCard isAdmin /></I18nProvider>);
  expect(await screen.findByText("Installed on GitHub, not linked to this workspace")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Link installation" }));
  await waitFor(() => expect(api.linkGitHubAppInstallation).toHaveBeenCalledWith("42"));
  await waitFor(() => expect(api.fetchGitHubApp).toHaveBeenCalledTimes(2));
});

it("does not look up unlinked installations for members or without a linked account", async () => {
  api.fetchGitHubApp.mockResolvedValue({ ...status, linkedAccount: { login: "octo", linkedAt: "2026-01-01T00:00:00Z" } });
  render(<I18nProvider><GitHubAppCard isAdmin={false} /></I18nProvider>);
  expect(await screen.findByText("acme")).toBeInTheDocument();
  api.fetchGitHubApp.mockResolvedValue(status);
  render(<I18nProvider><GitHubAppCard isAdmin /></I18nProvider>);
  await waitFor(() => expect(api.fetchGitHubApp).toHaveBeenCalledTimes(2));
  expect(api.fetchAvailableGitHubAppInstallations).not.toHaveBeenCalled();
});
