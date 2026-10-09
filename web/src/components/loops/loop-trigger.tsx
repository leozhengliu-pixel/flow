import { Fragment, useMemo, useState, type ReactNode } from "react";
import { CalendarClock, ChevronDown, CircleDot, FileText, MessageSquare, Plus, Tag, UserRound, UsersRound, X } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { DateTimeControl } from "@/components/ui/date-time-control";
import { SelectControl } from "@/components/ui/select-control";
import { UserAvatar } from "@/components/ui/user-avatar";
import { NoAssigneeIcon, PriorityIcon, ProjectIcon, StatusIcon, TeamIcon } from "@/components/issue/issue-icons";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, LoopTriggerType, WorkflowState } from "@/types/flow";
import {
  ENTITY_EVENTS,
  ENTITY_NAMES,
  PRIORITY_NAMES,
  WEEKDAYS,
  configString,
  configStrings,
  defaultScheduleConfig,
  eventValue,
  formatScheduleDate,
  loopEvent,
  loopFilters,
  scheduleStart,
  scheduleTimeOptions,
  type LoopFilter,
  type LoopFilterField,
  type LoopWeekday,
} from "./loop-model";

type Config = Record<string, unknown>;
type TriggerData = Pick<BootstrapData, "teams" | "states" | "users" | "projects" | "labels">;
export type TriggerChange = (triggerType: LoopTriggerType, config: Config) => void;

const NONE = "__none__";
const ANY = "any";

/** Issue property events with a "Set to…" submenu, in Linear's order. */
const PROPERTY_EVENTS = [
  { id: "status", label: "Status", any: "Any status", subject: "status" },
  { id: "priority", label: "Priority", any: "Any priority", subject: "priority" },
  { id: "assignee", label: "Assignee", any: "Anyone", subject: "assignee" },
  { id: "agent", label: "Agent", any: "Any agent", subject: "agent" },
  { id: "project", label: "Project", any: "Any project", subject: "project" },
  { id: "team", label: "Team", any: "Any team", subject: "team" },
  { id: "labels", label: "Labels", any: "Any label", subject: "label" },
] as const;

type ValueOption = { id: string; label: string; icon?: ReactNode; entity?: boolean };

const STATE_TYPE_ORDER: WorkflowState["type"][] = ["backlog", "unstarted", "started", "completed", "canceled"];

/** Distinct state names across teams (Backlog, Todo, In Progress, Done, Canceled…), in workflow order. */
function stateOptions(data: TriggerData): ValueOption[] {
  const seen = new Set<string>();
  const states = [...data.states]
    .sort((a, b) => STATE_TYPE_ORDER.indexOf(a.type) - STATE_TYPE_ORDER.indexOf(b.type) || a.position - b.position)
    .filter((state) => {
      const key = state.name.toLowerCase();
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  const options: ValueOption[] = [
    { id: "triage", label: "Triage", icon: <TriageGlyph /> },
    ...states.map((state) => ({ id: state.name, label: state.name, icon: <StatusIcon state={state} size={14} />, entity: true })),
  ];
  if (!seen.has("duplicate"))
    options.push({ id: "duplicate", label: "Duplicate", icon: <StatusIcon state={{ id: "duplicate", name: "Duplicate", color: "var(--theme-text-tertiary)", type: "canceled" }} size={14} /> });
  return options;
}

function valueOptions(data: TriggerData, field: string): ValueOption[] {
  switch (field) {
    case "status":
      return stateOptions(data);
    case "priority":
      return PRIORITY_NAMES.map((label, index) => ({ id: String(index), label, icon: <PriorityIcon priority={index} size={14} /> }));
    case "assignee":
    case "creator":
      return [
        ...(field === "assignee" ? [{ id: NONE, label: "No assignee", icon: <NoAssigneeIcon size={14} /> }] : []),
        ...data.users.filter((user) => user.active !== false && !user.app).map((user) => ({ id: user.id, label: user.displayName || user.name, icon: <UserAvatar avatarUrl={user.avatarUrl} className="avatar loops-avatar" name={user.displayName || user.name} />, entity: true })),
      ];
    case "agent":
      return [
        { id: NONE, label: "No agent", icon: <NoAssigneeIcon size={14} /> },
        ...data.users.filter((user) => user.app || user.builtinAgent).map((user) => ({ id: user.id, label: user.displayName || user.name, icon: <UserAvatar avatarUrl={user.avatarUrl} className="avatar loops-avatar" name={user.displayName || user.name} />, entity: true })),
      ];
    case "project":
      return [
        { id: NONE, label: "No project", icon: <ProjectIcon size={14} /> },
        ...data.projects.map((project) => ({ id: project.id, label: project.name, icon: <ProjectIcon size={14} />, entity: true })),
      ];
    case "team":
      return data.teams.map((team) => ({ id: team.id, label: team.name, icon: <TeamIcon team={team} size={14} />, entity: true }));
    case "labels":
    case "label": {
      const seen = new Set<string>();
      return data.labels
        .filter((label) => {
          const key = label.name.toLowerCase();
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        })
        .map((label) => ({ id: label.id, label: label.name, icon: <span className="loops-label-dot" style={{ background: label.color }} />, entity: true }));
    }
    default:
      return [];
  }
}

function valueLabel(data: TriggerData, field: string, value: string | null | undefined, anyLabel: string) {
  if (value === undefined) return { label: anyLabel, entity: false, icon: undefined as ReactNode };
  const id = value === null ? NONE : value;
  const option = valueOptions(data, field).find((item) => item.id === id || (field === "status" && item.id.toLowerCase() === id.toLowerCase()));
  return { label: option?.label ?? id, entity: Boolean(option?.entity), icon: option?.icon };
}

const FILTER_FIELDS: { id: LoopFilterField; label: string }[] = [
  { id: "status", label: "Status" },
  { id: "priority", label: "Priority" },
  { id: "assignee", label: "Assignee" },
  { id: "creator", label: "Creator" },
  { id: "label", label: "Label" },
  { id: "project", label: "Project" },
  { id: "team", label: "Team" },
];

/** Trigger card body: Linear's single trigger dropdown ("An issue is in triage ▾"), then the team scope, schedule row or issue filters. `readOnly` renders the loop page summary. */
export function LoopTriggerEditor({
  data,
  triggerType,
  config,
  onChange,
  readOnly = false,
  level = "workspace",
}: {
  data: TriggerData;
  triggerType: LoopTriggerType;
  config: Config;
  onChange?: TriggerChange;
  readOnly?: boolean;
  /** Team loops already belong to one team, so the sentence has no "in [teams]" scope (Linear: "An issue is in triage"). */
  level?: "workspace" | "team";
}) {
  const { t } = useI18n();
  const change: TriggerChange = (type, next) => onChange?.(type, next);
  const set = (patch: Config) => change(triggerType, { ...config, ...patch });
  if (triggerType === "schedule")
    return (
      <div className={`loops-trigger-sentence${readOnly ? " is-readonly" : ""}`} data-trigger="schedule">
        <TriggerMenu data={data} triggerType={triggerType} config={config} onChange={change} readOnly={readOnly} />
        <ScheduleSentence config={config} onChange={set} readOnly={readOnly} />
      </div>
    );
  const teamScoped = level === "team" || triggerType === "team";
  return (
    <div className="loops-trigger-event">
      <div className={`loops-trigger-sentence${readOnly ? " is-readonly" : ""}`} data-trigger={triggerType}>
        <TriggerMenu data={data} triggerType={triggerType} config={config} onChange={change} readOnly={readOnly} />
        {!teamScoped && (
          <>
            <span className="loops-sentence-text">{t("in")}</span>
            <TeamScope data={data} config={config} onChange={set} readOnly={readOnly} />
          </>
        )}
      </div>
      {triggerType === "issue" && <IssueFilters data={data} config={config} onChange={set} readOnly={readOnly} />}
    </div>
  );
}

/** Issue property events: "changes" when any value matches, "is set to {value}" for one value. */
const PROPERTY_SENTENCES: Record<(typeof PROPERTY_EVENTS)[number]["id"], [string, string]> = {
  status: ["An issue's status changes", "An issue's status is set to {value}"],
  priority: ["An issue's priority changes", "An issue's priority is set to {value}"],
  assignee: ["An issue's assignee changes", "An issue's assignee is set to {value}"],
  agent: ["An issue's agent changes", "An issue's agent is set to {value}"],
  project: ["An issue's project changes", "An issue's project is set to {value}"],
  team: ["An issue's team changes", "An issue's team is set to {value}"],
  labels: ["A label is added to an issue", "Label {value} is added to an issue"],
};

const ISSUE_SENTENCES: Record<string, string> = {
  created: "An issue is created",
  updated: "An issue is updated",
  triage: "An issue is in triage",
  comment: "An issue gets a new comment",
  customerRequest: "An issue gets a new customer request",
};

const ENTITY_SENTENCES: Record<Exclude<LoopTriggerType, "schedule" | "issue">, Record<string, string>> = {
  project: { created: "A project is created", updated: "A project is updated", status: "A project's status changes", update: "A project gets a new update" },
  initiative: { created: "An initiative is created", updated: "An initiative is updated", status: "An initiative's status changes", update: "An initiative gets a new update" },
  release: { created: "A release is created", updated: "A release is updated", status: "A release's status changes" },
  team: { created: "A team is created", updated: "A team is updated" },
  cycle: { created: "A cycle is created", updated: "A cycle is updated", started: "A cycle starts", completed: "A cycle is completed" },
};

/** The trigger as one sentence ("An issue is in triage", "An issue's status is set to Done"); entity names stay untranslated. */
function TriggerSentence({ data, triggerType, config }: { data: TriggerData; triggerType: LoopTriggerType; config: Config }) {
  const { t } = useI18n();
  if (triggerType === "schedule") return <>{t("Schedule")}</>;
  const event = loopEvent(config);
  if (triggerType !== "issue") {
    const sentence = ENTITY_SENTENCES[triggerType]?.[event];
    return <>{sentence ? t(sentence) : `${t(ENTITY_NAMES[triggerType])} · ${t(ENTITY_EVENTS[triggerType]?.find((item) => item.id === event)?.label ?? event)}`}</>;
  }
  const property = PROPERTY_EVENTS.find((item) => item.id === event);
  if (!property) return <>{t(ISSUE_SENTENCES[event] ?? "An issue changes")}</>;
  const value = eventValue(config);
  const [anyValue, oneValue] = PROPERTY_SENTENCES[property.id];
  if (value === undefined || value === ANY) return <>{t(anyValue)}</>;
  const current = valueLabel(data, property.id, value, property.any);
  const [before, after = ""] = t(oneValue).split("{value}");
  return (
    <>
      {before}
      <span className="loops-sentence-value" data-i18n-ignore={current.entity || undefined}>
        {current.entity ? current.label : t(current.label)}
      </span>
      {after}
    </>
  );
}

/** The trigger type's glyph (also the loops list's Trigger group headers). */
export function TriggerIcon({ triggerType, event = "" }: { triggerType: LoopTriggerType; event?: string }) {
  if (triggerType === "schedule") return <CalendarClock size={14} />;
  if (triggerType === "issue") return event === "triage" ? <TriageGlyph /> : <CircleDot size={14} />;
  if (triggerType === "project") return <ProjectIcon size={14} />;
  if (triggerType === "team") return <UsersRound size={14} />;
  return <FileText size={14} />;
}

/** Linear's trigger picker: one dropdown labelled with the whole trigger; Issue ▸ Status ▸ Done… picks event and value together. */
function TriggerMenu({ data, triggerType, config, onChange, readOnly }: { data: TriggerData; triggerType: LoopTriggerType; config: Config; onChange: TriggerChange; readOnly: boolean }) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const event = loopEvent(config);
  // Linear's loop page reads the trigger as plain text: "[icon] An issue is in triage".
  if (readOnly)
    return (
      <span className="loops-sentence-subject">
        <TriggerIcon triggerType={triggerType} event={event} />
        <span>
          <TriggerSentence data={data} triggerType={triggerType} config={config} />
        </span>
      </span>
    );
  const keep = triggerType === "issue" ? { teamIds: config.teamIds, filters: config.filters } : { teamIds: config.teamIds };
  const chooseIssue = (nextEvent: string, value?: string | null) =>
    onChange("issue", { ...keep, event: nextEvent, ...(value === undefined ? {} : { value }) });
  const matches = (label: string) => !query.trim() || t(label).toLowerCase().includes(query.trim().toLowerCase()) || label.toLowerCase().includes(query.trim().toLowerCase());
  const entityTypes = triggerType === "cycle" ? (["project", "initiative", "release", "team", "cycle"] as const) : (["project", "initiative", "release", "team"] as const);
  return (
    <DropdownMenu onOpenChange={(open) => !open && setQuery("")}>
      <DropdownMenuTrigger className="loops-trigger-button" aria-label={t("Trigger")}>
        <span className="loops-trigger-button-label">
          <TriggerSentence data={data} triggerType={triggerType} config={config} />
        </span>
        <ChevronDown size={14} />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="loops-menu loops-trigger-menu">
        <DropdownMenuItem onSelect={() => onChange("schedule", triggerType === "schedule" ? config : defaultScheduleConfig())}>
          <CalendarClock size={14} />
          {t("Schedule")}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <CircleDot size={14} />
            {t("Issue")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="loops-menu loops-issue-events">
            <div className="loops-menu-filter">
              <input
                aria-label={t("Filter…")}
                placeholder={t("Filter…")}
                value={query}
                onChange={(item) => setQuery(item.target.value)}
                onKeyDown={(item) => {
                  if (item.key !== "Escape" && item.key !== "ArrowDown" && item.key !== "ArrowUp") item.stopPropagation();
                }}
              />
            </div>
            {matches("Created") && <DropdownMenuItem onSelect={() => chooseIssue("created")}>{t("Created")}</DropdownMenuItem>}
            {matches("Is in triage") && <DropdownMenuItem onSelect={() => chooseIssue("triage")}>{t("Is in triage")}</DropdownMenuItem>}
            {matches("Property updated") && <DropdownMenuItem onSelect={() => chooseIssue("updated")}>{t("Property updated")}</DropdownMenuItem>}
            {!query.trim() && <DropdownMenuSeparator />}
            {PROPERTY_EVENTS.filter((item) => matches(item.label)).map((item) => (
              <DropdownMenuSub key={item.id}>
                <DropdownMenuSubTrigger>{t(item.label)}</DropdownMenuSubTrigger>
                <DropdownMenuSubContent className="loops-menu loops-value-menu">
                  <DropdownMenuLabel>{t(item.id === "labels" ? "Added…" : "Set to…")}</DropdownMenuLabel>
                  <DropdownMenuItem onSelect={() => chooseIssue(item.id, ANY)}>{t(item.any)}</DropdownMenuItem>
                  {valueOptions(data, item.id).map((option) => (
                    <DropdownMenuItem
                      key={option.id}
                      onSelect={() =>
                        item.id === "status" && option.id === "triage"
                          ? chooseIssue("triage")
                          : chooseIssue(item.id, option.id === NONE ? null : option.id)
                      }
                    >
                      {option.icon}
                      <span data-i18n-ignore={option.entity || undefined}>{option.entity ? option.label : t(option.label)}</span>
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuSubContent>
              </DropdownMenuSub>
            ))}
            {matches("New comment") && (
              <DropdownMenuSub>
                <DropdownMenuSubTrigger>{t("New comment")}</DropdownMenuSubTrigger>
                <DropdownMenuSubContent className="loops-menu">
                  <DropdownMenuItem onSelect={() => chooseIssue("comment")}>
                    <MessageSquare size={14} />
                    {t("Any comment")}
                  </DropdownMenuItem>
                </DropdownMenuSubContent>
              </DropdownMenuSub>
            )}
            {matches("New customer request") && <DropdownMenuItem onSelect={() => chooseIssue("customerRequest")}>{t("New customer request")}</DropdownMenuItem>}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        {/* Linear has no Cycle trigger; loops that already use one keep it so they stay editable. */}
        {entityTypes.map((type) => (
          <DropdownMenuSub key={type}>
            <DropdownMenuSubTrigger>
              <TriggerIcon triggerType={type} />
              {t(ENTITY_NAMES[type])}
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="loops-menu">
              {ENTITY_EVENTS[type].map((item) => (
                <DropdownMenuItem key={item.id} onSelect={() => onChange(type, { event: item.id, teamIds: config.teamIds })}>
                  {t(item.label)}
                </DropdownMenuItem>
              ))}
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** "in [Select teams… ▾]" — empty means every team the loop may access. */
function TeamScope({ data, config, onChange, readOnly }: { data: TriggerData; config: Config; onChange: (patch: Config) => void; readOnly: boolean }) {
  const { t } = useI18n();
  const ids = configStrings(config, "teamIds");
  const selected = data.teams.filter((team) => ids.includes(team.id));
  const label =
    selected.length === 1 ? (
      <>
        <TeamIcon team={selected[0]} size={14} />
        <span data-i18n-ignore>{selected[0].name}</span>
      </>
    ) : selected.length > 1 ? (
      <span>{t(`${selected.length} teams`)}</span>
    ) : (
      <span>{t(readOnly ? "All teams" : "Select teams…")}</span>
    );
  if (readOnly) return <span className="loops-sentence-token">{label}</span>;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className={`loops-sentence-token is-button${selected.length ? "" : " is-placeholder"}`} aria-label={t("Teams")}>
        {label}
        <ChevronDown size={12} />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="loops-menu">
        {data.teams.map((team) => (
          <DropdownMenuCheckboxItem
            key={team.id}
            checked={ids.includes(team.id)}
            onSelect={(item) => item.preventDefault()}
            onCheckedChange={(checked) => onChange({ teamIds: checked ? [...ids, team.id] : ids.filter((id) => id !== team.id) })}
          >
            <TeamIcon team={team} size={14} />
            <span data-i18n-ignore>{team.name}</span>
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function IssueFilters({ data, config, onChange, readOnly }: { data: TriggerData; config: Config; onChange: (patch: Config) => void; readOnly: boolean }) {
  const { t } = useI18n();
  const filters = loopFilters(config);
  const save = (next: LoopFilter[]) => onChange({ filters: next, filter: undefined, filterField: undefined, filterOperator: undefined, filterValue: undefined });
  if (readOnly && !filters.length) return null;
  return (
    <div className="loops-trigger-filters">
      <svg className="loops-filter-connector" viewBox="0 0 14 14" aria-hidden="true">
        <path d="M3.5 0v6.5a3 3 0 0 0 3 3H14" fill="none" stroke="currentColor" />
      </svg>
      {filters.map((filter, index) => {
        const field = FILTER_FIELDS.find((item) => item.id === filter.field);
        const current = valueLabel(data, filter.field, filter.value, "");
        return (
          <span className="loops-filter-chip-row" key={`${filter.field}-${index}`}>
            <span className="loops-filter-chip-part">{t(field?.label ?? filter.field)}</span>
            {readOnly ? (
              <span className="loops-filter-chip-part">{t(filter.operator === "isNot" ? "is not" : "is")}</span>
            ) : (
              <button
                className="loops-filter-chip-part is-button"
                type="button"
                onClick={() => save(filters.map((item, itemIndex) => (itemIndex === index ? { ...item, operator: item.operator === "is" ? "isNot" : "is" } : item)))}
              >
                {t(filter.operator === "isNot" ? "is not" : "is")}
              </button>
            )}
            {readOnly ? (
              <span className="loops-filter-chip-part">
                {current.icon}
                <span data-i18n-ignore={current.entity || undefined}>{current.entity ? current.label : t(current.label)}</span>
              </span>
            ) : (
              <DropdownMenu>
                <DropdownMenuTrigger className="loops-filter-chip-part is-button" aria-label={t(`${field?.label ?? filter.field} value`)}>
                  {current.icon}
                  <span data-i18n-ignore={current.entity || undefined}>{current.entity ? current.label : t(current.label)}</span>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="loops-menu loops-value-menu">
                  {valueOptions(data, filter.field).map((option) => (
                    <DropdownMenuItem key={option.id} onSelect={() => save(filters.map((item, itemIndex) => (itemIndex === index ? { ...item, value: option.id === NONE ? null : option.id } : item)))}>
                      {option.icon}
                      <span data-i18n-ignore={option.entity || undefined}>{option.entity ? option.label : t(option.label)}</span>
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
            {!readOnly && (
              <button className="loops-filter-chip-part is-remove" type="button" aria-label={t("Remove filter")} onClick={() => save(filters.filter((_, itemIndex) => itemIndex !== index))}>
                <X size={12} />
              </button>
            )}
          </span>
        );
      })}
      {!readOnly && (
        <DropdownMenu>
          <DropdownMenuTrigger className="loops-add-filter" aria-label={t("Add filter")} title={t("Add filter")}>
            <Plus size={14} />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="loops-menu">
            {FILTER_FIELDS.map((field) => (
              <DropdownMenuSub key={field.id}>
                <DropdownMenuSubTrigger>
                  <FilterFieldIcon field={field.id} />
                  {t(field.label)}
                </DropdownMenuSubTrigger>
                <DropdownMenuSubContent className="loops-menu loops-value-menu">
                  {valueOptions(data, field.id).map((option) => (
                    <DropdownMenuItem key={option.id} onSelect={() => save([...filters, { field: field.id, operator: "is", value: option.id === NONE ? null : option.id }])}>
                      {option.icon}
                      <span data-i18n-ignore={option.entity || undefined}>{option.entity ? option.label : t(option.label)}</span>
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuSubContent>
              </DropdownMenuSub>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}

function FilterFieldIcon({ field }: { field: LoopFilterField }) {
  if (field === "assignee" || field === "creator") return <UserRound size={14} />;
  if (field === "label") return <Tag size={14} />;
  if (field === "project") return <ProjectIcon size={14} />;
  if (field === "priority") return <PriorityIcon priority={2} size={14} />;
  return <CircleDot size={14} />;
}

/** "Starting [09/29/2026] every [1] [day ▾] at [7AM ▾]" plus weekday chips for weekly schedules. */
function ScheduleSentence({ config, onChange, readOnly }: { config: Config; onChange: (patch: Config) => void; readOnly: boolean }) {
  const { t } = useI18n();
  const start = scheduleStart(config);
  const interval = Math.max(1, Number(config.interval ?? 1) || 1);
  const unit = configString(config, "unit") ?? "day";
  const time = configString(config, "time") ?? "07:00";
  const weekdays = configStrings(config, "weekdays") as LoopWeekday[];
  const timeOptions = useMemo(() => {
    const options = scheduleTimeOptions();
    return options.some((option) => option.value === time) ? options : [...options, { value: time, label: time }];
  }, [time]);
  const units = [
    { value: "hour", label: interval === 1 ? "hour" : "hours" },
    { value: "day", label: interval === 1 ? "day" : "days" },
    { value: "week", label: interval === 1 ? "week" : "weeks" },
    { value: "month", label: interval === 1 ? "month" : "months" },
  ];
  const timeLabel = timeOptions.find((option) => option.value === time)?.label ?? time;
  const unitLabel = units.find((item) => item.value === unit)?.label ?? unit;
  const setStart = (value: string) => {
    const next: Config = { ...config, startDate: value.slice(0, 10) };
    delete next.starting;
    onChange(next);
  };
  return (
    <>
      <span className="loops-sentence-text">{t("Starting")}</span>
      {readOnly ? (
        <span className="loops-sentence-token">{formatScheduleDate(start)}</span>
      ) : (
        <DateTimeControl className="loops-date-control" format={formatScheduleDate} label={t("Start date")} value={start} onChange={setStart} />
      )}
      <span className="loops-sentence-text">{t("every")}</span>
      {readOnly ? (
        <span className="loops-sentence-token">{interval}</span>
      ) : (
        <input
          aria-label={t("Interval")}
          className="loops-interval-input"
          inputMode="numeric"
          min={1}
          max={99}
          type="number"
          value={interval}
          onChange={(item) => onChange({ interval: Math.min(99, Math.max(1, Number(item.target.value) || 1)) })}
        />
      )}
      {readOnly ? (
        <span className="loops-sentence-token">{t(unitLabel)}</span>
      ) : (
        <SelectControl className="loops-unit-select" label={t("Interval unit")} value={unit} onChange={(value) => onChange({ unit: value, ...(value === "week" || value === "day" ? {} : { weekdays: undefined }) })} options={units.map((item) => ({ value: item.value, label: t(item.label) }))} />
      )}
      <span className="loops-sentence-text">{t("at")}</span>
      {readOnly ? (
        <span className="loops-sentence-token">{timeLabel}</span>
      ) : (
        <SelectControl className="loops-time-select" label={t("Time")} value={time} onChange={(value) => onChange({ time: value })} options={timeOptions} />
      )}
      {unit === "week" && (!readOnly || WEEKDAYS.some((day) => weekdays.includes(day.id))) && (
        <span className="loops-weekdays" role="group" aria-label={t("Weekdays")}>
          <span className="loops-sentence-text">{t("On")}</span>
          {WEEKDAYS.map((day) => {
            const active = weekdays.includes(day.id);
            if (readOnly)
              return active ? (
                <span className="loops-weekday is-active" key={day.id}>
                  {t(day.short)}
                </span>
              ) : (
                <Fragment key={day.id} />
              );
            return (
              <button
                aria-pressed={active}
                className={`loops-weekday${active ? " is-active" : ""}`}
                key={day.id}
                type="button"
                onClick={() => onChange({ weekdays: active ? weekdays.filter((item) => item !== day.id) : WEEKDAYS.map((item) => item.id).filter((item) => item === day.id || weekdays.includes(item)) })}
              >
                {t(day.short)}
              </button>
            );
          })}
        </span>
      )}
    </>
  );
}

function TriageGlyph() {
  return (
    <svg aria-hidden="true" className="loops-triage-glyph" viewBox="0 0 14 14" width="14" height="14">
      <circle cx="7" cy="7" r="6" fill="none" stroke="currentColor" strokeWidth="1.5" />
      <path d="M4.5 7h5M7 4.5 9.5 7 7 9.5" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
