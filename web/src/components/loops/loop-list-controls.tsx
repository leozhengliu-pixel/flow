import { useEffect, useState, type ReactNode } from "react";
import * as Popover from "@radix-ui/react-popover";
import { ArrowDownNarrowWide, ArrowDownWideNarrow, X } from "lucide-react";
import { DisplayIcon } from "@/components/ui/view-action-icons";
import { FlowTooltip } from "@/components/ui/tooltip";
import { SelectControl } from "@/components/ui/select-control";
import { Toggle } from "@/components/ui/toggle";
import { DirectoryFilterMenu, type DirectoryFilterGroup } from "@/components/workspace-directory/directory-menus";
import { teamDateChoices } from "@/components/workspace-directory/team-directory-model";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData } from "@/types/flow";
import {
  CURRENT_USER,
  LOOP_FILTER_FIELDS,
  LOOP_GROUPINGS,
  LOOP_ORDERINGS,
  NEVER_EXECUTED,
  RUNS_OPERATORS,
  defaultDescending,
  emptyLoopFilters,
  type LoopDisplaySettings,
  type LoopGrouping,
  type LoopListFilterField,
  type LoopListFilters,
  type LoopOrdering,
  type Translate,
} from "./loop-list-model";

function lastRunLabel(value: string, t: Translate) {
  if (value === NEVER_EXECUTED) return t("Never executed");
  const choice = teamDateChoices.find((item) => item.id === value);
  return choice ? t(choice.label) : value.replace("date:", "").replace("/", " – ");
}

/** Linear's filter bar under the toolbar: property · operator · value chips, the advanced "Match all/any" heading, + and Clear. */
export function LoopFilterBar({
  data,
  filters,
  groups,
  onChange,
  onChoice,
  onDate,
}: {
  data: BootstrapData;
  filters: LoopListFilters;
  groups: DirectoryFilterGroup[];
  onChange: (filters: LoopListFilters) => void;
  onChoice: (field: string, id: string, checked: boolean) => void;
  onDate: () => void;
}) {
  const { t } = useI18n();
  const selected = Object.fromEntries(LOOP_FILTER_FIELDS.map((field) => [field, new Set(filters[field])]));
  const advanced = () => onChange({ ...filters, advanced: true });
  const menu = (field?: LoopListFilterField, triggerNode?: ReactNode) => (
    <DirectoryFilterMenu groups={field ? groups.filter((group) => group.id === field) : groups} selected={selected} onChoice={onChoice} onAdvanced={advanced} trigger="add" triggerNode={triggerNode} showAdvanced={!field} />
  );
  const userName = (id: string) => (id === CURRENT_USER ? t("Current user") : (data.users.find((user) => user.id === id)?.displayName ?? t("Unknown user")));
  return (
    <div className={`workspace-filter-bar workspace-team-filter-bar loops-filter-bar${filters.advanced ? " workspace-team-advanced" : ""}`} role="group" aria-label={t("Loop filters")}>
      {filters.advanced && (
        <div className="workspace-team-advanced__heading">
          <span>{t("Match")}</span>
          <SelectControl label={t("Filter conjunction")} value={filters.conjunction} options={[{ value: "and", label: t("all filters") }, { value: "or", label: t("any filter") }]} onChange={(value) => onChange({ ...filters, conjunction: value === "or" ? "or" : "and" })} />
          <button type="button" aria-label={t("Remove advanced filter")} onClick={() => onChange({ ...filters, advanced: false, conjunction: "and" })}>
            <X size={14} />
          </button>
        </div>
      )}
      {LOOP_FILTER_FIELDS.map((field) => {
        const values = filters[field];
        const group = groups.find((item) => item.id === field);
        if (!values.length || !group) return null;
        const value = values[0];
        const operators =
          field === "runs"
            ? RUNS_OPERATORS.map((option) => ({ value: option.value, label: option.label }))
            : field === "lastRun" && value !== NEVER_EXECUTED
              ? [
                  { value: "is", label: t(value.includes("/") ? "between" : "after") },
                  { value: "isNot", label: t(value.includes("/") ? "not between" : "before") },
                ]
              : [
                  { value: "is", label: t("is") },
                  { value: "isNot", label: t("is not") },
                ];
        const label = field === "owner" ? values.map(userName).join(", ") : field === "lastRun" ? lastRunLabel(value, t) : value;
        return (
          <span className="workspace-filter-chip" key={field}>
            <strong>
              {group.icon}
              {group.label}
            </strong>
            <SelectControl
              label={t("{field} operator").replace("{field}", group.label)}
              value={filters.operators[field] ?? (field === "runs" ? "gte" : "is")}
              options={operators}
              onChange={(operator) => onChange({ ...filters, operators: { ...filters.operators, [field]: operator } })}
            />
            {menu(
              field,
              <button type="button" className="workspace-team-filter-value" aria-label={t("{field} values").replace("{field}", group.label)} data-i18n-ignore={field === "owner" || undefined}>
                {label}
              </button>,
            )}
            {field === "lastRun" && value.startsWith("date:") && (
              <button type="button" className="workspace-team-filter-value" onClick={onDate}>
                {t("Edit date")}
              </button>
            )}
            <button type="button" aria-label={t("Remove {field} filter").replace("{field}", group.label)} onClick={() => onChange({ ...filters, [field]: [] })}>
              <X />
            </button>
          </span>
        );
      })}
      {menu()}
      <button className="workspace-filter-bar__clear" type="button" onClick={() => onChange(emptyLoopFilters())}>
        {t("Clear")}
      </button>
    </div>
  );
}

function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLElement && (target.isContentEditable || Boolean(target.closest("input, textarea, select, [role='textbox'], [role='searchbox']")));
}

export function LoopDisplayMenu({ settings, onChange, hideTeamToggle = false }: { settings: LoopDisplaySettings; onChange: (settings: LoopDisplaySettings) => void; hideTeamToggle?: boolean }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  useEffect(() => {
    // Linear: ⇧V shows display options.
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || !event.shiftKey || event.altKey || event.metaKey || event.ctrlKey || event.key.toLowerCase() !== "v" || isEditableTarget(event.target)) return;
      event.preventDefault();
      setOpen((current) => !current);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);
  const update = (change: Partial<LoopDisplaySettings>) => onChange({ ...settings, ...change });
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <FlowTooltip disabled={open} label={t("Show display options")} shortcut="⇧ V">
        <Popover.Trigger asChild>
          <button type="button" className="loops-icon-button" aria-label={t("Display options")}>
            <DisplayIcon />
          </button>
        </Popover.Trigger>
      </FlowTooltip>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating" align="end" className="loops-display-menu" sideOffset={4} collisionPadding={8} aria-label={t("Display options")}>
          <div className="loops-display-row">
            <span>{t("Grouping")}</span>
            <SelectControl
              align="end"
              className="loops-display-select"
              label={t("Grouping")}
              value={settings.grouping}
              options={LOOP_GROUPINGS.map((item) => ({ value: item.id, label: t(item.label) }))}
              onChange={(grouping) => update({ grouping: grouping as LoopGrouping })}
            />
          </div>
          <div className="loops-display-row">
            <span>{t("Ordering")}</span>
            <button
              type="button"
              className="loops-display-direction"
              aria-label={t("Direction")}
              aria-pressed={settings.descending}
              title={t(settings.descending ? "Descending" : "Ascending")}
              onClick={() => update({ descending: !settings.descending })}
            >
              {settings.descending ? <ArrowDownWideNarrow /> : <ArrowDownNarrowWide />}
            </button>
            <SelectControl
              align="end"
              className="loops-display-select"
              label={t("Ordering")}
              value={settings.ordering}
              options={LOOP_ORDERINGS.map((item) => ({ value: item.id, label: t(item.label) }))}
              onChange={(ordering) => update({ ordering: ordering as LoopOrdering, descending: defaultDescending(ordering as LoopOrdering) })}
            />
          </div>
          <div className="loops-display-toggles">
            {!hideTeamToggle && (
              <label>
                <span>{t("Show team loops")}</span>
                <Toggle label={t("Show team loops")} checked={settings.showTeamLoops} onChange={(showTeamLoops) => update({ showTeamLoops })} />
              </label>
            )}
            <label>
              <span>{t("Show disabled loops")}</span>
              <Toggle label={t("Show disabled loops")} checked={settings.showDisabledLoops} onChange={(showDisabledLoops) => update({ showDisabledLoops })} />
            </label>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
