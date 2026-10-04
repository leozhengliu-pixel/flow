import { ExternalLink, GitBranch, MoreHorizontal, Pencil, Plus, Repeat2, SquareArrowOutUpRight, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { ISSUE_QUERY_INVALIDATED, type IssueQueryInvalidation } from "@/components/issue-explorer/paged-issue-invalidation";
import { IssueDescriptionEditor } from "@/components/issue/issue-description-editor";
import type { DescriptionSnapshot } from "@/components/issue/editor/editor-content";
import { LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, StatusIcon, WorkflowStatusGlyph } from "@/components/issue/issue-icons";
import { Avatar } from "@/components/issue/issue-row";
import { RecurrenceCadenceFields, RecurrenceDialog } from "@/components/issue/recurrence-picker";
import { PropertyMenu, type PropertyOption } from "@/components/property/property-menu";
import { TRIAGE_STATUS } from "@/components/triage/triage-model";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { ProjectGlyph } from "@/components/views/project-glyph";
import { ViewGlyph, ViewIconPicker } from "@/components/views/view-icon-picker";
import { useI18n } from "@/i18n/i18n";
import { createRecurringIssue, fetchIssueRecord, listRecurringIssues, updateIssue, type RecurringIssueCreateInput } from "@/lib/api";
import { issuePath } from "@/lib/app-routes";
import { labelTeamScopeIds, labelsForResource, toggleGroupedLabelIds } from "@/lib/labels";
import {
  defaultFirstDue,
  describeRepeats,
  nextRecurrenceDue,
  parseRecurrence,
  recurrenceDate,
  recurrenceDueDate,
  simpleRecurrence,
  toDateInput,
  type RecurrenceFrequency,
} from "@/lib/recurrence";
import { canManageTeamSettings } from "@/lib/settings-permissions";
import type { BootstrapData, Issue, Team, TemplateSubIssue, WorkflowState } from "@/types/flow";

import { SubIssueTemplateComposer, SubIssueTemplateRow } from "./issues-projects-settings";
import { SettingsRow, SettingsSection, TeamSettingsCrumb, SettingsCrumb } from "./settings-primitives";
import "./issue-template-settings.css";
import "./recurring-issues-settings.css";

const DOCS_URL = "https://flow.app/docs/recurring-issues";
const DEFAULT_ICON = "Page";
const ICON_COLOR = "var(--theme-text-secondary)";
/** Pseudo status id for Linear's "Triage" chip: the team's backlog state, not yet triaged. */
const TRIAGE_ID = "triage";

type Props = {
  data: BootstrapData;
  team: Team;
  subPath?: string;
  onBack: () => void;
  onNavigateSubPath: (subPath?: string) => void;
  onReload: () => Promise<void>;
};

/** Team settings > Recurring issues: the list, or the "New recurring issue" page at recurring-issues/new. */
export function RecurringIssuesSettingsPage(props: Props) {
  const sourceId = new URLSearchParams(window.location.search).get("fromIssue") ?? "";
  if (props.subPath === "new" || sourceId) return <NewRecurringIssuePage key={sourceId || "new"} {...props} sourceId={sourceId} />;
  return <RecurringIssuesList {...props} />;
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Index-served list of the team's schedule owners. Refetched when realtime issue
 * changes touch this team's recurring issues (paged workspaces never hold every issue).
 */
function useRecurringIssues(data: BootstrapData, teamId: string) {
  const [state, setState] = useState<{ teamId: string; issues: Issue[]; loaded: boolean; error?: string }>({ teamId, issues: [], loaded: false });
  const [revision, setRevision] = useState(0);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  useEffect(() => {
    const abort = new AbortController();
    listRecurringIssues(teamId, abort.signal)
      .then((result) => setState({ teamId, issues: result.issues ?? [], loaded: true }))
      .catch((error) => {
        if (!abort.signal.aborted) setState((current) => ({ teamId, issues: current.teamId === teamId ? current.issues : [], loaded: true, error: errorMessage(error) }));
      });
    return () => abort.abort();
  }, [teamId, revision]);
  // Schedule owners and series instances of this team held by the client cache;
  // a change (new instance, edited or stopped schedule) refetches the list.
  const signature = useMemo(
    () =>
      data.issues
        .filter((issue) => issue.team.id === teamId && (issue.recurrence || issue.recurrenceSeriesId))
        .map((issue) => [issue.id, issue.recurrence, issue.dueDate, issue.nextOccurrenceAt, issue.title, issue.icon, issue.archivedAt, issue.updatedAt].join("|"))
        .sort()
        .join(";"),
    [data.issues, teamId],
  );
  // A listed issue whose cached copy no longer owns a schedule (stopped elsewhere).
  const stale = useMemo(() => {
    if (!state.issues.length) return false;
    const listed = new Set(state.issues.map((issue) => issue.id));
    return data.issues.some((issue) => listed.has(issue.id) && (!issue.recurrence || Boolean(issue.archivedAt)));
  }, [data.issues, state.issues]);
  const timer = useRef<number | undefined>(undefined);
  const schedule = useCallback(() => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(refresh, 150);
  }, [refresh]);
  const seen = useRef({ signature, revision: data.issueCollectionRevision });
  useEffect(() => {
    if (!stale && seen.current.signature === signature && seen.current.revision === data.issueCollectionRevision) return;
    seen.current = { signature, revision: data.issueCollectionRevision };
    schedule();
  }, [data.issueCollectionRevision, schedule, signature, stale]);
  useEffect(() => {
    const invalidate = (event: Event) => {
      if ((event as CustomEvent<IssueQueryInvalidation>).detail?.workspaceKey === data.workspace.urlKey) schedule();
    };
    window.addEventListener(ISSUE_QUERY_INVALIDATED, invalidate);
    return () => {
      window.removeEventListener(ISSUE_QUERY_INVALIDATED, invalidate);
      window.clearTimeout(timer.current);
    };
  }, [data.workspace.urlKey, schedule]);
  const replace = useCallback((issue: Issue) => {
    setState((current) => ({ ...current, issues: issue.recurrence ? current.issues.map((item) => (item.id === issue.id ? { ...item, ...issue } : item)) : current.issues.filter((item) => item.id !== issue.id) }));
  }, []);
  return { ...(state.teamId === teamId ? state : { issues: [], loaded: false, error: undefined }), refresh, replace };
}

function RecurringIssuesList({ data, team, onBack, onNavigateSubPath }: Props) {
  const { t, locale, formatDate } = useI18n();
  const navigate = useNavigate();
  const timeZone = data.teamSettings?.[team.id]?.timezone;
  const canManage = canManageTeamSettings(data, team.id, "recurring-issues");
  const { issues, loaded, error, refresh, replace } = useRecurringIssues(data, team.id);
  const [editingId, setEditingId] = useState("");
  const [busy, setBusy] = useState(false);
  const editing = issues.find((issue) => issue.id === editingId);
  const save = async (issue: Issue, input: { recurrence: string; dueDate?: string }, success: string) => {
    setBusy(true);
    try {
      replace(await updateIssue(issue.id, input));
      setEditingId("");
      toast.success(t(success));
      refresh();
    } catch (failure) {
      toast.error(errorMessage(failure));
    } finally {
      setBusy(false);
    }
  };
  const openIssue = (issue: Issue) => navigate(issuePath(data.workspace.urlKey, issue));
  const dateLabel = (date: Date) => formatDate(date.toISOString(), { month: "short", day: "numeric", year: date.getFullYear() === new Date().getFullYear() ? undefined : "numeric" });
  const newButton = (
    <button type="button" className="settings-action recurring-issues-new" disabled={!canManage} onClick={() => onNavigateSubPath("new")}>
      <Plus size={13} />
      {t("New recurring issue")}
    </button>
  );
  return (
    <>
      <TeamSettingsCrumb team={team} onClick={onBack} />
      <header className="settings-page-header team-settings-header">
        <div>
          <h1>{t("Recurring issues")}</h1>
          <p>
            {t("Automatically create issues that need to be completed on a regular schedule. Each issue is created with a due date set by the schedule. A new instance is created after each due date passes.")}{" "}
            <a className="recurring-issues-docs" href={DOCS_URL} target="_blank" rel="noreferrer">
              {t("Docs")}
              <ExternalLink size={11} aria-hidden="true" />
            </a>
          </p>
        </div>
      </header>
      <SettingsSection className="recurring-issues-section">
        {issues.map((issue) => {
          const due = recurrenceDueDate(issue, timeZone);
          const schedule = due ? parseRecurrence(issue.recurrence, due) : null;
          const next = due && schedule ? nextRecurrenceDue(schedule, due) : undefined;
          return (
            <SettingsRow
              key={issue.id}
              className="personal-row-link recurring-issue-row"
              icon={<ViewGlyph icon={issue.icon || DEFAULT_ICON} color={ICON_COLOR} />}
              role="button"
              tabIndex={0}
              onClick={() => openIssue(issue)}
              onKeyDown={(event) => { if (event.key === "Enter" && event.target === event.currentTarget) openIssue(issue); }}
              title={
                <span className="recurring-issue-title" data-i18n-ignore>
                  <span>{issue.title}</span>
                  <small>{issue.identifier}</small>
                </span>
              }
              description={
                <span className="recurring-issue-meta">
                  <Repeat2 size={12} aria-hidden="true" />
                  {describeRepeats(issue.recurrence, { t, locale, anchor: due })}
                  {next && <> · {t("Next due {date}").replace("{date}", dateLabel(next))}</>}
                  {Boolean(issue.subIssueCount) && <> · <GitBranch size={12} aria-hidden="true" />{issue.subIssueCount === 1 ? t("1 sub-issue") : t("{count} sub-issues").replace("{count}", String(issue.subIssueCount))}</>}
                </span>
              }
            >
              {due && <span className="recurring-issue-due">{t("Due {date}").replace("{date}", dateLabel(due))}</span>}
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button type="button" className="settings-icon-action" aria-label={`${t("Recurring issue actions")} ${issue.identifier}`} onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
                    <MoreHorizontal size={15} />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" onClick={(event) => event.stopPropagation()}>
                  <DropdownMenuItem onSelect={() => openIssue(issue)}>
                    <SquareArrowOutUpRight size={14} />
                    {t("Open issue")}
                  </DropdownMenuItem>
                  <DropdownMenuItem disabled={!canManage} onSelect={() => setEditingId(issue.id)}>
                    <Pencil size={14} />
                    {t("Edit schedule")}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem className="danger-item" disabled={!canManage || busy} onSelect={() => void save(issue, { recurrence: "" }, "Recurring stopped")}>
                    <X size={14} />
                    {t("Stop recurring")}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </SettingsRow>
          );
        })}
        <SettingsRow className="settings-row--action recurring-issues-empty" title={issues.length ? "" : loaded ? t("No recurring issues") : t("Loading…")}>
          {newButton}
        </SettingsRow>
      </SettingsSection>
      {error && <p className="recurring-issues-error" role="alert">{error}</p>}
      <RecurrenceDialog
        open={Boolean(editing)}
        onOpenChange={(open) => !open && setEditingId("")}
        busy={busy}
        value={editing?.recurrence}
        dueDate={editing?.dueDate}
        nextOccurrenceAt={editing?.nextOccurrenceAt}
        timeZone={timeZone}
        onSave={(recurrence, firstDue) => editing && save(editing, { recurrence, dueDate: firstDue }, "Recurring schedule saved")}
        onStop={() => editing && save(editing, { recurrence: "" }, "Recurring stopped")}
      />
    </>
  );
}

/** Workflow states for the team (inheriting a parent team's statuses when configured). */
function teamStates(data: BootstrapData, teamId: string, seen = new Set<string>()): WorkflowState[] {
  if (seen.has(teamId)) return [];
  seen.add(teamId);
  const settings = data.teamSettings?.[teamId];
  if (settings?.inheritWorkflowStatuses && settings.parentTeamId) return teamStates(data, settings.parentTeamId, seen);
  const specific = data.states.some((state) => state.teamId === teamId);
  return data.states.filter((state) => (specific ? state.teamId === teamId : !state.teamId)).sort((a, b) => (a.position ?? 0) - (b.position ?? 0));
}

const PRIORITY_NAMES = ["No priority", "Urgent", "High", "Medium", "Low"];

/** Linear's "New recurring issue" page: the first instance, its schedule and first due date. */
function NewRecurringIssuePage({ data, team, sourceId, onNavigateSubPath, onReload }: Props & { sourceId: string }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const settings = data.teamSettings?.[team.id];
  const triage = Boolean(settings?.triageEnabled);
  const states = useMemo(() => teamStates(data, team.id), [data, team.id]);
  const backlog = states.find((state) => state.type === "backlog");
  const defaultState = states.find((state) => state.id === settings?.defaultStateId) ?? states.find((state) => state.default) ?? states.find((state) => state.type === "unstarted") ?? states[0];
  const [source, setSource] = useState<Issue | undefined>(() => data.issues.find((issue) => issue.id === sourceId && !issue.isSummary));
  const [sourceError, setSourceError] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState<DescriptionSnapshot | null>(null);
  const [descriptionKey, setDescriptionKey] = useState(0);
  const [icon, setIcon] = useState("");
  const [iconColor, setIconColor] = useState(ICON_COLOR);
  const [stateId, setStateId] = useState(triage && backlog ? TRIAGE_ID : defaultState?.id ?? "");
  const [priority, setPriority] = useState(0);
  const [assigneeId, setAssigneeId] = useState("");
  const [projectId, setProjectId] = useState("");
  const [labelIds, setLabelIds] = useState<string[]>([]);
  const [subIssues, setSubIssues] = useState<TemplateSubIssue[]>([]);
  const [composerOpen, setComposerOpen] = useState(false);
  const [firstDue, setFirstDue] = useState(toDateInput(defaultFirstDue()));
  const [frequency, setFrequency] = useState<RecurrenceFrequency>("weekly");
  const [interval, setRepeatInterval] = useState(1);
  /** A converted issue's richer schedule (e.g. several weekdays) stays unless the cadence is edited. */
  const [keptRecurrence, setKeptRecurrence] = useState("");
  const [saving, setSaving] = useState(false);
  const canManage = canManageTeamSettings(data, team.id, "recurring-issues");

  useEffect(() => {
    if (!sourceId || source) return;
    const abort = new AbortController();
    fetchIssueRecord(sourceId, abort.signal).then(setSource).catch((error) => { if (!abort.signal.aborted) setSourceError(errorMessage(error)); });
    return () => abort.abort();
  }, [source, sourceId]);
  const prefilled = useRef("");
  useEffect(() => {
    if (!source || prefilled.current === source.id) return;
    prefilled.current = source.id;
    setTitle(source.title);
    setIcon(source.icon ?? "");
    setPriority(source.priority);
    setAssigneeId(source.assignee?.id ?? "");
    setProjectId(source.project?.id ?? "");
    setLabelIds(source.labels.map((label) => label.id));
    setStateId(source.state.id);
    setDescriptionKey((value) => value + 1);
    const today = recurrenceDate();
    const due = source.dueDate ? recurrenceDate(source.dueDate.slice(0, 10)) : undefined;
    const start = due && due >= today ? due : defaultFirstDue(today);
    setFirstDue(toDateInput(start));
    const schedule = parseRecurrence(source.recurrence, start);
    if (schedule) {
      setFrequency(schedule.frequency);
      setRepeatInterval(schedule.interval);
      setKeptRecurrence(source.recurrence ?? "");
    }
  }, [source]);

  const close = () => {
    const url = new URL(window.location.href);
    if (url.searchParams.has("fromIssue")) {
      url.searchParams.delete("fromIssue");
      window.history.replaceState(window.history.state, "", url);
    }
    onNavigateSubPath(undefined);
  };
  const recurrence = keptRecurrence || simpleRecurrence(frequency, interval);
  const resolvedStateId = stateId === TRIAGE_ID ? backlog?.id : stateId || undefined;
  const create = async () => {
    if (!title.trim() || !firstDue || saving) return;
    setSaving(true);
    try {
      if (source) {
        const issue = await updateIssue(source.id, {
          title: title.trim(),
          ...(description ? { description: description.markdown.trim(), descriptionState: description.documentJSON } : {}),
          ...(resolvedStateId && resolvedStateId !== source.state.id ? { stateId: resolvedStateId } : {}),
          priority,
          assigneeId,
          projectId,
          labelIds,
          icon,
          recurrence,
          dueDate: firstDue,
        });
        toast.success(t("Recurring schedule saved"));
        close();
        await onReload();
        navigate(issuePath(data.workspace.urlKey, issue));
        return;
      }
      const input: RecurringIssueCreateInput = {
        title: title.trim(),
        description: description?.markdown.trim() ?? "",
        ...(description?.documentJSON ? { descriptionState: description.documentJSON } : {}),
        ...(resolvedStateId ? { stateId: resolvedStateId } : {}),
        priority,
        ...(assigneeId ? { assigneeId } : {}),
        ...(projectId ? { projectId } : {}),
        labelIds,
        ...(icon ? { icon } : {}),
        dueDate: firstDue,
        recurrence,
        ...(subIssues.length
          ? { subIssues: subIssues.map((item) => ({ title: item.title, ...(item.description ? { description: item.description } : {}), priority: item.priority ?? 0, ...(item.assigneeId ? { assigneeId: item.assigneeId } : {}), labelIds: item.labelIds ?? [] })) }
          : {}),
      };
      await createRecurringIssue(team.id, input);
      toast.success(t("Recurring issue created"));
      close();
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const labelScopes = new Set(labelTeamScopeIds(team.id, data.teams, data.teamSettings));
  const labels = labelsForResource(data.labels, "issue", data.labelGroups).filter((label) => !label.scope || label.scope === "Workspace" || labelScopes.has(label.scope));
  const labelGroupNames = new Map(data.labelGroups.map((group) => [group.id, group.name]));
  const labelOptions: PropertyOption[] = labels.map((label) => ({ id: label.id, label: label.name, color: label.color, description: label.description, scope: label.scope, groupId: label.groupId, groupLabel: label.groupId ? labelGroupNames.get(label.groupId) : undefined, i18nIgnore: true }));
  const selectedLabels = labels.filter((label) => labelIds.includes(label.id));
  const state = states.find((item) => item.id === stateId);
  const assignee = data.users.find((user) => user.id === assigneeId);
  const project = data.projects.find((item) => item.id === projectId);
  const statusOptions: PropertyOption[] = [
    ...(triage && backlog ? [{ id: TRIAGE_ID, label: t("Triage"), icon: <WorkflowStatusGlyph state={TRIAGE_STATUS} /> }] : []),
    ...states.map((item) => ({ id: item.id, label: item.name, icon: <StatusIcon state={item} size={14} />, i18nIgnore: true })),
  ];
  const heading = source ? t("Make recurring") : t("New recurring issue");

  return (
    <div className="recurring-issue-editor">
      <SettingsCrumb onClick={close}>{t("Recurring issues")}</SettingsCrumb>
      <div className="recurring-issue-editor__inner">
        <h1>{heading}</h1>
        <p className="recurring-issue-editor__intro">
          {t("Create the first instance of your recurring issue below, including the schedule and initial due date. New instances will be created after each due date passes.")}{" "}
          <a className="recurring-issues-docs" href={DOCS_URL} target="_blank" rel="noreferrer">
            {t("Docs")}
            <ExternalLink size={11} aria-hidden="true" />
          </a>
        </p>
        {sourceError && <p className="recurring-issues-error" role="alert">{sourceError}</p>}
        <div className="recurring-issue-editor__icon">
          <span>{t("Icon")}</span>
          <ViewIconPicker
            ariaLabel={t("Icon")}
            icon={icon || DEFAULT_ICON}
            color={iconColor}
            triggerClassName={`it-icon-trigger recurring-issue-editor__icon-trigger${icon ? " is-set" : ""}`}
            onChange={(visual) => { setIcon(visual.icon); setIconColor(visual.color); }}
          />
        </div>
        <section className="recurring-issue-editor__card">
          <input
            autoFocus
            className="recurring-issue-editor__title"
            aria-label={t("Issue title")}
            placeholder={t("Issue title")}
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            onKeyDown={(event) => { if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) void create(); }}
          />
          <IssueDescriptionEditor
            key={descriptionKey}
            className="recurring-issue-editor__description"
            ariaLabel={t("Issue description")}
            placeholder={t("Add description…")}
            users={data.users}
            value={source?.description ?? ""}
            state={source?.descriptionState}
            onChange={setDescription}
            onSubmit={() => void create()}
          />
          <div className="recurring-issue-editor__properties">
            <PropertyMenu
              compact
              label={t("Status")}
              ariaLabel={t("Change status")}
              searchPlaceholder={t("Change status…")}
              value={stateId === TRIAGE_ID ? t("Triage") : state?.name ?? t("Status")}
              valueIsEntityName={stateId !== TRIAGE_ID && Boolean(state)}
              selectedId={stateId}
              triggerClassName="it-property"
              icon={stateId === TRIAGE_ID ? <WorkflowStatusGlyph state={TRIAGE_STATUS} /> : state ? <StatusIcon state={state} size={14} /> : undefined}
              options={statusOptions}
              onChange={setStateId}
            />
            <PropertyMenu
              compact
              label={t("Priority")}
              ariaLabel={t("Change priority")}
              searchPlaceholder={t("Change priority…")}
              value={priority ? t(PRIORITY_NAMES[priority]) : t("Priority")}
              selectedId={String(priority)}
              triggerClassName="it-property"
              icon={<PriorityIcon priority={priority} size={14} />}
              options={PRIORITY_NAMES.map((name, index) => ({ id: String(index), label: t(name), icon: <PriorityIcon priority={index} size={14} />, shortcut: String(index) }))}
              onChange={(value) => setPriority(Number(value))}
            />
            <PropertyMenu
              compact
              label={t("Assignee")}
              ariaLabel={t("Change assignee")}
              searchPlaceholder={t("Change assignee…")}
              value={assignee?.displayName ?? t("Assignee")}
              valueIsEntityName={Boolean(assignee)}
              selectedId={assigneeId}
              triggerClassName="it-property"
              icon={assignee ? <Avatar name={assignee.displayName} /> : <NoAssigneeIcon size={14} />}
              options={[
                { id: "", label: t("No assignee"), icon: <NoAssigneeIcon size={14} /> },
                ...data.users.filter((user) => user.active).map((user) => ({ id: user.id, label: user.displayName, keywords: `${user.name} ${user.email}`, icon: <Avatar name={user.displayName} />, i18nIgnore: true })),
              ]}
              onChange={setAssigneeId}
            />
            <PropertyMenu
              compact
              label={t("Project")}
              ariaLabel={t("Change project")}
              searchPlaceholder={t("Change project…")}
              value={project?.name ?? t("Project")}
              valueIsEntityName={Boolean(project)}
              selectedId={projectId}
              triggerClassName="it-property"
              icon={project ? <ProjectGlyph project={project} size={14} /> : <NoProjectIcon size={14} />}
              options={[
                { id: "", label: t("No project"), icon: <NoProjectIcon size={14} /> },
                ...data.projects.map((item) => ({ id: item.id, label: item.name, color: item.color, icon: <ProjectGlyph project={item} size={14} />, i18nIgnore: true })),
              ]}
              onChange={setProjectId}
            />
            <PropertyMenu
              compact
              multiple
              kind="labels"
              label={t("Labels")}
              ariaLabel={t("Change labels")}
              searchPlaceholder={t("Change labels…")}
              value={selectedLabels.length === 1 ? selectedLabels[0].name : selectedLabels.length ? `${selectedLabels.length} ${t("Labels")}` : t("Labels")}
              valueIsEntityName={selectedLabels.length === 1}
              selectedIds={labelIds}
              triggerClassName="it-property"
              icon={<LabelIcon size={14} />}
              options={labelOptions}
              onChange={(id) => setLabelIds((current) => toggleGroupedLabelIds(current, id, labelOptions))}
            />
            {!source && (
              <button type="button" className="it-property" aria-expanded={composerOpen} onClick={() => setComposerOpen(true)}>
                <GitBranch aria-hidden="true" />
                {t("Sub-issues")}
                {subIssues.length > 0 && <span className="recurring-issue-editor__count">{subIssues.length}</span>}
              </button>
            )}
          </div>
          {(subIssues.length > 0 || composerOpen) && (
            <div className="recurring-issue-editor__subissues">
              {subIssues.map((item) => (
                <SubIssueTemplateRow
                  key={item.id}
                  item={item}
                  onChange={(next) => setSubIssues((current) => current.map((value) => (value.id === next.id ? next : value)))}
                  onRemove={() => setSubIssues((current) => current.filter((value) => value.id !== item.id))}
                />
              ))}
              {composerOpen && (
                <SubIssueTemplateComposer
                  data={data}
                  showTeam={false}
                  labelTeamId={team.id}
                  onCancel={() => setComposerOpen(false)}
                  onAdd={(item) => { setSubIssues((current) => [...current, item]); setComposerOpen(false); }}
                />
              )}
            </div>
          )}
        </section>
        <footer className="recurring-issue-editor__footer">
          <RecurrenceCadenceFields
            firstDue={firstDue}
            frequency={frequency}
            interval={interval}
            min={toDateInput(recurrenceDate())}
            onFirstDue={setFirstDue}
            onFrequency={(value) => { setFrequency(value); setKeptRecurrence(""); }}
            onInterval={(value) => { setRepeatInterval(value); setKeptRecurrence(""); }}
          />
          <span className="recurring-issue-editor__actions">
            <button type="button" onClick={close}>{t("Cancel")}</button>
            <button type="button" className="primary" disabled={!canManage || saving || !title.trim() || !firstDue} onClick={() => void create()}>
              {saving ? t("Saving…") : t(source ? "Save" : "Create")}
            </button>
          </span>
        </footer>
      </div>
    </div>
  );
}
