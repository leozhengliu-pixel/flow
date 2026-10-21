import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import type { ReviewImportStatus } from "@/types/flow";

const api = vi.hoisted(() => ({
  fetchReviewImport: vi.fn(),
  updateReviewImport: vi.fn(),
  importReviews: vi.fn(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import { ReviewImportSettings } from "./review-import-settings";

const status = (overrides: Partial<ReviewImportStatus> = {}): ReviewImportStatus => ({
  available: true,
  enabled: false,
  active: false,
  interval: 600,
  repositories: [{ provider: "github", repository: "acme/store" }],
  limits: { repositoriesPerRun: 10, requestsPerRepository: 50, requestsPerRun: 200, writesPerRun: 20, writeDelaySeconds: 2, maxRepositories: 100 },
  running: false,
  backoffs: [],
  ...overrides,
});

beforeEach(() => {
  vi.clearAllMocks();
});

it("lets admins turn the import on and approve repositories, keeping other hosts' approvals", async () => {
  const user = userEvent.setup();
  let current = status({ repositories: [{ provider: "github", repository: "acme/store" }, { provider: "gitlab", repository: "group/app" }] });
  api.fetchReviewImport.mockResolvedValue(current);
  api.updateReviewImport.mockImplementation(async (input) => (current = { ...current, ...input, active: Boolean(input.enabled ?? current.enabled) }));
  render(<I18nProvider><ReviewImportSettings provider="gitlab" data={makeBootstrap({ viewerRole: "admin" })} /></I18nProvider>);

  const toggle = await screen.findByRole("checkbox", { name: "Import approved repositories" });
  expect(screen.getByText("group/app")).toBeInTheDocument();
  expect(screen.queryByText("acme/store")).not.toBeInTheDocument();
  await user.click(toggle);
  expect(api.updateReviewImport).toHaveBeenCalledWith({ enabled: true });

  await user.type(screen.getByRole("combobox", { name: "Repository to approve" }), "group/other");
  await user.click(screen.getByRole("button", { name: "Approve" }));
  await waitFor(() => expect(api.updateReviewImport).toHaveBeenLastCalledWith({ repositories: [
    { provider: "github", repository: "acme/store" },
    { provider: "gitlab", repository: "group/app" },
    { provider: "gitlab", repository: "group/other" },
  ] }));
});

it("shows the last run, backoffs and a server-wide off switch read-only to members", async () => {
  api.fetchReviewImport.mockResolvedValue(status({
    available: false,
    enabled: true,
    last: { repositories: 1, scanned: 12, imported: 2, updated: 1, closed: 0, skippedUnchanged: 9, failed: 1, errors: [], startedAt: "2026-10-20T07:49:57Z", finishedAt: "2026-10-20T07:50:15Z" },
    backoffs: [{ target: "github:acme/store#7", message: "merge request diffs: HTTP 500", failures: 2, backoffUntil: "2026-10-20T08:20:15Z" }],
  }));
  render(<I18nProvider><ReviewImportSettings provider="github" data={makeBootstrap({ viewerRole: "member" })} /></I18nProvider>);

  expect(await screen.findByRole("note")).toHaveTextContent("FLOW_REVIEW_IMPORT=on");
  expect(screen.getByRole("checkbox", { name: "Import approved repositories" })).toBeDisabled();
  expect(screen.queryByRole("combobox", { name: "Repository to approve" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Sync now" })).not.toBeInTheDocument();
  expect(screen.getByText("Unchanged").nextElementSibling).toHaveTextContent("9");
  const backoffs = screen.getByRole("list", { name: "Retrying later" });
  expect(within(backoffs).getByText("acme/store#7")).toBeInTheDocument();
  expect(within(backoffs).getByText("merge request diffs: HTTP 500")).toBeInTheDocument();
});
