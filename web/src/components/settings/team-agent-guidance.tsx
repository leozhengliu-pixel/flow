import { useEffect, useState } from "react";
import { ChevronRight } from "lucide-react";
import { updateStructuredTeamSettings } from "@/lib/api";
import { canManageTeamSettings } from "@/lib/settings-permissions";
import { toast } from "@/lib/toast";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Team } from "@/types/flow";
import { inheritedTeamAgentGuidance, TEAM_AGENT_GUIDANCE_LIMIT } from "./team-agent-guidance-model";
import { SettingsSection } from "./settings-primitives";
import "./team-agent-guidance.css";

/** Team settings › Team agents › Additional guidance (the reference app's team-level agent guidance). */
export function TeamAgentGuidanceSettings({ data, team, onReload }: { data: BootstrapData; team: Team; onReload: () => Promise<void> }) {
  const { formatNumber, t } = useI18n();
  const saved = data.teamSettings?.[team.id]?.agentGuidance ?? "";
  const [draft, setDraft] = useState(saved);
  const [saving, setSaving] = useState(false);
  useEffect(() => setDraft(saved), [saved, team.id]);
  const canEdit = canManageTeamSettings(data, team.id, "agents");
  const inherited = inheritedTeamAgentGuidance(data, team.id);
  const dirty = draft.trim() !== saved.trim();
  const save = async () => {
    setSaving(true);
    try {
      await updateStructuredTeamSettings(team.id, { agentGuidance: draft.trim() });
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not save team settings"));
    } finally {
      setSaving(false);
    }
  };
  return (
    <SettingsSection
      title={t("Additional guidance")}
      description={t("Instructions for Flow Agent and installed agents working on this team’s issues and projects, such as preferred repositories, conventions or review steps.")}
      headerClassName="team-settings-section-title"
    >
      <div className="team-agent-guidance">
        <p className="team-agent-guidance-note">
          {t("This team inherits workspace guidance and parent team guidance. Its own guidance is applied last and wins when they conflict.")}
        </p>
        <textarea
          aria-label={t("Team agent guidance")}
          placeholder={t("e.g. Open pull requests against the web repository and link them to the issue.")}
          maxLength={TEAM_AGENT_GUIDANCE_LIMIT}
          disabled={!canEdit || saving}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
        />
        <footer>
          <span>
            {formatNumber(draft.length)} / {formatNumber(TEAM_AGENT_GUIDANCE_LIMIT)}
          </span>
          {canEdit && dirty && (
            <button type="button" className="settings-action" disabled={saving} onClick={() => setDraft(saved)}>
              {t("Cancel")}
            </button>
          )}
          <button type="button" className="settings-action primary" disabled={!canEdit || saving || !dirty} onClick={() => void save()}>
            {t("Save")}
          </button>
        </footer>
        {!canEdit && <p className="team-agent-guidance-note">{t("Only admins and team owners can modify the team’s agent guidance prompts")}</p>}
        {inherited.length > 0 && (
          <details className="team-agent-guidance-inherited">
            <summary>
              <ChevronRight size={12} aria-hidden />
              {t("Inherited guidance")} <span>{formatNumber(inherited.length)}</span>
            </summary>
            {inherited.map((item) => (
              <section key={item.key}>
                <h4>
                  {t(item.source)}
                  {item.teamName && <span data-i18n-ignore> · {item.teamName}</span>}
                </h4>
                <p data-i18n-ignore>{item.body}</p>
              </section>
            ))}
          </details>
        )}
      </div>
    </SettingsSection>
  );
}
