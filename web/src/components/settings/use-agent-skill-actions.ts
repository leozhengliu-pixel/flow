/** Edit, Use in chat and Delete for a personal agent skill, shared by the skills list rows and the skill page. */
import { useNavigate } from "react-router-dom";
import { writeAgentDraft } from "@/components/agent/agent-drafts";
import { useI18n } from "@/i18n/i18n";
import { deleteAgentSkill, updateStructuredTeamSettings } from "@/lib/api";
import { issueMoveTeams } from "@/lib/issue-team-move";
import type { Team } from "@/types/flow";
import { agentPath, agentSkillPath } from "@/lib/app-routes";
import { agentSkillsSettingsBackLink } from "@/lib/agent-skills-settings-back-link";
import { toast } from "@/lib/toast";
import type { BootstrapData, PersonalAgentSkill } from "@/types/flow";
import { confirmTemplateAction } from "./template-confirm";

export function useAgentSkillActions(data: BootstrapData, onReload: () => Promise<void>) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const urlKey = data.workspace.urlKey;
  const memberOf = new Set((data.teamMembers ?? []).filter((member) => member.userId === data.viewer.id).map((member) => member.teamId));
  return {
    /** The teams a skill can move to: the viewer's own teams first, then the rest, each with its parent teams. */
    moveTargets: issueMoveTeams(data)
      .map((target) => ({ ...target, joined: memberOf.has(target.team.id) }))
      .sort((a, b) => Number(b.joined) - Number(a.joined)),
    /** Shares the skill with a team (an enabled team skill with the same instructions) and removes the personal copy. */
    moveToTeam: async (skill: PersonalAgentSkill, team: Team, afterMove?: () => void) => {
      try {
        const existing = data.teamSettings?.[team.id]?.agentSkills ?? [];
        await updateStructuredTeamSettings(team.id, {
          agentSkills: [...existing, { id: `skill_${Date.now()}`, name: skill.name, instructions: skill.instructions, enabled: true }],
        });
        await deleteAgentSkill(skill.id);
        toast.success(t("Moved to {name}").replace("{name}", () => team.name));
        afterMove?.();
        await onReload();
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("Could not move skill"));
      }
    },
    edit: (skill: PersonalAgentSkill) => navigate(agentSkillPath(urlKey, skill.id)),
    /** Opens a new agent chat with the skill selected in its composer. */
    useInChat: (skill: PersonalAgentSkill) => {
      writeAgentDraft(`flow:agent-draft:${data.workspace.id}`, { input: "", skillIds: [skill.id] });
      navigate(agentPath(urlKey));
    },
    /** Confirms, deletes and reloads; `afterDelete` runs once it is gone (the skill page returns to the list). */
    remove: async (skill: PersonalAgentSkill, afterDelete?: () => void) => {
      const confirmed = await confirmTemplateAction({
        title: t('Delete the skill "{name}"?').replace("{name}", () => skill.name),
        description: t("This action cannot be undone."),
        confirmLabel: t("Delete"),
        cancelLabel: t("Cancel"),
      });
      if (!confirmed) return;
      try {
        await deleteAgentSkill(skill.id);
        afterDelete?.();
        await onReload();
      } catch (error) {
        toast.error(error instanceof Error ? error.message : t("Could not delete skill"));
      }
    },
    backHref: agentSkillsSettingsBackLink(urlKey).href,
  };
}
