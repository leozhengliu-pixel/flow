import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";

const api = vi.hoisted(() => ({
  fetchGitHubApp: vi.fn(),
  startGitHubAppInstall: vi.fn(),
  removeGitHubAppInstallation: vi.fn(),
  startGitHubAppAuthorization: vi.fn(),
  removeGitHubAppAuthorization: vi.fn(),
}));
vi.mock("@/lib/coding-sessions-api", () => api);

import { GitHubAppCard } from "./github-app-card";

const status = {
  configured: true,
  hostName: "github.com",
  slug: "flow-dev",
  canLinkAccount: true,
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

it("lists installations, unlinks one and links the viewer's account", async () => {
  api.fetchGitHubApp.mockResolvedValue(status);
  api.removeGitHubAppInstallation.mockResolvedValue(undefined);
  api.startGitHubAppAuthorization.mockResolvedValue({ url: "https://github.com/login/oauth/authorize?x" });
  const assign = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, assign } as Location);
  render(<I18nProvider><GitHubAppCard isAdmin /></I18nProvider>);
  expect(await screen.findByText("acme")).toBeInTheDocument();
  expect(screen.getByText("Selected repositories")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Remove installation" }));
  await waitFor(() => expect(api.removeGitHubAppInstallation).toHaveBeenCalledWith("chi_1"));
  fireEvent.click(await screen.findByRole("button", { name: "Link account" }));
  await waitFor(() => expect(assign).toHaveBeenCalledWith("https://github.com/login/oauth/authorize?x"));
});

it("hides admin actions from members and shows a linked account", async () => {
  api.fetchGitHubApp.mockResolvedValue({ ...status, linkedAccount: { login: "alice", linkedAt: "2026-01-01T00:00:00Z" } });
  api.removeGitHubAppAuthorization.mockResolvedValue(undefined);
  render(<I18nProvider><GitHubAppCard isAdmin={false} /></I18nProvider>);
  expect(await screen.findByText("alice")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Install GitHub App" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove installation" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Unlink" }));
  await waitFor(() => expect(api.removeGitHubAppAuthorization).toHaveBeenCalled());
});
