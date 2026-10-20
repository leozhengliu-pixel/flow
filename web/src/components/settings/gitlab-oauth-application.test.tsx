import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import type { IntegrationConnection } from "@/types/flow";

const api = vi.hoisted(() => ({
  fetchGitLabOAuthApplication: vi.fn(),
  saveGitLabOAuthApplication: vi.fn(),
  removeGitLabOAuthApplication: vi.fn(),
}));
vi.mock("@/lib/code-host-accounts-api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/code-host-accounts-api")>("@/lib/code-host-accounts-api");
  return { ...actual, ...api };
});

import { GitLabOAuthApplicationSection } from "./gitlab-integration-settings";

const connection = { id: "conn_1", provider: "gitlab", config: { hostName: "gitlab.com" } } as unknown as IntegrationConnection;

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
});

it("shows the redirect URL and scopes and saves the application", async () => {
  api.fetchGitLabOAuthApplication.mockResolvedValue({
    configured: false,
    applicationId: "",
    redirectUrl: "https://flow.test/api/account/code-host-accounts/gitlab/callback",
    scopes: ["api", "read_user"],
  });
  api.saveGitLabOAuthApplication.mockResolvedValue({
    configured: true,
    applicationId: "app-1",
    redirectUrl: "https://flow.test/api/account/code-host-accounts/gitlab/callback",
    scopes: ["api", "read_user"],
  });
  render(<I18nProvider><GitLabOAuthApplicationSection connection={connection} /></I18nProvider>);
  expect(await screen.findByDisplayValue("https://flow.test/api/account/code-host-accounts/gitlab/callback")).toHaveAttribute("readonly");
  expect(screen.getByDisplayValue("api read_user")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Application ID"), { target: { value: "app-1" } });
  const secret = screen.getByLabelText("Secret");
  expect(secret).toHaveAttribute("type", "password");
  fireEvent.change(secret, { target: { value: "s3cret" } });
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(api.saveGitLabOAuthApplication).toHaveBeenCalledWith("conn_1", { applicationId: "app-1", secret: "s3cret" }),
  );
  expect(await screen.findByRole("button", { name: "Remove" })).toBeInTheDocument();
  expect(screen.getByLabelText("Secret")).toHaveValue("");
});

it("removes a configured application", async () => {
  api.fetchGitLabOAuthApplication.mockResolvedValue({ configured: true, applicationId: "app-1", redirectUrl: "https://flow.test/cb", scopes: ["api", "read_user"] });
  api.removeGitLabOAuthApplication.mockResolvedValue(undefined);
  render(<I18nProvider><GitLabOAuthApplicationSection connection={connection} /></I18nProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
  await waitFor(() => expect(api.removeGitLabOAuthApplication).toHaveBeenCalledWith("conn_1"));
});
