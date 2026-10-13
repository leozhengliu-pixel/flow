import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import type { WebhookDelivery } from "@/types/flow";

const api = vi.hoisted(() => ({
  fetchWebhookDeliveries: vi.fn(),
  fetchWebhookDelivery: vi.fn(),
  redeliverWebhookDelivery: vi.fn(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import { WebhookDeliveries } from "./webhook-deliveries";

const delivery = (overrides: Partial<WebhookDelivery> = {}): WebhookDelivery => ({
  id: "whd_1",
  webhookId: "wh_1",
  kind: "event",
  eventId: "evt_1",
  eventType: "Issue",
  action: "create",
  url: "https://ci.example.com/flow",
  status: "failed",
  attempt: 4,
  maxAttempts: 4,
  responseStatus: 500,
  responseBody: "boom",
  durationMs: 120,
  attempts: [
    { attempt: 1, at: "2026-09-22T00:00:00Z", responseStatus: 500, durationMs: 120, succeeded: false, error: "HTTP 500" },
  ],
  createdAt: "2026-09-22T00:00:00Z",
  updatedAt: "2026-09-22T06:00:00Z",
  ...overrides,
});

beforeEach(() => {
  vi.clearAllMocks();
  api.fetchWebhookDeliveries.mockResolvedValue({
    items: [delivery(), delivery({ id: "whd_2", status: "succeeded", responseStatus: 200, attempt: 1, action: "update" })],
    hasMore: true,
    nextCursor: "next",
  });
});

it("lists deliveries with status, response code and duration, and pages", async () => {
  const user = userEvent.setup();
  render(<WebhookDeliveries webhookId="wh_1" />);
  const table = await screen.findByRole("table");
  expect(within(table).getByText("Issue · create")).toBeInTheDocument();
  expect(within(table).getByText("Failed")).toBeInTheDocument();
  expect(within(table).getByText("Delivered")).toBeInTheDocument();
  expect(within(table).getByText("500")).toBeInTheDocument();
  expect(within(table).getAllByText("120 ms").length).toBeGreaterThan(0);
  expect(api.fetchWebhookDeliveries).toHaveBeenCalledWith("wh_1", { cursor: undefined, status: undefined });
  await user.click(screen.getByRole("button", { name: "Load more" }));
  expect(api.fetchWebhookDeliveries).toHaveBeenCalledWith("wh_1", { cursor: "next", status: undefined });
  await user.click(screen.getByRole("button", { name: "Failed" }));
  await waitFor(() =>
    expect(api.fetchWebhookDeliveries).toHaveBeenCalledWith("wh_1", { cursor: undefined, status: "failed" }),
  );
});

it("shows a delivery's payload and attempts and redelivers it", async () => {
  const user = userEvent.setup();
  api.fetchWebhookDelivery.mockResolvedValue(delivery({ payload: { type: "Issue", action: "create" } }));
  api.redeliverWebhookDelivery.mockResolvedValue(delivery({ id: "whd_3", status: "succeeded", redeliveryOf: "whd_1" }));
  render(<WebhookDeliveries webhookId="wh_1" />);
  await screen.findByRole("table");
  await user.click(screen.getByRole("button", { name: "View delivery Issue · create" }));
  const dialog = await screen.findByRole("dialog");
  expect(await within(dialog).findByText(/"type": "Issue"/)).toBeInTheDocument();
  expect(within(dialog).getByText("boom")).toBeInTheDocument();
  expect(within(dialog).getByText("#1")).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Redeliver" }));
  expect(api.redeliverWebhookDelivery).toHaveBeenCalledWith("wh_1", "whd_1");
  await waitFor(() => expect(api.fetchWebhookDelivery).toHaveBeenCalledWith("wh_1", "whd_3"));
});

it("hides redelivery for disabled webhooks", async () => {
  const user = userEvent.setup();
  api.fetchWebhookDelivery.mockResolvedValue(delivery({ payload: {} }));
  render(<WebhookDeliveries webhookId="wh_1" canRedeliver={false} />);
  await screen.findByRole("table");
  await user.click(screen.getByRole("button", { name: "View delivery Issue · create" }));
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByText("Request body");
  expect(within(dialog).queryByRole("button", { name: "Redeliver" })).toBeNull();
});
