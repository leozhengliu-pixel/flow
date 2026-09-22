import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentTrustedSourcesSettings } from "./agent-trusted-sources-settings";

vi.mock("@/lib/api", () => ({
  updateWorkspacePreferences: vi.fn().mockResolvedValue({}),
}));

vi.mock("@/i18n/i18n", () => ({
  useI18n: () => ({ t: (value: string) => value }),
}));

vi.mock("@/components/automation/automation-trusted-source-editor", () => ({
  AutomationTrustedSourceEditor: () => (
    <div data-testid="automation-trusted-source-editor" />
  ),
}));

describe("AgentTrustedSourcesSettings", () => {
  it("renders the trusted sources settings shell", () => {
    render(
      <AgentTrustedSourcesSettings
        data={{
          workspace: { urlKey: "acme" },
          workspaceSettings: {
            trustedSourcesMode: "none",
            trustedSourcesAllowlist: [],
          },
          users: [],
          integrationConnections: [],
        } as never}
      />,
    );
    expect(screen.getByTestId("agent-trusted-sources-settings")).toBeVisible();
    expect(screen.getByText("Trusted sources")).toBeVisible();
    expect(screen.getByTestId("automation-trusted-source-editor")).toBeVisible();
  });
});
