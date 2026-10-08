import {
  Book,
  ChevronDown,
  ChevronLeft,
  Copy,
  Grid2x2Plus,
  Info,
  Mail,
  TriangleAlert,
} from "lucide-react";
import { Fragment, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { toast } from "sonner";

import { confirmAction } from "@/components/ui/action-dialog-service";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { GridLoader } from "@/components/ui/grid-loader";
import { Toggle } from "@/components/ui/toggle";
import { ScopedFlowTooltip } from "@/components/ui/tooltip";
import { UserAvatar } from "@/components/ui/user-avatar";
import { useI18n } from "@/i18n/i18n";
import {
  connectGitLab,
  disconnectIntegrationConnection,
  rotateGitLabToken,
  setGitLabTokenRotation,
  testIntegrationConnection,
  updateGitLabToken,
  updateIntegrationConnection,
} from "@/lib/api";
import { ApiError } from "@/lib/api-client";
import type { BootstrapData, IntegrationConnection } from "@/types/flow";
import { GITLAB_BRANCH_FORMATS, gitlabBranchExample } from "@/lib/gitlab-branch-formats";
import { IntegrationBrandIcon } from "./integration-brand-icon";
import { SettingsSelect } from "./settings-primitives";

import "./gitlab-integration-settings.css";

const DOCS_URL = "https://docs.gitlab.com/user/profile/personal_access_tokens/";
const TOKEN_SETTINGS_URL = "https://gitlab.com/-/user_settings/personal_access_tokens";
const HTTPS_URL = "Please enter a valid HTTPS URL.";
const TOKEN_REQUIRED = "Please enter a valid access token.";

type ConnectionError = {
  message: string;
  request?: string;
  headers?: string;
  body?: string;
};

type SetupState =
  | { step: "form" }
  | { step: "webhook"; url: string; secret: string };

function connectionError(error: unknown, fallback: string): ConnectionError {
  if (error instanceof ApiError) {
    const details = (error.current ?? {}) as Record<string, string | undefined>;
    return {
      message: error.message || fallback,
      request: details.errorRequest,
      headers: details.errorResponseHeaders,
      body: details.errorResponseBody,
    };
  }
  return { message: error instanceof Error && error.message ? error.message : fallback };
}

function isExpired(value?: string) {
  return Boolean(value && new Date(value).getTime() <= Date.now());
}

/** Splits "a {x} b" into text and the supplied nodes. */
function template(text: string, parts: Record<string, ReactNode>) {
  return text.split(/(\{\w+\})/).map((piece, index) => {
    const key = /^\{(\w+)\}$/.exec(piece)?.[1];
    return <Fragment key={index}>{key && key in parts ? parts[key] : piece}</Fragment>;
  });
}

export function GitLabIntegrationSettings({
  data,
  onBack,
  onReload,
}: {
  data: BootstrapData;
  onBack: () => void;
  onReload: () => Promise<void>;
}) {
  const { t, formatDate } = useI18n();
  const connection = data.integrationConnections.find((item) => item.provider === "gitlab");
  const [setup, setSetup] = useState<SetupState | null>(null);
  const [aboutOpen, setAboutOpen] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [disabling, setDisabling] = useState(false);
  const connected = Boolean(connection);
  const showAbout = connected ? aboutOpen : !setup;
  const enabledBy = connection
    ? (data.users.find((user) => user.id === connection.connectedBy) ?? data.viewer)
    : undefined;

  const disable = async () => {
    if (!connection) return;
    const confirmed = await confirmAction(t("Disconnect GitLab?"), {
      description: t("Integration functionality will stop and related settings will be deleted."),
      confirmLabel: t("Disconnect"),
      danger: true,
    });
    if (!confirmed) return;
    setDisabling(true);
    try {
      await disconnectIntegrationConnection("gitlab", connection.id);
      setSetup(null);
      await onReload();
      toast.info(t("Disconnected GitLab integration"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not disconnect GitLab"));
    } finally {
      setDisabling(false);
    }
  };

  const updateSetting = async (key: string, value: string) => {
    if (!connection) return;
    try {
      await updateIntegrationConnection("gitlab", connection.id, { config: { [key]: value } });
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update integration"));
    }
  };

  return (
    <div className="gitlab-settings">
      <button type="button" className="gitlab-settings-back" onClick={onBack}>
        <ChevronLeft aria-hidden="true" />
        <span>{t("Integrations")}</span>
      </button>
      <header className="gitlab-settings-hero">
        <span className="gitlab-settings-logo" aria-hidden="true">
          <IntegrationBrandIcon provider="gitlab" size={28} />
        </span>
        <div>
          <h2 data-i18n-ignore>GitLab</h2>
          <h3>{t("Automate your Merge Request workflow")}</h3>
        </div>
      </header>

      <section className="gitlab-settings-about" aria-label={t("About integration")}>
        <div className="gitlab-settings-status">
          <ul className="gitlab-settings-meta">
            {connection && enabledBy && (
              <li className="gitlab-settings-meta-enabled">
                <UserAvatar
                  avatarUrl={enabledBy.avatarUrl}
                  className="avatar gitlab-settings-avatar"
                  name={enabledBy.displayName || enabledBy.name}
                />
                <div>
                  <span>{t("Enabled by")}</span>
                  <strong>
                    <b data-i18n-ignore>{enabledBy.displayName || enabledBy.name}</b>
                    <small>
                      {formatDate(connection.createdAt, { month: "short", day: "numeric", year: "numeric" })}
                    </small>
                  </strong>
                </div>
              </li>
            )}
            <li>
              <span>{t(connection ? "Support" : "Built by")}</span>
              <span className="gitlab-settings-meta-value">
                <Mail aria-hidden="true" />
                <span data-i18n-ignore>Flow</span>
              </span>
            </li>
            <li>
              <span>{t("Docs")}</span>
              <a href={DOCS_URL} target="_blank" rel="noreferrer">
                <Book aria-hidden="true" />
                {t("Docs")}
              </a>
            </li>
          </ul>
          {connected ? (
            <div className="gitlab-settings-status-actions">
              <button
                type="button"
                className="gitlab-settings-button"
                disabled={disabling}
                onClick={() => void disable()}
              >
                {t("Disable")}
              </button>
              <ScopedFlowTooltip label={t(aboutOpen ? "Hide information" : "Show more information")}>
                <button
                  type="button"
                  className="gitlab-settings-icon-button"
                  aria-label={t(aboutOpen ? "Hide information" : "Show more information")}
                  aria-expanded={aboutOpen}
                  onClick={() => {
                    if (!aboutOpen) setExpanded(true);
                    setAboutOpen((open) => !open);
                  }}
                >
                  <Info aria-hidden="true" />
                </button>
              </ScopedFlowTooltip>
            </div>
          ) : setup ? (
            <span className="gitlab-settings-progress" role="status">
              <GridLoader variant="pong" size={16} />
              <span>{t("Setting up GitLab…")}</span>
            </span>
          ) : (
            <button
              type="button"
              className="gitlab-settings-button primary"
              onClick={() => setSetup({ step: "form" })}
            >
              <Grid2x2Plus aria-hidden="true" />
              {t("Enable")}
            </button>
          )}
        </div>
        {showAbout && (
          <>
            <hr />
            <article className="gitlab-settings-overview">
              <h2>{t("Overview")}</h2>
              <p>
                {template(
                  t(
                    "Our GitLab integration keeps your work in sync in both applications. It links issues to Merge Requests so that issues update automatically from {started} to {done} as the MR moves from drafted to merged – there is no need to update the issue in Flow at all. Move even faster by using a keyboard shortcut that creates the issue's git branch name, assigns the issue and moves the issue to {started} in one step.",
                  ),
                  { started: <em>{t("In Progress")}</em>, done: <em>{t("Done")}</em> },
                )}
              </p>
              {expanded ? (
                <>
                  <h2>{t("How it works")}</h2>
                  <p>
                    {t(
                      "Include the issue ID in a branch name, merge request title or description to link the merge request to the issue. Flow moves the issue as the merge request is drafted, opened, merged or closed, and posts a linkback comment on the merge request when the token has the api scope.",
                    )}
                  </p>
                </>
              ) : (
                <button type="button" className="gitlab-settings-read-more" onClick={() => setExpanded(true)}>
                  {t("Read more")}
                </button>
              )}
            </article>
          </>
        )}
      </section>

      {setup?.step === "form" && (
        <GitLabSetupForm
          onCancel={() => setSetup(null)}
          onConnected={async (result) => {
            await onReload();
            setSetup(
              result.webhookSecret
                ? {
                    step: "webhook",
                    secret: result.webhookSecret,
                    url: `${window.location.origin}${result.webhookPath ?? "/api/integrations/gitlab/webhook"}`,
                  }
                : null,
            );
          }}
        />
      )}
      {setup?.step === "webhook" && (
        <GitLabWebhookSetup url={setup.url} secret={setup.secret} onDone={() => setSetup(null)} />
      )}

      {connection && <GitLabTokenSection connection={connection} onReload={onReload} />}
      {connection && <GitLabConnectivityIssue connection={connection} onReload={onReload} />}

      <div className="gitlab-settings-sections" aria-disabled={!connection || undefined}>
        <section className="gitlab-settings-card">
          <header>
            <h3>{t("Branch format")}</h3>
            <span>
              {template(t("Copy a branch name for an issue using the {action} action"), {
                action: <b>{t("Copy git branch name")}</b>,
              })}{" "}
              <span className="gitlab-settings-shortcut">
                (
                <span>
                  <kbd>⌘</kbd>
                  <kbd>⇧</kbd>
                  <kbd>.</kbd>
                </span>
                )
              </span>
            </span>
          </header>
          <div className="gitlab-settings-row">
            <div>
              <strong>{t("Branch format")}</strong>
              <span>
                {t("Example:")}{" "}
                <span data-i18n-ignore>
                  {gitlabBranchExample(connection?.config?.branchFormat || GITLAB_BRANCH_FORMATS[0])}
                </span>
              </span>
            </div>
            <SettingsSelect
              label={t("Branch format")}
              disabled={!connection}
              value={connection?.config?.branchFormat || GITLAB_BRANCH_FORMATS[0]}
              onChange={(value) => void updateSetting("branchFormat", value)}
              options={GITLAB_BRANCH_FORMATS.map((value) => ({ value, label: value, entityName: true }))}
            />
          </div>
        </section>
        <GitLabLinkbacks connection={connection} onChange={updateSetting} />
      </div>
    </div>
  );
}

function TokenHelp() {
  const { t } = useI18n();
  return (
    <div className="gitlab-settings-help">
      {template(t("Get an access token from GitLab {userSettings} or {projectSettings}"), {
        userSettings: (
          <a href={TOKEN_SETTINGS_URL} target="_blank" rel="noreferrer">
            {t("user settings")}
          </a>
        ),
        projectSettings: <b>{t("project settings")}</b>,
      })}
      <ul>
        <li>
          {template(
            t(
              "The token requires {api} or {readApi} scope. With the {readApi} scope, Flow will not post linkbacks to the issue on GitLab merge requests.",
            ),
            { api: <b data-i18n-ignore>api</b>, readApi: <b data-i18n-ignore>read_api</b> },
          )}
        </li>
        <li>
          {template(t("If you use a project access token, it requires {reporter} role access."), {
            reporter: <b>{t("reporter")}</b>,
          })}
        </li>
        <li>
          {template(t("If the token has the {selfRotate} scope, it can be automatically renewed."), {
            selfRotate: <b data-i18n-ignore>self_rotate</b>,
          })}
        </li>
      </ul>
    </div>
  );
}

function ResponseDetails({ error }: { error: ConnectionError }) {
  const { t } = useI18n();
  if (!error.request && !error.headers && !error.body) return null;
  const copy = (label: string, value: string) =>
    void navigator.clipboard?.writeText(value).then(() => toast.info(t(`${label} copied to clipboard`)));
  const block = (label: string, copyLabel: string, value?: string) =>
    value ? (
      <>
        <div className="gitlab-settings-details-label">
          <span>{t(`${label}:`)}</span>
          <ScopedFlowTooltip label={t(`Copy ${copyLabel}`)}>
            <button type="button" aria-label={t(`Copy ${copyLabel}`)} onClick={() => copy(label, value)}>
              <Copy aria-hidden="true" />
            </button>
          </ScopedFlowTooltip>
        </div>
        <pre data-i18n-ignore>{value}</pre>
      </>
    ) : null;
  return (
    <details open className="gitlab-settings-details">
      <summary>{t("Response details")}</summary>
      <div>
        {block("Request", "request", error.request)}
        {block("Headers", "headers", error.headers)}
        {block("Body", "body", error.body)}
      </div>
    </details>
  );
}

function ConnectionErrorMessage({ error }: { error: ConnectionError }) {
  const { t } = useI18n();
  const hasDetails = Boolean(error.request || error.headers || error.body);
  return (
    <div className="gitlab-settings-connection-error" role="alert">
      <p>
        {t(error.message)}
        {hasDetails ? ` ${t("See below for more details.")}` : ""}
      </p>
      <ResponseDetails error={error} />
    </div>
  );
}

function GitLabSetupForm({
  onCancel,
  onConnected,
}: {
  onCancel: () => void;
  onConnected: (result: Awaited<ReturnType<typeof connectGitLab>>) => Promise<void>;
}) {
  const { t } = useI18n();
  const [token, setToken] = useState("");
  const [url, setUrl] = useState("");
  const [urlTouched, setUrlTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ConnectionError>();
  const urlError = url.trim() && !/^https:\/\/[^\s/]+\.[^\s]+$/i.test(url.trim()) ? HTTPS_URL : "";
  const tokenError = !token.trim() ? TOKEN_REQUIRED : "";

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setUrlTouched(true);
    setError(undefined);
    if (urlError) return;
    if (tokenError) {
      setError({ message: TOKEN_REQUIRED });
      return;
    }
    setBusy(true);
    try {
      const result = await connectGitLab({ token: token.trim(), url: url.trim() });
      setToken("");
      await onConnected(result);
    } catch (caught) {
      setError(connectionError(caught, "Something unexpected went wrong when connecting with GitLab."));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="gitlab-settings-card gitlab-settings-setup" aria-label={t("Set up GitLab")}>
      <form noValidate onSubmit={(event) => void submit(event)}>
        <div className="gitlab-settings-field">
          <label htmlFor="gitlab-access-token">{t("API access token")}</label>
          <TokenHelp />
          <input
            id="gitlab-access-token"
            type="password"
            autoComplete="off"
            placeholder="••••••••••••••••••••••••••"
            value={token}
            onChange={(event) => setToken(event.target.value)}
          />
        </div>
        <div className="gitlab-settings-field">
          <label htmlFor="gitlab-custom-url">{t("Custom GitLab URL (optional, self-hosted only)")}</label>
          <input
            id="gitlab-custom-url"
            type="url"
            inputMode="url"
            autoComplete="off"
            placeholder="https://gitlab.your-company.com"
            value={url}
            aria-invalid={urlTouched && Boolean(urlError) ? true : undefined}
            aria-describedby={urlTouched && urlError ? "gitlab-custom-url-error" : undefined}
            onBlur={() => setUrlTouched(true)}
            onChange={(event) => setUrl(event.target.value)}
          />
          {urlTouched && urlError && (
            <p className="gitlab-settings-field-error" id="gitlab-custom-url-error">
              {t(urlError)}
            </p>
          )}
        </div>
        <footer>
          <button type="button" className="gitlab-settings-button" onClick={onCancel}>
            {t("Cancel")}
          </button>
          {/* Linear keeps the label and adds the grid loader after it while connecting. */}
          <button type="submit" className="gitlab-settings-button primary" disabled={busy} aria-busy={busy || undefined}>
            {t("Connect")}
            {busy && <GridLoader variant="pong" size={16} label={t("Connecting…")} />}
          </button>
        </footer>
      </form>
      {error && <ConnectionErrorMessage error={error} />}
    </section>
  );
}

function CopyField({ id, label, value, kind }: { id?: string; label?: string; value: string; kind: "URL" | "secret" }) {
  const { t } = useI18n();
  const copy = () =>
    void navigator.clipboard?.writeText(value).then(() =>
      toast.info(t("Copied to clipboard"), {
        description: t(kind === "URL" ? "The webhook URL has been copied to your clipboard." : "The webhook secret has been copied to your clipboard."),
      }),
    );
  return (
    <div className="gitlab-settings-copy">
      <input id={id} aria-label={label} readOnly value={value} data-i18n-ignore onFocus={(event) => event.target.select()} />
      <ScopedFlowTooltip label={t("Copy to clipboard")}>
        <button type="button" aria-label={t("Copy to clipboard")} onClick={copy}>
          <Copy aria-hidden="true" />
        </button>
      </ScopedFlowTooltip>
    </div>
  );
}

function GitLabWebhookSetup({ url, secret, onDone }: { url: string; secret: string; onDone: () => void }) {
  const { t } = useI18n();
  return (
    <section className="gitlab-settings-card gitlab-settings-webhook" aria-label={t("Set up webhook")}>
      <h4>{t("Set up webhook")}</h4>
      <div className="gitlab-settings-field">
        <label htmlFor="gitlab-webhook-url">{t("Webhook URL")}</label>
        <p>
          {template(
            t(
              "Create a new webhook and paste this URL in {project} or {group} settings. To connect multiple GitLab projects to Flow, use a {group} webhook.",
            ),
            { project: <b>{t("project")}</b>, group: <b>{t("group")}</b> },
          )}
        </p>
        <CopyField id="gitlab-webhook-url" value={url} kind="URL" />
        <p>{t("Add the following secret to the webhook:")}</p>
        <CopyField label={t("Webhook secret")} value={secret} kind="secret" />
        <div className="gitlab-settings-help">
          {t("Enable these triggers:")}
          <ul>
            <li>{t("Push events")}</li>
            <li>{t("Comments")}</li>
            <li>{t("Merge request events")}</li>
            <li>{t("Pipeline events")}</li>
          </ul>
        </div>
      </div>
      <footer>
        <button type="button" className="gitlab-settings-button primary" onClick={onDone}>
          {t("Done")}
        </button>
      </footer>
    </section>
  );
}

function GitLabTokenSection({ connection, onReload }: { connection: IntegrationConnection; onReload: () => Promise<void> }) {
  const { t, formatDate } = useI18n();
  const [busy, setBusy] = useState(false);
  const [updating, setUpdating] = useState(false);
  const config = connection.config ?? {};
  const when = (value: string) => formatDate(value, { dateStyle: "medium", timeStyle: "short" });
  const expired = isExpired(config.expiresAt);
  const rotationEnabled = config.rotationEnabled === "true";
  const cannotRotate = expired
    ? t("Update the expired token to enable automatic rotation.")
    : config.canSelfRotate === "true"
      ? undefined
      : t("This token cannot rotate itself. Update it with api or both read_api and self_rotate scopes.");
  const failure = config.rotationFailureReason ? t(config.rotationFailureReason) : undefined;
  const attention = expired
    ? t("Token expired")
    : failure || (rotationEnabled && !config.nextRotationAt)
      ? t("Needs attention")
      : undefined;
  const expiry = config.expiresAt
    ? (expired ? t("Expired {date}. Update the token to reconnect.") : t("Expires {date}")).replace("{date}", when(config.expiresAt))
    : t("Expiry unavailable");
  const rotateTooltip = [
    config.lastRotatedAt ? t("Last rotated {date}.").replace("{date}", when(config.lastRotatedAt)) : "",
    t("Rotating revokes the current token and saves its replacement in Flow."),
  ]
    .filter(Boolean)
    .join(" ");
  const rotationDescription =
    cannotRotate ||
    failure ||
    (rotationEnabled
      ? config.nextRotationAt
        ? t("Next rotation {date}").replace("{date}", when(config.nextRotationAt))
        : t("Automatic rotation needs attention")
      : t("Automatically renew the token before it expires."));

  const rotate = async () => {
    setBusy(true);
    try {
      await rotateGitLabToken(connection.id);
      await onReload();
      toast.success(t("GitLab token rotated"));
    } catch (error) {
      await onReload().catch(() => undefined);
      toast.error(t("GitLab token rotation failed"), { description: error instanceof Error ? t(error.message) : undefined });
    } finally {
      setBusy(false);
    }
  };
  const toggleRotation = async () => {
    setBusy(true);
    try {
      await setGitLabTokenRotation(connection.id, !rotationEnabled);
      await onReload();
    } catch (error) {
      toast.error(t("Could not update automatic rotation"), { description: error instanceof Error ? t(error.message) : undefined });
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="gitlab-settings-card" aria-label={t("Access token")}>
      <div className="gitlab-settings-row">
        <div>
          <strong className="gitlab-settings-row-title">
            {t("Access token")}
            {attention && <TriangleAlert className="gitlab-settings-warning-icon" aria-label={t("Needs attention")} />}
          </strong>
          <span>{expiry}</span>
        </div>
        <DropdownMenu>
          <ScopedFlowTooltip label={busy ? t("A token change is in progress.") : undefined}>
            <DropdownMenuTrigger asChild>
              <button type="button" className="gitlab-settings-status-button" aria-label={t("Manage GitLab token")}>
                <i data-state={attention ? "warning" : "ok"} aria-hidden="true" />
                <span>{attention ?? t("Connected")}</span>
                <ChevronDown aria-hidden="true" />
              </button>
            </DropdownMenuTrigger>
          </ScopedFlowTooltip>
          <DropdownMenuContent align="end" className="gitlab-settings-menu">
            <DropdownMenuItem
              disabled={busy || Boolean(cannotRotate || failure)}
              title={cannotRotate || failure || rotateTooltip}
              onSelect={() => void rotate()}
            >
              {t("Rotate token")}
            </DropdownMenuItem>
            <DropdownMenuItem disabled={busy} onSelect={() => setUpdating(true)}>
              {t("Update token")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <div className="gitlab-settings-row">
        <div>
          <strong>
            <label htmlFor="gitlab-automatic-token-rotation">{t("Automatic rotation")}</label>
          </strong>
          <span>{rotationDescription}</span>
        </div>
        <ScopedFlowTooltip label={cannotRotate}>
          <span className="gitlab-settings-toggle">
            <Toggle
              checked={rotationEnabled}
              disabled={busy || Boolean(cannotRotate)}
              label={t("Automatic rotation")}
              onChange={() => void toggleRotation()}
              size="regular"
            />
          </span>
        </ScopedFlowTooltip>
      </div>
      <GitLabUpdateTokenDialog
        open={updating}
        connection={connection}
        onClose={() => setUpdating(false)}
        onUpdated={async () => {
          setUpdating(false);
          await onReload();
          toast.success(t("GitLab token updated"));
        }}
      />
    </section>
  );
}

function GitLabUpdateTokenDialog({
  open,
  connection,
  onClose,
  onUpdated,
}: {
  open: boolean;
  connection: IntegrationConnection;
  onClose: () => void;
  onUpdated: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const close = () => {
    setToken("");
    setError(undefined);
    onClose();
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy || !token.trim()) return;
    setBusy(true);
    setError(undefined);
    try {
      await updateGitLabToken(connection.id, token.trim());
      setToken("");
      await onUpdated();
    } catch (caught) {
      setError(caught instanceof Error && caught.message ? caught.message : "The token could not be updated. Please try again.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(next) => !next && close()}>
      <DialogContent className="gitlab-settings-dialog" closeLabel={t("Close")} aria-describedby="gitlab-token-update-description">
        <form onSubmit={(event) => void submit(event)}>
          <DialogTitle>{t("Update GitLab token")}</DialogTitle>
          <div className="gitlab-settings-dialog-body">
            <div id="gitlab-token-update-description">
              <TokenHelp />
            </div>
            <input
              aria-label={t("API access token")}
              type="password"
              autoComplete="off"
              autoFocus
              value={token}
              disabled={busy}
              aria-invalid={error ? true : undefined}
              onChange={(event) => setToken(event.target.value)}
            />
            {error && (
              <p className="gitlab-settings-field-error" role="alert">
                {t(error)}
              </p>
            )}
          </div>
          <footer>
            <button type="button" className="gitlab-settings-button" disabled={busy} onClick={close}>
              {t("Cancel")}
            </button>
            <button type="submit" className="gitlab-settings-button primary" disabled={busy || !token.trim()} aria-busy={busy || undefined}>
              {t("Update token")}
              {busy && <GridLoader variant="pong" size={16} label={t("Updating…")} />}
            </button>
          </footer>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function GitLabConnectivityIssue({ connection, onReload }: { connection: IntegrationConnection; onReload: () => Promise<void> }) {
  const { t } = useI18n();
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState<ConnectionError>();
  if (connection.lastTestStatus !== "error" || isExpired(connection.config?.expiresAt)) return null;
  const test = async () => {
    setTesting(true);
    setError(undefined);
    try {
      await testIntegrationConnection("gitlab", connection.id);
      await onReload();
      toast.success(t("Connection restored"), { description: t("Your GitLab integration is now active.") });
    } catch (caught) {
      const failure = connectionError(caught, "Unknown error");
      setError(failure);
      toast.error(t("Connection test failed"), { description: t(failure.message) });
    } finally {
      setTesting(false);
    }
  };
  return (
    <section className="gitlab-settings-card" aria-label={t("GitLab connectivity issues")}>
      <div className="gitlab-settings-row">
        <TriangleAlert className="gitlab-settings-warning-icon gitlab-settings-row-image" aria-hidden="true" />
        <div>
          <strong>{t("GitLab connectivity issues")}</strong>
          <span>
            {t("The last request to your GitLab instance failed")}
            {connection.lastError ? `: ${t(connection.lastError)}` : "."}
          </span>
        </div>
        <button type="button" className="gitlab-settings-button borderless" disabled={testing} onClick={() => void test()}>
          {testing ? t("Testing…") : t("Test connection")}
        </button>
      </div>
      {error && (
        <div className="gitlab-settings-card-footer">
          <ConnectionErrorMessage error={error} />
        </div>
      )}
    </section>
  );
}

function GitLabLinkbacks({
  connection,
  onChange,
}: {
  connection?: IntegrationConnection;
  onChange: (key: string, value: string) => Promise<void>;
}) {
  const { t } = useI18n();
  const config = connection?.config ?? {};
  const readonly = config.readonly === "true";
  const enabled = (key: string, fallback: boolean) => (config[key] ?? String(fallback)) === "true" && !readonly;
  const privateOn = enabled("privateLinkbacks", true);
  const publicOn = enabled("publicLinkbacks", false);
  const reason = readonly ? t("Read-only GitLab API access token") : undefined;
  const row = (key: string, label: string, checked: boolean) => (
    <div className="gitlab-settings-row" key={key}>
      <div>
        <strong>{t(label)}</strong>
      </div>
      <ScopedFlowTooltip label={reason}>
        <span className="gitlab-settings-toggle">
          <Toggle
            checked={checked}
            disabled={!connection || readonly}
            label={t(label)}
            onChange={(value) => onChange(key, String(value))}
            size="regular"
          />
        </span>
      </ScopedFlowTooltip>
    </div>
  );
  return (
    <section className="gitlab-settings-card">
      <header>
        <h3>{t("Linkbacks")}</h3>
        <span>
          {t(
            "Automatically comment in GitLab with a link to the Flow issue. Private team issue titles will not be included in the comment.",
          )}
        </span>
      </header>
      {row("privateLinkbacks", "Private/Internal repositories", privateOn)}
      {row("publicLinkbacks", "Public repositories", publicOn)}
      {(privateOn || publicOn) && row("includeDescriptions", "Include issue descriptions in comments", enabled("includeDescriptions", true))}
    </section>
  );
}
