import { useMemo, useRef, useState } from "react";
import * as Popover from "@radix-ui/react-popover";
import { Check, Plus, Search } from "lucide-react";
import { newAgentSkillPath } from "@/lib/app-routes";
import type { BootstrapData } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { usePropertyCommand } from "@/components/property/use-property-command";
import { AgentChevronDownIcon, AgentSkillsIcon } from "./agent-icons";
import styles from "./agent-page.module.css";
import { usePhoneLayout } from "@/hooks/use-phone-layout";
import { PhoneMenu } from "@/components/ui/phone-menu";

/** The reference app's "Skills" pill in an agent composer: pick the viewer's skills for the next message, or create one. */
export function AgentSkillsPicker({
  className,
  data,
  selectedIds,
  onChange,
  onNavigate,
}: {
  className?: string;
  data: BootstrapData;
  selectedIds: string[];
  onChange: (ids: string[]) => void;
  onNavigate: (href: string) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const phone = usePhoneLayout();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const options = useMemo(
    // The reference app's skill list holds the skills only; "Create skill" is offered while there are none to pick.
    () => {
      const skills = (data.agentSkills ?? []).map((item) => ({ id: item.id, label: item.name }));
      return skills.length ? skills : [{ id: "__create__", label: t("Create skill") }];
    },
    [data.agentSkills, t],
  );
  const { activeId, choose, filteredOptions, inputRef, isSelected, onKeyDown, onQueryChange, query, setActiveId } = usePropertyCommand({
    closeOnSelect: false,
    open,
    options,
    selectedIds,
    onOpenChange: setOpen,
    onSelect: (option) => {
      if (option.id === "__create__") {
        onNavigate(newAgentSkillPath(data.workspace.urlKey));
        return;
      }
      onChange(selectedIds.includes(option.id) ? selectedIds.filter((id) => id !== option.id) : [...selectedIds, option.id]);
    },
  });
  const content = <>
          <div className={styles.skillsMenuHeader}>{t("Select skill")}</div>
          <div className={styles.menuSearch}>
            <Search />
            <input
              ref={inputRef}
              autoFocus
              aria-label={t("Search skills…")}
              placeholder={t("Search skills…")}
              value={query}
              onChange={(event) => onQueryChange(event.target.value)}
            />
          </div>
          <div role="listbox" data-phone-menu-options={phone ? '' : undefined}>
            {filteredOptions.map((option) => (
              <button
                className="menu-option"
                aria-selected={activeId === option.id}
                key={option.id}
                onMouseMove={() => setActiveId(option.id)}
                onClick={() => choose(option)}
                role="option"
                type="button"
              >
                {option.id === "__create__" ? <Plus /> : <AgentSkillsIcon />}
                <span data-i18n-ignore>{option.label}</span>
                {option.id !== "__create__" && isSelected(option.id) && <Check />}
              </button>
            ))}
          </div>
  </>;
  const trigger = <button ref={triggerRef} aria-expanded={open} aria-label={t("Skills")} data-agent-skills="" className={`${styles.skillsButton}${className ? ` ${className}` : ""}`} type="button" onClick={phone ? () => setOpen(value => !value) : undefined}>
    <AgentSkillsIcon /><span>{t("Skills")}</span><AgentChevronDownIcon />
  </button>;
  if (phone) return <>
    {trigger}
    <PhoneMenu open={open} onOpenChange={setOpen} label={t("Skills")} className={styles.skillsMenu} onCloseAutoFocus={event => { event.preventDefault(); triggerRef.current?.focus({ preventScroll: true }); }}><div onKeyDown={onKeyDown}>{content}</div></PhoneMenu>
  </>;
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild>{trigger}</Popover.Trigger>
    <Popover.Portal><Popover.Content data-flow-motion="floating" align="start" className={styles.skillsMenu} side="bottom" sideOffset={4} onKeyDown={onKeyDown}>{content}</Popover.Content></Popover.Portal>
  </Popover.Root>;
}
