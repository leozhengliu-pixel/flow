import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";

const api = vi.hoisted(() => ({
  createExport: vi.fn(),
  exportDownloadUrl: vi.fn((id: string) => `/api/exports/${id}/download`),
  getAnalyticsOverview: vi.fn(),
  getExport: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

vi.mock("./flow-insight-graph", () => ({
  InsightBar: ({ points }: { points: unknown[] }) => (
    <div data-testid="insight-bar">{points.length}</div>
  ),
  InsightLine: ({ points }: { points: unknown[] }) => (
    <div data-testid="insight-line">{points.length}</div>
  ),
}));

import { AnalyticsDashboardPage } from "./analytics-dashboard-page";

function renderPage() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <AnalyticsDashboardPage />
      </I18nProvider>
    </MemoryRouter>,
  );
}

describe("analytics dashboard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  it("filters archived status rows and reloads when the date range changes", async () => {
    const user = userEvent.setup();
    api.getAnalyticsOverview.mockResolvedValue({
      issues: { total: 2, active: 1 },
      status: { Backlog: 1, Archived: 1 },
      team: {},
      throughput: [],
    });

    renderPage();
    await screen.findByText("Backlog");
    expect(screen.queryByText("Archived")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Insights display options" }));
    await user.click(screen.getByRole("menuitemcheckbox", { name: "Show archived issues" }));
    expect(screen.getByText("Archived")).toBeVisible();

    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Date range" }));
    await user.click(screen.getByRole("menuitem", { name: "Last 7 days" }));
    await waitFor(() => expect(api.getAnalyticsOverview).toHaveBeenCalledTimes(2));
    expect(api.getAnalyticsOverview.mock.calls[1][0]).toMatch(/T/);
  });

  it("shows a recoverable error state when analytics cannot load", async () => {
    const user = userEvent.setup();
    api.getAnalyticsOverview.mockRejectedValue(new Error("offline"));

    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load analytics");
    api.getAnalyticsOverview.mockResolvedValue({
      issues: { total: 0, active: 0 },
      status: {},
      team: {},
      throughput: [],
    });
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(api.getAnalyticsOverview).toHaveBeenCalledTimes(2));
    expect(screen.getAllByText("No data")[0]).toBeVisible();
  });

  it("waits for the export job before exposing a download", async () => {
    const user = userEvent.setup();
    api.getAnalyticsOverview.mockResolvedValue({
      issues: { total: 1, active: 1 },
      status: { Backlog: 1 },
      team: {},
      throughput: [],
    });
    api.createExport.mockResolvedValue({ id: "export-1", status: "queued" });
    api.getExport.mockResolvedValue({ id: "export-1", status: "completed" });

    renderPage();
    await screen.findByText("Backlog");
    await user.click(screen.getByRole("button", { name: "Open menu" }));
    await user.click(screen.getByRole("menuitem", { name: /Export insights/ }));
    await waitFor(() => expect(api.getExport).toHaveBeenCalledWith("export-1"));
    await user.click(screen.getByRole("button", { name: "Open menu" }));
    expect(screen.getByRole("menuitem", { name: "Download latest" })).toBeVisible();
  });
});
