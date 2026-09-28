/**
 * LS-0364 JiraOAuth + LS-0365 entry — Cloud / custom personal OAuth and sync links.
 * REST-only. Without deployment secrets, connect surfaces honest errors (no fake success).
 *
 * Product chrome mirrors Linear's post-enable CONNECTION rows (Connected Instances +
 * personal status). Linear Enable / marketing hero is OUT_OF_SCOPE_BRAND — not cloned.
 */
import { ChevronLeft, ChevronRight, Plus } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { IntegrationBrandIcon } from "@/components/settings/integration-brand-icon";
import { useI18n } from "@/i18n/i18n";
import {
  authorizeIntegration,
  connectIntegration,
  disconnectIntegration,
  startIntegrationOAuth,
} from "@/lib/api";
import type { BootstrapData, IntegrationConnection, JiraLink } from "@/types/flow";

import "./feature-settings.css";
import "./jira-settings.css";

type Mode = "cloud" | "custom_personal";

type Props = {
  data: BootstrapData;
  onBack: () => void;
  onReload: () => Promise<void>;
  onOpenSyncNew: () => void;
  onOpenSyncEdit: (jiraProjectId: string) => void;
  onOpenConnectedAccounts?: () => void;
};

function jiraConnection(data: BootstrapData): IntegrationConnection | undefined {
  return data.integrationConnections.find((item) => item.provider === "jira");
}

function connected(connection?: IntegrationConnection) {
  return connection?.status === "connected";
}

export function JiraSettingsPage({
  data,
  onBack,
  onReload,
  onOpenSyncNew,
  onOpenSyncEdit,
  onOpenConnectedAccounts,
}: Props) {
  const { t } = useI18n();
  const connection = jiraConnection(data);
  const links = data.jiraLinks ?? [];
  const [busy, setBusy] = useState(false);
  const [configureOpen, setConfigureOpen] = useState(!connected(connection));
  const [mode, setMode] = useState<Mode>(
    (connection?.config?.mode as Mode) || "cloud",
  );
  const [custom, setCustom] = useState({
    siteURL: connection?.config?.siteURL ?? "",
    authorizationURL: connection?.config?.authorizationURL ?? "",
    tokenURL: connection?.config?.tokenURL ?? "",
    clientID: connection?.config?.clientID ?? "",
    redirectURI:
      connection?.config?.redirectURI ??
      `${window.location.origin}/api/integrations/jira/oauth/callback`,
    clientSecretEnv:
      connection?.config?.clientSecretEnv ?? "FLOW_INTEGRATION_JIRA_CLIENT_SECRET",
  });


  const personalLabel = useMemo(() => {
    if (connected(connection)) return t("Personal Jira account connected");
    return t("Personal Jira account not connected");
  }, [connection, t]);

  const saveAndAuthorize = async () => {
    setBusy(true);
    try {
      const config: Record<string, string> =
        mode === "cloud"
          ? { mode: "cloud" }
          : {
              mode: "custom_personal",
              siteURL: custom.siteURL.trim(),
              authorizationURL: custom.authorizationURL.trim(),
              tokenURL: custom.tokenURL.trim(),
              clientID: custom.clientID.trim(),
              redirectURI: custom.redirectURI.trim(),
              clientSecretEnv: custom.clientSecretEnv.trim(),
            };
      await authorizeIntegration(
        "jira",
        { name: mode === "cloud" ? "Jira Cloud" : "Jira", config },
        Boolean(connection),
      );
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("OAuth integration is unavailable; configure provider endpoints and credentials"),
      );
      await onReload();
    } finally {
      setBusy(false);
    }
  };

  const saveConfigOnly = async () => {
    setBusy(true);
    try {
      const config: Record<string, string> =
        mode === "cloud"
          ? { mode: "cloud" }
          : {
              mode: "custom_personal",
              siteURL: custom.siteURL.trim(),
              authorizationURL: custom.authorizationURL.trim(),
              tokenURL: custom.tokenURL.trim(),
              clientID: custom.clientID.trim(),
              redirectURI: custom.redirectURI.trim(),
              clientSecretEnv: custom.clientSecretEnv.trim(),
            };
      await connectIntegration("jira", {
        name: mode === "cloud" ? "Jira Cloud" : "Jira",
        config,
      });
      await onReload();
      toast.success(t("Jira connection configuration saved"));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("Could not save Jira configuration"),
      );
    } finally {
      setBusy(false);
    }
  };

  const resumeOAuth = async () => {
    setBusy(true);
    try {
      const authorization = await startIntegrationOAuth("jira");
      window.location.assign(authorization.authorizationURL);
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("OAuth integration is unavailable; configure provider endpoints and credentials"),
      );
      await onReload();
    } finally {
      setBusy(false);
    }
  };

  const disconnect = async () => {
    setBusy(true);
    try {
      await disconnectIntegration("jira");
      await onReload();
      setConfigureOpen(true);
      toast.success(t("Jira disconnected"));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("Could not disconnect Jira"),
      );
    } finally {
      setBusy(false);
    }
  };

  const onAddInstance = () => {
    if (connected(connection)) {
      onOpenSyncNew();
      return;
    }
    setConfigureOpen(true);
    toast.message(t("Connect Jira first"), {
      description: t("Project sync wizard is available after a successful OAuth connection."),
    });
  };

  return (
    <div className="feature-integrations">
      <div className="jira-settings feature-settings">
        <header className="feature-header">
          <button type="button" className="feature-back-link" onClick={onBack}>
            <ChevronLeft size={14} />
            {t("Integrations")}
          </button>
          <div className="jira-settings-title">
            <IntegrationBrandIcon provider="jira" size={32} />
            <div>
              <h1 data-i18n-ignore>Jira</h1>
              <p>
                {t(
                  "Sync issues and status between Jira and Flow. Cloud OAuth or custom personal OAuth.",
                )}
              </p>
            </div>
          </div>
        </header>

        <section className="feature-section jira-connection-section" data-jira-product="connection">
          <header className="jira-section-label">
            <h2>{t("Connection")}</h2>
          </header>

          <div className="jira-connection-stack" data-jira-product="connection-rows">
            <div className="feature-card jira-connection-card">
              <div className="jira-connection-row">
                <strong>{t("Connected instances")}</strong>
                <aside>
                  <button
                    type="button"
                    className="jira-icon-button"
                    aria-label={t("Add connected instance")}
                    onClick={onAddInstance}
                  >
                    <Plus size={16} />
                  </button>
                </aside>
              </div>
            </div>
            <div className="feature-card jira-connection-card">
              <div className="jira-connection-row">
                <strong>{personalLabel}</strong>
                <aside>
                  {connected(connection) ? (
                    <button
                      type="button"
                      className="feature-button danger"
                      disabled={busy}
                      onClick={() => void disconnect()}
                    >
                      {t("Disconnect")}
                    </button>
                  ) : (
                    <button
                      type="button"
                      className="jira-text-link"
                      onClick={() => {
                        if (onOpenConnectedAccounts) onOpenConnectedAccounts();
                        else setConfigureOpen(true);
                      }}
                    >
                      {t("Connected accounts")}
                      <ChevronRight size={14} />
                    </button>
                  )}
                </aside>
              </div>
            </div>
          </div>

          {!connected(connection) && configureOpen ? (
            <div className="feature-card jira-configure-card" data-jira-product="configure">
              <div className="jira-configure-header">
                <strong>{t("Configure OAuth")}</strong>
                <span>
                  {t("Cloud or custom personal OAuth. Deployment secrets stay on the API.")}
                </span>
              </div>
              {connection?.lastError ? (
                <div className="jira-callout error" role="status">
                  <strong>{t("Connection error")}</strong>
                  {connection.lastError}
                </div>
              ) : null}
              <div className="jira-mode-tabs" role="tablist" aria-label={t("Jira OAuth mode")}>
                <button
                  type="button"
                  role="tab"
                  aria-selected={mode === "cloud"}
                  onClick={() => setMode("cloud")}
                >
                  {t("Atlassian Cloud")}
                </button>
                <button
                  type="button"
                  role="tab"
                  aria-selected={mode === "custom_personal"}
                  onClick={() => setMode("custom_personal")}
                >
                  {t("Custom OAuth (personal)")}
                </button>
              </div>
              {mode === "cloud" ? (
                <div className="jira-callout">
                  <strong>{t("Deployment secrets required")}</strong>
                  {t(
                    "Set FLOW_INTEGRATION_JIRA_CLIENT_ID, FLOW_INTEGRATION_JIRA_CLIENT_SECRET, and FLOW_INTEGRATION_JIRA_REDIRECT_URI on the API. Without them, Authorize returns an honest unavailable error — never a fake success.",
                  )}
                </div>
              ) : (
                <div className="jira-form">
                  <label>
                    {t("Jira site URL")}
                    <input
                      aria-label={t("Jira site URL")}
                      value={custom.siteURL}
                      onChange={(event) =>
                        setCustom((value) => ({ ...value, siteURL: event.target.value }))
                      }
                      placeholder="https://jira.example.com"
                    />
                  </label>
                  <label>
                    {t("Authorization URL")}
                    <input
                      aria-label={t("Authorization URL")}
                      value={custom.authorizationURL}
                      onChange={(event) =>
                        setCustom((value) => ({
                          ...value,
                          authorizationURL: event.target.value,
                        }))
                      }
                    />
                  </label>
                  <label>
                    {t("Token URL")}
                    <input
                      aria-label={t("Token URL")}
                      value={custom.tokenURL}
                      onChange={(event) =>
                        setCustom((value) => ({ ...value, tokenURL: event.target.value }))
                      }
                    />
                  </label>
                  <label>
                    {t("OAuth client ID")}
                    <input
                      aria-label={t("OAuth client ID")}
                      value={custom.clientID}
                      onChange={(event) =>
                        setCustom((value) => ({ ...value, clientID: event.target.value }))
                      }
                    />
                  </label>
                  <label>
                    {t("Redirect URI")}
                    <input
                      aria-label={t("Redirect URI")}
                      value={custom.redirectURI}
                      onChange={(event) =>
                        setCustom((value) => ({ ...value, redirectURI: event.target.value }))
                      }
                    />
                  </label>
                  <label>
                    {t("Client secret env var")}
                    <input
                      aria-label={t("Client secret env var")}
                      value={custom.clientSecretEnv}
                      onChange={(event) =>
                        setCustom((value) => ({
                          ...value,
                          clientSecretEnv: event.target.value,
                        }))
                      }
                    />
                  </label>
                </div>
              )}
              <div className="jira-form-actions" style={{ padding: "0 16px 16px" }}>
                {connection ? (
                  <button
                    type="button"
                    className="feature-button primary"
                    disabled={busy}
                    onClick={() => void resumeOAuth()}
                  >
                    {t("Authorize")}
                  </button>
                ) : null}
                <button
                  type="button"
                  className="feature-button"
                  disabled={busy}
                  onClick={() => void saveConfigOnly()}
                >
                  {t("Save configuration")}
                </button>
                <button
                  type="button"
                  className="feature-button primary"
                  disabled={busy}
                  onClick={() => void saveAndAuthorize()}
                >
                  {t("Connect with OAuth")}
                </button>
              </div>
            </div>
          ) : null}

          {connected(connection) ? (
            <div className="jira-configure-toggle">
              <button
                type="button"
                className="jira-text-link"
                onClick={() => setConfigureOpen((open) => !open)}
              >
                {configureOpen ? t("Hide OAuth settings") : t("Configure OAuth")}
                <ChevronRight size={14} />
              </button>
            </div>
          ) : null}

          {connected(connection) && configureOpen ? (
            <div className="feature-card jira-configure-card" data-jira-product="configure">
              <div className="jira-callout">
                <strong>{t("Connected")}</strong>
                {t(
                  "OAuth tokens are managed by the API. Disconnect above to clear the connection, or re-authorize if scopes change.",
                )}
              </div>
              <div className="jira-form-actions" style={{ padding: "0 16px 16px" }}>
                <button
                  type="button"
                  className="feature-button"
                  disabled={busy}
                  onClick={() => void resumeOAuth()}
                >
                  {t("Re-authorize")}
                </button>
              </div>
            </div>
          ) : null}
        </section>

        <section className="feature-section" data-jira-product="project-sync">
          <header className="jira-section-label">
            <h2>{t("Project sync")}</h2>
            <p>
              {t(
                "Link Jira projects to Flow teams and configure sync direction and status mapping.",
              )}
            </p>
          </header>
          {connected(connection) ? (
            <>
              {links.length ? (
                <div className="feature-card jira-link-list">
                  {links.map((link) => (
                    <JiraLinkRow
                      key={link.id}
                      link={link}
                      teamName={
                        data.teams.find((team) => team.id === link.teamId)?.name ?? link.teamId
                      }
                      onOpen={() => onOpenSyncEdit(link.jiraProjectId)}
                    />
                  ))}
                </div>
              ) : (
                <div className="feature-empty">
                  <Plus size={24} />
                  <h3>{t("No Jira project links")}</h3>
                  <button
                    type="button"
                    className="feature-button primary"
                    onClick={onOpenSyncNew}
                  >
                    {t("Create Jira link")}
                  </button>
                </div>
              )}
              {links.length > 0 ? (
                <div className="feature-section-action">
                  <button type="button" className="feature-button" onClick={onOpenSyncNew}>
                    <Plus size={14} />
                    {t("New sync")}
                  </button>
                </div>
              ) : null}
            </>
          ) : (
            <div className="jira-callout" role="status">
              <strong>{t("Connect Jira first")}</strong>
              {t("Project sync wizard is available after a successful OAuth connection.")}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

function JiraLinkRow({
  link,
  teamName,
  onOpen,
}: {
  link: JiraLink;
  teamName: string;
  onOpen: () => void;
}) {
  const { t } = useI18n();
  const direction =
    link.syncDirection === "bidirectional"
      ? t("Bi-directional")
      : t("Jira → Flow");
  return (
    <button type="button" className="jira-link-row" onClick={onOpen}>
      <div>
        <strong data-i18n-ignore>
          {link.jiraProjectName || link.jiraProjectKey || link.jiraProjectId}
        </strong>
        <span>
          {teamName} · {direction}
        </span>
      </div>
      <ChevronRight size={15} />
    </button>
  );
}
