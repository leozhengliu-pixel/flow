import { ArrowLeft, Check, Copy, ExternalLink, Link2, Pencil, Plus, RefreshCw, Save, Trash2, Unplug } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { NavLink } from "react-router-dom";

import { confirmAction } from "@/components/ui/action-dialog-service";
import { GridLoader } from "@/components/ui/grid-loader";
import { useI18n } from "@/i18n/i18n";
import {
  CHAT_ERROR_MESSAGES,
  chatErrorCodeMessage,
  chatErrorMessage,
  disconnectChatIntegration,
  fetchChatIntegration,
  fetchChatIntegrationChats,
  removeChatBinding,
  saveChatBinding,
  saveChatIntegration,
  testChatIntegration,
} from "@/lib/chat-integrations-api";
import { chatProviderName } from "@/lib/integration-catalog";
import { isWorkspaceAdmin } from "@/lib/settings-access";
import { settingsPath } from "@/lib/app-routes";
import { toast } from "@/lib/toast";
import type { BotChat, ChatBinding, ChatBindingInput, ChatIntegrationEvent, ChatIntegrationProvider, ChatIntegrationSettings as Settings } from "@/types/chat-integrations";
import type { BootstrapData } from "@/types/flow";

import { IntegrationBrandIcon } from "./integration-brand-icon";
import { SettingsSelect, SettingsToggle } from "./settings-primitives";
import "./feature-settings.css";
import "./chat-integration-settings.css";

const EVENTS: { value: ChatIntegrationEvent; label: string }[] = [
  { value: "issue.created", label: "Issue created" },
  { value: "issue.status_changed", label: "Status changed" },
  { value: "issue.assignee_changed", label: "Assignee changed" },
  { value: "comment.created", label: "Comment added" },
  { value: "project.update_created", label: "Project update published" },
];
// Radix Select shows its trigger text only for a non-empty value; this one matches no option.
const NO_SELECTION = "__none__";

/** A settings row: copy on the left, the control on the right (shared feature-row layout). */
function FieldRow({ id, label, description, inline, children }: { id?: string; label: ReactNode; description?: ReactNode; inline?: boolean; children?: ReactNode }) {
  return <div className={`feature-row${inline ? " chat-inline" : ""}`}>
    <div><strong>{id ? <label htmlFor={id}>{label}</label> : label}</strong>{description && <span>{description}</span>}</div>
    {children && <aside>{children}</aside>}
  </div>;
}

export function ChatIntegrationSettings({ provider, data, onBack, onReload }: {
  provider: ChatIntegrationProvider;
  data: BootstrapData;
  onBack: () => void;
  onReload: () => Promise<void>;
}) {
  const { t, formatDate } = useI18n();
  const name = chatProviderName(provider, t);
  const docs = provider === "feishu" ? "https://open.feishu.cn/app" : "https://open.larksuite.com/app";
  const [settings, setSettings] = useState<Settings | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [appId, setAppId] = useState("");
  const [appSecret, setAppSecret] = useState("");
  const [encryptKey, setEncryptKey] = useState("");
  const [verificationToken, setVerificationToken] = useState("");
  const [editing, setEditing] = useState<ChatBinding | "new" | null>(null);
  const mounted = useRef(true);
  const canManage = isWorkspaceAdmin(data) && settings?.canManage === true;
  const connection = settings?.connection;
  const connected = connection?.status === "connected";
  const sharingBlocked = Boolean(data.workspaceSettings.hipaaCompliance);

  const load = useCallback(async () => {
    const next = await fetchChatIntegration(provider);
    if (mounted.current) setSettings(next);
    return next;
  }, [provider]);

  useEffect(() => {
    mounted.current = true;
    void load().then((next) => {
      if (!mounted.current) return;
      setAppId(next.connection?.config?.appId ?? "");
    }).catch((cause: unknown) => {
      if (mounted.current) setError(chatErrorMessage(cause, t, "Could not load integration"));
    }).finally(() => {
      if (mounted.current) setLoading(false);
    });
    return () => { mounted.current = false; };
  }, [load, t]);

  const reload = async () => {
    setLoading(true);
    setError("");
    try {
      const next = await load();
      if (next.connection) setAppId(next.connection.config?.appId ?? "");
    }
    catch (cause) { setError(chatErrorMessage(cause, t, "Could not load integration")); }
    finally { setLoading(false); }
  };

  const mutate = async (action: string, operation: () => Promise<unknown>, success: string, after?: () => void) => {
    setBusy(action);
    setError("");
    try {
      await operation();
      after?.();
      toast.success(t(success));
      await load();
      await onReload();
    } catch (cause) {
      setError(chatErrorMessage(cause, t, "Could not update integration"));
    } finally {
      setBusy("");
    }
  };

  const saveCredentials = (event: FormEvent) => {
    event.preventDefault();
    void mutate("credentials", () => saveChatIntegration(provider, {
      name: provider === "feishu" ? "Feishu" : "Lark",
      appId: appId.trim(),
      ...(appSecret ? { appSecret } : {}),
      ...(encryptKey ? { encryptKey } : {}),
      ...(verificationToken ? { verificationToken } : {}),
    }), "Application settings saved", () => {
      setAppSecret("");
      setEncryptKey("");
      setVerificationToken("");
      setEditing(null);
    });
  };

  const disconnect = async () => {
    if (!await confirmAction(t("Disconnect {name}?").replace("{name}", name), {
      description: t("Group notifications and issue actions will stop. Group and account bindings will be removed."),
      confirmLabel: t("Disconnect"), danger: true,
    })) return;
    await mutate("disconnect", () => disconnectChatIntegration(provider), "Integration disconnected", () => {
      setAppId("");
      setAppSecret("");
      setEncryptKey("");
      setVerificationToken("");
      setEditing(null);
    });
  };

  const removeBinding = async (binding: ChatBinding) => {
    if (!await confirmAction(t("Remove group binding?"), {
      description: binding.chatName,
      confirmLabel: t("Remove"), danger: true,
    })) return;
    await mutate(`remove:${binding.id}`, () => removeChatBinding(provider, binding.id), "Group binding removed");
  };

  const copyWebhook = async () => {
    try {
      await navigator.clipboard.writeText(settings?.webhookURL ?? "");
      toast.success(t("Copied to clipboard"));
    } catch { setError(t("Could not copy callback URL")); }
  };

  const connectionError = connection?.lastError ? chatErrorCodeMessage(settings?.lastErrorCode, t) ?? t(CHAT_ERROR_MESSAGES.chat_error) : "";
  const status = t(connected ? "Connected" : connection?.status === "error" ? "Connection failed" : connection ? "Configured" : "Not connected");
  const statusDescription = connectionError || t(connected ? "The application and workspace are linked." : settings?.credentialsConfigured ? "Link an administrator account in Connected accounts to verify the workspace tenant." : "A workspace administrator needs to configure the application.");
  const secretPlaceholder = settings?.credentialsConfigured ? t("Leave blank to keep existing secret") : "";

  return (
    <div className="feature-integrations chat-integration-settings">
      <div className="feature-settings">
        <header className="feature-header">
          <button type="button" className="feature-back-link" onClick={onBack}><ArrowLeft size={14} aria-hidden="true" />{t("Integrations")}</button>
          <div className="integration-settings-title">
            <span className="chat-integration-logo"><IntegrationBrandIcon provider={provider} size={28} /></span>
            <div><h1 data-i18n-ignore>{name}</h1><p>{t("Get issue updates and manage issues from group chats")}</p></div>
            <button type="button" className="feature-button chat-icon-button" aria-label={t("Refresh integration")} title={t("Refresh integration")} disabled={loading || Boolean(busy)} onClick={() => void reload()}>{loading && settings ? <GridLoader size={14} /> : <RefreshCw size={16} aria-hidden="true" />}</button>
          </div>
        </header>

        {error && <div className="feature-card chat-integration-alert" role="alert"><div className="feature-row"><div><span>{error}</span></div><aside><button type="button" className="feature-button" disabled={loading || Boolean(busy)} onClick={() => void reload()}><RefreshCw size={14} aria-hidden="true" />{t("Retry")}</button></aside></div></div>}
        {loading && !settings && <div className="feature-empty" role="status"><GridLoader size={16} /><h3>{t("Loading integration…")}</h3></div>}

        {settings && <>
          <section className="feature-section" aria-label={t("Connection")}>
            <header><h2>{t("Connection")}</h2></header>
            <div className="feature-card">
              <div className="feature-row chat-connection-row">
                <div>
                  <strong><span className={`chat-status-dot is-${connected ? "connected" : connection?.status === "error" || connectionError ? "error" : connection ? "pending" : "none"}`} aria-hidden="true" />{status}</strong>
                  <span>{statusDescription}</span>
                  {connection?.lastTestAt && <span>{t("Last checked")}: {formatDate(connection.lastTestAt, { dateStyle: "medium", timeStyle: "short" })}</span>}
                </div>
                {canManage && connection && <aside className="chat-actions">
                  {!connected && settings.credentialsConfigured && <NavLink className="feature-button" to={settingsPath(data.workspace.urlKey, "connections")}><Link2 size={14} aria-hidden="true" />{t("Verify workspace")}</NavLink>}
                  <button type="button" className="feature-button" disabled={Boolean(busy)} onClick={() => void mutate("test", () => testChatIntegration(provider), "Connection checked")}>{busy === "test" ? <GridLoader size={14} /> : <RefreshCw size={14} aria-hidden="true" />}{t("Test connection")}</button>
                  <button type="button" className="feature-button danger" disabled={Boolean(busy)} onClick={() => void disconnect()}><Unplug size={14} aria-hidden="true" />{t("Disconnect")}</button>
                </aside>}
              </div>
            </div>
          </section>

          {canManage && <section className="feature-section" aria-label={t("Application settings")}>
            <header>
              <h2>{t("Application settings")}</h2>
              <p>{t(connection ? "To use another application, disconnect this integration first." : "Publish the application with bot and group access enabled.")} <a className="feature-docs-link" href={docs} rel="noreferrer" target="_blank">{t("Open developer console")}<ExternalLink size={12} aria-hidden="true" /></a></p>
            </header>
            <form onSubmit={saveCredentials}>
              <fieldset className="chat-fieldset" disabled={Boolean(busy)}>
                <div className="feature-card">
                  <FieldRow id={`${provider}-app-id`} label={t("App ID")}><input id={`${provider}-app-id`} className="settings-input" autoComplete="off" spellCheck={false} required readOnly={Boolean(connection)} value={appId} onChange={(event) => setAppId(event.target.value)} /></FieldRow>
                  <FieldRow id={`${provider}-app-secret`} label={t("App Secret")}><input id={`${provider}-app-secret`} className="settings-input" type="password" autoComplete="new-password" required={!settings.credentialsConfigured} placeholder={secretPlaceholder} value={appSecret} onChange={(event) => setAppSecret(event.target.value)} /></FieldRow>
                  <FieldRow id={`${provider}-encrypt-key`} label={t("Encrypt Key")}><input id={`${provider}-encrypt-key`} className="settings-input" type="password" autoComplete="new-password" required={!settings.credentialsConfigured} placeholder={secretPlaceholder} value={encryptKey} onChange={(event) => setEncryptKey(event.target.value)} /></FieldRow>
                  <FieldRow id={`${provider}-verification-token`} label={t("Verification Token")}><input id={`${provider}-verification-token`} className="settings-input" type="password" autoComplete="new-password" required={!settings.credentialsConfigured} placeholder={secretPlaceholder} value={verificationToken} onChange={(event) => setVerificationToken(event.target.value)} /></FieldRow>
                  {settings.webhookURL && <FieldRow id={`${provider}-callback-url`} label={t("Event and card callback URL")}>
                    <input id={`${provider}-callback-url`} className="settings-input" readOnly data-i18n-ignore value={settings.webhookURL} onFocus={(event) => event.target.select()} />
                    <button type="button" className="feature-button chat-icon-button" aria-label={t("Copy callback URL")} title={t("Copy callback URL")} onClick={() => void copyWebhook()}><Copy size={14} aria-hidden="true" /></button>
                  </FieldRow>}
                </div>
                <div className="feature-section-action"><button type="submit" className="feature-button primary" disabled={Boolean(busy) || !appId.trim()}>{busy === "credentials" ? <GridLoader size={14} /> : <Save size={14} aria-hidden="true" />}{t("Save settings")}</button></div>
              </fieldset>
            </form>
          </section>}

          {canManage && <section className="feature-section" aria-label={t("Group bindings")}>
            <header><h2>{t("Group bindings")}</h2><p>{t(sharingBlocked ? "Workspace policy prevents sharing updates with chat applications." : "Share selected public team updates with approved internal groups.")}</p></header>
            <div className="feature-card">
              {settings.bindings.length === 0 && <FieldRow label={<span className="chat-muted">{t(connected ? "No groups are bound." : "Connect the application before adding groups.")}</span>} />}
              {settings.bindings.map((binding) => {
                const team = data.teams.find((item) => item.id === binding.teamId);
                const project = binding.projectId ? data.projects.find((item) => item.id === binding.projectId) : undefined;
                return <div className="feature-row chat-inline" key={binding.id}>
                  <div>
                    <strong><span className="chat-binding-name" title={binding.chatName} data-i18n-ignore>{binding.chatName}</span>{!binding.enabled && <small>{t("Paused")}</small>}</strong>
                    <span className="chat-binding-scope">
                      {team ? <span data-i18n-ignore>{team.name}</span> : t("Team unavailable")}
                      {binding.projectId && <> / {project ? <span data-i18n-ignore>{project.name}</span> : t("Project unavailable")}</>}
                      {` · ${binding.events.length === 1 ? t("1 notification type") : t("{count} notification types").replace("{count}", String(binding.events.length))}`}
                    </span>
                  </div>
                  <aside>
                    <button type="button" className="feature-button chat-icon-button" aria-label={`${t("Edit group binding")}: ${binding.chatName}`} title={t("Edit group binding")} disabled={Boolean(busy) || editing !== null || !connected || sharingBlocked} onClick={() => setEditing(binding)}><Pencil size={14} aria-hidden="true" /></button>
                    <button type="button" className="feature-button danger chat-icon-button" aria-label={`${t("Remove group binding")}: ${binding.chatName}`} title={t("Remove group binding")} disabled={Boolean(busy) || editing !== null} onClick={() => void removeBinding(binding)}>{busy === `remove:${binding.id}` ? <GridLoader size={14} /> : <Trash2 size={14} aria-hidden="true" />}</button>
                  </aside>
                </div>;
              })}
            </div>
            {!editing && <div className="feature-section-action"><button type="button" className="feature-button" disabled={!connected || Boolean(busy) || sharingBlocked} onClick={() => setEditing("new")}><Plus size={14} aria-hidden="true" />{t("Add group")}</button></div>}
          </section>}

          {canManage && editing && !sharingBlocked && <ChatBindingEditor key={editing === "new" ? "new" : editing.id} provider={provider} data={data} bindableTeamIds={settings.bindableTeamIds ?? []} binding={editing === "new" ? undefined : editing} busy={Boolean(busy)} onCancel={() => setEditing(null)} onSave={(input) => mutate("binding", () => saveChatBinding(provider, input), "Group binding saved", () => setEditing(null))} />}
        </>}
      </div>
    </div>
  );
}

function ChatBindingEditor({ provider, data, bindableTeamIds, binding, busy, onCancel, onSave }: {
  provider: ChatIntegrationProvider;
  data: BootstrapData;
  bindableTeamIds: string[];
  binding?: ChatBinding;
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
  const [teamId, setTeamId] = useState(binding?.teamId ?? "");
  const [projectId, setProjectId] = useState(binding?.projectId ?? "");
  const [templateId, setTemplateId] = useState(binding?.templateId ?? "");
  const [events, setEvents] = useState<ChatIntegrationEvent[]>(binding?.events ?? ["issue.created", "issue.status_changed", "issue.assignee_changed"]);
  const [enabled, setEnabled] = useState(binding?.enabled ?? true);
  const [allowCreate, setAllowCreate] = useState(binding?.allowCreate ?? true);
  const [allowUpdate, setAllowUpdate] = useState(binding?.allowUpdate ?? true);
  const [approved, setApproved] = useState(false);
  const mounted = useRef(true);
  // The server decides which teams may be shared; see chatBindingScopePermitted.
  const bindable = new Set(bindableTeamIds);
  const teams = data.teams.filter((team) => bindable.has(team.id));
  const projects = data.projects.filter((project) => !project.archivedAt && project.teamIds.includes(teamId) && project.teamIds.every((id) => bindable.has(id)));
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
    void loadChats();
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
      events, enabled, allowCreate, allowUpdate,
    });
  };
  const title = t(binding ? "Edit group binding" : "Add group binding");
  const groupStatus = loading ? t("Loading groups…") : !chats.length && !error ? t("No groups found. Add the bot to an internal group and refresh.") : t("Only internal groups can be bound.");

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
        <FieldRow label={t("Team")} description={!teams.length ? t("No public teams can be shared.") : undefined}><SettingsSelect label={t("Team")} options={teams.map((team) => ({ value: team.id, label: team.name, entityName: true }))} value={teamId || NO_SELECTION} placeholder={t("Select team")} disabled={!teams.length} onChange={(value) => { setTeamId(value); setProjectId(""); setTemplateId(""); setApproved(false); }} /></FieldRow>
        <FieldRow label={t("Project")}><SettingsSelect label={t("Project")} options={[{ value: NO_SELECTION, label: t("All team projects") }, ...projects.map((project) => ({ value: project.id, label: project.name, entityName: true }))]} value={projectId || NO_SELECTION} disabled={!teamId} onChange={(value) => { setProjectId(value === NO_SELECTION ? "" : value); setTemplateId(""); setApproved(false); }} /></FieldRow>
        <FieldRow label={t("Default template")}><SettingsSelect label={t("Default template")} options={[{ value: NO_SELECTION, label: t("No template") }, ...templates.map((template) => ({ value: template.id, label: template.name, entityName: true }))]} value={templateId || NO_SELECTION} disabled={!teamId || !allowCreate} onChange={(value) => setTemplateId(value === NO_SELECTION ? "" : value)} /></FieldRow>
      </div>
      <h3 className="chat-subheading">{t("Notifications")}</h3>
      <div className="feature-card">
        {EVENTS.map((item) => <FieldRow key={item.value} inline label={t(item.label)}><SettingsToggle label={t(item.label)} checked={events.includes(item.value)} onChange={(checked) => setEvents((current) => checked ? [...current, item.value] : current.filter((value) => value !== item.value))} /></FieldRow>)}
      </div>
      <h3 className="chat-subheading">{t("Group actions")}</h3>
      <div className="feature-card">
        <FieldRow inline label={t("Binding enabled")}><SettingsToggle label={t("Binding enabled")} checked={enabled} onChange={setEnabled} /></FieldRow>
        <FieldRow inline label={t("Allow creating issues")}><SettingsToggle label={t("Allow creating issues")} checked={allowCreate} onChange={setAllowCreate} /></FieldRow>
        <FieldRow inline label={t("Allow updating issues")}><SettingsToggle label={t("Allow updating issues")} checked={allowUpdate} onChange={setAllowUpdate} /></FieldRow>
      </div>
      <div className="feature-card chat-approval">
        <FieldRow inline label={t("Share with this group")} description={t("I approve sharing this team's selected updates with everyone in this group.")}><SettingsToggle label={t("I approve sharing this team's selected updates with everyone in this group.")} checked={approved} onChange={setApproved} /></FieldRow>
      </div>
      <div className="feature-section-action chat-actions">
        <button type="button" className="feature-button" disabled={busy} onClick={onCancel}>{t("Cancel")}</button>
        <button type="submit" className="feature-button primary" disabled={busy || !valid}>{busy ? <GridLoader size={14} /> : <Check size={14} aria-hidden="true" />}{t("Save group binding")}</button>
      </div>
    </fieldset>
  </form>;
}
