import { GitFork, UserRound } from "lucide-react";
import { useState } from "react";
import type { FormEvent, ReactNode } from "react";

import { useI18n } from "@/i18n/i18n";
import { fillTemplate } from "@/lib/fill-template";
import {
  authorizeGitHubAccount,
  authorizeGitLabAccount,
  connectGitLabToken,
  disconnectCodeHostAccount,
  MissingScopesError,
} from "@/lib/code-host-accounts-api";
import type { CodeHostReturnTo } from "@/lib/code-host-accounts-api";
import { toast } from "@/lib/toast";
import type {
  CodeHostAccount,
  CodeHostKind,
  GitLabInstanceOption,
} from "@/types/code-host-accounts";

import { useCodeHostAccounts, useCodeHostLinkResult } from "./code-host-accounts-hooks";
import { SettingsRow } from "./settings-primitives";

import "./code-host-accounts.css";

function accountFor(accounts: CodeHostAccount[] | undefined, kind: CodeHostKind) {
  return (accounts ?? []).filter((item) => item.codeHost === kind);
}

function useRedirect() {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const go = async (start: () => Promise<{ url: string }>) => {
    setBusy(true);
    try {
      const { url } = await start();
      window.location.assign(url);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not start the connection"));
      setBusy(false);
    }
  };
  return { busy, go };
}

function AccountAction({
  children,
  danger,
  primary,
  disabled,
  onClick,
  label,
}: {
  children: ReactNode;
  danger?: boolean;
  primary?: boolean;
  disabled?: boolean;
  onClick: () => void;
  label?: string;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      className={`personal-action${danger ? " danger" : ""}${primary ? " primary" : ""}`}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

/** The GitHub and GitLab rows of Settings › Account › Connected accounts. */
export function CodeHostConnectionRows({ githubDescription }: { githubDescription: string }) {
  const { t } = useI18n();
  const { data, loaded, reload } = useCodeHostAccounts();
  useCodeHostLinkResult();
  const github = accountFor(data?.accounts, "github")[0];
  const gitlab = accountFor(data?.accounts, "gitlab");
  // A member has one account per GitLab host: offer only instances not
  // linked yet (a linked one reconnects from its own row).
  const unlinkedGitLab = (data?.gitlabInstances ?? []).filter(
    (instance) => !gitlab.some((account) => account.instanceId === instance.id || account.hostName === instance.hostName),
  );
  const { busy, go } = useRedirect();
  const [removing, setRemoving] = useState("");

  const disconnect = async (id: string) => {
    setRemoving(id);
    try {
      await disconnectCodeHostAccount(id);
      await reload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not disconnect the account"));
    } finally {
      setRemoving("");
    }
  };

  const accountDescription = (account: CodeHostAccount, host?: string) => (
    <span>
      <span data-i18n-ignore>
        @{account.login}
        {host ? ` · ${host}` : ""}
      </span>
      {account.status === "reconnect" && (
        <>
          {" · "}
          {account.statusReason ? <span data-i18n-ignore>{account.statusReason}</span> : t("Reconnect needed")}
        </>
      )}
    </span>
  );

  const renderControls = (account: CodeHostAccount, reconnect: () => void) => (
    <>
      {account.status === "reconnect" && (
        <AccountAction primary disabled={busy} onClick={reconnect}>
          {t("Reconnect")}
        </AccountAction>
      )}
      <AccountAction danger disabled={removing === account.id} onClick={() => void disconnect(account.id)}>
        {t("Disconnect")}
      </AccountAction>
    </>
  );

  return (
    <>
      <div className="settings-card" data-testid="github-account-row">
        <SettingsRow
          icon={<GitFork />}
          title={<span data-i18n-ignore>GitHub</span>}
          description={github ? accountDescription(github) : t(githubDescription)}
        >
          {github ? (
            <div className="code-host-actions">
              {renderControls(github, () => void go(() => authorizeGitHubAccount("account")))}
            </div>
          ) : data?.github.available ? (
            <AccountAction disabled={busy} onClick={() => void go(() => authorizeGitHubAccount("account"))}>
              {t("Connect")}
            </AccountAction>
          ) : loaded ? (
            <span className="personal-connection-soon">{t("Not configured")}</span>
          ) : null}
        </SettingsRow>
      </div>
      {gitlab.map((account) => {
        const instance = data?.gitlabInstances.find((item) => item.id === account.instanceId);
        return (
          <div className="settings-card" key={account.id}>
            <SettingsRow
              icon={<GitFork />}
              title={<span data-i18n-ignore>GitLab</span>}
              description={accountDescription(account, account.hostName)}
            >
              <div className="code-host-actions">
                {renderControls(account, () => {
                  if (instance?.oauthAvailable) void go(() => authorizeGitLabAccount(instance.id, "account"));
                  else document.getElementById("gitlab-token")?.focus();
                })}
              </div>
            </SettingsRow>
          </div>
        );
      })}
      {data && (unlinkedGitLab.length > 0 || data.gitlabInstances.length === 0) && (
        <div className="settings-card">
          <SettingsRow
            icon={<GitFork />}
            title={<span data-i18n-ignore>GitLab</span>}
            description={
              unlinkedGitLab.length
                ? t("Link your GitLab account to review merge requests and act as yourself")
                : t("Ask an admin to connect GitLab")
            }
            control={unlinkedGitLab.length > 0}
          >
            {unlinkedGitLab.length > 0 && (
              <GitLabConnect instances={unlinkedGitLab} onLinked={reload} returnTo="account" />
            )}
          </SettingsRow>
        </div>
      )}
    </>
  );
}

function GitLabConnect({
  instances,
  onLinked,
  returnTo,
}: {
  instances: GitLabInstanceOption[];
  onLinked: () => Promise<void>;
  returnTo: CodeHostReturnTo;
}) {
  const { t } = useI18n();
  const [instanceId, setInstanceId] = useState(instances.length === 1 ? instances[0].id : "");
  const [token, setToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [missing, setMissing] = useState<string[]>([]);
  const { busy, go } = useRedirect();
  const instance = instances.find((item) => item.id === instanceId);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!instance || !token.trim()) return;
    setSaving(true);
    setError("");
    setMissing([]);
    try {
      await connectGitLabToken(instance.id, token.trim());
      setToken("");
      toast.success(t("GitLab account connected"));
      await onLinked();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Could not connect the account"));
      if (err instanceof MissingScopesError) setMissing(err.missingScopes);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="code-host-connect">
      <select
        aria-label={t("GitLab instance")}
        value={instanceId}
        onChange={(event) => {
          setInstanceId(event.target.value);
          setError("");
          setMissing([]);
        }}
      >
        {instances.length > 1 && <option value="">{t("Select an instance")}</option>}
        {instances.map((item) => (
          <option key={item.id} value={item.id} data-i18n-ignore>
            {item.name} — {item.hostName}
          </option>
        ))}
      </select>
      {instance?.oauthAvailable && (
        <AccountAction disabled={busy} onClick={() => void go(() => authorizeGitLabAccount(instance.id, returnTo))}>
          {t("Connect with GitLab")}
        </AccountAction>
      )}
      {instance && !instance.oauthAvailable && (
        <form className="code-host-token" onSubmit={(event) => void submit(event)}>
          <label htmlFor="gitlab-token">{t("Personal access token")}</label>
          <input
            id="gitlab-token"
            type="password"
            autoComplete="off"
            value={token}
            onChange={(event) => setToken(event.target.value)}
          />
          <p>
            {fillTemplate(t("Create a token on {host} with the api and read_user scopes."), { host: <b data-i18n-ignore>{instance.hostName}</b> })}{" "}
            <a
              href={`${instance.baseUrl}/-/user_settings/personal_access_tokens?name=Flow&scopes=api,read_user`}
              target="_blank"
              rel="noreferrer"
            >
              {t("Create a token")}
            </a>
          </p>
          {error && (
            <p className="code-host-error" role="alert">
              <span data-i18n-ignore>{error}</span>
              {missing.length > 0 && (
                <>
                  {" "}
                  {t("Missing scopes:")} <span data-i18n-ignore>{missing.join(", ")}</span>
                </>
              )}
            </p>
          )}
          <button type="submit" className="personal-action primary" disabled={saving || !token.trim()}>
            {t("Connect")}
          </button>
        </form>
      )}
    </div>
  );
}

/**
 * "Your personal GitHub/GitLab account" row on a code integration page:
 * shows the viewer's link state and sends them to Connected accounts to manage it.
 */
export function PersonalCodeHostRow({
  codeHost,
  onManage,
}: {
  codeHost: CodeHostKind;
  onManage?: () => void;
}) {
  const { t } = useI18n();
  const { data, loaded } = useCodeHostAccounts();
  useCodeHostLinkResult();
  const { busy, go } = useRedirect();
  if (!loaded) return null;
  const account = accountFor(data?.accounts, codeHost)[0];
  const github = data?.github;
  const instances = data?.gitlabInstances ?? [];
  const canConnect = codeHost === "github" ? Boolean(github?.available) : instances.length > 0;
  const heading = codeHost === "github" ? t("Your personal GitHub account") : t("Your personal GitLab account");
  const reconnect = account?.status === "reconnect";

  const connect = () => {
    if (codeHost === "github") return void go(() => authorizeGitHubAccount("integration"));
    const only = instances.length === 1 ? instances[0] : undefined;
    if (only?.oauthAvailable) return void go(() => authorizeGitLabAccount(only.id, "integration"));
    onManage?.();
  };

  return (
    <div className="code-connection-row" data-testid={`personal-${codeHost}-row`}>
      <div className={`code-provider-avatar ${codeHost}`}>
        <UserRound />
      </div>
      <div>
        <strong>{heading}</strong>
        <span>
          {account ? (
            <>
              {reconnect ? t("Reconnect needed") : t("Connected")}{" "}
              <span data-i18n-ignore>@{account.login}</span>
            </>
          ) : (
            t("Not connected")
          )}
        </span>
      </div>
      {account ? (
        <button className="code-manage" onClick={() => (reconnect ? connect() : onManage?.())} disabled={busy}>
          {reconnect ? t("Reconnect") : t("Manage")}
        </button>
      ) : canConnect ? (
        <button className="code-manage" onClick={connect} disabled={busy}>
          {t("Connect")}
        </button>
      ) : (
        <span>{t(codeHost === "github" ? "Not configured" : "Ask an admin to connect GitLab")}</span>
      )}
    </div>
  );
}
