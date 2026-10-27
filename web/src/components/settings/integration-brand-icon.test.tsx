import { render } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { IntegrationBrandIcon } from "./integration-brand-icon";
import { EnabledIntegrationNav } from "./enabled-integration-nav";
import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";

it.each(["feishu", "lark"] as const)("uses the official bundled %s brand asset", (provider) => {
  const { container } = render(<IntegrationBrandIcon provider={provider} size={28} />);
  const mark = container.querySelector("img");
  expect(mark).toHaveAttribute("src", `/integrations/${provider}.${provider === "feishu" ? "svg" : "png"}`);
  expect(mark).toHaveAttribute("width", "28");
  expect(mark).toHaveAttribute("height", "28");
  expect(container.querySelector("svg")).toBeNull();
});

it("keeps actual Feishu and Lark marks in enabled integration navigation", () => {
  const data = makeBootstrap({ integrationConnections: [
    { id: "feishu", provider: "feishu", name: "Feishu", status: "connected" },
    { id: "lark", provider: "lark", name: "Lark", status: "pending_authorization" },
  ] as never });
  const { container } = render(<I18nProvider><EnabledIntegrationNav data={data} onIntegrationsPage slug="feishu" onOpenAll={vi.fn()} onOpen={vi.fn()} /></I18nProvider>);
  expect(container.querySelector('img[src="/integrations/feishu.svg"]')).not.toBeNull();
  expect(container.querySelector('img[src="/integrations/lark.png"]')).not.toBeNull();
});
