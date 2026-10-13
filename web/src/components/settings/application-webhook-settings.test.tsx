import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { TooltipProvider } from "@/components/ui/tooltip";
import {
  fetchApplicationWebhook,
  rotateApplicationWebhookSecret,
  updateApplicationWebhook,
} from "@/lib/api";
import type { OAuthApplication } from "@/types/flow";
import { ApplicationWebhookSettingsSection } from "./application-webhook-settings";

vi.mock("@/lib/api", () => ({
  fetchApplicationWebhook: vi.fn(),
  updateApplicationWebhook: vi.fn(),
  rotateApplicationWebhookSecret: vi.fn(),
}));
vi.mock("@/components/ui/action-dialog-service", () => ({
  confirmAction: vi.fn().mockResolvedValue(true),
}));

const app: OAuthApplication = {
  id: "application-1",
  clientId: "client-1",
  name: "Deploy bot",
  creatorId: "user-1",
  scopes: ["read"],
  redirectUris: [],
  createdAt: "",
  updatedAt: "",
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchApplicationWebhook).mockResolvedValue({
    applicationId: app.id,
    clientId: app.clientId,
    enabled: false,
    url: "",
    resourceTypes: ["Issue"],
    signingEnabled: false,
    installations: 2,
  });
});

function renderSection() {
  const reload = vi.fn().mockResolvedValue(undefined);
  render(
    <TooltipProvider>
      <ApplicationWebhookSettingsSection app={app} onReload={reload} />
    </TooltipProvider>,
  );
  return reload;
}

it("validates the URL, saves events as ids and shows the new secret once", async () => {
  const user = userEvent.setup();
  vi.mocked(updateApplicationWebhook).mockResolvedValue({
    applicationId: app.id,
    clientId: app.clientId,
    enabled: true,
    url: "https://bot.example/hook",
    resourceTypes: ["Issue", "AgentSessionEvent"],
    signingEnabled: true,
    secretPrefix: "flow_wh_abc",
    secret: "flow_wh_abcSECRET",
    installations: 2,
  });
  const reload = renderSection();
  await waitFor(() => expect(fetchApplicationWebhook).toHaveBeenCalledWith(app.id));
  expect(await screen.findByText("2")).toBeInTheDocument();
  expect(screen.getByRole("checkbox", { name: "Issues" })).toBeChecked();
  const url = screen.getByRole("textbox", { name: "Webhook URL" });
  await user.type(url, "http://localhost/hook");
  await user.click(screen.getByRole("checkbox", { name: "Application webhook enabled" }));
  expect(screen.getByRole("alert")).toHaveTextContent("HTTPS");
  expect(updateApplicationWebhook).not.toHaveBeenCalled();

  await user.clear(url);
  await user.type(url, "https://bot.example/hook");
  await user.click(screen.getByRole("checkbox", { name: "Agent session events" }));
  await user.click(screen.getByRole("checkbox", { name: "Application webhook enabled" }));
  await waitFor(() =>
    expect(updateApplicationWebhook).toHaveBeenCalledWith(app.id, {
      enabled: true,
      url: "https://bot.example/hook",
      resourceTypes: ["issues", "agent_session_events"],
    }),
  );
  expect(await screen.findByText("flow_wh_abcSECRET")).toBeInTheDocument();
  expect(reload).toHaveBeenCalled();
});

it("rotates the signing secret after confirmation", async () => {
  const user = userEvent.setup();
  vi.mocked(fetchApplicationWebhook).mockResolvedValue({
    applicationId: app.id,
    clientId: app.clientId,
    enabled: true,
    url: "https://bot.example/hook",
    resourceTypes: ["issues"],
    signingEnabled: true,
    secretPrefix: "flow_wh_old",
    installations: 1,
  });
  vi.mocked(rotateApplicationWebhookSecret).mockResolvedValue({
    applicationId: app.id,
    clientId: app.clientId,
    enabled: true,
    url: "https://bot.example/hook",
    resourceTypes: ["issues"],
    signingEnabled: true,
    secretPrefix: "flow_wh_new",
    secret: "flow_wh_newSECRET",
    installations: 1,
  });
  renderSection();
  expect(await screen.findByText("flow_wh_old…")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Rotate webhook signing secret" }));
  await waitFor(() => expect(rotateApplicationWebhookSecret).toHaveBeenCalledWith(app.id));
  expect(await screen.findByText("flow_wh_newSECRET")).toBeInTheDocument();
});
