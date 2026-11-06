import * as DropdownMenuPrimitive from "@radix-ui/react-dropdown-menu";
import { useMemo, useRef, type ReactElement } from "react";
import type { PersonalAgentSkill } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { usePhoneLayout } from "@/hooks/use-phone-layout";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { ScopedFlowTooltip as FlowTooltip } from "@/components/ui/tooltip";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { FlowDropdownMenuContent, FlowMenuItem, FlowMenuOptions, FlowSubmenu, type FlowMenuOption } from "@/components/ui/row-context-menu";
import { AgentAttachIcon, AgentSkillsIcon } from "./agent-icons";
import { SKILL_SEARCH_AFTER, orderedAgentSkills, skillMatches } from "./agent-skill-menu";
import "./agent-add-menu.css";

const CREATE_ID = "__create__";

/**
 * The agent composer's "+" button (the reference app's "Add files and more"): a menu with "File" and a "Skill" submenu
 * listing the viewer's skills, newest first, with a search field once there are more than three. With no skills the
 * submenu offers "Create skill". Phones get the same rows as the shared phone command sheet, drilling into the skills.
 */
export function AgentAddMenu({ className, disabled, skills, onFile, onSkill, onCreateSkill, onClosed }: {
  className?: string;
  disabled?: boolean;
  skills: readonly PersonalAgentSkill[] | undefined;
  /** Opens the file picker. */
  onFile?: () => void;
  /** A skill was picked: the composer puts it in the message. */
  onSkill: (skill: PersonalAgentSkill) => void;
  onCreateSkill: () => void;
  /** The menu closed after a choice; the composer takes focus back. */
  onClosed?: () => void;
}) {
  const { t } = useI18n();
  const phone = usePhoneLayout();
  const ordered = useMemo(() => orderedAgentSkills(skills), [skills]);
  const picked = useRef(false);
  const label = t("Add files and more");
  const choose = (run: () => void) => () => { picked.current = true; run(); };
  const pickSkill = (id: string) => {
    if (id === CREATE_ID) { picked.current = true; onCreateSkill(); return; }
    const skill = ordered.find((item) => item.id === id);
    if (skill) { picked.current = true; onSkill(skill); }
  };
  // After a choice focus goes to the composer; closing with Escape returns it to the "+" button.
  const closeAutoFocus = (event: Event) => {
    if (!picked.current) return;
    picked.current = false;
    event.preventDefault();
    onClosed?.();
  };
  // The tooltip wraps the menu trigger (not the other way round) so both reach the button.
  const withTooltip = (trigger: ReactElement) => <FlowTooltip label={label} sideOffset={8}>{trigger}</FlowTooltip>;
  const button = <button aria-label={label} className={className} data-agent-add="" disabled={disabled} type="button"><FlowGlyph name="plus" size={14} /></button>;
  const searchable = ordered.length > SKILL_SEARCH_AFTER;
  const options: FlowMenuOption[] = ordered.length
    ? ordered.map((skill) => ({ id: skill.id, label: skill.name, translate: false }))
    : [{ id: CREATE_ID, label: "Create skill", icon: <FlowGlyph name="plus" size={16} /> }];
  if (phone) return <DropdownMenu>
    {withTooltip(<DropdownMenuTrigger asChild>{button}</DropdownMenuTrigger>)}
    <DropdownMenuContent aria-label={label} onCloseAutoFocus={closeAutoFocus}>
      {onFile && <DropdownMenuItem onSelect={choose(onFile)}><AgentAttachIcon size={16} /><span>{t("File")}</span></DropdownMenuItem>}
      <DropdownMenuSub>
        <DropdownMenuSubTrigger phoneChevron={false}><AgentSkillsIcon size={16} /><span>{t("Skill")}</span></DropdownMenuSubTrigger>
        <DropdownMenuSubContent aria-label={t("Skill")} phonePlaceholder="Search skills…">
          {options.map((option) => <DropdownMenuItem key={option.id} textValue={option.label} onSelect={() => pickSkill(option.id)}>{option.icon}<span data-i18n-ignore={option.translate === false ? "" : undefined}>{option.translate === false ? option.label : t(option.label)}</span></DropdownMenuItem>)}
        </DropdownMenuSubContent>
      </DropdownMenuSub>
    </DropdownMenuContent>
  </DropdownMenu>;
  return <DropdownMenuPrimitive.Root>
    {withTooltip(<DropdownMenuPrimitive.Trigger asChild>{button}</DropdownMenuPrimitive.Trigger>)}
    <DropdownMenuPrimitive.Portal>
      <FlowDropdownMenuContent align="start" className="agent-add-menu" collisionPadding={11} label={label} loop onCloseAutoFocus={closeAutoFocus} side="top" sideOffset={4}>
        {onFile && <FlowMenuItem icon={<AgentAttachIcon size={16} />} label="File" onSelect={choose(onFile)} shortcut="⌘ ⇧ U" />}
        <FlowSubmenu alignOffset={searchable ? -43 : -6.5} className="agent-add-submenu" icon={<AgentSkillsIcon size={16} />} label="Skill" loop search={searchable} sideOffset={-1.5}>
          <FlowMenuOptions
            matches={(option, query) => skillMatches({ name: option.label }, query)}
            onChoose={pickSkill}
            options={options}
            placeholder={searchable ? "Search skills…" : undefined}
            selected={NONE}
          />
        </FlowSubmenu>
      </FlowDropdownMenuContent>
    </DropdownMenuPrimitive.Portal>
  </DropdownMenuPrimitive.Root>;
}

const NONE: ReadonlySet<string> = new Set();
