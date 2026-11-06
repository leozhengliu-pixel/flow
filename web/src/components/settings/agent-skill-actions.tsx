/**
 * The row and page menu for a personal agent skill: Edit, Move to team, Use in chat and Delete, as in the reference
 * app's skill menus. The same menu serves the skills list rows (right-click and the "…" button) and the skill page's
 * title row.
 */
import * as ContextMenuPrimitive from "@radix-ui/react-context-menu";
import { ChevronRight } from "lucide-react";
import { SubmenuChevron } from "@/components/ui/submenu-chevron";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu";
import type { ReactNode } from "react";
import { TeamIcon } from "@/components/issue/issue-icons";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { useI18n } from "@/i18n/i18n";
import type { PersonalAgentSkill } from "@/types/flow";
import type { useAgentSkillActions } from "./use-agent-skill-actions";

type SkillActions = ReturnType<typeof useAgentSkillActions>;
type MoveTarget = SkillActions["moveTargets"][number];

/** The people-with-arrow glyph of the reference app's "Move to team" row. */
function MoveToTeamGlyph() {
  return <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="m11.6 9 .072.005.016.001a.6.6 0 0 1 .313.148l2.887 2.592a.4.4 0 0 1 0 .595l-2.887 2.593A.6.6 0 0 1 11 14.487v-1.114C6.333 12.77 4 12.313 4 12.002c0-.311 2.333-.768 7-1.371V9.6a.6.6 0 0 1 .6-.6ZM6.782 7.645l.914.562a2.702 2.702 0 0 1 1.2 1.668c-2.96.437-4.76.785-5.084 1.044-.476.207-.765.419-.807.944L3 12h-.5A1.5 1.5 0 0 1 1 10.5c0-.87.42-1.684 1.119-2.19l.874-.551a3.768 3.768 0 0 1 3.789-.114Zm6 0 .914.562A2.702 2.702 0 0 1 14.969 9.5c0 .285-.08.552-.218.78l-2.082-1.87a1.6 1.6 0 0 0-2.662 1.044L10 9.719l-.031.004A3 3 0 0 0 8.76 7.724l-.605-.439.838-.526a3.768 3.768 0 0 1 3.789-.114ZM4.969 2a2 2 0 1 1 0 4 2 2 0 0 1 0-4Zm6-1a2 2 0 1 1 0 4 2 2 0 0 1 0-4Z" /></svg>;
}

/** A team of the "Move to team" submenu: its icon, the path from its parent teams, and its key. */
function TeamRow({ target }: { target: MoveTarget }) {
  return <>
    <span className="agent-skill-team-icon" aria-hidden="true"><TeamIcon team={target.team} size={14} /></span>
    <span className="issue-team-path" data-i18n-ignore>
      {target.ancestors.map((parent) => <span className="issue-team-parent" key={parent.id}><span>{parent.name}</span><ChevronRight viewBox="8 5 8 14" aria-hidden="true" /></span>)}
      <span className="issue-team-name">{target.team.name}</span>
      <span className="issue-team-key">{target.team.key}</span>
    </span>
  </>;
}

function SkillMenuItems({ skill, actions, context = false, hideEdit, onDeleted }: {
  skill: PersonalAgentSkill;
  actions: SkillActions;
  context?: boolean;
  hideEdit?: boolean;
  onDeleted?: () => void;
}) {
  const { t } = useI18n();
  const Item = context ? ContextMenuItem : DropdownMenuItem;
  const Separator = context ? ContextMenuSeparator : DropdownMenuSeparator;
  const joined = actions.moveTargets.filter((target) => target.joined);
  const others = actions.moveTargets.filter((target) => !target.joined);
  const teamItems = (targets: MoveTarget[]) => targets.map((target) => <Item key={target.team.id} onSelect={() => void actions.moveToTeam(skill, target.team, onDeleted)}><TeamRow target={target} /></Item>);
  const teams = <>{teamItems(joined)}{joined.length > 0 && others.length > 0 && <Separator />}{teamItems(others)}</>;
  const moveLabel = t("Move to team");
  const move = actions.moveTargets.length === 0 ? null : context
    ? <ContextMenuPrimitive.Sub>
      <ContextMenuPrimitive.SubTrigger className="menu-item"><MoveToTeamGlyph /><span>{moveLabel}</span><SubmenuChevron /></ContextMenuPrimitive.SubTrigger>
      <ContextMenuPrimitive.Portal>
        <ContextMenuPrimitive.SubContent data-flow-motion="floating" data-i18n-ignore className="menu-content agent-skill-menu agent-skill-submenu" aria-label={moveLabel} sideOffset={-2} alignOffset={-6.5}>{teams}</ContextMenuPrimitive.SubContent>
      </ContextMenuPrimitive.Portal>
    </ContextMenuPrimitive.Sub>
    : <DropdownMenuSub>
      <DropdownMenuSubTrigger className="agent-skill-sub-trigger"><MoveToTeamGlyph /><span>{moveLabel}</span></DropdownMenuSubTrigger>
      <DropdownMenuSubContent data-i18n-ignore className="agent-skill-menu agent-skill-submenu" aria-label={moveLabel} sideOffset={-2} alignOffset={-6.5}>{teams}</DropdownMenuSubContent>
    </DropdownMenuSub>;
  return <>
    {!hideEdit && <Item onSelect={() => actions.edit(skill)}>
      <FlowGlyph name="edit" size={16} aria-hidden="true" /><span>{t("Edit")}</span>
    </Item>}
    {move}
    {(!hideEdit || move) && <Separator />}
    <Item onSelect={() => actions.useInChat(skill)}>
      <FlowGlyph name="agentPointer" size={16} aria-hidden="true" /><span>{t("Use in chat")}</span>
    </Item>
    <Separator />
    <Item onSelect={() => void actions.remove(skill, onDeleted)}>
      <FlowGlyph name="delete" size={16} aria-hidden="true" /><span>{t("Delete")}</span>
    </Item>
  </>;
}

export function AgentSkillContextMenu({ skill, actions, children }: { skill: PersonalAgentSkill; actions: SkillActions; children: ReactNode }) {
  const { t } = useI18n();
  return <ContextMenu>
    <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
    <ContextMenuContent data-i18n-ignore className="agent-skill-menu" aria-label={t("Skill menu")} sideOffset={4} alignOffset={-0.5}>
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
      <DropdownMenuContent data-i18n-ignore align="end" sideOffset={4} className="agent-skill-menu" aria-label={t("Skill menu")}>
        <SkillMenuItems skill={skill} actions={actions} hideEdit={hideEdit} onDeleted={onDeleted} />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
