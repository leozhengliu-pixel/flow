/**
 * LS-0338 IntegrationSettingsPage — generic /settings/integrations/:slug shell + registry.
 * P1-D: long-tail OAuth uses catalog coming-soon *product* patterns — no honesty/gap banners.
 * P2-D: Teams / PagerDuty / Front use browser oauth/start + CompleteOAuthView.
 */
import { Plug } from "lucide-react";

import { IntegrationBrandIcon } from "@/components/settings/integration-brand-icon";
import { useI18n } from "@/i18n/i18n";
import {
  authorizeIntegration,
  disconnectIntegration,
} from "@/lib/api";
import {
  availabilityLabel,
  getIntegrationCatalogEntry,
  getIntegrationProductPattern,
  subtypesFor,
  type IntegrationCatalogEntry,
} from "@/lib/integration-catalog";
import type { BootstrapData, IntegrationConnection } from "@/types/flow";
import { toast } from "sonner";
import { useState } from "react";

type Props = {
  slug: string;
  data: BootstrapData;
  onBack: () => void;
  onReload: () => Promise<void>;
};

export function IntegrationSettingsPage({
  slug,
  data,
  onBack,
  onReload,
}: Props) {
  const { t } = useI18n();
  const entry = getIntegrationCatalogEntry(slug);
  const [busy, setBusy] = useState(false);

  if (!entry) {
    return (
      <div className="feature-integrations">
        <div className="feature-settings">
          <header className="feature-header">
            <button type="button" className="feature-back-link" onClick={onBack}>
              {t("Integrations")}
            </button>
            <h1>{t("Integration not found")}</h1>
            <p>{t("This integration is not in the Flow catalog.")}</p>
          </header>
          <button type="button" className="feature-button" onClick={onBack}>
            {t("Back to integrations")}
          </button>
        </div>
      </div>
    );
  }

  const connection = entry.connectProvider
    ? data.integrationConnections.find((item) => item.provider === entry.connectProvider)
    : undefined;
  const connected = connection?.status === "connected";
  const subtypes = subtypesFor(entry.slug);
  const pattern = getIntegrationProductPattern(entry);

  const oauthProviders = new Set(["slack", "microsoftteams", "pagerduty", "front"]);
  const canBrowserOAuth =
    entry.availability === "supported" &&
    Boolean(entry.connectProvider) &&
    oauthProviders.has(entry.connectProvider!);

  const toggleOAuth = async () => {
    if (!entry.connectProvider || !canBrowserOAuth) return;
    const provider = entry.connectProvider;
    setBusy(true);
    try {
      if (connected) await disconnectIntegration(provider);
      else {
        const config =
          provider === "slack"
            ? { mode: "workspace" }
            : provider === "microsoftteams"
              ? { mode: "workspace" }
              : {};
        await authorizeIntegration(
          provider,
          { name: entry.name, config },
          Boolean(connection),
        );
      }
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update integration"));
      await onReload();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="feature-integrations">
      <div className="feature-settings">
        <header className="feature-header">
          <button type="button" className="feature-back-link" onClick={onBack}>
            {t("Integrations")}
          </button>
          <div className="integration-settings-title">
            <IntegrationSettingsIcon entry={entry} />
            <div>
              <h1 data-i18n-ignore>{entry.name}</h1>
              <p>{t(entry.description)}</p>
            </div>
          </div>
        </header>

        <section className="feature-section" style={{ marginTop: 32 }}>
          <header>
            <h2>{t("About")}</h2>
          </header>
          <div className="feature-card">
            <div className="feature-row">
              <div>
                <strong>{t("Status")}</strong>
                <span>
                  {connected
                    ? t("Enabled")
                    : t(availabilityLabel(entry.availability))}
                </span>
              </div>
              <aside>
                {canBrowserOAuth ? (
                  <button
                    type="button"
                    className={`feature-button${connected ? " danger" : " primary"}`}
                    disabled={busy}
                    onClick={() => void toggleOAuth()}
                  >
                    {t(connected ? "Disconnect" : "Enable")}
                  </button>
                ) : entry.availability === "supported" ? (
                  <button type="button" className="feature-button primary" disabled>
                    {t("Enable")}
                  </button>
                ) : (
                  <span className={`integration-availability-badge is-${entry.availability}`}>
                    {t(availabilityLabel(entry.availability))}
                  </span>
                )}
              </aside>
            </div>
            {connection && (
              <div className="feature-row">
                <div>
                  <strong>{t("Installed by")}</strong>
                  <span data-i18n-ignore>{connection.name}</span>
                </div>
              </div>
            )}
          </div>
        </section>

        {entry.availability !== "supported" && pattern && (
          <section className="feature-section" aria-label={t("What you can do")}>
            <header>
              <h2>{t(pattern.headline)}</h2>
              <p>{t(pattern.summary)}</p>
            </header>
            <div className="feature-card">
              {pattern.capabilities.map((capability) => (
                <div key={capability} className="feature-row">
                  <div>
                    <strong>{t(capability)}</strong>
                  </div>
                </div>
              ))}
              <div className="feature-row">
                <div>
                  <strong>{t("Connect")}</strong>
                  <span>
                    {t(
                      entry.availability === "coming_soon"
                        ? "Available when this integration ships in Flow."
                        : "Not available in Flow.",
                    )}
                  </span>
                </div>
                <aside>
                  <button type="button" className="feature-button" disabled>
                    {t(
                      entry.availability === "coming_soon"
                        ? "Coming soon"
                        : "Not supported",
                    )}
                  </button>
                </aside>
              </div>
            </div>
          </section>
        )}

        {subtypes.length > 0 && (
          <section className="feature-section">
            <header>
              <h2>{t("Related connections")}</h2>
              <p>{t("Subtype integrations that belong to this provider")}</p>
            </header>
            <div className="feature-card">
              {subtypes.map((subtype) => (
                <div key={subtype.slug} className="feature-row">
                  <div>
                    <strong data-i18n-ignore>{subtype.name}</strong>
                    <span>{t(subtype.description)}</span>
                  </div>
                  <aside>
                    <span className={`integration-availability-badge is-${subtype.availability}`}>
                      {t(availabilityLabel(subtype.availability))}
                    </span>
                  </aside>
                </div>
              ))}
            </div>
          </section>
        )}
      </div>
    </div>
  );
}

function IntegrationSettingsIcon({ entry }: { entry: IntegrationCatalogEntry }) {
  if (
    entry.connectProvider === "github" ||
    entry.connectProvider === "gitlab" ||
    entry.connectProvider === "slack"
  ) {
    return <IntegrationBrandIcon provider={entry.connectProvider} size={32} />;
  }
  return (
    <span className="feature-row-icon" style={{ width: 40, height: 40 }}>
      <Plug size={20} />
    </span>
  );
}

export function resolveIntegrationSettings(
  slug: string,
  data: BootstrapData,
): IntegrationConnection | undefined {
  const entry = getIntegrationCatalogEntry(slug);
  if (!entry?.connectProvider) return undefined;
  return data.integrationConnections.find((item) => item.provider === entry.connectProvider);
}
