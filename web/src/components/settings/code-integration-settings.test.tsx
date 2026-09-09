import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import type { IntegrationConnection } from "@/types/flow";

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

it("distinguishes stored configuration from completed authorization", () => {
  const connection = {id:'connection',provider:'github',name:'Example',status:'configured',config:{organization:'example'},connectedBy:'user',createdAt:'2026-09-10T00:00:00Z',updatedAt:'2026-09-10T00:00:00Z',scopes:[],channels:[],linkbackEnabled:false,deliveryAttempts:0} as IntegrationConnection;
  const view=(item:IntegrationConnection)=><I18nProvider><CodeIntegrationSettings provider="github" data={makeBootstrap({integrationConnections:[item],integrationDeliveries:[]})} onBack={vi.fn()} onReload={vi.fn()}/></I18nProvider>;
  const {rerender}=render(view(connection));
  expect(screen.getByRole('button',{name:'Manage connection'})).toHaveTextContent('Configured');
  rerender(view({...connection,status:'connected'}));
  expect(screen.getByRole('button',{name:'Manage connection'})).toHaveTextContent('Connected');
});

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
