/** Edit, Use in chat and Delete for a personal agent skill, shared by the skills list rows and the skill page. */
import { useNavigate } from "react-router-dom";
import { writeAgentDraft } from "@/components/agent/agent-drafts";
import { useI18n } from "@/i18n/i18n";
import { deleteAgentSkill } from "@/lib/api";
import { agentPath, agentSkillPath } from "@/lib/app-routes";
import { agentSkillsSettingsBackLink } from "@/lib/agent-skills-settings-back-link";
import { toast } from "@/lib/toast";
import type { BootstrapData, PersonalAgentSkill } from "@/types/flow";
import { confirmTemplateAction } from "./template-confirm";

export function useAgentSkillActions(data: BootstrapData, onReload: () => Promise<void>) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const urlKey = data.workspace.urlKey;
  return {
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
