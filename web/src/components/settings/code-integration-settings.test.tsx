import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";

const api = vi.hoisted(() => ({
  connectIntegration: vi.fn(),
  disconnectIntegrationConnection: vi.fn(),
  retryIntegrationDelivery: vi.fn(),
  testIntegrationConnection: vi.fn(),
  updateIntegrationConnection: vi.fn(),
}));
vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  ...api,
}));

import { CodeIntegrationSettings } from "./code-integration-settings";

it("tests a GitLab token against the configured host before connecting", async () => {
  const user = userEvent.setup();
  api.testIntegrationConnection.mockResolvedValue({
    provider: "gitlab",
    status: "ready",
    testedAt: new Date().toISOString(),
  });
  render(
    <I18nProvider>
      <CodeIntegrationSettings
        provider="gitlab"
        data={makeBootstrap({ integrationConnections: [] })}
        onBack={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
      />
    </I18nProvider>,
  );

  await user.type(screen.getByLabelText("API access token"), "glpat-test");
  await user.type(
    screen.getByRole("textbox", {
      name: "Custom GitLab URL (optional, self-hosted only)",
    }),
    "https://gitlab.example.com",
  );
  await user.click(screen.getByRole("button", { name: "Test connection" }));

  expect(api.testIntegrationConnection).toHaveBeenCalledWith(
    "gitlab",
    undefined,
    {
      token: "glpat-test",
      host: "https://gitlab.example.com",
    },
  );
});
