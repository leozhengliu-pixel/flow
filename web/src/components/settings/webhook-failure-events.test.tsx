import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchWebhookFailures } from "@/lib/api";
import type { WebhookFailureEvent } from "@/types/flow";
import { WebhookFailureEvents } from "./webhook-failure-events";

vi.mock("@/lib/api", () => ({
  fetchWebhookFailures: vi.fn(),
}));

const sample: WebhookFailureEvent = {
  id: "whfail_1",
  webhookId: "webhook_1",
  executionId: "evt_1",
  url: "https://example.com/hook",
  httpStatus: 502,
  responseOrError: "upstream unavailable",
  createdAt: "2026-09-20T06:00:00.000Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem("flow:locale", "en-US");
});

describe("WebhookFailureEvents", () => {
  it("shows loading then empty state", async () => {
    let resolve!: (value: WebhookFailureEvent[]) => void;
    vi.mocked(fetchWebhookFailures).mockReturnValue(
      new Promise((r) => {
        resolve = r;
      }),
    );
    render(<WebhookFailureEvents webhookId="webhook_1" />);
    expect(screen.getByRole("status")).toHaveTextContent("Loading failures");
    resolve([]);
    expect(
      await screen.findByText("No failures recorded for this webhook"),
    ).toBeVisible();
  });

  it("renders failure rows and opens the detail dialog", async () => {
    vi.mocked(fetchWebhookFailures).mockResolvedValue([sample]);
    render(<WebhookFailureEvents webhookId="webhook_1" />);
    expect(await screen.findByText("upstream unavailable")).toBeVisible();
    expect(screen.getByText("502")).toBeVisible();
    await userEvent.click(
      screen.getByRole("button", { name: /View delivery failure/ }),
    );
    expect(
      await screen.findByRole("heading", { name: "Webhook delivery failure" }),
    ).toBeVisible();
    expect(screen.getByText("evt_1")).toBeVisible();
    expect(screen.getByText("https://example.com/hook")).toBeVisible();
  });

  it("shows error state and retries", async () => {
    vi.mocked(fetchWebhookFailures)
      .mockRejectedValueOnce(new Error("Unavailable"))
      .mockResolvedValueOnce([]);
    render(<WebhookFailureEvents webhookId="webhook_1" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Unavailable");
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() =>
      expect(fetchWebhookFailures).toHaveBeenCalledTimes(2),
    );
    expect(
      await screen.findByText("No failures recorded for this webhook"),
    ).toBeVisible();
  });
});
