/**
 * Settings › AI & agents › Code Intelligence.
 * Honest gate: the toggle only works with code access; otherwise a call to action connects the integration first.
 */

import { ArrowRight } from "lucide-react";

import { useI18n } from "@/i18n/i18n";
import {
  DEFAULT_REPOSITORY_ACCESS,
  githubConnection,
  organizationHasCodeAccess,
  type RepositoryAccessSettings,
} from "@/lib/code-access";
import type { BootstrapData, WorkspaceSettings } from "@/types/flow";
import { FeatureCard, FeatureRow, FeatureSection, FeatureSelect, FeatureShell, Toggle } from "./feature-primitives";

type Props = {
  data: BootstrapData;
  settings: WorkspaceSettings;
  busy: boolean;
  setEnabled: (id: string, value: boolean) => void;
  onBack: () => void;
  onOpenGitHub: () => void;
  onGrantCodeAccess: () => void;
  repositoryAccess: RepositoryAccessSettings;
  onRepositoryAccessChange: (next: RepositoryAccessSettings) => void;
};

export function CodeIntelligencePage({
  data,
  settings,
  busy,
  setEnabled,
  onBack,
  onOpenGitHub,
  onGrantCodeAccess,
  repositoryAccess,
  onRepositoryAccessChange,
}: Props) {
  const { t } = useI18n();
  const hasIntegration = Boolean(githubConnection(data));
  const hasCodeAccess = organizationHasCodeAccess(data);
  const isEnabled = settings.featureFlags["code-intelligence"] ?? false;
  const policy = { ...DEFAULT_REPOSITORY_ACCESS, ...repositoryAccess };
  const policyDisabled = busy || !isEnabled || !hasCodeAccess;
  const scopeDisabled = policyDisabled || !policy.extendAccessToAllMembers;

  return (
    <FeatureShell
      className="feature-ai-settings feature-ai-compact"
      title="Code Intelligence"
      description="Allow Flow Agent to analyze and answer questions about your repositories"
      back={{ label: "AI & agents", onClick: onBack }}
    >
      <GitHubCodeAccessFeatureSetting
        hasIntegration={hasIntegration}
        hasCodeAccess={hasCodeAccess}
        isEnabled={isEnabled}
        busy={busy}
        onToggle={(value) => setEnabled("code-intelligence", value)}
        onEnableIntegration={onOpenGitHub}
        onEnableCodeAccess={onGrantCodeAccess}
      />
      <FeatureSection
        title="Repository access"
        disabled={policyDisabled}
        description={
          <>
            {t("By default, Code Intelligence only uses connected repos that a member can access on GitHub.")}
            <button type="button" className="feature-section-link" onClick={onOpenGitHub}>
              {t("Manage GitHub connections")}
              <ArrowRight size={12} />
            </button>
          </>
        }
      >
        <div className="feature-card-stack">
          <FeatureCard>
            <FeatureRow title="Extend access to all members" description="Let members use Code Intelligence for repos regardless of their GitHub access">
              <Toggle
                checked={policy.extendAccessToAllMembers}
                disabled={policyDisabled}
                label="Extend access to all members"
                onChange={(extendAccessToAllMembers) => onRepositoryAccessChange({ ...policy, extendAccessToAllMembers })}
              />
            </FeatureRow>
          </FeatureCard>
          <FeatureCard>
            <div className={scopeDisabled ? "feature-row-dim" : undefined}>
              <FeatureRow title="Accessible repositories" description="Which workspace-connected repos are available through extended access">
                <FeatureSelect
                  label="Accessible repositories"
                  value={policy.scope}
                  disabled={scopeDisabled}
                  options={[
                    { value: "all", label: "All repositories" },
                    { value: "selected", label: "Only select repositories" },
                  ]}
                  onChange={(scope) => onRepositoryAccessChange({ ...policy, scope: scope as RepositoryAccessSettings["scope"] })}
                />
              </FeatureRow>
              {policy.scope === "selected" && (
                <FeatureRow
                  title="Selected repositories"
                  description={
                    policy.allowedRepositories.length
                      ? t("{count} repositories selected").replace("{count}", String(policy.allowedRepositories.length))
                      : "Repository listing requires GitHub code access — Coming soon"
                  }
                />
              )}
            </div>
          </FeatureCard>
        </div>
      </FeatureSection>
    </FeatureShell>
  );
}

/** The enable row: a toggle with code access, otherwise the way to get it. */
function GitHubCodeAccessFeatureSetting({
  hasIntegration,
  hasCodeAccess,
  isEnabled,
  busy,
  onToggle,
  onEnableIntegration,
  onEnableCodeAccess,
}: {
  hasIntegration: boolean;
  hasCodeAccess: boolean;
  isEnabled: boolean;
  busy?: boolean;
  onToggle: (value: boolean) => void;
  onEnableIntegration: () => void;
  onEnableCodeAccess: () => void;
}) {
  const { t } = useI18n();

  if (hasCodeAccess) {
    return (
      <FeatureCard>
        <FeatureRow title="Enable Code Intelligence" description="When enabled, Flow Agent can read and understand your codebase">
          <Toggle checked={isEnabled} disabled={busy} label="Enable Code Intelligence" onChange={onToggle} />
        </FeatureRow>
      </FeatureCard>
    );
  }

  return (
    <FeatureCard>
      <div className="feature-row code-access-gate">
        <div>
          <strong>{t("Enable Code Intelligence")}</strong>
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
    </FeatureCard>
  );
}
