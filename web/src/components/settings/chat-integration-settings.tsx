import { ArrowLeft, Check, Copy, ExternalLink, Link2, Pencil, Plus, RefreshCw, Save, Trash2, Unplug, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { NavLink } from "react-router-dom";

import { confirmAction } from "@/components/ui/action-dialog-service";
import { GridLoader } from "@/components/ui/grid-loader";
import { Toggle } from "@/components/ui/toggle";
import { useI18n } from "@/i18n/i18n";
import {
  disconnectChatIntegration,
  fetchChatIntegration,
  fetchChatIntegrationChats,
  removeChatBinding,
  saveChatBinding,
  saveChatIntegration,
  testChatIntegration,
} from "@/lib/chat-integrations-api";
import { isWorkspaceAdmin } from "@/lib/settings-access";
import { settingsPath } from "@/lib/app-routes";
import { toast } from "@/lib/toast";
import type { BotChat, ChatBinding, ChatBindingInput, ChatIntegrationEvent, ChatIntegrationProvider, ChatIntegrationSettings as Settings } from "@/types/chat-integrations";
import type { BootstrapData } from "@/types/flow";

import { IntegrationBrandIcon } from "./integration-brand-icon";
import { SettingsSelect } from "./settings-primitives";
import "./feature-settings.css";
import "./chat-integration-settings.css";

const EVENTS: { value: ChatIntegrationEvent; label: string }[] = [
  { value: "issue.created", label: "Issue created" },
  { value: "issue.status_changed", label: "Status changed" },
  { value: "issue.assignee_changed", label: "Assignee changed" },
  { value: "comment.created", label: "Comment added" },
  { value: "project.update_created", label: "Project update published" },
];
const NO_SELECTION = "__none__";

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

function publicChatTeam(data: BootstrapData, teamId: string): boolean {
  const seen = new Set<string>();
  let current = teamId;
  while (current) {
    if (seen.has(current)) return false;
    seen.add(current);
    const team = data.teams.find((item) => item.id === current);
    const settings = data.teamSettings?.[current];
    if (!team || team.private || team.archivedAt || team.retiredAt || ["private", "restricted"].includes(settings?.access?.trim().toLowerCase() ?? "") || ["members", "owners"].includes(settings?.membershipRestriction?.trim().toLowerCase() ?? "")) return false;
    current = settings?.parentTeamId ?? "";
  }
  return Boolean(teamId);
}

export function ChatIntegrationSettings({ provider, data, onBack, onReload }: {
  provider: ChatIntegrationProvider;
  data: BootstrapData;
  onBack: () => void;
  onReload: () => Promise<void>;
}) {
  const { t, formatDate } = useI18n();
  const name = provider === "feishu" ? "Feishu" : "Lark";
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
      if (mounted.current) setError(errorMessage(cause, t("Could not load integration")));
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
    catch (cause) { setError(errorMessage(cause, t("Could not load integration"))); }
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
      setError(errorMessage(cause, t("Could not update integration")));
    } finally {
      setBusy("");
    }
  };

  const saveCredentials = (event: FormEvent) => {
    event.preventDefault();
    void mutate("credentials", () => saveChatIntegration(provider, {
      name,
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
    if (!await confirmAction(t("Disconnect integration?"), {
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
    } catch (cause) { setError(errorMessage(cause, t("Could not copy callback URL"))); }
  };

  return (
    <div className="feature-integrations chat-integration-settings">
      <div className="feature-settings">
        <header className="feature-header">
          <button type="button" className="feature-back-link" onClick={onBack}><ArrowLeft size={14} aria-hidden="true" />{t("Integrations")}</button>
          <div className="integration-settings-title">
            <span className="chat-integration-logo"><IntegrationBrandIcon provider={provider} size={28} /></span>
            <div><h1 data-i18n-ignore>{name}</h1><p>{t("Get issue updates and manage issues from group chats")}</p></div>
            <button type="button" className="feature-button chat-icon-button" aria-label={t("Refresh integration")} title={t("Refresh integration")} disabled={loading || Boolean(busy)} onClick={() => void reload()}><RefreshCw size={15} /></button>
          </div>
        </header>

        {error && <div className="chat-integration-error" role="alert"><span>{error}</span><button type="button" className="feature-button" disabled={loading || Boolean(busy)} onClick={() => void reload()}><RefreshCw size={14} />{t("Retry")}</button></div>}
        {loading && !settings && <div className="chat-integration-loading" role="status"><GridLoader size={16} />{t("Loading integration…")}</div>}

        {settings && <>
          <section className="feature-section" aria-label={t("Connection")}>
            <header><h2>{t("Connection")}</h2></header>
            <div className="chat-integration-summary">
              <div><strong>{t(connected ? "Connected" : connection?.status === "error" ? "Connection failed" : connection ? "Configured" : "Not connected")}</strong>
                <p>{connection?.lastError || t(connected ? "The application and workspace are linked." : settings.credentialsConfigured ? "Link an administrator account in Connected accounts to verify the workspace tenant." : "A workspace administrator needs to configure the application.")}</p>
                {connection?.lastTestAt && <small>{t("Last checked")}: {formatDate(connection.lastTestAt, { dateStyle: "medium", timeStyle: "short" })}</small>}
              </div>
              {canManage && connection && <div className="chat-integration-actions">
                {!connected && settings.credentialsConfigured && <NavLink className="feature-button" to={settingsPath(data.workspace.urlKey, "connections")}><Link2 size={14} />{t("Verify workspace")}</NavLink>}
                <button type="button" className="feature-button" disabled={Boolean(busy)} onClick={() => void mutate("test", () => testChatIntegration(provider), "Connection checked")}><RefreshCw size={14} />{t("Test connection")}</button>
                <button type="button" className="feature-button danger" disabled={Boolean(busy)} onClick={() => void disconnect()}><Unplug size={14} />{t("Disconnect")}</button>
              </div>}
            </div>
          </section>

          {canManage && <section className="feature-section" aria-label={t("Application settings")}>
            <header className="chat-section-header"><h2>{t("Application settings")}</h2><a href={docs} rel="noreferrer" target="_blank">{t("Open developer console")}<ExternalLink size={13} /></a></header>
            <form className="chat-application-form" onSubmit={saveCredentials}>
              <fieldset disabled={Boolean(busy)}>
                <label>{t("App ID")}<input aria-label={t("App ID")} autoComplete="off" required readOnly={Boolean(connection)} value={appId} onChange={(event) => setAppId(event.target.value)} /></label>
                <label>{t("App Secret")}<input aria-label={t("App Secret")} type="password" autoComplete="new-password" required={!settings.credentialsConfigured} placeholder={settings.credentialsConfigured ? t("Leave blank to keep existing secret") : ""} value={appSecret} onChange={(event) => setAppSecret(event.target.value)} /></label>
                <label>{t("Encrypt Key")}<input aria-label={t("Encrypt Key")} type="password" autoComplete="new-password" required={!settings.credentialsConfigured} placeholder={settings.credentialsConfigured ? t("Leave blank to keep existing secret") : ""} value={encryptKey} onChange={(event) => setEncryptKey(event.target.value)} /></label>
                <label>{t("Verification Token")}<input aria-label={t("Verification Token")} type="password" autoComplete="new-password" required={!settings.credentialsConfigured} placeholder={settings.credentialsConfigured ? t("Leave blank to keep existing secret") : ""} value={verificationToken} onChange={(event) => setVerificationToken(event.target.value)} /></label>
              </fieldset>
              <div className="chat-form-footer"><span>{t(connection ? "To use another application, disconnect this integration first." : "Publish the application with bot and group access enabled.")}</span><button type="submit" className="feature-button primary" disabled={Boolean(busy) || !appId.trim()}>{busy === "credentials" ? <GridLoader size={14} /> : <Save size={14} />}{t("Save settings")}</button></div>
            </form>
            {settings.webhookURL && <div className="chat-callback-field"><label>{t("Event and card callback URL")}<input aria-label={t("Event and card callback URL")} readOnly value={settings.webhookURL} /></label><button type="button" className="feature-button chat-icon-button" aria-label={t("Copy callback URL")} title={t("Copy callback URL")} onClick={() => void copyWebhook()}><Copy size={15} /></button></div>}
          </section>}

          {canManage && <section className="feature-section" aria-label={t("Group bindings")}>
            <header className="chat-section-header"><div><h2>{t("Group bindings")}</h2><p>{t(sharingBlocked ? "Workspace policy prevents sharing updates with chat applications." : "Share selected public team updates with approved internal groups.")}</p></div><button type="button" className="feature-button" disabled={!connected || Boolean(busy) || editing !== null || sharingBlocked} onClick={() => setEditing("new")}><Plus size={14} />{t("Add group")}</button></header>
            {settings.bindings.length === 0 && !editing && <p className="chat-integration-empty">{t(connected ? "No groups are bound." : "Connect the application before adding groups.")}</p>}
            <div className="chat-binding-list">
              {settings.bindings.map((binding) => <div className="chat-binding-row" key={binding.id}>
                <div><strong data-i18n-ignore>{binding.chatName}</strong><span data-i18n-ignore>{data.teams.find((team) => team.id === binding.teamId)?.name ?? t("Team unavailable")}{binding.projectId ? ` / ${data.projects.find((project) => project.id === binding.projectId)?.name ?? t("Project unavailable")}` : ""}</span><small>{t(binding.enabled ? "Enabled" : "Paused")}{` · ${binding.events.length} ${t("notification types")}`}</small></div>
                <div className="chat-integration-actions"><button type="button" className="feature-button chat-icon-button" aria-label={`${t("Edit group binding")}: ${binding.chatName}`} title={t("Edit group binding")} disabled={Boolean(busy) || editing !== null || !connected || sharingBlocked} onClick={() => setEditing(binding)}><Pencil size={14} /></button><button type="button" className="feature-button danger chat-icon-button" aria-label={`${t("Remove group binding")}: ${binding.chatName}`} title={t("Remove group binding")} disabled={Boolean(busy) || editing !== null} onClick={() => void removeBinding(binding)}><Trash2 size={14} /></button></div>
              </div>)}
            </div>
            {editing && !sharingBlocked && <ChatBindingEditor key={editing === "new" ? "new" : editing.id} provider={provider} data={data} binding={editing === "new" ? undefined : editing} busy={Boolean(busy)} onCancel={() => setEditing(null)} onSave={(input) => mutate("binding", () => saveChatBinding(provider, input), "Group binding saved", () => setEditing(null))} />}
          </section>}
        </>}
      </div>
    </div>
  );
}

function ChatBindingEditor({ provider, data, binding, busy, onCancel, onSave }: {
  provider: ChatIntegrationProvider;
  data: BootstrapData;
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
  const teams = data.teams.filter((team) => publicChatTeam(data, team.id));
  const publicTeamIDs = new Set(teams.map((team) => team.id));
  const projects = data.projects.filter((project) => !project.archivedAt && project.teamIds.includes(teamId) && project.teamIds.every((id) => publicTeamIDs.has(id)));
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
      if (mounted.current) setError(errorMessage(cause, t("Could not load groups")));
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

  return <form className="chat-binding-editor" onSubmit={submit} aria-label={t(binding ? "Edit group binding" : "Add group binding")}>
    <div className="chat-section-header"><h3>{t(binding ? "Edit group binding" : "Add group binding")}</h3><button type="button" className="feature-button chat-icon-button" aria-label={t("Cancel group binding")} title={t("Cancel")} disabled={busy} onClick={onCancel}><X size={15} /></button></div>
    <fieldset disabled={busy}>
      <div className="chat-binding-fields">
        <label>{t("Group")}<SettingsSelect label={t("Group")} align="start" options={chatOptions} value={chatId} placeholder={t("Select group")} disabled={loading || !chatOptions.length} onChange={(value) => { setChatId(value); setApproved(false); }} /></label>
        <label>{t("Team")}<SettingsSelect label={t("Team")} align="start" options={teams.map((team) => ({ value: team.id, label: team.name, entityName: true }))} value={teamId} placeholder={t("Select team")} disabled={!teams.length} onChange={(value) => { setTeamId(value); setProjectId(""); setTemplateId(""); setApproved(false); }} /></label>
        <label>{t("Project")}<SettingsSelect label={t("Project")} align="start" options={[{ value: NO_SELECTION, label: t("All team projects") }, ...projects.map((project) => ({ value: project.id, label: project.name, entityName: true }))]} value={projectId || NO_SELECTION} disabled={!teamId} onChange={(value) => { setProjectId(value === NO_SELECTION ? "" : value); setTemplateId(""); setApproved(false); }} /></label>
        <label>{t("Default template")}<SettingsSelect label={t("Default template")} align="start" options={[{ value: NO_SELECTION, label: t("No template") }, ...templates.map((template) => ({ value: template.id, label: template.name, entityName: true }))]} value={templateId || NO_SELECTION} disabled={!teamId || !allowCreate} onChange={(value) => setTemplateId(value === NO_SELECTION ? "" : value)} /></label>
      </div>
      <div className="chat-group-pagination"><span role="status">{loading ? t("Loading groups…") : !chats.length ? t("No groups found. Add the bot to an internal group and refresh.") : t("Only internal groups can be bound.")}</span><button type="button" className="feature-button chat-icon-button" aria-label={t("Refresh groups")} title={t("Refresh groups")} disabled={loading} onClick={() => void loadChats()}><RefreshCw size={14} /></button>{hasMore && <button type="button" className="feature-button" disabled={loading || !cursor} onClick={() => void loadChats(cursor)}>{t("Load more groups")}</button>}</div>
      {error && <div role="alert" className="chat-integration-error"><span>{error}</span><button type="button" className="feature-button" disabled={loading} onClick={() => void loadChats(cursor)}>{t("Retry")}</button></div>}
      <div className="chat-binding-events"><strong>{t("Notifications")}</strong>{EVENTS.map((item) => <label key={item.value}><input type="checkbox" checked={events.includes(item.value)} onChange={(event) => setEvents((current) => event.target.checked ? [...current, item.value] : current.filter((value) => value !== item.value))} />{t(item.label)}</label>)}</div>
      <div className="chat-binding-toggle"><span>{t("Binding enabled")}</span><Toggle label={t("Binding enabled")} checked={enabled} onChange={setEnabled} /></div>
      <div className="chat-binding-toggle"><span>{t("Allow creating issues")}</span><Toggle label={t("Allow creating issues")} checked={allowCreate} onChange={setAllowCreate} /></div>
      <div className="chat-binding-toggle"><span>{t("Allow updating issues")}</span><Toggle label={t("Allow updating issues")} checked={allowUpdate} onChange={setAllowUpdate} /></div>
      <label className="chat-binding-approval"><input type="checkbox" checked={approved} onChange={(event) => setApproved(event.target.checked)} />{t("I approve sharing this team's selected updates with everyone in this group.")}</label>
      <div className="chat-form-footer"><button type="button" className="feature-button" disabled={busy} onClick={onCancel}>{t("Cancel")}</button><button type="submit" className="feature-button primary" disabled={busy || !valid}>{busy ? <GridLoader size={14} /> : <Check size={14} />}{t("Save group binding")}</button></div>
    </fieldset>
  </form>;
}
