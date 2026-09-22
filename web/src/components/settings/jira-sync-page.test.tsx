import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";

const api = vi.hoisted(() => ({
  createJiraLink: vi.fn(),
  fetchJiraRemoteProjects: vi.fn(),
  fetchJiraRemoteStatuses: vi.fn(),
  updateJiraLink: vi.fn(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import { JiraSyncPage } from "./jira-sync-page";

it("walks SelectProjects → ConfigureSync → StatusMapping with honest empty remote state", async () => {
  api.fetchJiraRemoteProjects.mockResolvedValue({
    projects: [],
    error: "Live Jira project listing is not enabled in this deployment.",
    status: "unavailable",
  });
  api.fetchJiraRemoteStatuses.mockResolvedValue({
    statuses: [],
    error: "Failed to load Jira statuses. The link can still be created.",
    status: "unavailable",
  });
  api.createJiraLink.mockResolvedValue({
    id: "link-1",
    jiraProjectId: "10000",
    teamId: "team-1",
    syncDirection: "bidirectional",
  });

  const onBack = vi.fn();
  const onReload = vi.fn().mockResolvedValue(undefined);
  render(
    <I18nProvider>
      <JiraSyncPage
        data={makeBootstrap({
          teams: [{ id: "team-1", key: "ENG", name: "Engineering", color: "#5e6ad2" } as never],
          states: [
            {
              id: "state-1",
              name: "Todo",
              type: "unstarted",
              color: "#ccc",
              position: 1,
              teamId: "team-1",
            } as never,
          ],
        })}
        mode="new"
        onBack={onBack}
        onReload={onReload}
      />
    </I18nProvider>,
  );

  await waitFor(() =>
    expect(screen.getByText(/Live projects unavailable/i)).toBeInTheDocument(),
  );
  expect(screen.getByText(/Linear pixel follow-up/i)).toBeInTheDocument();

  fireEvent.change(screen.getByLabelText(/Jira project ID/i), {
    target: { value: "10000" },
  });
  fireEvent.change(screen.getByLabelText(/Project key/i), {
    target: { value: "OPS" },
  });
  fireEvent.click(screen.getByRole("radio", { name: /Engineering/i }));
  fireEvent.click(screen.getByRole("button", { name: /Continue/i }));

  expect(screen.getByText(/Bi-directional/i)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /Continue/i }));

  await waitFor(() =>
    expect(screen.getByRole("button", { name: /Create link/i })).toBeInTheDocument(),
  );
  fireEvent.click(screen.getByRole("button", { name: /Create link/i }));
  await waitFor(() => expect(api.createJiraLink).toHaveBeenCalled());
  expect(api.createJiraLink.mock.calls[0][0]).toMatchObject({
    jiraProjectId: "10000",
    jiraProjectKey: "OPS",
    teamId: "team-1",
    syncDirection: "bidirectional",
  });
  expect(onBack).toHaveBeenCalled();
});
