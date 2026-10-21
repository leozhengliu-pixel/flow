import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import type { PersonalConnection, PersonalConnectionsResponse } from "@/types/personal-connections";

const api = vi.hoisted(() => ({
  fetchPersonalConnections: vi.fn(),
  authorizeGoogleCalendar: vi.fn(),
  disconnectGoogleCalendar: vi.fn(),
  connectNotion: vi.fn(),
  disconnectNotion: vi.fn(),
}));
const dialogs = vi.hoisted(() => ({ confirmAction: vi.fn() }));
vi.mock("@/components/ui/action-dialog-service", () => dialogs);
vi.mock("@/lib/personal-connections-api", () => api);

import { PersonalConnectionRows } from "./personal-connection-rows";

const connection = (overrides: Partial<PersonalConnection> = {}): PersonalConnection => ({
  id: "pcn_1",
  provider: "google-calendar",
  account: "member@example.com",
  scopes: [],
  status: "active",
  linkedAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  ...overrides,
});

const response = (overrides: Partial<PersonalConnectionsResponse> = {}): PersonalConnectionsResponse => ({
  googleCalendar: { available: true },
  notion: { available: true },
  ...overrides,
});

function renderRows() {
  return render(
    <I18nProvider>
      <PersonalConnectionRows />
    </I18nProvider>,
  );
}

const row = (provider: string) => screen.getByTestId(`${provider}-connection-row`);

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  dialogs.confirmAction.mockReset();
  dialogs.confirmAction.mockResolvedValue(true);
});

it("shows Not configured instead of a Connect button when the deployment has no Google or Notion setup", async () => {
  api.fetchPersonalConnections.mockResolvedValue(response({ googleCalendar: { available: false }, notion: { available: false } }));
  renderRows();
  await waitFor(() => expect(within(row("google-calendar")).getByText("Not configured")).toBeInTheDocument());
  expect(within(row("notion")).getByText("Not configured")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Connect/ })).not.toBeInTheDocument();
  expect(screen.queryByText("Coming soon")).not.toBeInTheDocument();
});

it("starts Google authorization from the Connect button", async () => {
  api.fetchPersonalConnections.mockResolvedValue(response());
  api.authorizeGoogleCalendar.mockResolvedValue({ url: "https://accounts.google.com/o/oauth2/v2/auth?x" });
  const assign = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, assign } as Location);
  renderRows();
  fireEvent.click(await within(row("google-calendar")).findByRole("button", { name: /Connect/ }));
  await waitFor(() => expect(assign).toHaveBeenCalledWith("https://accounts.google.com/o/oauth2/v2/auth?x"));
});

it("shows a connected Google account and disconnects after confirming", async () => {
  api.fetchPersonalConnections.mockResolvedValue(response({ googleCalendar: { available: true, connection: connection() } }));
  api.disconnectGoogleCalendar.mockResolvedValue(undefined);
  renderRows();
  expect(await screen.findByText("member@example.com")).toBeInTheDocument();
  fireEvent.keyDown(screen.getByRole("button", { name: "Google Calendar account options" }), { key: "Enter" });
  fireEvent.click(await screen.findByRole("menuitem", { name: "Disconnect…" }));
  await waitFor(() =>
    expect(dialogs.confirmAction).toHaveBeenCalledWith("Disconnect Google Calendar?", expect.objectContaining({ confirmLabel: "Disconnect", danger: true })),
  );
  await waitFor(() => expect(api.disconnectGoogleCalendar).toHaveBeenCalled());
  await waitFor(() => expect(api.fetchPersonalConnections).toHaveBeenCalledTimes(2));
});

it("keeps the connection when the disconnect is cancelled", async () => {
  api.fetchPersonalConnections.mockResolvedValue(response({ notion: { available: true, connection: connection({ provider: "notion", account: undefined }) } }));
  dialogs.confirmAction.mockResolvedValueOnce(false);
  renderRows();
  fireEvent.keyDown(await screen.findByRole("button", { name: "Notion account options" }), { key: "Enter" });
  fireEvent.click(await screen.findByRole("menuitem", { name: "Disconnect…" }));
  await waitFor(() => expect(dialogs.confirmAction).toHaveBeenCalledWith("Disconnect Notion?", expect.anything()));
  expect(api.disconnectNotion).not.toHaveBeenCalled();
});

it("shows the out-of-office window the calendar reported", async () => {
  const until = new Date(Date.now() + 3 * 86_400_000).toISOString();
  api.fetchPersonalConnections.mockResolvedValue(response({ googleCalendar: { available: true, connection: connection({ awayUntil: until }) } }));
  renderRows();
  expect(await screen.findByText(/Out of office until/)).toBeInTheDocument();
});

it("offers Reconnect when Google revoked the grant", async () => {
  api.fetchPersonalConnections.mockResolvedValue(
    response({ googleCalendar: { available: true, connection: connection({ status: "reconnect", statusReason: "Google revoked the authorization" }) } }),
  );
  api.authorizeGoogleCalendar.mockResolvedValue({ url: "https://accounts.google.com/o/oauth2/v2/auth?y" });
  const assign = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, assign } as Location);
  renderRows();
  expect(await screen.findByText("Google revoked the authorization")).toBeInTheDocument();
  expect(screen.getByText("Reconnect needed")).toBeInTheDocument();
  fireEvent.keyDown(screen.getByRole("button", { name: "Google Calendar account options" }), { key: "Enter" });
  fireEvent.click(await screen.findByRole("menuitem", { name: "Reconnect" }));
  await waitFor(() => expect(assign).toHaveBeenCalledWith("https://accounts.google.com/o/oauth2/v2/auth?y"));
});

it("turns on Notion and goes to its connection page", async () => {
  api.fetchPersonalConnections.mockResolvedValue(response());
  api.connectNotion.mockResolvedValue({ url: "https://www.notion.com/connections/flow" });
  const assign = vi.fn();
  vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, assign } as Location);
  renderRows();
  fireEvent.click(await within(row("notion")).findByRole("button", { name: /Connect/ }));
  await waitFor(() => expect(assign).toHaveBeenCalledWith("https://www.notion.com/connections/flow"));
});

it("says previews are on for Notion, not that Notion authorized Flow, and offers Manage in Notion", async () => {
  api.fetchPersonalConnections.mockResolvedValue(response({ notion: { available: true, connection: connection({ provider: "notion", account: undefined }) } }));
  api.connectNotion.mockResolvedValue({ url: "https://www.notion.com/connections/flow" });
  const open = vi.spyOn(window, "open").mockReturnValue(null);
  renderRows();
  expect(await within(row("notion")).findByText("Previews on")).toBeInTheDocument();
  expect(within(row("notion")).queryByText("Connected")).not.toBeInTheDocument();
  expect(within(row("notion")).getByText(/Authorize Flow in Notion/)).toBeInTheDocument();
  fireEvent.keyDown(screen.getByRole("button", { name: "Notion account options" }), { key: "Enter" });
  fireEvent.click(await screen.findByRole("menuitem", { name: "Manage in Notion" }));
  await waitFor(() => expect(open).toHaveBeenCalledWith("https://www.notion.com/connections/flow", "_blank", "noopener,noreferrer"));
});
