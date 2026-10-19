import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchCodingEnvironments, fetchCodingSettings, updateCodingSettings } from "@/lib/api";
import { I18nProvider } from "@/i18n/i18n";
import type { CodingSettings } from "@/types/flow";
import { makeEnvironment, makeSettings } from "./coding-sessions-test-kit";
import { LoopCodeAccessSection } from "./loop-code-access";

vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  fetchCodingSettings: vi.fn(),
  updateCodingSettings: vi.fn(),
  fetchCodingEnvironments: vi.fn(),
}));
vi.mock("@/lib/toast", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem("flow:locale", "en-US");
  vi.mocked(fetchCodingEnvironments).mockResolvedValue([makeEnvironment(), makeEnvironment({ id: "env2", name: "API", repository: "acme/api" })]);
  vi.mocked(fetchCodingSettings).mockResolvedValue(makeSettings({ loopRepositoryAccess: "selected", loopRepositories: ["env1"] }));
  vi.mocked(updateCodingSettings).mockImplementation(async (patch) => makeSettings({ loopRepositoryAccess: "selected", loopRepositories: ["env1"], ...(patch as Partial<CodingSettings>) }));
});

describe("Loops › Capabilities › Allow loops to access code", () => {
  it("limits Loops to selected repositories and switches the access level", async () => {
    render(<I18nProvider><LoopCodeAccessSection /></I18nProvider>);
    expect(screen.getByText("Capabilities")).toBeVisible();
    const api = await screen.findByRole("checkbox", { name: /acme\/api/ });
    expect(screen.getByRole("checkbox", { name: /acme\/web/ })).toBeChecked();
    fireEvent.click(api);
    expect(updateCodingSettings).toHaveBeenCalledWith({ loopRepositories: ["env1", "env2"] });
    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox", { name: "Allow loops to access code" }));
    expect(await screen.findByRole("menuitem", { name: /^Disabled/ })).toBeVisible();
    await user.click(screen.getByRole("menuitem", { name: /^All repositories/ }));
    expect(updateCodingSettings).toHaveBeenCalledWith({ loopRepositoryAccess: "all" });
  });

  it("is read-only when disabled", async () => {
    render(<I18nProvider><LoopCodeAccessSection disabled /></I18nProvider>);
    expect(await screen.findByRole("combobox", { name: "Allow loops to access code" })).toBeDisabled();
  });
});
