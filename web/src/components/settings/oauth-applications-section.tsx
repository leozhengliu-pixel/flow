import { useNavigate } from "react-router-dom";

import { useI18n } from "@/i18n/i18n";
import { applicationSettingsPath } from "@/lib/app-routes";
import type { BootstrapData } from "@/types/flow";
import { FlowTooltip } from "@/components/ui/tooltip";
import { AppLogo } from "@/components/ui/app-logo";

import { SettingsRow as Row, SettingsSection as Section } from "./settings-primitives";
import "./oauth-application-settings.css";

/**
 * An application's icon: its uploaded/linked image, else the brand mark of a well-known client (Claude, Cursor, …, matched
 * by name and redirect URIs), else the reference app's default icon.
 */
export function OAuthAppAvatar({ logoUrl, name, redirectUris, size = 32 }: { logoUrl?: string; name?: string; redirectUris?: string[]; size?: number }) {
  return <AppLogo className="oauth-app-avatar" logoUrl={logoUrl} name={name} redirectUris={redirectUris} size={size} />;
}

/** Settings › API › OAuth applications: the card on the API page (the reference app's list, or its empty row). */
export function OAuthApplicationsSection({ data }: { data: BootstrapData }) {
  const { t, formatDate } = useI18n();
  const navigate = useNavigate();
  const apps = data.oauthApplications;
  const createPath = applicationSettingsPath(data.workspace.urlKey, "new");
  return (
    <Section className="oauth-app-section" title={t("OAuth applications")} description={t("Manage your organization's OAuth applications.")}>
      {apps.length ? (
        <>
          <div className="oauth-app-list-header">
            <span>{apps.length === 1 ? t("1 OAuth application") : t("{count} OAuth applications").replace("{count}", String(apps.length))}</span>
            <FlowTooltip label={t("New OAuth application")}>
              <button type="button" className="oauth-app-icon-button is-medium" aria-label={t("New OAuth application")} onClick={() => navigate(createPath)}>
                <PlusGlyph />
              </button>
            </FlowTooltip>
          </div>
          <ul className="oauth-app-list">
            {apps.map((app) => (
              <li key={app.id}>
                <a
                  className="oauth-app-list-row"
                  href={applicationSettingsPath(data.workspace.urlKey, app.id)}
                  onClick={(event) => {
                    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
                    event.preventDefault();
                    navigate(applicationSettingsPath(data.workspace.urlKey, app.id));
                  }}
                >
                  <OAuthAppAvatar logoUrl={app.logoUrl} name={app.name} redirectUris={app.redirectUris} />
                  <span className="oauth-app-list-copy">
                    <strong data-i18n-ignore>{app.name}</strong>
                    <span>{t("Updated {date}").replace("{date}", formatDate(app.updatedAt || app.createdAt, { month: "short", day: "numeric", year: "numeric" }))}</span>
                  </span>
                </a>
              </li>
            ))}
          </ul>
        </>
      ) : (
        <Row title={t("No OAuth applications")} className="settings-row--action">
          <button type="button" className="settings-action" onClick={() => navigate(createPath)}>
            <PlusGlyph />
            {t("New OAuth application")}
          </button>
        </Row>
      )}
    </Section>
  );
}

function PlusGlyph() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
      <path d="M8.75 3a.75.75 0 0 0-1.5 0v4.25H3a.75.75 0 0 0 0 1.5h4.25V13a.75.75 0 0 0 1.5 0V8.75H13a.75.75 0 0 0 0-1.5H8.75V3Z" />
    </svg>
  );
}
