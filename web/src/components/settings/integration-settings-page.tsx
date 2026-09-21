/**
 * LS-0338 IntegrationSettingsPage — generic /settings/integrations/:slug shell + registry.
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

  const toggleSlack = async () => {
    if (!entry.connectProvider || entry.connectProvider !== "slack") return;
    setBusy(true);
    try {
      if (connected) await disconnectIntegration("slack");
      else
        await authorizeIntegration(
          "slack",
          { name: "Slack", config: { mode: "workspace" } },
          Boolean(connection),
        );
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update integration"));
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
                {entry.availability === "supported" && entry.connectProvider === "slack" ? (
                  <button
                    type="button"
                    className={`feature-button${connected ? " danger" : " primary"}`}
                    disabled={busy}
                    onClick={() => void toggleSlack()}
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
            {entry.availability !== "supported" && (
              <div className="feature-row">
                <div>
                  <strong>{t("Availability")}</strong>
                  <span>
                    {t(
                      entry.availability === "coming_soon"
                        ? "OAuth for this integration is not wired yet. This settings page is an honest shell."
                        : "This integration is listed for catalog parity and is not planned for Flow yet.",
                    )}
                  </span>
                </div>
              </div>
            )}
          </div>
        </section>

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

export function connectionForCatalog(
  data: BootstrapData,
  entry: IntegrationCatalogEntry,
): IntegrationConnection | undefined {
  if (!entry.connectProvider) return undefined;
  return data.integrationConnections.find((item) => item.provider === entry.connectProvider);
}
