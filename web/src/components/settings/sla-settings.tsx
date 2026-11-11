/**
 * Settings › Issues › SLAs. One card switches SLAs on (and picks the work week for business-day
 * deadlines); below it every automation rule reads "When <conditions> Then add an SLA of … from
 * escalation / remove the SLA". Rules are evaluated top to bottom, so they can be dragged
 * (or moved with Alt + arrow keys) to change which one an issue follows.
 */
import { useState, type DragEvent, type KeyboardEvent } from "react";
import { Plus, X } from "lucide-react";

import { toast } from "@/lib/toast";
import {
  createSLARule,
  deleteSLARule,
  reorderSLARules,
  updateSLARule,
  updateSLASettings,
} from "@/lib/api";
import { useI18n } from "@/i18n/i18n";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { PriorityIcon, ProjectIcon, StatusIcon, TeamIcon } from "@/components/issue/issue-icons";
import { labelsForResource } from "@/lib/labels";
import type { BootstrapData, SLARule } from "@/types/flow";

import { SettingsSelect, SettingsToggle, type SettingsSelectOption } from "./settings-primitives";
import { SettingsNavGlyph } from "./settings-nav-glyphs";
import { StatusMenuIcon } from "./issues-projects-settings";
import {
  SLA_CONDITION_KINDS,
  SLA_PRIORITIES,
  SLA_STATUS_TYPES,
  STATUS_TYPE_NAMES,
  conditionsToRule,
  draftFromRule,
  draftMinutes,
  formatSlaDuration,
  newDraft,
  priorityName,
  ruleAction,
  ruleConditions,
  ruleNameFor,
  slaFeatureEnabled,
  validAmount,
  type SlaCondition,
  type SlaConditionKind,
  type SlaDraft,
  type SlaUnit,
} from "./sla-model";
import "./sla-settings.css";

const DOCS_URL = "https://flow.app/docs/sla";

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Could not save setting";
}

export function SLASettings({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const settings = (data.settings?.sla ?? {}) as Record<string, unknown>;
  const enabled = slaFeatureEnabled(data);
  const rules = data.slaRules ?? [];
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<SLARule | "new" | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);

  const run = async (action: () => Promise<unknown>) => {
    setSaving(true);
    try {
      await action();
      await onReload();
      return true;
    } catch (error) {
      toast.error(t(errorMessage(error)));
      return false;
    } finally {
      setSaving(false);
    }
  };
  const moveTo = (id: string, index: number) => {
    const ids = rules.map((rule) => rule.id);
    const from = ids.indexOf(id);
    if (from < 0 || index < 0 || index >= ids.length || from === index) return;
    ids.splice(index, 0, ...ids.splice(from, 1));
    void run(() => reorderSLARules(ids));
  };
  const remove = async (rule: SLARule) => {
    const confirmed = await confirmAction(t("Delete this SLA?"), {
      description: t("This action cannot be undone."),
      confirmLabel: t("Delete"),
    });
    if (confirmed) await run(() => deleteSLARule(rule.id));
  };

  return (
    <div className="sla-page">
      <header className="sla-header">
        <h1>{t("SLAs")}</h1>
        <p>
          {t(
            "Service-level agreements (SLAs) automatically apply deadlines to issues when they match predefined parameters. While often used to define response times to customer issues, they can also be used to define internal standards for bug and time-sensitive issue resolution.",
          )}{" "}
          <a className="sla-docs-link" href={DOCS_URL} rel="noopener noreferrer" target="_blank">
            {t("Docs")}
          </a>
        </p>
      </header>

      <fieldset className="sla-reset" aria-label={t("Settings")}>
        <section className="sla-card">
          <div className="sla-row">
            <div className="sla-row-copy">
              <strong>{t("Enable SLAs")}</strong>
              <span>{t("Workspace-wide access to issue SLA automations and notifications")}</span>
            </div>
            <SettingsToggle
              checked={enabled}
              disabled={saving}
              label={t("Enable SLAs")}
              onChange={(value) => void run(() => updateSLASettings({ enabled: value }))}
            />
          </div>
          {enabled && (
            <div className="sla-row">
              <div className="sla-row-copy">
                <strong>{t("Work week")}</strong>
                <span>{t("Used to determine business day SLAs")}</span>
              </div>
              <SettingsSelect
                label={t("Work week")}
                value={settings.workWeek === "sunThu" ? "sunThu" : "monFri"}
                options={[
                  { value: "monFri", label: t("Mon-Fri") },
                  { value: "sunThu", label: t("Sun-Thu") },
                ]}
                onChange={(value) =>
                  void run(() => updateSLASettings({ workWeek: value as "monFri" | "sunThu" }))
                }
              />
            </div>
          )}
        </section>
      </fieldset>

      <fieldset className="sla-rules" id="automation-rules" disabled={!enabled} aria-labelledby="sla-rules-title">
        <div className="sla-rules-header">
          <div className="sla-rules-copy">
            <h3 id="sla-rules-title">{t("Automation rules")}</h3>
            <div>{t("Use automation rules to automatically add or remove SLAs based on filters.")}</div>
          </div>
          <button type="button" className="sla-add-rule" disabled={saving} onClick={() => setEditing("new")}>
            {t("Add rule")}
          </button>
        </div>
        {rules.length === 0 ? (
          <section className="sla-card">
            <div className="sla-row">
              <div className="sla-row-copy">
                <strong>{t("No automation rules")}</strong>
                <span>
                  {enabled
                    ? t("Add a rule to begin applying SLAs.")
                    : t("Enable SLAs to add automation rules.")}
                </span>
              </div>
            </div>
          </section>
        ) : (
          <div className="sla-rule-list" role="list">
            {rules.map((rule, index) => (
              <RuleCard
                key={rule.id}
                data={data}
                rule={rule}
                dragging={dragging === rule.id}
                busy={saving}
                onDragStart={() => setDragging(rule.id)}
                onDragEnd={() => setDragging(null)}
                onDrop={() => {
                  if (dragging) moveTo(dragging, index);
                  setDragging(null);
                }}
                onMove={(delta) => moveTo(rule.id, index + delta)}
                onEdit={() => setEditing(rule)}
                onDelete={() => void remove(rule)}
              />
            ))}
          </div>
        )}
      </fieldset>

      {editing && (
        <RuleDialog
          data={data}
          rule={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSave={async (input) => {
            const saved = await run(() =>
              editing === "new" ? createSLARule(input) : updateSLARule(editing.id, input),
            );
            if (saved) setEditing(null);
          }}
        />
      )}
    </div>
  );
}

function RuleCard({
  data,
  rule,
  dragging,
  busy,
  onDragStart,
  onDragEnd,
  onDrop,
  onMove,
  onEdit,
  onDelete,
}: {
  data: BootstrapData;
  rule: SLARule;
  dragging: boolean;
  busy: boolean;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDrop: () => void;
  onMove: (delta: number) => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { t } = useI18n();
  const conditions = ruleConditions(rule, data);
  const adds = ruleAction(rule) === "add";
  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (!event.altKey || event.target !== event.currentTarget) return;
    if (event.key === "ArrowUp" || event.key === "ArrowDown") {
      event.preventDefault();
      onMove(event.key === "ArrowUp" ? -1 : 1);
    }
  };
  return (
    <section
      className="sla-rule"
      role="listitem"
      aria-roledescription={t("sortable")}
      data-rule-id={rule.id}
      data-dragging={dragging || undefined}
      data-disabled={rule.enabled ? undefined : true}
      tabIndex={0}
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={(event: DragEvent) => event.preventDefault()}
      onDrop={(event: DragEvent) => {
        event.preventDefault();
        onDrop();
      }}
      onKeyDown={onKeyDown}
    >
      <div className="sla-rule-actions">
        <button type="button" className="sla-icon-button" aria-label={t("Edit configuration")} disabled={busy} onClick={onEdit}>
          <StatusMenuIcon name="edit" />
        </button>
        <button type="button" className="sla-icon-button" aria-label={t("Delete configuration")} disabled={busy} onClick={onDelete}>
          <StatusMenuIcon name="delete" />
        </button>
      </div>
      <div className="sla-rule-line">
        <span className="sla-rule-label">{t("When")}</span>
        <div className="sla-rule-value">
          {conditions.length === 0 ? (
            <div className="sla-chip">
              <div className="sla-chip-part sla-chip-value">
                <span>{t("Any issue")}</span>
              </div>
            </div>
          ) : (
            conditions.map((condition) => <ConditionChip key={condition.kind} condition={condition} data={data} />)
          )}
        </div>
      </div>
      <div className="sla-rule-line">
        <span className="sla-rule-label">{t("Then")}</span>
        <div className="sla-rule-value sla-rule-result">
          {adds ? (
            <>
              <SettingsNavGlyph name="sla" size={16} />
              <span>{t("Add SLA")}</span>
              <span>
                {t(rule.startMode === "issueCreation" ? "of {duration} from issue creation" : "of {duration} from escalation").replace("{duration}", () =>
                  formatSlaDuration(rule.targetMinutes, rule.businessHours, t),
                )}
              </span>
            </>
          ) : (
            <>
              <RemoveSlaGlyph />
              <span>{t("Remove SLA")}</span>
            </>
          )}
        </div>
      </div>
    </section>
  );
}

function RemoveSlaGlyph() {
  return (
    <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
      <path d="M8 15A7 7 0 1 0 8 1a7 7 0 0 0 0 14ZM6.53 5.47 8 6.94l1.47-1.47a.75.75 0 1 1 1.06 1.06L9.06 8l1.47 1.47a.75.75 0 1 1-1.06 1.06L8 9.06l-1.47 1.47a.75.75 0 1 1-1.06-1.06L6.94 8 5.47 6.53a.75.75 0 0 1 1.06-1.06Z" />
    </svg>
  );
}

const KIND_LABELS: Record<SlaConditionKind, string> = {
  priority: "Priority",
  team: "Team",
  status: "Status",
  label: "Label",
  project: "Project",
};

/** "Priority | is | Urgent": the property, the operator and the value, as the issue filters draw them. */
function ConditionChip({ condition, data }: { condition: SlaCondition; data: BootstrapData }) {
  const { t } = useI18n();
  return (
    <div className="sla-chip" data-kind={condition.kind}>
      <div className="sla-chip-part sla-chip-property">
        <span>{t(KIND_LABELS[condition.kind])}</span>
      </div>
      <div className="sla-chip-part sla-chip-operator">
        <span>{t("is")}</span>
      </div>
      <div className="sla-chip-part sla-chip-value">
        <ConditionValue condition={condition} data={data} />
      </div>
    </div>
  );
}

function ConditionValue({ condition, data }: { condition: SlaCondition; data: BootstrapData }) {
  const { t } = useI18n();
  const { kind, value } = condition;
  if (kind === "priority") {
    return (
      <>
        <PriorityIcon priority={Number(value)} size={12} className="sla-chip-icon" style={{ color: "var(--set-control-text)" }} />
        <span>{t(priorityName(value))}</span>
      </>
    );
  }
  if (kind === "status") {
    const state = data.states.find((item) => item.type === value);
    return (
      <>
        {state && <span className="sla-chip-icon"><StatusIcon state={state} size={12} /></span>}
        <span>{t(STATUS_TYPE_NAMES[value] ?? value)}</span>
      </>
    );
  }
  if (kind === "team") {
    const teams = value.split(",").map((id) => data.teams.find((team) => team.id === id));
    return (
      <>
        {teams.length === 1 && teams[0] && <span className="sla-chip-icon"><TeamIcon team={teams[0]} size={12} /></span>}
        <span data-i18n-ignore>{teams.map((team, index) => team?.name ?? value.split(",")[index]).join(", ")}</span>
      </>
    );
  }
  if (kind === "label") {
    const label = data.labels.find((item) => item.id === value || item.name.toLowerCase() === value.toLowerCase());
    return (
      <>
        <span className="sla-chip-dot" style={{ background: label?.color }} aria-hidden="true" />
        <span data-i18n-ignore>{label?.name ?? value}</span>
      </>
    );
  }
  const project = data.projects.find((item) => item.id === value);
  return (
    <>
      <span className="sla-chip-icon"><ProjectIcon size={12} /></span>
      <span data-i18n-ignore>{project?.name ?? value}</span>
    </>
  );
}

type RuleInput = Parameters<typeof createSLARule>[0];

function RuleDialog({
  data,
  rule,
  onClose,
  onSave,
}: {
  data: BootstrapData;
  rule: SLARule | null;
  onClose: () => void;
  onSave: (input: RuleInput) => Promise<void>;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState<SlaDraft>(() => (rule ? draftFromRule(rule, data) : newDraft()));
  const [amountText, setAmountText] = useState(String(draft.amount));
  const [busy, setBusy] = useState(false);
  const adds = draft.action === "add";
  const amount = Number(amountText.trim());
  const amountOk = !adds || (amountText.trim() !== "" && validAmount(amount) && draftMinutes({ ...draft, amount }) <= 153722867);
  const valuesOk = draft.conditions.length > 0 && draft.conditions.every((condition) => condition.value !== "");
  const valid = amountOk && valuesOk;
  const unused = SLA_CONDITION_KINDS.filter((kind) => !draft.conditions.some((condition) => condition.kind === kind));

  const issueLabels = labelsForResource(data.labels, "issue", data.labelGroups);
  const projects = data.projects.filter((project) => !project.archivedAt);
  const optionsFor = (condition: SlaCondition): SettingsSelectOption[] => {
    let options: SettingsSelectOption[];
    switch (condition.kind) {
      case "priority":
        options = SLA_PRIORITIES.map((priority) => ({
          value: String(priority),
          label: t(priorityName(priority)),
          icon: <PriorityIcon priority={priority} size={14} />,
        }));
        break;
      case "team":
        options = data.teams.map((team) => ({
          value: team.id,
          label: team.name,
          entityName: true,
          icon: <TeamIcon team={team} size={14} />,
        }));
        break;
      case "status":
        options = SLA_STATUS_TYPES.map((type) => {
          const state = data.states.find((item) => item.type === type);
          return { value: type, label: t(STATUS_TYPE_NAMES[type]), icon: state ? <StatusIcon state={state} size={14} /> : undefined };
        });
        break;
      case "label":
        options = issueLabels.map((label) => ({
          value: label.id,
          label: label.name,
          entityName: true,
          icon: <span className="sla-chip-dot" style={{ background: label.color }} aria-hidden="true" />,
        }));
        break;
      default:
        options = projects.map((project) => ({ value: project.id, label: project.name, entityName: true, icon: <ProjectIcon size={14} /> }));
    }
    // A saved value the lists no longer offer (a team subset, a deleted label) stays selectable so editing never drops it.
    if (condition.value && !options.some((option) => typeof option !== "string" && option.value === condition.value)) {
      const label = ruleNameFor([condition], data).replace(/^\w+ is /, "");
      options = [...options, { value: condition.value, label, entityName: true }];
    }
    return options;
  };
  const setCondition = (index: number, next: SlaCondition) =>
    setDraft((current) => ({ ...current, conditions: current.conditions.map((item, at) => (at === index ? next : item)) }));
  const defaultValue = (kind: SlaConditionKind) => {
    const first = optionsFor({ kind, value: "" })[0];
    return first && typeof first !== "string" ? first.value : "";
  };
  const addCondition = () => {
    const kind = unused[0];
    if (!kind) return;
    setDraft((current) => ({ ...current, conditions: [...current.conditions, { kind, value: defaultValue(kind) }] }));
  };
  const submit = async () => {
    if (!valid || busy) return;
    setBusy(true);
    const { filters, teamIds } = conditionsToRule(draft.conditions);
    const input: RuleInput = {
      name: ruleNameFor(draft.conditions, data),
      filters,
      teamIds,
      action: draft.action,
      enabled: draft.enabled,
      businessHours: adds ? draft.businessHours : (rule?.businessHours ?? false),
      ...(draft.startMode === "issueCreation" || rule?.startMode ? { startMode: draft.startMode ?? "ruleMatch" } : {}),
      ...(adds ? { targetMinutes: draftMinutes({ ...draft, amount }) } : {}),
    };
    try {
      await onSave(input);
    } finally {
      setBusy(false);
    }
  };
  const unitOptions: SettingsSelectOption[] = (["minutes", "hours", "days", "weeks"] as SlaUnit[]).map((unit) => ({
    value: unit,
    label: t(
      { minutes: "Minutes", hours: "Hours", days: draft.businessHours ? "Business days" : "Days", weeks: draft.businessHours ? "Business weeks" : "Weeks" }[unit],
    ),
  }));

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="sla-dialog"
        aria-describedby={undefined}
        closeLabel={t("Close")}
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) void submit();
        }}
      >
        <DialogTitle>{rule ? t("Edit rule") : t("Add rule")}</DialogTitle>
        <div className="sla-dialog-block">
          <span className="sla-rule-label">{t("When")}</span>
          <div className="sla-dialog-conditions">
            {draft.conditions.map((condition, index) => (
              <div className="sla-dialog-condition" key={condition.kind}>
                <SettingsSelect
                  align="start"
                  menuClassName="settings-select-menu sla-select-menu"
                  label={t("Condition property")}
                  value={condition.kind}
                  options={[condition.kind, ...unused].map((kind) => ({ value: kind, label: t(KIND_LABELS[kind]) }))}
                  onChange={(kind) =>
                    setCondition(index, { kind: kind as SlaConditionKind, value: defaultValue(kind as SlaConditionKind) })
                  }
                />
                <span className="sla-dialog-is">{t("is")}</span>
                <SettingsSelect
                  align="start"
                  menuClassName="settings-select-menu sla-select-menu"
                  label={t(KIND_LABELS[condition.kind])}
                  value={condition.value}
                  placeholder={t("None available")}
                  options={optionsFor(condition)}
                  disabled={optionsFor(condition).length === 0}
                  onChange={(value) => setCondition(index, { ...condition, value })}
                />
                {draft.conditions.length > 1 && (
                  <button
                    type="button"
                    className="sla-icon-button"
                    aria-label={t("Remove condition")}
                    onClick={() => setDraft((current) => ({ ...current, conditions: current.conditions.filter((_, at) => at !== index) }))}
                  >
                    <X size={14} />
                  </button>
                )}
              </div>
            ))}
            {unused.length > 0 && (
              <button type="button" className="sla-text-button" onClick={addCondition}>
                <Plus size={14} />
                {t("Add condition")}
              </button>
            )}
          </div>
        </div>
        <div className="sla-dialog-block">
          <span className="sla-rule-label">{t("Then")}</span>
          <div className="sla-dialog-conditions">
            <div className="sla-dialog-condition">
              <SettingsSelect
                align="start"
                menuClassName="settings-select-menu sla-select-menu"
                label={t("Action")}
                value={draft.action}
                options={[
                  { value: "add", label: t("Add SLA") },
                  { value: "remove", label: t("Remove SLA") },
                ]}
                onChange={(action) => setDraft((current) => ({ ...current, action: action as SlaDraft["action"] }))}
              />
            </div>
            {adds && (
              <>
                <div className="sla-dialog-condition">
                  <span className="sla-dialog-is">{t("of")}</span>
                  <input
                    className="sla-amount"
                    aria-label={t("Amount")}
                    aria-invalid={!amountOk || undefined}
                    inputMode="numeric"
                    value={amountText}
                    onChange={(event) => setAmountText(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") void submit();
                    }}
                  />
                  <SettingsSelect
                    align="start"
                    menuClassName="settings-select-menu sla-select-menu"
                    label={t("Unit")}
                    value={draft.unit}
                    options={unitOptions}
                    onChange={(unit) => setDraft((current) => ({ ...current, unit: unit as SlaUnit }))}
                  />
                  <SettingsSelect
                    align="start"
                    menuClassName="settings-select-menu sla-select-menu"
                    label={t("Start counting")}
                    value={draft.startMode ?? "ruleMatch"}
                    options={[{ value: "ruleMatch", label: t("from escalation") }, { value: "issueCreation", label: t("from issue creation") }]}
                    onChange={(startMode) => setDraft(current => ({ ...current, startMode: startMode as SlaDraft["startMode"] }))}
                  />
                </div>
                <div className="sla-dialog-condition">
                  <SettingsSelect
                    align="start"
                    menuClassName="settings-select-menu sla-select-menu"
                    label={t("Counting")}
                    value={draft.businessHours ? "business" : "calendar"}
                    options={[
                      { value: "calendar", label: t("Calendar time") },
                      { value: "business", label: t("Business hours") },
                    ]}
                    onChange={(value) => setDraft((current) => ({ ...current, businessHours: value === "business" }))}
                  />
                </div>
                {!amountOk && <div className="sla-field-error" role="alert">{t("Enter a whole number of 1 or more")}</div>}
              </>
            )}
          </div>
        </div>
        {rule && (
          <div className="sla-dialog-toggle">
            <span>{t("Rule enabled")}</span>
            <SettingsToggle
              checked={draft.enabled}
              label={t("Rule enabled")}
              onChange={(value) => setDraft((current) => ({ ...current, enabled: value }))}
            />
          </div>
        )}
        <footer>
          <button type="button" className="sla-button" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button type="button" className="sla-button is-primary" disabled={!valid || busy} onClick={() => void submit()}>
            {rule ? t("Save") : t("Add rule")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}
