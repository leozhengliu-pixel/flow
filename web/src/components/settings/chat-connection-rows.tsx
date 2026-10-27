import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";

import { GridLoader } from "@/components/ui/grid-loader";
import { useI18n } from "@/i18n/i18n";
import { integrationSettingsPath } from "@/lib/app-routes";
import { chatErrorCodeMessage, chatErrorMessage, chatIdentityAuthorizationURL, fetchChatIntegration, removeChatIdentity, startChatIdentityBinding } from "@/lib/chat-integrations-api";
import { CHAT_PROVIDERS, chatProviderName, isChatProvider } from "@/lib/integration-catalog";
import { isWorkspaceAdmin } from "@/lib/settings-access";
import { toast } from "@/lib/toast";
import type { ChatIntegrationProvider, ChatIntegrationSettings } from "@/types/chat-integrations";
import type { BootstrapData } from "@/types/flow";

import { ConnectAction } from "./code-host-accounts";
import { IntegrationBrandIcon } from "./integration-brand-icon";
import { ConnectionStatusMenu } from "./personal-connection-rows";
import { SettingsConfirmDialog } from "./settings-confirm-dialog";
import { SettingsRow } from "./settings-primitives";
import "./code-host-accounts.css";

/** Personal chat accounts, only for the chat applications this workspace has set up. */
export function ChatConnectionRows({ data }: { data: BootstrapData }) {
  if (data.viewerRole !== "member" && !isWorkspaceAdmin(data)) return null;
  const configured = new Set((data.integrationConnections ?? []).map((item) => item.provider).filter(isChatProvider));
  return <>{CHAT_PROVIDERS.filter((provider) => configured.has(provider)).map((provider) => <ChatConnectionRow key={`${data.workspace.id}:${provider}`} provider={provider} data={data} />)}</>;
}

function ChatConnectionRow({ provider, data }: { provider: ChatIntegrationProvider; data: BootstrapData }) {
  const { t } = useI18n();
  const name = chatProviderName(provider, t);
  const [settings, setSettings] = useState<ChatIntegrationSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);
  const mounted = useRef(true);

  const load = useCallback(async () => {
    const next = await fetchChatIntegration(provider);
    if (!mounted.current) return;
    setSettings(next);
    const url = new URL(window.location.href);
    if (url.searchParams.get("chat_provider") !== provider || !url.searchParams.has("chat_auth")) return;
    if (url.searchParams.get("chat_auth") === "success" && next.identity) toast.success(t("Account linked"));
    else setError(chatErrorCodeMessage(url.searchParams.get("chat_auth_error"), t) ?? t("Could not link account. Please try again."));
    for (const key of ["chat_auth", "chat_provider", "chat_auth_error"]) url.searchParams.delete(key);
    window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  }, [provider, t]);

  useEffect(() => {
    mounted.current = true;
    // Started from a microtask so the effect itself never sets state.
    queueMicrotask(() => {
      if (!mounted.current) return;
      void load().catch((cause: unknown) => {
        if (mounted.current) setError(chatErrorMessage(cause, t, "Could not load connected accounts"));
      }).finally(() => { if (mounted.current) setLoading(false); });
    });
    return () => { mounted.current = false; };
  }, [load, t]);

  const retry = async () => {
    setLoading(true);
    setError("");
    try { await load(); }
    catch (cause) { setError(chatErrorMessage(cause, t, "Could not load connected accounts")); }
    finally { setLoading(false); }
  };

  const connect = async () => {
    setBusy(true);
    setError("");
    try {
      const result = await startChatIdentityBinding(provider);
      window.location.assign(chatIdentityAuthorizationURL(provider, result.authorizeUrl));
    } catch (cause) {
      setError(chatErrorMessage(cause, t, "Could not link account"));
      setBusy(false);
    }
  };

  const disconnect = async () => {
    setConfirming(false);
    setBusy(true);
    setError("");
    try {
      await removeChatIdentity(provider);
      setSettings((current) => current ? { ...current, identity: null } : current);
      toast.success(t("Account unlinked"));
      await load();
    } catch (cause) {
      setError(chatErrorMessage(cause, t, "Could not disconnect the account"));
    } finally { setBusy(false); }
  };

  const identity = settings?.identity;
  const admin = isWorkspaceAdmin(data) && settings?.canManage === true;
  const ready = Boolean(settings?.credentialsConfigured && (settings.connection?.status === "connected" || admin));
  const description = t("Create and update issues from group chats in {workspace}").replace("{workspace}", data.workspace.name);
  let control: ReactNode;
  if (loading) control = <span role="status" aria-label={`${t("Loading account")}: ${name}`}><GridLoader size={14} /></span>;
  else if (identity) control = <ConnectionStatusMenu name={name} reconnect={Boolean(settings?.identityExpired)} disabled={busy} onReconnect={() => void connect()} onDisconnect={() => setConfirming(true)} />;
  else if (ready) control = <ConnectAction disabled={busy} onClick={() => void connect()} />;
  else control = <span className="personal-connection-soon">{t(settings?.connection ? "Waiting for workspace setup" : "Not configured")}</span>;

  return <>
    <section className="settings-card" aria-label={t("{name} account").replace("{name}", name)} data-testid={`${provider}-account-row`}>
      <SettingsRow icon={<IntegrationBrandIcon provider={provider} size={16} />} title={<>{name}{identity?.name && <span data-i18n-ignore>{` · ${identity.name}`}</span>}</>} description={description}>{control}</SettingsRow>
      {error && <div className="chat-account-error" role="alert"><span>{error}</span><button type="button" className="personal-action" disabled={busy || loading} onClick={() => void retry()}><RefreshCw size={14} aria-hidden="true" />{t("Retry")}</button></div>}
      {!loading && admin && !ready && !identity && <div className="chat-account-setup-link"><NavLink to={integrationSettingsPath(data.workspace.urlKey, provider)}>{t("Set up workspace integration")}</NavLink></div>}
    </section>
    {confirming && <SettingsConfirmDialog title={t("Disconnect personal {name} account?").replace("{name}", name)} description={t("Link your account again to resume your group actions.")} confirmLabel={t("Disconnect")} busy={busy} onCancel={() => setConfirming(false)} onConfirm={() => void disconnect()} />}
  </>;
}
