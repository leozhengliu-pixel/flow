/**
 * LS-0110 CodeIntelligenceSettingsPage + LS-0280 GitHubCodeAccessFeatureSetting
 * Honest gate: toggle only when hasCodeAccess; otherwise CTA to connect / enable code access.
 */
import { Sparkles } from "lucide-react";
import type { ReactNode } from "react";

import { useI18n } from "@/i18n/i18n";
import {
  githubConnection,
  organizationHasCodeAccess,
  type RepositoryAccessSettings,
  DEFAULT_REPOSITORY_ACCESS,
} from "@/lib/code-access";
import type { BootstrapData, WorkspaceSettings } from "@/types/flow";
import { SettingsToggle } from "./settings-primitives";

type Props = {
  data: BootstrapData;
  settings: WorkspaceSettings;
  busy: boolean;
  setEnabled: (id: string, value: boolean) => void;
  onOpenGitHub: () => void;
  onGrantCodeAccess: () => void;
  repositoryAccess: RepositoryAccessSettings;
  onRepositoryAccessChange: (next: RepositoryAccessSettings) => void;
};

export function CodeIntelligenceSettingsSection({
  data,
  settings,
  busy,
  setEnabled,
  onOpenGitHub,
  onGrantCodeAccess,
  repositoryAccess,
  onRepositoryAccessChange,
}: Props) {
  const { t } = useI18n();
  const hasIntegration = Boolean(githubConnection(data));
  const hasCodeAccess = organizationHasCodeAccess(data);
  const isEnabled = settings.featureFlags["code-intelligence"] ?? false;

  return (
    <section className="feature-section" aria-labelledby="code-intelligence-heading">
      <header>
        <h2 id="code-intelligence-heading">{t("Code Intelligence")}</h2>
        <p>{t("Allow Flow Agent to analyze and answer questions about your code")}</p>
      </header>
      <GitHubCodeAccessFeatureSetting
        hasIntegration={hasIntegration}
        hasCodeAccess={hasCodeAccess}
        isEnabled={isEnabled}
        busy={busy}
        enabledDescription="When enabled, Flow Agent can read and understand your codebase"
        onToggle={(value) => setEnabled("code-intelligence", value)}
        onEnableIntegration={onOpenGitHub}
        onEnableCodeAccess={onGrantCodeAccess}
      />
      <RepositoryAccessPolicySection
        featureName="Code Intelligence"
        disabled={busy || !isEnabled || !hasCodeAccess}
        value={repositoryAccess}
        onChange={onRepositoryAccessChange}
        onManageGitHub={onOpenGitHub}
      />
    </section>
  );
}

/** LS-0280 */
export function GitHubCodeAccessFeatureSetting({
  hasIntegration,
  hasCodeAccess,
  isEnabled,
  busy,
  enabledDescription,
  onToggle,
  onEnableIntegration,
  onEnableCodeAccess,
}: {
  hasIntegration: boolean;
  hasCodeAccess: boolean;
  isEnabled: boolean;
  busy?: boolean;
  enabledDescription: string;
  onToggle: (value: boolean) => void;
  onEnableIntegration: () => void;
  onEnableCodeAccess: () => void;
}) {
  const { t } = useI18n();

  if (hasCodeAccess) {
    return (
      <div className="feature-card">
        <div className="feature-row">
          <span className="feature-row-icon">
            <Sparkles size={18} />
          </span>
          <div>
            <strong>
              {t("Enable Code Intelligence")}
              <small>{t("Beta")}</small>
            </strong>
            <span>{t(enabledDescription)}</span>
          </div>
          <aside>
            <SettingsToggle
              checked={isEnabled}
              disabled={busy}
              label={t("Enable Code Intelligence")}
              onChange={onToggle}
            />
          </aside>
        </div>
      </div>
    );
  }

  return (
    <div className="feature-card">
      <div className="feature-row code-access-gate">
        <span className="feature-row-icon">
          <Sparkles size={18} />
        </span>
        <div>
          <strong>
            {t("Enable Code Intelligence")}
            <small>{t("Beta")}</small>
          </strong>
          <span>
            {t(
              hasIntegration
                ? "Needs code access via GitHub integration"
                : "Requires a GitHub integration with code access",
            )}
          </span>
        </div>
        <aside>
          <button
            type="button"
            className="feature-button primary"
            disabled={busy}
            onClick={hasIntegration ? onEnableCodeAccess : onEnableIntegration}
          >
            {t(hasIntegration ? "Enable code access" : "Enable integration")}
          </button>
        </aside>
      </div>
    </div>
  );
}

/** Repository access policy body for Code Intelligence (honest shell). */
export function RepositoryAccessPolicySection({
  featureName,
  disabled,
  value,
  onChange,
  onManageGitHub,
}: {
  featureName: string;
  disabled?: boolean;
  value: RepositoryAccessSettings;
  onChange: (next: RepositoryAccessSettings) => void;
  onManageGitHub: () => void;
}) {
  const { t } = useI18n();
  const policy = { ...DEFAULT_REPOSITORY_ACCESS, ...value };

  return (
    <div
      className={`feature-subsection${disabled ? " is-disabled" : ""}`}
      aria-disabled={disabled || undefined}
      id="repository-access"
    >
      <header>
        <h3>{t("Repository access")}</h3>
        <p>
          {t(
            `By default, ${featureName} only uses connected repos that a member has access to on GitHub`,
          ).replace(featureName, featureName)}
        </p>
      </header>
      <div className="feature-card">
        <PolicyRow
          title="Extend access to all members"
          description={`Let members use ${featureName} for repos regardless of their GitHub access`}
        >
          <SettingsToggle
            checked={policy.extendAccessToAllMembers}
            disabled={disabled}
            label={t("Extend access to all members")}
            onChange={(extendAccessToAllMembers) =>
              onChange({ ...policy, extendAccessToAllMembers })
            }
          />
        </PolicyRow>
        <PolicyRow
          title="Allow access from Automations"
          description={`Let Automations use ${featureName} for workspace-connected repos`}
        >
          <SettingsToggle
            checked={policy.allowAutomationAccess}
            disabled={disabled}
            label={t("Allow access from Automations")}
            onChange={(allowAutomationAccess) =>
              onChange({ ...policy, allowAutomationAccess })
            }
          />
        </PolicyRow>
        <PolicyRow
          title="Accessible repositories"
          description={
            policy.allowAutomationAccess
              ? "Which workspace-connected repos are available to Automations"
              : "Which workspace-connected repos are available through extended access"
          }
        >
          <div className="feature-repo-scope" role="group" aria-label={t("Accessible repositories")}>
            <label className="feature-radio">
              <input
                type="radio"
                name="repository-scope"
                checked={policy.scope === "all"}
                disabled={disabled}
                onChange={() => onChange({ ...policy, scope: "all" })}
              />
              <span>{t("All repositories")}</span>
            </label>
            <label className="feature-radio">
              <input
                type="radio"
                name="repository-scope"
                checked={policy.scope === "selected"}
                disabled={disabled}
                onChange={() => onChange({ ...policy, scope: "selected" })}
              />
              <span>{t("Only select repositories")}</span>
            </label>
          </div>
        </PolicyRow>
        {policy.scope === "selected" && (
          <div className="feature-row">
            <div>
              <strong>{t("Selected repositories")}</strong>
              <span>
                {policy.allowedRepositories.length
                  ? t(
                      `${policy.allowedRepositories.length} ${
                        policy.allowedRepositories.length === 1 ? "repository" : "repositories"
                      } selected`,
                    )
                  : t("No repositories selected")}
              </span>
              <span className="feature-state">
                {t("Repository listing requires GitHub code access — Coming soon")}
              </span>
            </div>
            <aside>
              <button type="button" className="feature-button" disabled>
                {t(policy.allowedRepositories.length ? "Change repositories" : "Add repositories")}
              </button>
            </aside>
          </div>
        )}
      </div>
      <p className="feature-inline-link">
        {t("Configure code access for repositories on a")}{" "}
        <button type="button" className="feature-text-link" onClick={onManageGitHub}>
          {t("GitHub integration")}
        </button>
        .{" "}
        <button type="button" className="feature-text-link" onClick={onManageGitHub}>
          {t("Manage GitHub connections")}
        </button>
      </p>
    </div>
  );
}

function PolicyRow({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <div className="feature-row">
      <div>
        <strong>{t(title)}</strong>
        <span>{t(description)}</span>
      </div>
      <aside>{children}</aside>
    </div>
  );
}
