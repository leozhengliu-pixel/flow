/**
 * LS-0248 EnabledIntegrationsSettingsPage — merge connections + authorized apps.
 */
import { AppWindow, Plug } from "lucide-react";

import { IntegrationBrandIcon } from "@/components/settings/integration-brand-icon";
import { useI18n } from "@/i18n/i18n";
import { disconnectIntegration, revokeOAuthAuthorization } from "@/lib/api";
import {
  getIntegrationCatalogEntry,
  primaryIntegrations,
} from "@/lib/integration-catalog";
import type { BootstrapData } from "@/types/flow";
import { toast } from "sonner";

type Props = {
  data: BootstrapData;
  onBack: () => void;
  onOpenSlug: (slug: string) => void;
  onReload: () => Promise<void>;
};

export function EnabledIntegrationsSettingsPage({ data, onBack, onOpenSlug, onReload }: Props) {
  const { t, formatDate } = useI18n();
  const authorizations = (data.oauthAuthorizations ?? []).filter((item) => !item.revokedAt);
  const connections = data.integrationConnections.filter(
    (item) => item.status === "connected" || item.status === "configured",
  );
  const empty = !connections.length && !authorizations.length;

  const revokeConnection = async (provider: string) => {
    try {
      await disconnectIntegration(provider);
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not revoke access"));
    }
  };

  return (
    <div className="feature-integrations">
      <div className="feature-settings">
        <header className="feature-header">
          <button type="button" className="feature-back-link" onClick={onBack}>
            {t("Integrations")}
          </button>
          <h1>{t("Enabled integrations")}</h1>
          <p>{t("Workspace connections and applications currently enabled for this workspace.")}</p>
        </header>

        {connections.length > 0 && (
          <section className="feature-section" style={{ marginTop: 32 }}>
            <header>
              <h2>{t("Connections")}</h2>
              <p>{t("Integrations connected to this workspace")}</p>
            </header>
            <div className="feature-card">
              {connections.map((item) => {
                const catalog =
                  getIntegrationCatalogEntry(item.provider) ??
                  primaryIntegrations().find((entry) => entry.connectProvider === item.provider);
                const slug = catalog?.slug ?? item.provider;
                const brand =
                  item.provider === "github" || item.provider === "gitlab" || item.provider === "slack"
                    ? (item.provider as "github" | "gitlab" | "slack")
                    : null;
                return (
                  <div key={item.id} className="feature-row">
                    <span className="feature-row-icon">
                      {brand ? <IntegrationBrandIcon provider={brand} size={16} /> : <Plug size={16} />}
                    </span>
                    <div>
                      <strong data-i18n-ignore>
                        <button type="button" className="feature-text-link" onClick={() => onOpenSlug(slug)}>
                          {item.name}
                        </button>
                      </strong>
                      <span>
                        {t("Enabled")}
                        {item.createdAt ? ` · ${formatDate(item.createdAt)}` : ""}
                        {" · "}
                        {t(item.status)}
                      </span>
                    </div>
                    <aside>
                      <button type="button" className="feature-button" onClick={() => onOpenSlug(slug)}>
                        {t("Manage")}
                      </button>
                      <button
                        type="button"
                        className="feature-button danger"
                        onClick={() => void revokeConnection(item.provider)}
                      >
                        {t("Revoke access")}
                      </button>
                    </aside>
                  </div>
                );
              })}
            </div>
          </section>
        )}

        {authorizations.length > 0 && (
          <section className="feature-section">
            <header>
              <h2>{t("Applications")}</h2>
              <p>{t("Third-party applications authorized for this workspace")}</p>
            </header>
            <div className="feature-card">
              {authorizations.map((item) => (
                <div key={item.id} className="feature-row">
                  <span className="feature-row-icon">
                    <AppWindow size={16} />
                  </span>
                  <div>
                    <strong data-i18n-ignore>{item.clientName}</strong>
                    <span>
                      MCP · {item.scopes.join(", ")} ·{" "}
                      {item.lastUsedAt
                        ? `${t("last used")} ${formatDate(item.lastUsedAt)}`
                        : `${t("authorized")} ${formatDate(item.createdAt)}`}
                    </span>
                  </div>
                  <aside>
                    <button
                      type="button"
                      className="feature-button danger"
                      onClick={() =>
                        void revokeOAuthAuthorization(item.id)
                          .then(onReload)
                          .catch((error: unknown) =>
                            toast.error(
                              error instanceof Error ? error.message : t("Could not revoke access"),
                            ),
                          )
                      }
                    >
                      {t("Revoke access")}
                    </button>
                  </aside>
                </div>
              ))}
            </div>
          </section>
        )}

        {empty && (
          <div className="feature-empty">
            <Plug size={24} />
            <h3>{t("No enabled integrations")}</h3>
            <p>{t("Connect an integration from the catalog to see it here.")}</p>
            <button type="button" className="feature-button primary" onClick={onBack}>
              {t("Browse integrations")}
            </button>
          </div>
        )}

      </div>
    </div>
  );
}
