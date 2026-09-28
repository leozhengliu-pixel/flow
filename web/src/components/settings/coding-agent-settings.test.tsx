import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CodingAgentSettingsPage } from "./coding-agent-settings";

vi.mock("@/lib/api", () => ({
  updateWorkspacePreferences: vi.fn().mockResolvedValue({}),
}));

vi.mock("@/i18n/i18n", () => ({
  useI18n: () => ({ t: (value: string) => value }),
}));

const data = {
  workspace: { urlKey: "acme" },
  workspaceSettings: {
    featureFlags: { "coding-sessions": true },
    codingAgentSettings: { harness: "claude", model: "auto", environments: [] },
  },
} as never;

describe("CodingAgentSettingsPage", () => {
  it("renders coding agent shell without billing chrome", () => {
    render(<CodingAgentSettingsPage data={data} />);
    expect(screen.getByTestId("coding-agent-settings")).toBeVisible();
    expect(screen.getByText("Coding sessions")).toBeVisible();
    expect(screen.getByLabelText("Agent")).toBeVisible();
    expect(screen.getByLabelText("Model")).toBeVisible();
    expect(screen.queryByText(/credit/i)).toBeNull();
    expect(screen.queryByText(/upsell/i)).toBeNull();
  });

  it("renders environments shell", () => {
    render(<CodingAgentSettingsPage data={data} mode="environments" />);
    expect(screen.getByTestId("coding-environment-settings")).toBeVisible();
    expect(screen.getByText("Environments")).toBeVisible();
    expect(screen.getByText("No environments configured")).toBeVisible();
  });
});
