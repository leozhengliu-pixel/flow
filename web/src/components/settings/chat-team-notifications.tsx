import { Lock, Plus, RefreshCw } from "lucide-react";
import { useState } from "react";
import type { ReactNode } from "react";
import { useNavigate } from "react-router-dom";

import { GridLoader } from "@/components/ui/grid-loader";
import { useI18n } from "@/i18n/i18n";
import { integrationSettingsPath } from "@/lib/app-routes";
import { chatErrorMessage, removeChatBinding, revokeChatBinding, saveChatBinding } from "@/lib/chat-integrations-api";
import { chatProviderName } from "@/lib/integration-catalog";
import { toast } from "@/lib/toast";
import type { ChatBinding, ChatIntegrationProvider } from "@/types/chat-integrations";
import type { BootstrapData, Team } from "@/types/flow";

import { confirmRemoveBinding, confirmRevokeBinding, TEAM_CHAT_DEFAULT_EVENTS, useChatIntegrationSettings } from "./chat-binding-support";
import { ChatBindingEditor, ChatBindingRow } from "./chat-group-bindings";
import { IntegrationBrandIcon } from "./integration-brand-icon";
import { SettingsRow, SettingsSection } from "./settings-primitives";
import "./feature-settings.css";
import "./chat-integration-settings.css";

/**
 * A team's group notifications on Lark or Feishu, in place of its Slack
 * channel: the team's owners connect groups (private teams included, with
 * their authorization). Bindings are listed to those who manage them only.
 */
export function ChatTeamNotifications({ provider, data, team }: { provider: ChatIntegrationProvider; data: BootstrapData; team: Team }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const name = chatProviderName(provider, t);
  const { settings, loading, error, setError, reload } = useChatIntegrationSettings(provider);
  const [busy, setBusy] = useState("");
  const [editing, setEditing] = useState<ChatBinding | "new" | null>(null);
  const connected = settings?.connection?.status === "connected";
  const tenantAuthorized = settings?.permissions ? settings.permissions.tenant.status === "authorized" : connected;
  const canManage = Boolean(settings?.permissions?.teams.manageableTeamIds.includes(team.id) || settings?.canManage);
  const bindable = Boolean(settings?.bindableTeamIds?.includes(team.id));
  const privateTeam = Boolean(settings?.permissions?.teams.privateTeamIds.includes(team.id) || team.private);
  const bindings = (settings?.bindings ?? []).filter((binding) => binding.teamId === team.id);
  const sharingBlocked = Boolean(data.workspaceSettings.hipaaCompliance);
  const settingsHref = integrationSettingsPath(data.workspace.urlKey, provider);

  const mutate = async (action: string, operation: () => Promise<unknown>, success: string, after?: () => void) => {
    setBusy(action);
    setError("");
    try {
      await operation();
      after?.();
      toast.success(t(success));
      await reload();
    } catch (cause) {
      setError(chatErrorMessage(cause, t, "Could not update integration"));
    } finally {
      setBusy("");
    }
  };

  let state: { description: string; action?: ReactNode };
  if (loading && !settings) state = { description: t("Loading integration…"), action: <GridLoader size={14} /> };
  else if (!settings?.connection) state = { description: t("Connect {name} to your workspace to broadcast notifications from this team").replace("{name}", name), action: <button type="button" className="settings-action" onClick={() => navigate(settingsHref)}>{t("Connect")}</button> };
  else if (!tenantAuthorized) state = { description: t("{name} hasn't approved the app in your organization yet. Your {name} admin approves the app version in the admin console; Flow then connects automatically.").replaceAll("{name}", name), action: <button type="button" className="settings-action" onClick={() => navigate(settingsHref)}>{t("Open settings")}</button> };
  else if (sharingBlocked) state = { description: t("Workspace policy prevents sharing updates with chat applications.") };
  else if (!canManage) state = { description: t("Only the team's owners and workspace administrators can connect groups to this team.") };
  else if (!bindable) state = { description: t("This team can't be shared with a group right now.") };
  else state = {
    description: t(privateTeam ? "Connect a group to broadcast this private team's selected updates. Binding doesn't add anyone to the team." : "Connect a group to broadcast notifications from this team"),
    action: !editing ? <button type="button" className="settings-action" disabled={Boolean(busy)} onClick={() => setEditing("new")}><Plus size={14} aria-hidden="true" />{t("Connect {name} group").replace("{name}", name)}</button> : undefined,
  };

  return <div className="chat-integration-settings chat-team-notifications" data-provider={provider}>
    <SettingsSection headerClassName="team-settings-section-title">
      <SettingsRow icon={<IntegrationBrandIcon provider={provider} size={16} />} title={<>{t("Connect a {name} group").replace("{name}", name)}{privateTeam && <small className="chat-binding-badge"><Lock size={11} aria-hidden="true" />{t("Private team")}</small>}</>} description={state.description}>
        {state.action}
      </SettingsRow>
    </SettingsSection>
    {error && <div className="feature-card chat-integration-alert" role="alert"><div className="feature-row"><div><span>{error}</span></div><aside><button type="button" className="feature-button" disabled={loading || Boolean(busy)} onClick={() => void reload()}><RefreshCw size={14} aria-hidden="true" />{t("Retry")}</button></aside></div></div>}
    {settings?.connection && (bindings.length > 0 || canManage) && <SettingsSection title={t("Groups")} headerClassName="team-settings-section-title">
      <div className="feature-card">
        {bindings.length === 0 && <div className="feature-row"><div><span className="chat-muted">{t("No groups are connected to this team.")}</span></div></div>}
        {bindings.map((binding) => <ChatBindingRow key={binding.id} binding={binding} data={data} busy={busy} canEdit={canManage && editing === null && connected && !sharingBlocked}
          onEdit={() => setEditing(binding)}
          onRevoke={() => void (async () => { if (await confirmRevokeBinding(binding, t)) await mutate(`revoke:${binding.id}`, () => revokeChatBinding(provider, binding.id), "Group binding revoked"); })()}
          onRemove={() => void (async () => { if (await confirmRemoveBinding(binding, t)) await mutate(`remove:${binding.id}`, () => removeChatBinding(provider, binding.id), "Group binding removed"); })()} />)}
      </div>
    </SettingsSection>}
    {editing && canManage && settings && <ChatBindingEditor key={editing === "new" ? "new" : editing.id} provider={provider} data={data} bindableTeamIds={settings.bindableTeamIds ?? []} privateTeamIds={settings.permissions?.teams.privateTeamIds} binding={editing === "new" ? undefined : editing} fixedTeamId={team.id} defaultEvents={TEAM_CHAT_DEFAULT_EVENTS} busy={Boolean(busy)} onCancel={() => setEditing(null)} onSave={(input) => mutate("binding", () => saveChatBinding(provider, input), "Group binding saved", () => setEditing(null))} />}
  </div>;
}
