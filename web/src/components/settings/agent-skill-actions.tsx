/**
 * The row and page menu for a personal agent skill: Edit, Use in chat and Delete, as in the reference app's skill menus.
 * The same menu serves the skills list rows and the skill page's title row.
 */
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "@/components/ui/context-menu";
import type { ReactNode } from "react";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { useI18n } from "@/i18n/i18n";
import type { PersonalAgentSkill } from "@/types/flow";
import type { useAgentSkillActions } from "./use-agent-skill-actions";

type SkillActions = ReturnType<typeof useAgentSkillActions>;

function SkillMenuItems({ skill, actions, context = false, hideEdit, onDeleted }: {
  skill: PersonalAgentSkill;
  actions: SkillActions;
  context?: boolean;
  hideEdit?: boolean;
  onDeleted?: () => void;
}) {
  const { t } = useI18n();
  const Item = context ? ContextMenuItem : DropdownMenuItem;
  return <>
    {!hideEdit && <Item onSelect={() => actions.edit(skill)}>
      <FlowGlyph name="edit" size={16} aria-hidden="true" /><span>{t("Edit")}</span>
    </Item>}
    <Item onSelect={() => actions.useInChat(skill)}>
      <FlowGlyph name="agentPointer" size={16} aria-hidden="true" /><span>{t("Use in chat")}</span>
    </Item>
    <Item className="danger-item" onSelect={() => void actions.remove(skill, onDeleted)}>
      <FlowGlyph name="delete" size={16} aria-hidden="true" /><span>{t("Delete")}</span>
    </Item>
  </>;
}

export function AgentSkillContextMenu({ skill, actions, children }: { skill: PersonalAgentSkill; actions: SkillActions; children: ReactNode }) {
  const { t } = useI18n();
  return <ContextMenu>
    <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
    <ContextMenuContent data-i18n-ignore className="domain-label-row-menu agent-skill-menu" aria-label={t("Skill menu")}>
      <SkillMenuItems skill={skill} actions={actions} context />
    </ContextMenuContent>
  </ContextMenu>;
}

export function AgentSkillActionsMenu({
  skill,
  actions,
  className,
  hideEdit = false,
  onDeleted,
}: {
  skill: PersonalAgentSkill;
  actions: ReturnType<typeof useAgentSkillActions>;
  className?: string;
  /** The skill's own page is already the editor, so its menu leaves Edit out. */
  hideEdit?: boolean;
  onDeleted?: () => void;
}) {
  const { t } = useI18n();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" className={`agent-skill-menu-trigger${className ? ` ${className}` : ""}`} aria-label={t("Open menu")}>
          <FlowGlyph name="ellipsis" size={14} aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent data-i18n-ignore align="end" sideOffset={4} className="domain-label-row-menu agent-skill-menu" aria-label={t("Skill menu")}>
        <SkillMenuItems skill={skill} actions={actions} hideEdit={hideEdit} onDeleted={onDeleted} />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
