import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { makeBootstrap } from "@/test/fixtures";

const api = vi.hoisted(() => ({
  createWebhook: vi.fn(),
  updateWebhook: vi.fn(),
  deleteWebhook: vi.fn(),
  rotateWebhookSecret: vi.fn(),
  revokeWebhookSecret: vi.fn(),
  fetchWebhookFailures: vi.fn().mockResolvedValue([]),
  fetchWebhookDeliveries: vi
    .fn()
    .mockResolvedValue({ items: [], hasMore: false }),
  sendWebhookTestEvent: vi.fn(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import { WebhookEditPage } from "./webhook-edit-page";

it("renders full-page chrome (not dialog) with HTTPS + show-once secret (LS-0648 / LS-0711)", async () => {
  const user = userEvent.setup();
  const onClose = vi.fn();
  api.createWebhook.mockResolvedValue({
    id: "wh_1",
    name: "Deploy",
    url: "https://example.com/hook",
    resourceTypes: ["issues"],
    teamIds: [],
    enabled: true,
    creatorId: "user",
    secretPrefix: "flow_wh_abc",
    secret: "flow_wh_abcSECRET",
    createdAt: "2026-09-22T00:00:00Z",
    updatedAt: "2026-09-22T00:00:00Z",
  });

  render(
    <WebhookEditPage
      data={makeBootstrap({ teams: [], webhooks: [] })}
      webhook={null}
      onClose={onClose}
      onSaved={vi.fn().mockResolvedValue(undefined)}
    />,
  );

  // Full-page chrome — no dialog role / modal overlay.
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(screen.getByTestId("webhook-edit-page")).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { level: 1, name: "Create webhook" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: /API settings/i }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Create webhook" }),
  ).toBeInTheDocument();

  const nameInput = screen.getByRole("textbox", { name: /^Name$/i });
  await user.type(nameInput, "Deploy");
  const url = screen.getByPlaceholderText("https://example.com/webhooks/flow");
  await user.type(url, "http://example.com/hook");
  await user.tab();
  expect(screen.getByRole("alert")).toHaveTextContent(/HTTPS/i);
  expect(screen.getByRole("button", { name: "Create webhook" })).toBeDisabled();
  expect(api.createWebhook).not.toHaveBeenCalled();

  await user.clear(url);
  await user.type(url, "https://example.com/hook");
  expect(screen.getByRole("button", { name: "Create webhook" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "Create webhook" }));
  expect(api.createWebhook).toHaveBeenCalled();
  expect(screen.getByDisplayValue("flow_wh_abcSECRET")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Copy secret" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /API settings/i }));
  expect(onClose).toHaveBeenCalled();
});

const existing = {
  id: "wh_2",
  name: "Builds",
  url: "https://ci.example.com/flow",
  resourceTypes: ["issues"],
  teamIds: [],
  enabled: true,
  creatorId: "user",
  createdAt: "2026-09-22T00:00:00Z",
  updatedAt: "2026-09-22T00:00:00Z",
};

it("asks legacy webhooks to rotate for signing and to switch payload format", async () => {
  const user = userEvent.setup();
  api.updateWebhook.mockResolvedValue({ ...existing, payloadVersion: 2 });
  render(
    <WebhookEditPage
      data={makeBootstrap({ teams: [], webhooks: [] })}
      webhook={existing}
      onClose={vi.fn()}
      onSaved={vi.fn().mockResolvedValue(undefined)}
    />,
  );
  expect(
    screen.getByText(/Deliveries are not signed yet/),
  ).toBeInTheDocument();
  expect(screen.getByText(/legacy payload format/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Use current format" }));
  expect(api.updateWebhook).toHaveBeenCalledWith("wh_2", { payloadVersion: 2 });
  expect(await screen.findByText("No deliveries yet")).toBeInTheDocument();
});

it("sends a test event and shows the response", async () => {
  const user = userEvent.setup();
  api.sendWebhookTestEvent.mockResolvedValue({
    id: "whd_1",
    webhookId: "wh_2",
    kind: "test",
    eventId: "evt",
    eventType: "WebhookTest",
    action: "test",
    url: existing.url,
    status: "succeeded",
    attempt: 1,
    maxAttempts: 1,
    responseStatus: 200,
    durationMs: 42,
    attempts: [],
    createdAt: "2026-09-22T00:00:00Z",
    updatedAt: "2026-09-22T00:00:00Z",
  });
  render(
    <WebhookEditPage
      data={makeBootstrap({ teams: [], webhooks: [] })}
      webhook={{ ...existing, payloadVersion: 2, signingEnabled: true, secretPrefix: "flow_wh_x" }}
      onClose={vi.fn()}
      onSaved={vi.fn().mockResolvedValue(undefined)}
    />,
  );
  await user.click(screen.getByRole("button", { name: "Send test event" }));
  expect(api.sendWebhookTestEvent).toHaveBeenCalledWith("wh_2");
  expect(await screen.findByText("Test event delivered")).toBeInTheDocument();
  expect(screen.getByText("HTTP 200 · 42 ms")).toBeInTheDocument();
  // The delivery log reloads after the test.
  expect(api.fetchWebhookDeliveries.mock.calls.length).toBeGreaterThanOrEqual(2);
});

it("locks application-managed webhooks and offers re-enabling after failures", async () => {
  const user = userEvent.setup();
  api.updateWebhook.mockResolvedValue({ ...existing, enabled: true });
  render(
    <WebhookEditPage
      data={makeBootstrap({ teams: [], webhooks: [] })}
      webhook={{
        ...existing,
        managedBy: "application",
        payloadVersion: 2,
        enabled: false,
        disabledReason: "deliveryFailures",
      }}
      onClose={vi.fn()}
      onSaved={vi.fn().mockResolvedValue(undefined)}
    />,
  );
  expect(screen.getByText(/managed by an installed application/)).toBeInTheDocument();
  expect(screen.getByRole("textbox", { name: "Endpoint URL" })).toBeDisabled();
  expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Rotate" })).toBeNull();
  await user.click(screen.getByRole("button", { name: "Enable webhook" }));
  expect(api.updateWebhook).toHaveBeenCalledWith("wh_2", { enabled: true });
});
