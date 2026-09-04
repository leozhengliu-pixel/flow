import { useCallback, useEffect, useState } from "react";
import {
  CalendarDays,
  Check,
  ChevronRight,
  ExternalLink,
  FileText,
  Link2,
  LoaderCircle,
  MessageSquare,
  Play,
  Plus,
  RefreshCw,
  Settings2,
} from "lucide-react";
import {
  createWorkflowDefinition,
  fetchTeamResources,
  listWorkflowRuns,
  pinTeamResource,
  retryWorkflowRun,
  runWorkflowDefinition,
  updateWorkflowDefinition,
} from "@/lib/api";
import type {
  BootstrapData,
  Issue,
  ReleaseNote,
  Team,
  TeamPinnedResource,
  TeamResourceSection,
  WorkflowDefinition,
  WorkflowRun,
} from "@/types/flow";
import { UserAvatar } from "@/components/ui/user-avatar";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { useI18n } from "@/i18n/i18n";
import "./workspace-secondary-page.css";

export type WorkspaceSecondaryKind =
  | "diary"
  | "meeting"
  | "automations"
  | "automation-new"
  | "automation-detail"
  | "automation-runs"
  | "team-board"
  | "team-triage"
  | "team-updates"
  | "team-update"
  | "team-resources"
  | "team-links"
  | "release-note"
  | "label";

type Props = {
  data: BootstrapData;
  kind: WorkspaceSecondaryKind;
  team?: Team;
  workflowId?: string;
  workflowRunId?: string;
  editing?: boolean;
  resourceId?: string;
  resourceName?: string;
  resourceType?: "issue" | "project" | "initiative";
  releaseNote?: ReleaseNote;
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
};

export function WorkspaceSecondaryPage(props: Props) {
  const { t } = useI18n();
  const { data, kind, team } = props;
  const title = kind === "diary" ? t("Diary") : kind === "meeting" ? t("Meeting") :
      kind.startsWith("automation") ? t("Automations") : kind === "team-board" ? t("Issues") :
      kind === "team-triage" ? t("Triage") : kind === "team-updates" || kind === "team-update" ? t("Updates") :
        kind === "team-resources" ? t("Resources") : kind === "team-links" ? t("Links") :
          kind === "release-note" ? t("Release note") : t("Labels");

  return (
    <main className="secondary-page" aria-label={title}>
      <header className="secondary-page-header">
        <div className="secondary-breadcrumb">
          {team && <ViewGlyph className="secondary-team-icon" color={team.color} icon={team.icon || "Team"} />}
          <h1>{title}</h1>
          {team && <><ChevronRight aria-hidden size={14} /><span data-i18n-ignore>{team.name}</span></>}
        </div>
        <div className="secondary-header-actions" aria-hidden="true" />
      </header>
      {kind === "diary" && <DiaryPage onNavigate={props.onNavigate} />}
      {kind === "meeting" && <MeetingPage data={data} />}
      {(kind === "automations" || kind === "automation-new" || kind === "automation-detail" || kind === "automation-runs") && (
        <AutomationPage {...props} />
      )}
      {kind === "team-board" && team && <TeamBoard data={data} team={team} />}
      {kind === "team-triage" && team && <TriagePage data={data} team={team} />}
      {(kind === "team-updates" || kind === "team-update") && team && <TeamUpdates data={data} team={team} single={kind === "team-update"} onNavigate={props.onNavigate} />}
      {(kind === "team-resources" || kind === "team-links") && team && <ResourcesPage data={data} team={team} linksOnly={kind === "team-links"} />}
      {kind === "release-note" && <ReleaseNotePage data={data} note={props.releaseNote} />}
      {kind === "label" && <LabelPage data={data} labelName={props.resourceName} resourceType={props.resourceType} />}
    </main>
  );
}

function DiaryPage({ onNavigate: _onNavigate }: Pick<Props, "onNavigate">) {
  const { t } = useI18n();
  const today = new Date();
  const date = today.toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric" });
  return <section className="secondary-content narrow-content">
    <div className="secondary-empty-icon"><CalendarDays size={24} /></div>
    <h2>{t("Your diary")}</h2>
    <p>{t("Capture meeting notes and decisions in one place.")}</p>
    <div className="secondary-diary-date"><CalendarDays size={14} /><strong>{date}</strong><span>{t("No entries yet")}</span></div>
  </section>;
}

function MeetingPage({ data }: { data: BootstrapData }) {
  const { t, formatDate } = useI18n();
  const docs = data.documents.slice(0, 5);
  return <section className="secondary-content meeting-content">
    <div className="secondary-section-heading"><div><h2>{t("Meeting notes")}</h2><p>{t("Recent notes and decisions")}</p></div></div>
    <div className="secondary-list" role="list">{docs.map(doc => <article className="secondary-list-row" role="listitem" key={doc.id}><div className="secondary-row-icon"><FileText size={16} /></div><div className="secondary-row-main"><strong>{doc.title}</strong><small>{formatDate(doc.updatedAt, { dateStyle: "medium" })}</small></div><ChevronRight size={15} /></article>)}</div>
    {!docs.length && <EmptyState title={t("No meetings yet")} body={t("Create a meeting to start capturing notes.")} />}
  </section>;
}

function AutomationPage({ data, kind, workflowId, workflowRunId, editing, onReload, onNavigate }: Props) {
  const { t, formatDate } = useI18n();
  const [workflows, setWorkflows] = useState<WorkflowDefinition[]>(data.workflowDefinitions ?? []);
  const [runs, setRuns] = useState<WorkflowRun[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const selected = workflowId ? workflows.find(item => item.id === workflowId) : undefined;
  useEffect(() => { setWorkflows(data.workflowDefinitions ?? []); }, [data.workflowDefinitions]);
  useEffect(() => { if (selected) { setName(selected.name); setDescription(selected.description ?? ""); } }, [selected]);
  useEffect(() => { if (kind === "automation-runs" || selected) void listWorkflowRuns(selected?.id).then(setRuns).catch(() => setError(t("Could not load automation runs"))); }, [kind, selected, t]);
  const save = async () => {
    if (!name.trim()) return;
    setSaving(true); setError("");
    try { await createWorkflowDefinition({ name: name.trim(), trigger: "manual", conditions: {}, actions: [], enabled: true }); await onReload(); setName(""); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("Could not create automation")); }
    finally { setSaving(false); }
  };
  if (kind === "automation-new") return <section className="secondary-content narrow-content"><div className="secondary-section-heading"><div><h2>{t("New automation")}</h2><p>{t("Run actions when work changes in Flow.")}</p></div></div><label className="secondary-field"><span>{t("Name")}</span><input value={name} onChange={event => setName(event.target.value)} placeholder={t("Automation name")} autoFocus /></label><div className="secondary-callout"><Settings2 size={16} /><span>{t("Manual trigger. Add conditions and actions after creating this rule.")}</span></div>{error && <div className="secondary-error" role="alert">{error}</div>}<div className="secondary-form-actions"><button type="button" className="secondary-secondary-button" onClick={() => setName("")}>{t("Cancel")}</button><button type="button" className="secondary-primary-button" disabled={saving || !name.trim()} onClick={() => void save()}>{saving ? <LoaderCircle className="spin" size={14} /> : <Check size={14} />}{t("Create automation")}</button></div></section>;
  if (kind === "automation-detail" && selected && editing) return <section className="secondary-content narrow-content"><div className="secondary-section-heading"><div><h2>{t("Edit automation")}</h2><p>{t("Update the automation name and description.")}</p></div></div><label className="secondary-field"><span>{t("Name")}</span><input value={name} onChange={event => setName(event.target.value)} /></label><label className="secondary-field"><span>{t("Description")}</span><input value={description} onChange={event => setDescription(event.target.value)} /></label>{error && <div className="secondary-error" role="alert">{error}</div>}<div className="secondary-form-actions"><button type="button" className="secondary-secondary-button" onClick={() => onNavigate(`/${data.workspace.urlKey}/automation/${encodeURIComponent(selected.id)}`)}>{t("Cancel")}</button><button type="button" className="secondary-primary-button" disabled={saving || !name.trim()} onClick={() => void (async () => { setSaving(true); setError(""); try { await updateWorkflowDefinition(selected.id, { name: name.trim(), description: description.trim(), trigger: selected.trigger, schedule: selected.schedule, conditions: selected.conditions, actions: selected.actions, enabled: selected.enabled, maxAttempts: selected.maxAttempts }); await onReload(); onNavigate(`/${data.workspace.urlKey}/automation/${encodeURIComponent(selected.id)}`); } catch (cause) { setError(cause instanceof Error ? cause.message : t("Could not update automation")); } finally { setSaving(false); } })()}>{saving ? <LoaderCircle className="spin" size={14} /> : <Check size={14} />}{t("Save changes")}</button></div></section>;
  if (kind === "automation-detail" && selected) return <section className="secondary-content"><div className="secondary-section-heading"><div><h2>{selected.name}</h2><p>{selected.description || t("Automation details")}</p></div><button type="button" className="secondary-primary-button" onClick={() => void runWorkflowDefinition(selected.id).then(() => onReload())}><Play size={14} />{t("Run now")}</button></div><div className="secondary-detail-grid"><Detail label={t("Trigger")} value={selected.trigger} /><Detail label={t("Status")} value={selected.enabled ? t("Enabled") : t("Disabled")} /><Detail label={t("Actions")} value={String(selected.actions.length)} /><Detail label={t("Last run")} value={selected.lastRunAt ? formatDate(selected.lastRunAt, { dateStyle: "medium" }) : t("Never")} /></div><RunsList runs={runs} workflowRunId={workflowRunId} onRetry={id => void retryWorkflowRun(id).then(() => listWorkflowRuns(selected.id).then(setRuns))} /></section>;
  if (kind === "automation-runs") return <section className="secondary-content"><div className="secondary-section-heading"><div><h2>{t("Automation runs")}</h2><p>{t("Execution history and retry status")}</p></div><button type="button" className="secondary-icon-button" onClick={() => void listWorkflowRuns(workflowId).then(setRuns)} aria-label={t("Refresh")} title={t("Refresh")}><RefreshCw size={15} /></button></div><RunsList runs={runs} workflowRunId={workflowRunId} onRetry={id => void retryWorkflowRun(id).then(() => listWorkflowRuns(workflowId).then(setRuns))} /></section>;
  return <section className="secondary-content"><div className="secondary-section-heading"><div><h2>{t("Automations")}</h2><p>{t("Run durable automations on a schedule or when issues are created.")}</p></div></div><div className="secondary-list">{workflows.map(item => <a className="secondary-list-row" href={`/${data.workspace.urlKey}/automation/${encodeURIComponent(item.id)}`} onClick={event => { event.preventDefault(); onNavigate(`/${data.workspace.urlKey}/automation/${encodeURIComponent(item.id)}`); }} key={item.id}><div className={`secondary-status-dot ${item.enabled ? "is-on" : ""}`} /><div className="secondary-row-main"><strong>{item.name}</strong><small>{item.trigger} · {item.lastRunStatus || t("Never run")}</small></div><span className="secondary-row-meta">{item.lastRunAt ? formatDate(item.lastRunAt, { dateStyle: "medium" }) : ""}</span><ChevronRight size={15} /></a>)}</div>{!workflows.length && <EmptyState title={t("No automations yet")} body={t("Create an automation to automate repetitive work.")} />}</section>;
}

function RunsList({ runs, workflowRunId, onRetry }: { runs: WorkflowRun[]; workflowRunId?: string; onRetry: (id: string) => void }) { const { t, formatDate } = useI18n(); return <div className="secondary-list" role="list">{runs.map(run => <article className={`secondary-list-row ${run.id === workflowRunId ? "is-selected" : ""}`} key={run.id}><div className={`secondary-status-dot ${run.status === "succeeded" ? "is-on" : run.status === "failed" ? "is-failed" : ""}`} /><div className="secondary-row-main"><strong>{run.status === "failed" ? t("Failed") : run.status === "succeeded" ? t("Succeeded") : t("Running")}</strong><small>{formatDate(run.startedAt, { dateStyle: "medium", timeStyle: "short" })}</small></div>{run.error && <span className="secondary-row-error">{run.error}</span>}{run.status === "failed" && <button className="secondary-icon-button" type="button" onClick={() => onRetry(run.id)} aria-label={t("Retry")} title={t("Retry")}><RefreshCw size={14} /></button>}</article>)}{!runs.length && <EmptyState title={t("No runs yet")} body={t("Run this automation to see execution history.")} />}</div>; }

function TeamBoard({ data, team }: { data: BootstrapData; team: Team }) { const { t } = useI18n(); const issues = data.issues.filter(issue => issue.team.id === team.id); const states = data.states.filter(state => state.teamId === team.id); return <section className="secondary-content board-content"><div className="secondary-board-toolbar"><span>{issues.length} {t("issues")}</span></div><div className="secondary-board">{states.map(state => <div className="secondary-column" key={state.id}><header><span className="secondary-state-dot" style={{ background: state.color }} />{state.name}<small>{issues.filter(issue => issue.state.id === state.id).length}</small></header>{issues.filter(issue => issue.state.id === state.id).map(issue => <IssueCard issue={issue} key={issue.id} />)}</div>)}{!states.length && <EmptyState title={t("No workflow states")} body={t("Configure workflow states for this team to use the board.")} />}</div></section>; }
function IssueCard({ issue }: { issue: Issue }) { return <article className="secondary-issue-card"><span>{issue.identifier}</span><strong>{issue.title}</strong><small>{issue.priorityLabel}</small></article>; }

function TriagePage({ data, team }: { data: BootstrapData; team: Team }) { const { t } = useI18n(); const triage = data.issues.filter(issue => issue.team.id === team.id && issue.state.type === "backlog" && !issue.triagedAt); return <section className="secondary-content"><div className="secondary-section-heading"><div><h2>{t("Triage inbox")}</h2><p>{t("Review incoming issues before they enter the team workflow.")}</p></div></div><div className="secondary-list">{triage.map(issue => <IssueCard issue={issue} key={issue.id} />)}</div>{!triage.length && <EmptyState title={t("Triage is empty")} body={t("Incoming issues will appear here when triage is enabled.")} />}</section>; }

function TeamUpdates({ data, team, single, onNavigate: _onNavigate }: { data: BootstrapData; team: Team; single: boolean; onNavigate: (path: string) => void }) { const { t, formatDate } = useI18n(); const projects = data.projects.filter(project => project.teamIds.includes(team.id)); const updates = projects.flatMap(project => (data.projectUpdates[project.id] || []).map(update => ({ update, project }))).sort((a, b) => b.update.createdAt.localeCompare(a.update.createdAt)); const visible = single ? updates.slice(0, 1) : updates; return <section className="secondary-content"><div className="secondary-section-heading"><div><h2>{t("Team updates")}</h2><p>{t("Share progress, decisions, and risks with your team.")}</p></div></div><div className="secondary-list">{visible.map(({ update, project }) => { const author = update.user?.displayName || update.user?.name || t("Unknown"); return <article className="secondary-update-card" key={update.id}><div className="secondary-update-head"><strong>{project.name}</strong><span>{formatDate(update.createdAt, { dateStyle: "medium" })}</span></div><p>{update.body || t("No update text")}</p><div className="secondary-update-foot"><UserAvatar name={author} avatarUrl={update.user?.avatarUrl} /><span>{author}</span><span className="secondary-health">{update.health}</span><MessageSquare size={13} />{update.comments?.length ?? 0}</div></article>; })}</div>{!visible.length && <EmptyState title={t("No updates yet")} body={t("Project updates for this team will appear here.")} />}</section>; }

function ResourcesPage({ data: _data, team, linksOnly }: { data: BootstrapData; team: Team; linksOnly: boolean }) { const { t } = useI18n(); const [resources, setResources] = useState<TeamPinnedResource[]>([]); const [sections, setSections] = useState<TeamResourceSection[]>([]); const [url, setUrl] = useState(""); const [loading, setLoading] = useState(true); const load = useCallback(() => fetchTeamResources(team.id).then(result => { setResources(result.resources); setSections(result.sections); }).finally(() => setLoading(false)), [team.id]); useEffect(() => { void load(); }, [load]); const filtered = resources.filter(resource => linksOnly ? resource.resourceType === "link" : true); const add = async () => { if (!url.trim()) return; await pinTeamResource(team.id, { resourceType: "link", title: url.trim(), url: url.trim(), sectionId: sections[0]?.id }); setUrl(""); await load(); }; return <section className="secondary-content"><div className="secondary-section-heading"><div><h2>{linksOnly ? t("Team links") : t("Team resources")}</h2><p>{t("Add documents and links. Organize by creating sections.")}</p></div></div><div className="secondary-resource-add"><Link2 size={14} /><input value={url} onChange={event => setUrl(event.target.value)} onKeyDown={event => { if (event.key === "Enter") void add(); }} placeholder={t("Paste a link…")} /><button type="button" className="secondary-primary-button" disabled={!url.trim()} onClick={() => void add()}><Plus size={14} />{t("Add")}</button></div>{loading ? <div className="secondary-loading"><LoaderCircle className="spin" size={16} />{t("Loading…")}</div> : <div className="secondary-list">{filtered.map(resource => <a className="secondary-list-row" href={resource.url || undefined} target={resource.url ? "_blank" : undefined} rel={resource.url ? "noreferrer" : undefined} key={resource.id}><div className="secondary-row-icon">{resource.resourceType === "document" ? <FileText size={16} /> : <Link2 size={16} />}</div><div className="secondary-row-main"><strong>{resource.title}</strong><small>{resource.resourceType === "document" ? t("Document") : resource.url}</small></div>{resource.url && <ExternalLink size={14} />}</a>)}</div>}{!loading && !filtered.length && <EmptyState title={t("No resources yet")} body={t("Pin a document or add a link for your team.")} />}</section>; }

function ReleaseNotePage({ data, note }: { data: BootstrapData; note?: ReleaseNote }) { const { t, formatDate } = useI18n(); const item = note || data.releaseNotes[0]; if (!item) return <section className="secondary-content"><EmptyState title={t("Release note not found")} body={t("This release note is no longer available.")} /></section>; return <article className="secondary-content release-note-content"><div className="release-note-meta">{item.publishedAt ? t("Published") : t("Draft")} · {formatDate(item.updatedAt, { dateStyle: "medium" })}</div><h2>{item.title}</h2><div className="release-note-body">{item.body}</div><div className="release-note-author"><UserAvatar name={item.creator.displayName || item.creator.name} avatarUrl={item.creator.avatarUrl} /><span>{item.creator.displayName || item.creator.name}</span></div></article>; }

function LabelPage({ data, labelName, resourceType = "issue" }: { data: BootstrapData; labelName?: string; resourceType?: "issue" | "project" | "initiative" }) { const { t } = useI18n(); const label = data.labels.find(item => item.name === labelName && (item.resourceType ?? "issue") === resourceType) || data.labels.find(item => (item.resourceType ?? "issue") === resourceType); if (!label) return <section className="secondary-content"><EmptyState title={t("Label not found")} body={t("This label is no longer available.")} /></section>; const issues = data.issues.filter(issue => issue.labels.some(item => item.id === label.id)); const projects = data.projects.filter(project => project.labelIds.includes(label.id)); const initiatives = data.initiatives.filter(initiative => initiative.labelIds.includes(label.id)); const rows = resourceType === "project" ? projects.map(project => <article className="secondary-list-row" key={project.id}><div className="secondary-row-icon"><FileText size={16} /></div><div className="secondary-row-main"><strong data-i18n-ignore>{project.name}</strong><small>{t("Project")}</small></div></article>) : resourceType === "initiative" ? initiatives.map(initiative => <article className="secondary-list-row" key={initiative.id}><div className="secondary-row-icon"><FileText size={16} /></div><div className="secondary-row-main"><strong data-i18n-ignore>{initiative.name}</strong><small>{t("Initiative")}</small></div></article>) : issues.map(issue => <IssueCard issue={issue} key={issue.id} />); const noun = resourceType === "project" ? t("Projects") : resourceType === "initiative" ? t("Initiatives") : t("Issues"); return <section className="secondary-content"><div className="secondary-label-heading"><span className="secondary-label-dot" style={{ background: label.color }} /><div><h2>{label.name}</h2><p>{t("Items with this label")}: {noun}</p></div></div><div className="secondary-list">{rows}</div>{!rows.length && <EmptyState title={t("No items with this label")} body={t("Items using this label will appear here.")} />}</section>; }

function Detail({ label, value }: { label: string; value: string }) { return <div className="secondary-detail"><span>{label}</span><strong>{value}</strong></div>; }
function EmptyState({ title, body }: { title: string; body: string }) { return <div className="secondary-empty"><div className="secondary-empty-icon"><FileText size={20} /></div><h3>{title}</h3><p>{body}</p></div>; }
