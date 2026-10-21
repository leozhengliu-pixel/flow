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

const toastError = vi.hoisted(() => vi.fn());
vi.mock("@/lib/toast", () => ({ toast: { error: toastError, success: vi.fn() } }));

import { ApiError } from "@/lib/api-client";

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

it("translates backoff codes in zh-CN and falls back to the server message for unknown or missing codes", async () => {
  localStorage.setItem("flow:locale", "zh-CN");
  try {
    api.fetchReviewImport.mockResolvedValue(status({
      enabled: true,
      backoffs: [
        { target: "github:acme/store#1", code: "upstream_error", params: { status: "502" }, message: "github: HTTP 502: bad gateway", failures: 1, backoffUntil: "2026-10-20T08:20:15Z" },
        { target: "github:acme/store#2", code: "no_source", params: { provider: "GitHub" }, message: "no connected GitHub source reaches this repository", failures: 1 },
        { target: "github:acme/store#3", code: "from_a_newer_server", message: "a newer failure", failures: 1 },
        { target: "github:acme/store#4", message: "an old row without a code", failures: 1 },
      ],
    }));
    render(<I18nProvider><ReviewImportSettings provider="github" data={makeBootstrap({ viewerRole: "member" })} /></I18nProvider>);

    const backoffs = await screen.findByRole("list");
    expect(within(backoffs).getByText("代码托管平台暂时出现问题，稍后重试")).toHaveAttribute("title", "github: HTTP 502: bad gateway");
    expect(within(backoffs).getByText("没有已连接的 GitHub 来源可访问此仓库")).toBeInTheDocument();
    expect(within(backoffs).getByText("a newer failure")).toBeInTheDocument();
    expect(within(backoffs).getByText("an old row without a code")).toBeInTheDocument();
    expect(within(backoffs).queryByText("github: HTTP 502: bad gateway")).not.toBeInTheDocument();
  } finally {
    localStorage.removeItem("flow:locale");
  }
});

it("shows coded request errors in the member's language", async () => {
  localStorage.setItem("flow:locale", "zh-CN");
  try {
    const user = userEvent.setup();
    api.fetchReviewImport.mockResolvedValue(status());
    api.updateReviewImport.mockRejectedValue(new ApiError("Connect GitHub before approving its repositories", 400, "source_not_connected", undefined, { provider: "GitHub" }));
    render(<I18nProvider><ReviewImportSettings provider="github" data={makeBootstrap({ viewerRole: "admin" })} /></I18nProvider>);
    await user.type(await screen.findByRole("combobox"), "acme/other");
    await user.click(screen.getByRole("button", { name: "批准" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("请先连接 GitHub，再批准其仓库"));
  } finally {
    localStorage.removeItem("flow:locale");
  }
});
