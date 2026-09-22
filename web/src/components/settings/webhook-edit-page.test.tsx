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
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import { WebhookEditPage } from "./webhook-edit-page";

it("requires HTTPS and shows signing secret once on create (LS-0648 / LS-0711)", async () => {
  const user = userEvent.setup();
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
      onClose={vi.fn()}
      onSaved={vi.fn().mockResolvedValue(undefined)}
    />,
  );

  const nameInput = screen.getByRole("textbox", { name: /^Name$/i });
  await user.type(nameInput, "Deploy");
  const url = screen.getByPlaceholderText("https://example.com/webhooks/flow");
  await user.type(url, "http://example.com/hook");
  await user.tab();
  expect(screen.getByRole("alert")).toHaveTextContent(/HTTPS/i);
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  expect(api.createWebhook).not.toHaveBeenCalled();

  await user.clear(url);
  await user.type(url, "https://example.com/hook");
  expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(api.createWebhook).toHaveBeenCalled();
  expect(screen.getByDisplayValue("flow_wh_abcSECRET")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Copy secret" })).toBeInTheDocument();
});
