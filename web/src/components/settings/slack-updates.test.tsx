import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";

const api = vi.hoisted(() => ({
  authorizeIntegration: vi.fn(),
  disconnectIntegrationConnection: vi.fn(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import {
  findSlackUpdatesConnection,
  SlackUpdates,
} from "./slack-updates";

it("finds project and initiative Slack update connections (LS-0557)", () => {
  const data = makeBootstrap({
    integrationConnections: [
      {
        id: "p",
        provider: "slack",
        name: "proj",
        status: "connected",
        config: { scope: "orgProjectUpdates" },
        connectedBy: "user",
        createdAt: "2026-09-10T00:00:00Z",
        updatedAt: "2026-09-10T00:00:00Z",
        scopes: [],
        channels: [],
        linkbackEnabled: false,
        deliveryAttempts: 0,
      },
      {
        id: "i",
        provider: "slack",
        name: "init",
        status: "connected",
        config: { scope: "initiative-updates" },
        connectedBy: "user",
        createdAt: "2026-09-10T00:00:00Z",
        updatedAt: "2026-09-10T00:00:00Z",
        scopes: [],
        channels: [],
        linkbackEnabled: false,
        deliveryAttempts: 0,
      },
    ],
  });
  expect(findSlackUpdatesConnection(data, "project")?.id).toBe("p");
  expect(findSlackUpdatesConnection(data, "initiative")?.id).toBe("i");
});

it("connects a project updates Slack channel for broadcast", async () => {
  const user = userEvent.setup();
  const onReload = vi.fn().mockResolvedValue(undefined);
  api.authorizeIntegration.mockResolvedValue({});
  render(
    <I18nProvider>
      <SlackUpdates
        data={makeBootstrap({
          viewerRole: "admin",
          integrationConnections: [],
        })}
        kind="project"
        variant="row"
        onReload={onReload}
      />
    </I18nProvider>,
  );
  expect(
    screen.getByText("Send project updates to a Slack channel"),
  ).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /Connect/i }));
  expect(api.authorizeIntegration).toHaveBeenCalledWith("slack", {
    name: "Slack",
    config: { scope: "orgProjectUpdates", source: "project-updates" },
  });
  expect(onReload).toHaveBeenCalled();
});
