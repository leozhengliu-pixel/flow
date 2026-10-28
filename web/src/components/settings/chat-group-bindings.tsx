import { Ban, Check, Lock, Pencil, RefreshCw, Trash2 } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";

import { GridLoader } from "@/components/ui/grid-loader";
import { useI18n } from "@/i18n/i18n";
import { chatErrorMessage, fetchChatIntegrationChats } from "@/lib/chat-integrations-api";
import type { BotChat, ChatBinding, ChatBindingInput, ChatIntegrationEvent, ChatIntegrationProvider } from "@/types/chat-integrations";
import type { BootstrapData } from "@/types/flow";

import { CHAT_BINDING_EVENTS } from "./chat-binding-support";
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
  const project = binding.projectId ? data.projects.find((item) => item.id === binding.projectId) : undefined;
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
        {binding.projectId && <> / {project ? <span data-i18n-ignore>{project.name}</span> : t("Project unavailable")}</>}
        {` · ${binding.events.length === 1 ? t("1 notification type") : t("{count} notification types").replace("{count}", String(binding.events.length))}`}
        {` · ${t(binding.allowCreate ? "Creating issues allowed" : "Creating issues off")} · ${t(binding.allowUpdate ? "Updating issues allowed" : "Updating issues off")}`}
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
  const [chats, setChats] = useState<BotChat[]>([]);
  const [cursor, setCursor] = useState<string>();
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [chatId, setChatId] = useState(binding?.chatId ?? "");
  const [teamId, setTeamId] = useState(binding?.teamId ?? fixedTeamId ?? "");
  const [projectId, setProjectId] = useState(binding?.projectId ?? fixedProjectId ?? "");
  const [templateId, setTemplateId] = useState(binding?.templateId ?? "");
  const [events, setEvents] = useState<ChatIntegrationEvent[]>(binding?.events ?? defaultEvents ?? ["issue.created", "issue.status_changed", "issue.assignee_changed"]);
  const [enabled, setEnabled] = useState(binding?.enabled ?? true);
  const [allowCreate, setAllowCreate] = useState(binding?.allowCreate ?? true);
  const [allowUpdate, setAllowUpdate] = useState(binding?.allowUpdate ?? true);
  const [approved, setApproved] = useState(false);
  const mounted = useRef(true);
  // The server decides which teams may be shared; see chatBindingScopePermitted.
  const bindable = new Set(bindableTeamIds);
  const teams = data.teams.filter((team) => bindable.has(team.id) && (!fixedTeamId || team.id === fixedTeamId));
  const privateTeam = privateTeamIds.includes(teamId);
  const projects = data.projects.filter((project) => !project.archivedAt && project.teamIds.includes(teamId) && project.teamIds.every((id) => id === teamId || (bindable.has(id) && !privateTeamIds.includes(id))));
  const templates = data.issueTemplates.filter((template) => template.templateType !== "customForm" && !template.formFields?.length && (!template.teamId || template.teamId === teamId) && (!template.visibilityTeamId || template.visibilityTeamId === teamId) && (!template.projectId || template.projectId === projectId));
  const selectedChat = chats.find((chat) => chat.id === chatId);
  const chatOptions = chats.map((chat) => ({ value: chat.id, label: chat.external ? `${chat.name} (${t("External group")})` : chat.name, disabled: chat.external, entityName: true }));
  if (binding && !chatOptions.some((item) => item.value === binding.chatId)) chatOptions.unshift({ value: binding.chatId, label: binding.chatName, disabled: false, entityName: true });

  const loadChats = useCallback(async (nextCursor?: string) => {
    setLoading(true);
    setError("");
    try {
      const page = await fetchChatIntegrationChats(provider, nextCursor);
      if (!mounted.current) return;
      setChats((current) => {
        const unique = new Map((nextCursor ? current : []).map((chat) => [chat.id, chat]));
        for (const chat of page.items) unique.set(chat.id, chat);
        return [...unique.values()];
      });
      setHasMore(page.hasMore);
      setCursor(page.pageToken);
      if (page.hasMore && !page.pageToken) setError(t("The group list could not be continued. Refresh and try again."));
    } catch (cause) {
      if (mounted.current) setError(chatErrorMessage(cause, t, "Could not load groups"));
    } finally { if (mounted.current) setLoading(false); }
  }, [provider, t]);

  useEffect(() => {
    mounted.current = true;
    // load() flips the loading state up front; start it from a microtask so the effect itself never sets state.
    queueMicrotask(() => { if (mounted.current) void loadChats(); });
    return () => { mounted.current = false; };
  }, [loadChats]);

  const valid = Boolean(chatId && teamId && teams.some((team) => team.id === teamId) && (!projectId || projects.some((project) => project.id === projectId)) && (!templateId || templates.some((template) => template.id === templateId)) && selectedChat?.external !== true && (selectedChat || binding?.chatId === chatId) && (events.length > 0 || allowCreate || allowUpdate) && approved);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!valid) return;
    void onSave({
      ...(binding ? { id: binding.id } : {}),
      chatId, chatName: selectedChat?.name ?? binding?.chatName ?? "", teamId,
      ...(projectId ? { projectId } : {}), ...(templateId ? { templateId } : {}),
      events, enabled, allowCreate, allowUpdate, authorize: true,
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
          : <FieldRow label={t("Team")} description={!teams.length ? t("No teams you manage can be shared.") : undefined}><SettingsSelect label={t("Team")} options={teams.map((team) => ({ value: team.id, label: team.name, entityName: true }))} value={teamId || NO_SELECTION} placeholder={t("Select team")} disabled={!teams.length} onChange={(value) => { setTeamId(value); setProjectId(""); setTemplateId(""); setApproved(false); }} /></FieldRow>}
        {fixedProjectId
          ? <FieldRow label={t("Project")}><span className="chat-fixed-value" data-i18n-ignore>{fixedProject?.name}</span></FieldRow>
          : <FieldRow label={t("Project")}><SettingsSelect label={t("Project")} options={[{ value: NO_SELECTION, label: t("All team projects") }, ...projects.map((project) => ({ value: project.id, label: project.name, entityName: true }))]} value={projectId || NO_SELECTION} disabled={!teamId} onChange={(value) => { setProjectId(value === NO_SELECTION ? "" : value); setTemplateId(""); setApproved(false); }} /></FieldRow>}
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
