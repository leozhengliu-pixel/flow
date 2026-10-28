import { ArrowLeft, Copy, ExternalLink, KeyRound, Plus, RefreshCw, Save, ShieldCheck, Unplug, UsersRound } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";

import { confirmAction } from "@/components/ui/action-dialog-service";
import { GridLoader } from "@/components/ui/grid-loader";
import { useI18n } from "@/i18n/i18n";
import {
  CHAT_ERROR_MESSAGES,
  chatErrorCodeMessage,
  chatErrorMessage,
  chatIdentityAuthorizationURL,
  designateChatTenantAuthorizer,
  disconnectChatIntegration,
  fetchChatIntegration,
  removeChatBinding,
  revokeChatBinding,
  saveChatBinding,
  saveChatIntegration,
  startChatTenantAuthorization,
  testChatIntegration,
} from "@/lib/chat-integrations-api";
import { chatProviderName } from "@/lib/integration-catalog";
import { isWorkspaceAdmin } from "@/lib/settings-access";
import { toast } from "@/lib/toast";
import type { ChatBinding, ChatIntegrationProvider, ChatIntegrationSettings as Settings } from "@/types/chat-integrations";
import type { BootstrapData } from "@/types/flow";

import { confirmRemoveBinding, confirmRevokeBinding } from "./chat-binding-support";
import { ChatBindingEditor, ChatBindingRow, FieldRow } from "./chat-group-bindings";
import { IntegrationBrandIcon } from "./integration-brand-icon";
import { SettingsSelect } from "./settings-primitives";
import "./feature-settings.css";
import "./chat-integration-settings.css";

const ANY_ADMINISTRATOR = "__any__";

/** Reads a tenant authorization result the platform sign-in returned with, then clears it from the address. */
function consumeTenantResult(provider: ChatIntegrationProvider, t: (source: string) => string): string {
  const url = new URL(window.location.href);
  if (url.searchParams.get("chat_provider") !== provider || !url.searchParams.has("chat_auth")) return "";
  let error = "";
  if (url.searchParams.get("chat_auth") === "success") toast.success(t("Tenant authorized"));
  else error = chatErrorCodeMessage(url.searchParams.get("chat_auth_error"), t) ?? t("Tenant authorization failed. Try again.");
  for (const key of ["chat_auth", "chat_provider", "chat_auth_error", "chat_tenant"]) url.searchParams.delete(key);
  window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  return error;
}

function PermissionRow({ icon, title, granted, children, action }: { icon: ReactNode; title: string; granted: boolean; children: ReactNode; action?: ReactNode }) {
  return <div className="feature-row chat-permission-row" data-granted={granted}>
    <div>
      <strong>{icon}{title}<span className={`chat-status-dot is-${granted ? "connected" : "pending"}`} aria-hidden="true" /></strong>
      {children}
    </div>
    {action && <aside className="chat-actions">{action}</aside>}
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
  const permissions = settings?.permissions;
  const manageableTeamIds = permissions?.teams.manageableTeamIds ?? (canManage ? data.teams.map((team) => team.id) : []);
  const canBind = canManage || manageableTeamIds.length > 0;
  const tenant = permissions?.tenant;
  const tenantAuthorized = tenant ? tenant.status === "authorized" : connected;

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
      const returned = consumeTenantResult(provider, t);
      if (returned) setError(returned);
    }).catch((cause: unknown) => {
      if (mounted.current) setError(chatErrorMessage(cause, t, "Could not load integration"));
    }).finally(() => {
      if (mounted.current) setLoading(false);
    });
    return () => { mounted.current = false; };
  }, [load, provider, t]);

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

  const authorizeTenant = async () => {
    setBusy("tenant");
    setError("");
    try {
      const result = await startChatTenantAuthorization(provider);
      window.location.assign(chatIdentityAuthorizationURL(provider, result.authorizeUrl));
    } catch (cause) {
      setError(chatErrorMessage(cause, t, "Could not start tenant authorization"));
      setBusy("");
    }
  };

  const designate = (userId: string) => mutate("designate", () => designateChatTenantAuthorizer(provider, userId === ANY_ADMINISTRATOR ? "" : userId), "Tenant authorizer saved");

  const revokeBinding = async (binding: ChatBinding) => {
    if (!await confirmRevokeBinding(binding, t)) return;
    await mutate(`revoke:${binding.id}`, () => revokeChatBinding(provider, binding.id), "Group binding revoked");
  };

  const removeBinding = async (binding: ChatBinding) => {
    if (!await confirmRemoveBinding(binding, t)) return;
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
  const statusDescription = connectionError || t(connected ? "The application and workspace are linked." : settings?.credentialsConfigured ? "Waiting for the organization's tenant authorization." : "A workspace administrator needs to configure the application.");
  const secretPlaceholder = settings?.credentialsConfigured ? t("Leave blank to keep existing secret") : "";
  const designatedId = tenant?.designatedAuthorizerId ?? "";
  const designated = designatedId ? data.users.find((user) => user.id === designatedId) : undefined;
  const authorization = tenant?.authorization;
  const authorizer = authorization?.userId ? data.users.find((user) => user.id === authorization.userId) : undefined;
  const memberOptions = [{ value: ANY_ADMINISTRATOR, label: t("Workspace administrators") }, ...(data.members ?? []).filter((member) => member.status === "active" && member.role !== "guest").map((member) => ({ value: member.user.id, label: member.user.displayName || member.user.name, entityName: true }))];
  const visibleBindings = settings?.bindings ?? [];

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
                  <button type="button" className="feature-button" disabled={Boolean(busy)} onClick={() => void mutate("test", () => testChatIntegration(provider), "Connection checked")}>{busy === "test" ? <GridLoader size={14} /> : <RefreshCw size={14} aria-hidden="true" />}{t("Test connection")}</button>
                  <button type="button" className="feature-button danger" disabled={Boolean(busy)} onClick={() => void disconnect()}><Unplug size={14} aria-hidden="true" />{t("Disconnect")}</button>
                </aside>}
              </div>
            </div>
          </section>

          {settings.connection && <section className="feature-section" aria-label={t("Permissions")}>
            <header><h2>{t("Permissions")}</h2><p>{t("Each permission is granted separately. A Flow workspace role never stands in for the chat platform's tenant authorization.")}</p></header>
            <div className="feature-card">
              <PermissionRow icon={<KeyRound size={14} aria-hidden="true" />} title={t("Enterprise tenant authorization")} granted={tenantAuthorized}
                action={!tenantAuthorized && tenant?.canAuthorize && settings.credentialsConfigured ? <button type="button" className="feature-button primary" disabled={Boolean(busy)} onClick={() => void authorizeTenant()}>{busy === "tenant" ? <GridLoader size={14} /> : <ShieldCheck size={14} aria-hidden="true" />}{t("Authorize tenant")}</button> : undefined}>
                {tenantAuthorized
                  ? <span data-testid="chat-tenant-status">{authorization?.method === "legacy" || !authorization
                    ? t("Completed before tenant authorization was a separate step.")
                    : t("Completed by {name} on {date}.").replace("{name}", authorizer?.displayName || authorization.name || t("a former member")).replace("{date}", formatDate(authorization.authorizedAt, { dateStyle: "medium" }))}
                  {tenant?.tenantKey && <> {t("Tenant")}: <code data-i18n-ignore>{tenant.tenantKey}</code></>}</span>
                  : <span data-testid="chat-tenant-status">{t("Not completed. A tenant administrator of {name} authorizes the application for the whole organization once; then nobody is asked again.").replace("{name}", name)}</span>}
                {!tenantAuthorized && <span>{designated
                  ? t("Designated tenant authorizer: {name}.").replace("{name}", designated.displayName || designated.name)
                  : t("Workspace administrators can start it, or designate the member who is a tenant administrator on the chat platform.")}
                {!tenant?.canAuthorize && ` ${t("Ask a workspace administrator or the designated tenant authorizer.")}`}</span>}
              </PermissionRow>
              {canManage && !tenantAuthorized && <FieldRow label={t("Tenant authorizer")} description={t("The member who completes tenant authorization. Their chat account must be a tenant administrator.")}>
                <SettingsSelect label={t("Tenant authorizer")} options={memberOptions} value={designatedId || ANY_ADMINISTRATOR} disabled={Boolean(busy)} onChange={(value) => void designate(value)} />
              </FieldRow>}
              <PermissionRow icon={<ShieldCheck size={14} aria-hidden="true" />} title={t("Workspace integration")} granted={canManage}>
                <span>{t(canManage ? "You manage this workspace's application settings and all group bindings." : "Only workspace administrators manage the application settings.")}</span>
              </PermissionRow>
              <PermissionRow icon={<UsersRound size={14} aria-hidden="true" />} title={t("Team group bindings")} granted={canBind}>
                <span>{canManage
                  ? t("You can bind groups for every team.")
                  : manageableTeamIds.length
                    ? (manageableTeamIds.length === 1 ? t("You can bind groups for 1 team you own.") : t("You can bind groups for {count} teams you own.").replace("{count}", String(manageableTeamIds.length)))
                    : t("A team's owners bind its groups. Ask a team owner or a workspace administrator.")}</span>
              </PermissionRow>
            </div>
          </section>}

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

          {canBind && <section className="feature-section" aria-label={t("Group bindings")}>
            <header><h2>{t("Group bindings")}</h2><p>{t(sharingBlocked ? "Workspace policy prevents sharing updates with chat applications." : canManage ? "Share selected team updates with approved internal groups. Private teams are shared only with their owners' authorization." : "Share selected updates of the teams you own with approved internal groups.")}</p></header>
            <div className="feature-card">
              {visibleBindings.length === 0 && <FieldRow label={<span className="chat-muted">{t(connected ? "No groups are bound." : "Connect the application before adding groups.")}</span>} />}
              {visibleBindings.map((binding) => <ChatBindingRow key={binding.id} binding={binding} data={data} busy={busy} canEdit={editing === null && connected && !sharingBlocked} onEdit={() => setEditing(binding)} onRevoke={() => void revokeBinding(binding)} onRemove={() => void removeBinding(binding)} />)}
            </div>
            {!editing && <div className="feature-section-action"><button type="button" className="feature-button" disabled={!connected || Boolean(busy) || sharingBlocked} onClick={() => setEditing("new")}><Plus size={14} aria-hidden="true" />{t("Add group")}</button></div>}
          </section>}

          {canBind && editing && !sharingBlocked && <ChatBindingEditor key={editing === "new" ? "new" : editing.id} provider={provider} data={data} bindableTeamIds={settings.bindableTeamIds ?? []} privateTeamIds={permissions?.teams.privateTeamIds} binding={editing === "new" ? undefined : editing} busy={Boolean(busy)} onCancel={() => setEditing(null)} onSave={(input) => mutate("binding", () => saveChatBinding(provider, input), "Group binding saved", () => setEditing(null))} />}
        </>}
      </div>
    </div>
  );
}
