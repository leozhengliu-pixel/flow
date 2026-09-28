import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import { IntegrationSettingsPage } from "./integration-settings-page";

it("shows Intercom coming-soon product pattern without honesty banner chrome", () => {
  render(
    <I18nProvider>
      <IntegrationSettingsPage
        slug="intercom"
        data={makeBootstrap({ integrationConnections: [] })}
        onBack={vi.fn()}
        onReload={vi.fn()}
      />
    </I18nProvider>,
  );
  expect(screen.getByRole("heading", { name: "Intercom" })).toBeInTheDocument();
  expect(screen.getByText(/Conversations to issues/i)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Coming soon" })).toBeDisabled();
  expect(screen.queryByText(/honest shell/i)).not.toBeInTheDocument();
  expect(screen.queryByText(/not wired yet/i)).not.toBeInTheDocument();
  expect(screen.queryByText(/catalog parity/i)).not.toBeInTheDocument();
});
