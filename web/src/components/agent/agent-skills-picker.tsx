import { useMemo, useState } from "react";
import * as Popover from "@radix-ui/react-popover";
import { Check, Plus, Search } from "lucide-react";
import { newAgentSkillPath } from "@/lib/app-routes";
import type { BootstrapData } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { usePropertyCommand } from "@/components/property/use-property-command";
import { AgentChevronDownIcon, AgentSkillsIcon } from "./agent-icons";
import styles from "./agent-page.module.css";

/** Linear's "Skills" pill in an agent composer: pick the viewer's skills for the next message, or create one. */
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
  const options = useMemo(
    () => [...(data.agentSkills ?? []).map((item) => ({ id: item.id, label: item.name })), { id: "__create__", label: t("Create skill") }],
    [data.agentSkills, t],
  );
  const command = usePropertyCommand({
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
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button aria-expanded={open} aria-label={t("Skills")} className={`${styles.skillsButton}${className ? ` ${className}` : ""}`} type="button">
          <AgentSkillsIcon />
          <span>{t("Skills")}</span>
          <AgentChevronDownIcon />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating" align="start" className={styles.skillsMenu} side="bottom" sideOffset={4} onKeyDown={command.onKeyDown}>
          <div className={styles.menuSearch}>
            <Search />
            <input
              ref={command.inputRef}
              autoFocus
              aria-label={t("Search skills…")}
              placeholder={t("Search skills…")}
              value={command.query}
              onChange={(event) => command.onQueryChange(event.target.value)}
            />
          </div>
          <div role="listbox">
            {command.filteredOptions.map((option) => (
              <button
                aria-selected={command.activeId === option.id}
                key={option.id}
                onMouseMove={() => command.setActiveId(option.id)}
                onClick={() => command.choose(option)}
                role="option"
                type="button"
              >
                {option.id === "__create__" ? <Plus /> : <AgentSkillsIcon />}
                <span data-i18n-ignore>{option.label}</span>
                {option.id !== "__create__" && command.isSelected(option.id) && <Check />}
              </button>
            ))}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
