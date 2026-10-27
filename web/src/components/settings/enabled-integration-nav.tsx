import { SettingsNavGlyph } from "@/components/settings/settings-nav-glyphs";
import { IntegrationBrandIcon } from "@/components/settings/integration-brand-icon";
import { useI18n } from "@/i18n/i18n";
import { chatProviderName, getIntegrationCatalogEntry, isChatProvider, isInstalledIntegrationConnection, primaryIntegrations } from "@/lib/integration-catalog";
import type { ChatProvider } from "@/lib/integration-catalog";
import type { IntegrationProvider } from "@/lib/app-routes";
import type { BootstrapData } from "@/types/flow";

type EnabledIntegration = { id: string; target: string; name: string; brand: "github" | "gitlab" | ChatProvider | null };

const CODE_PROVIDERS = new Set(["github", "gitlab", "jira"]);

/** The workspace's enabled integrations, each opening its settings page (as from Settings › Integrations). */
function enabledIntegrations(data: Pick<BootstrapData, "integrationConnections">, t: (source: string) => string): EnabledIntegration[] {
  const seen = new Set<string>();
  return (data.integrationConnections ?? [])
    .filter(isInstalledIntegrationConnection)
    .flatMap((item) => {
      if (seen.has(item.provider)) return [];
      seen.add(item.provider);
      const catalog = getIntegrationCatalogEntry(item.provider) ?? primaryIntegrations().find((entry) => entry.connectProvider === item.provider);
      const brand: EnabledIntegration["brand"] = item.provider === "github" || item.provider === "gitlab" || isChatProvider(item.provider) ? item.provider : null;
      const name = isChatProvider(item.provider) ? chatProviderName(item.provider, t) : catalog?.name ?? item.name;
      return [{ id: item.provider, target: CODE_PROVIDERS.has(item.provider) ? item.provider : catalog?.slug ?? item.provider, name, brand }];
    })
    .sort((left, right) => left.name.localeCompare(right.name));
}

/**
 * The admin settings sidebar's Integrations section: "All
 * integrations", then each enabled integration (GitHub, GitLab, …) linking to
 * its settings page.
 */
export function EnabledIntegrationNav({ data, onIntegrationsPage, provider, slug, onOpenAll, onOpen }: {
  data: BootstrapData;
  onIntegrationsPage: boolean;
  provider?: string;
  slug?: string;
  onOpenAll: () => void;
  onOpen: (target: IntegrationProvider | string) => void;
}) {
  const { t } = useI18n();
  const items = enabledIntegrations(data, t);
  const current = onIntegrationsPage ? items.find((item) => item.target === provider || item.target === slug)?.target : undefined;
  const allActive = onIntegrationsPage && !current;
  return (
    <>
      <button aria-current={allActive ? "page" : undefined} className={allActive ? "active" : ""} onClick={onOpenAll}>
        <SettingsNavGlyph name="integrations" />
        <span>{t("All integrations")}</span>
      </button>
      {items.map((item) => (
        <button
          key={item.id}
          aria-current={current === item.target ? "page" : undefined}
          className={current === item.target ? "active" : ""}
          onClick={() => onOpen(item.target)}
        >
          {/* Monochrome brand marks like the rest of the nav; integrations without one use the integrations glyph. */}
          {isChatProvider(item.brand)
            ? <IntegrationBrandIcon provider={item.brand} size={16} />
            : <SettingsNavGlyph name={item.brand ?? "integrations"} />}
          <span data-i18n-ignore>{item.name}</span>
        </button>
      ))}
    </>
  );
}
