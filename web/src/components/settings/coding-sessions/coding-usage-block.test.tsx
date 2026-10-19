import { render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "@/i18n/i18n";
import { fetchCodingUsage } from "@/lib/api";
import { viewer } from "@/test/fixtures";
import { formatMicros } from "./coding-sessions-model";
import { CodingUsageBlock } from "./coding-usage-block";

vi.mock("@/lib/api", async (original) => ({ ...(await original<typeof import("@/lib/api")>()), fetchCodingUsage: vi.fn() }));
beforeEach(() => localStorage.setItem("flow:locale", "en-US"));

it("shows coding spend by user and loop", async () => {
  vi.mocked(fetchCodingUsage).mockResolvedValue({ since: "2026-10-01T00:00:00Z", totalCostMicros: 12_340_000, llmCostMicros: 10_000_000, runtimeCostMicros: 2_340_000, byUser: { [viewer.id]: 12_000_000, ghost: 340_000 }, byLoop: { loop1: 1_000_000 }, sessions: 7 });
  render(<I18nProvider><CodingUsageBlock users={[viewer as never]} loopNames={{ loop1: "Autofix bugs" }} /></I18nProvider>);
  expect(await screen.findByText("$12.34")).toBeVisible();
  expect(screen.getByText(/7 sessions/)).toBeVisible();
  expect(screen.getByText(viewer.name)).toBeVisible();
  expect(screen.getByText("ghost")).toBeVisible();
  expect(screen.getByText("Autofix bugs")).toBeVisible();
});

it("renders nothing when the usage API is unavailable", async () => {
  vi.mocked(fetchCodingUsage).mockRejectedValue(new Error("501"));
  const { container } = render(<I18nProvider><CodingUsageBlock users={[]} /></I18nProvider>);
  await Promise.resolve();
  expect(container).toBeEmptyDOMElement();
  expect(formatMicros(1_500_000)).toBe("$1.50");
});
