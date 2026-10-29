import { Ban, Check, Lock, Pencil, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import type { FormEvent, ReactNode } from "react";

import { PropertyMenu } from "@/components/property/property-menu";
import { GridLoader } from "@/components/ui/grid-loader";
import { useI18n } from "@/i18n/i18n";
import type { ChatBinding, ChatBindingInput, ChatBindingRequests, ChatIntegrationEvent, ChatIntegrationProvider } from "@/types/chat-integrations";
import type { BootstrapData, Project } from "@/types/flow";

import { bindingProjectIds, CHAT_BINDING_EVENTS, useBotChats } from "./chat-binding-support";
import { SettingsSelect, SettingsToggle } from "./settings-primitives";

// Radix Select shows its trigger text only for a non-empty value; this one matches no option.
const NO_SELECTION = "__none__";

/** A settings row: copy on the left, the control on the right (shared feature-row layout). */
export function FieldRow({ id, label, description, inline, children }: { id?: string; label: ReactNode; description?: ReactNode; inline?: boolean; children?: ReactNode }) {
  return <div className={`feature-row${inline ? " chat-inline" : ""}`}>
    <div><strong>{id ? <label htmlFor={id}>{label}</label> : label}</strong>{description && <span>{description}</span>}</div>
    {children && <aside>{children}</aside>}
  </div>;
}

const ALL_PROJECTS = "__all_projects";

/** Reactions a binding can take as a request (the platform's emoji types). */
const REQUEST_EMOJIS: { value: string; label: string }[] = [
  { value: "OK", label: "👌 OK" },
  { value: "THUMBSUP", label: "👍 Thumbs up" },
  { value: "DONE", label: "✅ Done" },
  { value: "JIAYI", label: "+1" },
];
const NO_EMOJI = "__no_emoji";
const DEFAULT_PRIORITY = "__default_priority";
const REQUEST_PRIORITIES = [{ value: "0", label: "No priority" }, { value: "1", label: "Urgent" }, { value: "2", label: "High" }, { value: "3", label: "Medium" }, { value: "4", label: "Low" }];

/** The binding's projects: all of the team's, or a chosen set, in the shared multi-select picker. */
function ChatProjectsPicker({ projects, selectedIds, onChange }: { projects: Project[]; selectedIds: string[]; onChange: (ids: string[]) => void }) {
  const { t } = useI18n();
  const selected = projects.filter((project) => selectedIds.includes(project.id));
  const value = !selectedIds.length ? t("All team projects") : selected.length === 1 ? selected[0].name : t("{count} projects").replace("{count}", String(selectedIds.length));
  return <PropertyMenu
    compact
    multiple
    label={t("Projects")}
    ariaLabel={t("Projects")}
    hideSearch={projects.length < 8}
    searchPlaceholder={t("Filter projects…")}
    value={value}
    valueIsEntityName={selected.length === 1 && selectedIds.length === 1}
    selectedIds={selectedIds.length ? selectedIds : [ALL_PROJECTS]}
    triggerClassName="settings-select chat-projects-picker"
    surfaceClassName="chat-projects-picker-menu"
    options={[{ id: ALL_PROJECTS, label: t("All team projects") }, ...projects.map((project) => ({ id: project.id, label: project.name, i18nIgnore: true }))]}
    onChange={(id) => onChange(id === ALL_PROJECTS ? [] : selectedIds.includes(id) ? selectedIds.filter((item) => item !== id) : [...selectedIds, id])}
  />;
}

/** One group binding: its group, scope, who authorized it, and its state. */
export function ChatBindingRow({ binding, data, busy, canEdit, onEdit, onRevoke, onRemove }: {
  binding: ChatBinding;
  data: BootstrapData;
  busy: string;
  canEdit: boolean;
  /** Omitted where bindings are not edited (the project dialog). */
  onEdit?: () => void;
  onRevoke: () => void;
  onRemove: () => void;
}) {
  const { t, formatDate } = useI18n();
  const team = data.teams.find((item) => item.id === binding.teamId);
  const projectIds = bindingProjectIds(binding) ?? [];
  const project = projectIds.length === 1 ? data.projects.find((item) => item.id === projectIds[0]) : undefined;
  const authorizer = binding.authorization ? data.users.find((user) => user.id === binding.authorization?.userId) : undefined;
  const revoked = Boolean(binding.revokedAt);
  return <div className="feature-row chat-inline chat-binding-row" data-revoked={revoked || undefined}>
    <div>
      <strong>
        <span className="chat-binding-name" title={binding.chatName} data-i18n-ignore>{binding.chatName}</span>
        {binding.authorization?.privateTeam && <small className="chat-binding-badge"><Lock size={11} aria-hidden="true" />{t("Private team")}</small>}
        {revoked ? <small className="chat-binding-badge is-revoked">{t("Revoked")}</small> : !binding.enabled && <small>{t("Paused")}</small>}
      </strong>
      <span className="chat-binding-scope">
        {team ? <span data-i18n-ignore>{team.name}</span> : t("Team unavailable")}
        {projectIds.length === 1 && <> / {project ? <span data-i18n-ignore>{project.name}</span> : t("Project unavailable")}</>}
        {projectIds.length > 1 && <> / <span title={projectIds.map((id) => data.projects.find((item) => item.id === id)?.name ?? t("Project unavailable")).join(", ")}>{t("{count} projects").replace("{count}", String(projectIds.length))}</span></>}
        {` · ${binding.events.length === 1 ? t("1 notification type") : t("{count} notification types").replace("{count}", String(binding.events.length))}`}
        {` · ${t(binding.allowCreate ? "Creating issues allowed" : "Creating issues off")} · ${t(binding.allowUpdate ? "Updating issues allowed" : "Updating issues off")}`}
        {binding.requests?.enabled && ` · ${t("Requests on")}`}
      </span>
      {binding.authorization && <span className="chat-binding-authorization">
        {t("Authorized by {name} on {date}").replace("{name}", authorizer?.displayName || authorizer?.name || t("a former member")).replace("{date}", formatDate(binding.authorization.authorizedAt, { dateStyle: "medium" }))}
        {revoked && binding.revokedAt && ` · ${t("Revoked on {date}").replace("{date}", formatDate(binding.revokedAt, { dateStyle: "medium" }))}`}
      </span>}
    </div>
    <aside>
      {!revoked && onEdit && <button type="button" className="feature-button chat-icon-button" aria-label={`${t("Edit group binding")}: ${binding.chatName}`} title={t("Edit group binding")} disabled={Boolean(busy) || !canEdit} onClick={onEdit}><Pencil size={14} aria-hidden="true" /></button>}
      {!revoked && <button type="button" className="feature-button chat-icon-button" aria-label={`${t("Revoke group binding")}: ${binding.chatName}`} title={t("Revoke group binding")} disabled={Boolean(busy)} onClick={onRevoke}>{busy === `revoke:${binding.id}` ? <GridLoader size={14} /> : <Ban size={14} aria-hidden="true" />}</button>}
      <button type="button" className="feature-button danger chat-icon-button" aria-label={`${t("Remove group binding")}: ${binding.chatName}`} title={t("Remove group binding")} disabled={Boolean(busy)} onClick={onRemove}>{busy === `remove:${binding.id}` ? <GridLoader size={14} /> : <Trash2 size={14} aria-hidden="true" />}</button>
    </aside>
  </div>;
}

export function ChatBindingEditor({ provider, data, bindableTeamIds, privateTeamIds = [], binding, fixedTeamId, fixedProjectId, defaultEvents, busy, onCancel, onSave }: {
  provider: ChatIntegrationProvider;
  data: BootstrapData;
  bindableTeamIds: string[];
  privateTeamIds?: string[];
  binding?: ChatBinding;
  /** The team page binds its own team only. */
  fixedTeamId?: string;
  /** The project dialog binds its project only. */
  fixedProjectId?: string;
  defaultEvents?: ChatIntegrationEvent[];
  busy: boolean;
  onCancel: () => void;
  onSave: (input: ChatBindingInput) => Promise<void>;
}) {
  const { t } = useI18n();
  const { chats, cursor, hasMore, loading, error, loadChats } = useBotChats(provider);
  const [chatId, setChatId] = useState(binding?.chatId ?? "");
  const [teamId, setTeamId] = useState(binding?.teamId ?? fixedTeamId ?? "");
  const [projectIds, setProjectIds] = useState<string[]>(bindingProjectIds(binding) ?? (fixedProjectId ? [fixedProjectId] : []));
  const [templateId, setTemplateId] = useState(binding?.templateId ?? "");
  const [events, setEvents] = useState<ChatIntegrationEvent[]>(binding?.events ?? defaultEvents ?? ["issue.created", "issue.status_changed", "issue.assignee_changed"]);
  const [enabled, setEnabled] = useState(binding?.enabled ?? true);
  const [allowCreate, setAllowCreate] = useState(binding?.allowCreate ?? true);
  const [allowUpdate, setAllowUpdate] = useState(binding?.allowUpdate ?? true);
  const [requestsEnabled, setRequestsEnabled] = useState(binding?.requests?.enabled ?? false);
  const [requestEmoji, setRequestEmoji] = useState(binding?.requests?.emoji ?? "");
  const [requestTemplateId, setRequestTemplateId] = useState(binding?.requests?.templateId ?? "");
  const [requestPriority, setRequestPriority] = useState(binding?.requests?.priority === undefined ? "" : String(binding.requests.priority));
  const [approved, setApproved] = useState(false);
  // The server decides which teams may be shared; see chatBindingScopePermitted.
  const bindable = new Set(bindableTeamIds);
  const teams = data.teams.filter((team) => bindable.has(team.id) && (!fixedTeamId || team.id === fixedTeamId));
  const privateTeam = privateTeamIds.includes(teamId);
  const projects = data.projects.filter((project) => !project.archivedAt && project.teamIds.includes(teamId) && project.teamIds.every((id) => id === teamId || (bindable.has(id) && !privateTeamIds.includes(id))));
  const templates = data.issueTemplates.filter((template) => template.templateType !== "customForm" && !template.formFields?.length && (!template.teamId || template.teamId === teamId) && (!template.visibilityTeamId || template.visibilityTeamId === teamId) && (!template.projectId || projectIds.includes(template.projectId)));
  const selectedChat = chats.find((chat) => chat.id === chatId);
  const chatOptions = chats.map((chat) => ({ value: chat.id, label: chat.external ? `${chat.name} (${t("External group")})` : chat.name, disabled: chat.external, entityName: true }));
  if (binding && !chatOptions.some((item) => item.value === binding.chatId)) chatOptions.unshift({ value: binding.chatId, label: binding.chatName, disabled: false, entityName: true });

  const emojiOptions = [{ value: NO_EMOJI, label: t("No reaction") }, ...REQUEST_EMOJIS.map((item) => ({ value: item.value, label: t(item.label) }))];
  if (requestEmoji && !REQUEST_EMOJIS.some((item) => item.value === requestEmoji)) emojiOptions.push({ value: requestEmoji, label: requestEmoji });
  const valid = Boolean(chatId && teamId && teams.some((team) => team.id === teamId) && projectIds.every((id) => projects.some((project) => project.id === id)) && (!templateId || templates.some((template) => template.id === templateId)) && (!requestsEnabled || !requestTemplateId || templates.some((template) => template.id === requestTemplateId)) && selectedChat?.external !== true && (selectedChat || binding?.chatId === chatId) && (events.length > 0 || allowCreate || allowUpdate || requestsEnabled) && approved);
  // Saved when turned on, or to turn off what the binding had.
  const requests: ChatBindingRequests | undefined = requestsEnabled || binding?.requests ? {
    enabled: requestsEnabled,
    ...(requestEmoji ? { emoji: requestEmoji } : {}),
    ...(requestTemplateId ? { templateId: requestTemplateId } : {}),
    ...(requestPriority ? { priority: Number(requestPriority) } : {}),
  } : undefined;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!valid) return;
    void onSave({
      ...(binding ? { id: binding.id } : {}),
      chatId, chatName: selectedChat?.name ?? binding?.chatName ?? "", teamId,
      ...(projectIds.length ? { projectIds } : {}), ...(templateId ? { templateId } : {}),
      events, enabled, allowCreate, allowUpdate, ...(requests ? { requests } : {}), authorize: true,
    });
  };
  const title = t(binding ? "Edit group binding" : "Add group binding");
  const groupStatus = loading ? t("Loading groups…") : !chats.length && !error ? t("No groups found. Add the bot to an internal group and refresh.") : t("Only internal groups can be bound.");
  const fixedTeam = fixedTeamId ? data.teams.find((team) => team.id === fixedTeamId) : undefined;
  const fixedProject = fixedProjectId ? data.projects.find((project) => project.id === fixedProjectId) : undefined;

  return <form className="feature-section chat-binding-editor" onSubmit={submit} aria-label={title}>
    <header><h2>{title}</h2><p role="status">{groupStatus}</p></header>
    <fieldset className="chat-fieldset" disabled={busy}>
      {error && <div className="feature-card chat-integration-alert" role="alert"><div className="feature-row"><div><span>{error}</span></div><aside><button type="button" className="feature-button" disabled={loading} onClick={() => void loadChats(cursor)}><RefreshCw size={14} aria-hidden="true" />{t("Retry")}</button></aside></div></div>}
      <div className="feature-card">
        <FieldRow label={t("Group")}>
          <SettingsSelect label={t("Group")} options={chatOptions} value={chatId || NO_SELECTION} placeholder={t("Select group")} disabled={loading || !chatOptions.length} onChange={(value) => { setChatId(value); setApproved(false); }} />
          <button type="button" className="feature-button chat-icon-button" aria-label={t("Refresh groups")} title={t("Refresh groups")} disabled={loading} onClick={() => void loadChats()}>{loading ? <GridLoader size={14} /> : <RefreshCw size={14} aria-hidden="true" />}</button>
        </FieldRow>
        {hasMore && <FieldRow label={<span className="chat-muted">{t("More groups are available.")}</span>}><button type="button" className="feature-button" disabled={loading || !cursor} onClick={() => void loadChats(cursor)}>{t("Load more groups")}</button></FieldRow>}
        {fixedTeamId
          ? <FieldRow label={t("Team")} description={!teams.length ? t("You can't bind groups for this team.") : undefined}><span className="chat-fixed-value" data-i18n-ignore>{fixedTeam?.name}</span></FieldRow>
          : <FieldRow label={t("Team")} description={!teams.length ? t("No teams you manage can be shared.") : undefined}><SettingsSelect label={t("Team")} options={teams.map((team) => ({ value: team.id, label: team.name, entityName: true }))} value={teamId || NO_SELECTION} placeholder={t("Select team")} disabled={!teams.length} onChange={(value) => { setTeamId(value); setProjectIds([]); setTemplateId(""); setRequestTemplateId(""); setApproved(false); }} /></FieldRow>}
        {fixedProjectId
          ? <FieldRow label={t("Project")}><span className="chat-fixed-value" data-i18n-ignore>{fixedProject?.name}</span></FieldRow>
          : <FieldRow label={t("Projects")} description={t("Updates and group actions cover the issues of these projects only.")}><ChatProjectsPicker projects={projects} selectedIds={projectIds} onChange={(ids) => { setProjectIds(ids); setTemplateId(""); setRequestTemplateId(""); setApproved(false); }} /></FieldRow>}
        <FieldRow label={t("Default template")}><SettingsSelect label={t("Default template")} options={[{ value: NO_SELECTION, label: t("No template") }, ...templates.map((template) => ({ value: template.id, label: template.name, entityName: true }))]} value={templateId || NO_SELECTION} disabled={!teamId || !allowCreate} onChange={(value) => setTemplateId(value === NO_SELECTION ? "" : value)} /></FieldRow>
      </div>
      <h3 className="chat-subheading">{t("Notifications")}</h3>
      <div className="feature-card">
        {CHAT_BINDING_EVENTS.map((item) => <FieldRow key={item.value} inline label={t(item.label)}><SettingsToggle label={t(item.label)} checked={events.includes(item.value)} onChange={(checked) => { setEvents((current) => checked ? [...current, item.value] : current.filter((value) => value !== item.value)); setApproved(false); }} /></FieldRow>)}
      </div>
      <h3 className="chat-subheading">{t("Group actions")}</h3>
      <div className="feature-card">
        <FieldRow inline label={t("Binding enabled")}><SettingsToggle label={t("Binding enabled")} checked={enabled} onChange={setEnabled} /></FieldRow>
        <FieldRow inline label={t("Allow creating issues")}><SettingsToggle label={t("Allow creating issues")} checked={allowCreate} onChange={(value) => { setAllowCreate(value); setApproved(false); }} /></FieldRow>
        <FieldRow inline label={t("Allow updating issues")}><SettingsToggle label={t("Allow updating issues")} checked={allowUpdate} onChange={(value) => { setAllowUpdate(value); setApproved(false); }} /></FieldRow>
      </div>
      <h3 className="chat-subheading">{t("Requests")}</h3>
      <div className="feature-card">
        <FieldRow inline label={t("Take requests")} description={t("Members turn a message into a request in the team's triage: mention the bot, add the reaction below, or use the message shortcut. The request follows its message's thread.")}><SettingsToggle label={t("Take requests")} checked={requestsEnabled} onChange={(value) => { setRequestsEnabled(value); setApproved(false); }} /></FieldRow>
        {requestsEnabled && <>
          <FieldRow label={t("Request reaction")} description={t("Adding this reaction to a message submits it as a request.")}><SettingsSelect label={t("Request reaction")} options={emojiOptions} value={requestEmoji || NO_EMOJI} onChange={(value) => setRequestEmoji(value === NO_EMOJI ? "" : value)} /></FieldRow>
          <FieldRow label={t("Request template")}><SettingsSelect label={t("Request template")} options={[{ value: NO_SELECTION, label: t("No template") }, ...templates.map((template) => ({ value: template.id, label: template.name, entityName: true }))]} value={requestTemplateId || NO_SELECTION} disabled={!teamId} onChange={(value) => setRequestTemplateId(value === NO_SELECTION ? "" : value)} /></FieldRow>
          <FieldRow label={t("Request priority")}><SettingsSelect label={t("Request priority")} options={[{ value: DEFAULT_PRIORITY, label: t("Team default") }, ...REQUEST_PRIORITIES.map((item) => ({ value: item.value, label: t(item.label) }))]} value={requestPriority || DEFAULT_PRIORITY} onChange={(value) => setRequestPriority(value === DEFAULT_PRIORITY ? "" : value)} /></FieldRow>
        </>}
      </div>
      <div className="feature-card chat-approval">
        {privateTeam && <div className="feature-row chat-private-notice" role="note"><div><strong><Lock size={13} aria-hidden="true" />{t("This team is private")}</strong><span>{t("Binding doesn't add anyone to the team or change who can see it in Flow. Everyone in the group sees the selected updates; group actions still use each member's own Flow permissions.")}</span></div></div>}
        <FieldRow inline label={t("Share with this group")} description={t("I approve sharing this team's selected updates with everyone in this group.")}><SettingsToggle label={t("I approve sharing this team's selected updates with everyone in this group.")} checked={approved} onChange={setApproved} /></FieldRow>
      </div>
      <div className="feature-section-action chat-actions">
        <button type="button" className="feature-button" disabled={busy} onClick={onCancel}>{t("Cancel")}</button>
        <button type="submit" className="feature-button primary" disabled={busy || !valid}>{busy ? <GridLoader size={14} /> : <Check size={14} aria-hidden="true" />}{t("Save group binding")}</button>
      </div>
    </fieldset>
  </form>;
}
