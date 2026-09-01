import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  AlertCircle,
  CheckCircle2,
  ChevronDown,
  Clock,
  ExternalLink,
  GitMerge,
  GitPullRequest,
  Plus,
  RefreshCw,
  Unplug,
  X,
} from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Toggle } from "@/components/ui/toggle";
import { useI18n } from "@/i18n/i18n";
import {
  connectIntegration,
  disconnectIntegrationConnection,
  retryIntegrationDelivery,
  testIntegrationConnection,
  updateIntegrationConnection,
} from "@/lib/api";
import type { BootstrapData, IntegrationConnection } from "@/types/flow";
import { SettingsSelect } from "./settings-primitives";

import "./code-integration-settings.css";

export function CodeIntegrationSettings({
  provider,
  data,
  onBack,
  onReload,
}: {
  provider: "github" | "gitlab";
  data: BootstrapData;
  onBack: () => void;
  onReload: () => Promise<void>;
}) {
  const { t, formatDate } = useI18n();
  const connections = data.integrationConnections.filter(
    (item) => item.provider === provider,
  );
  const [editing, setEditing] = useState(false),
    [busy, setBusy] = useState(false),
    [testing, setTesting] = useState(false),
    [disconnecting, setDisconnecting] = useState<IntegrationConnection>();
  const [organization, setOrganization] = useState(""),
    [token, setToken] = useState(""),
    [host, setHost] = useState("");
  const title = provider === "github" ? "GitHub" : "GitLab",
    Icon = provider === "github" ? GitPullRequest : GitMerge;
  const description =
    provider === "github"
      ? "Connect GitHub to automate PR workflows, review code in Flow, sync issues and understand your codebase with Flow Agent"
      : "Automate your Merge Request workflow";
  const connect = async () => {
    setBusy(true);
    try {
      await connectIntegration(provider, {
        name: provider === "github" ? organization || title : title,
        config:
          provider === "github" ? { organization } : { apiToken: token, host },
      });
      await onReload();
      setEditing(false);
      setOrganization("");
      setToken("");
      setHost("");
      toast.success(t(`${title} connected`));
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t(`Could not connect ${title}`),
      );
    } finally {
      setBusy(false);
    }
  };
  const disconnect = async () => {
    if (!disconnecting) return;
    setBusy(true);
    try {
      await disconnectIntegrationConnection(provider, disconnecting.id);
      await onReload();
      setDisconnecting(undefined);
      toast.success(t(`${title} disconnected`));
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t(`Could not disconnect ${title}`),
      );
    } finally {
      setBusy(false);
    }
  };
  const testConnection = async () => {
    if (provider !== "gitlab" || !token.trim()) return;
    setTesting(true);
    try {
      await testIntegrationConnection(
        "gitlab",
        editing ? connections[0]?.id : undefined,
        {
          token: token.trim(),
          host: host.trim() || undefined,
        },
      );
      toast.success(t("GitLab connection verified"));
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("Could not test GitLab connection"),
      );
    } finally {
      setTesting(false);
    }
  };
  const setting = async (
    connection: IntegrationConnection,
    key: string,
    value: string,
  ) => {
    try {
      await updateIntegrationConnection(provider, connection.id, {
        config: { [key]: value },
      });
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("Could not update integration"),
      );
    }
  };
  return (
    <div className="code-integration-settings">
      <button className="code-integration-back" onClick={onBack}>
        {t("Integrations")}
        <span>›</span>
      </button>
      <header>
        <div className={`code-provider-icon ${provider}`}>
          <Icon />
        </div>
        <div>
          <h1 data-i18n-ignore>{title}</h1>
          <p>{t(description)}</p>
        </div>
        <aside>
          <span>{connections.length ? t("Enabled by") : t("Built by")}</span>
          <strong data-i18n-ignore>
            {connections.length
              ? (data.users.find(
                  (user) => user.id === connections[0].connectedBy,
                )?.displayName ?? data.viewer.displayName)
              : "Flow"}
          </strong>
          <a
            href={
              provider === "github"
                ? "https://docs.github.com/en/apps"
                : "https://docs.gitlab.com/integration/"
            }
            target="_blank"
            rel="noreferrer"
          >
            {t("Docs")}
            <ExternalLink />
          </a>
        </aside>
      </header>
      <section className="code-integration-section">
        <div className="code-section-title">
          <div>
            <h2>
              {t(
                connections.length
                  ? "Connected organizations"
                  : `Connect ${title}`,
              )}
            </h2>
            {!connections.length && (
              <p>
                {t(
                  provider === "github"
                    ? "Install the Flow app for a GitHub organization."
                    : "Use a GitLab API access token with api or read_api scope.",
                )}
              </p>
            )}
          </div>
          {connections.length > 0 && (
            <button className="code-primary" onClick={() => setEditing(true)}>
              <Plus />
              {t(
                provider === "github"
                  ? "Connect a new organization"
                  : "Connect GitLab",
              )}
            </button>
          )}
        </div>
        {connections.map((connection) => (
          <div className="code-connection-row" key={connection.id}>
            <div className={`code-provider-avatar ${provider}`}>
              <Icon />
            </div>
            <div>
              <strong data-i18n-ignore>
                {connection.config?.organization ||
                  connection.config?.host ||
                  connection.name}
              </strong>
              <span>
                {t("Enabled by")}{" "}
                <b data-i18n-ignore>
                  {data.users.find((user) => user.id === connection.connectedBy)
                    ?.displayName ?? data.viewer.displayName}
                </b>{" "}
                ·{" "}
                {formatDate(connection.createdAt, {
                  month: "short",
                  day: "numeric",
                  year: "numeric",
                })}
                {connection.lastTestStatus && (
                  <>
                    {" · "}
                    {t(
                      connection.lastTestStatus === "ready"
                        ? "Connection verified"
                        : "Connection test failed",
                    )}
                  </>
                )}
              </span>
            </div>
            <DropdownMenu.Root>
              <DropdownMenu.Trigger asChild>
                <button
                  className="code-manage"
                  aria-label={t("Manage connection")}
                >
                  {t("Connected")}
                  <ChevronDown />
                </button>
              </DropdownMenu.Trigger>
              <DropdownMenu.Portal>
                <DropdownMenu.Content
                  className="code-manage-menu"
                  sideOffset={5}
                  align="end"
                >
                  <DropdownMenu.Item
                    onSelect={() =>
                      window.open(
                        connection.config?.host || `https://${provider}.com`,
                        "_blank",
                      )
                    }
                  >
                    <ExternalLink />
                    {t(`Configure in ${title}`)}
                  </DropdownMenu.Item>
                  {provider === "gitlab" && (
                    <DropdownMenu.Item onSelect={() => setEditing(true)}>
                      <RefreshCw />
                      {t("Test connection")}
                    </DropdownMenu.Item>
                  )}
                  <DropdownMenu.Separator />
                  <DropdownMenu.Item
                    className="danger"
                    onSelect={() => setDisconnecting(connection)}
                  >
                    <Unplug />
                    {t("Disconnect…")}
                  </DropdownMenu.Item>
                </DropdownMenu.Content>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>
          </div>
        ))}
        {(editing || !connections.length) && (
          <div className="code-connect-form">
            <div className="code-form-heading">
              <Icon />
              <strong>
                {t(
                  provider === "github"
                    ? "Connect a GitHub organization"
                    : "Setting up GitLab…",
                )}
              </strong>
              {connections.length > 0 && (
                <button
                  aria-label={t("Cancel")}
                  onClick={() => setEditing(false)}
                >
                  <X />
                </button>
              )}
            </div>
            {provider === "github" ? (
              <label>
                {t("GitHub organization")}
                <input
                  autoFocus
                  aria-label={t("GitHub organization")}
                  placeholder="e.g. heliumlabz"
                  value={organization}
                  onChange={(event) => setOrganization(event.target.value)}
                />
              </label>
            ) : (
              <>
                <label>
                  {t("API access token")}
                  <input
                    autoFocus
                    aria-label={t("API access token")}
                    type="password"
                    placeholder="••••••••••••••••••••"
                    value={token}
                    onChange={(event) => setToken(event.target.value)}
                  />
                </label>
                <label>
                  {t("Custom GitLab URL (optional, self-hosted only)")}
                  <input
                    aria-label={t(
                      "Custom GitLab URL (optional, self-hosted only)",
                    )}
                    placeholder="https://gitlab.your-company.com"
                    value={host}
                    onChange={(event) => setHost(event.target.value)}
                  />
                </label>
              </>
            )}
            <footer>
              <button
                onClick={() => {
                  setEditing(false);
                  setOrganization("");
                  setToken("");
                  setHost("");
                }}
              >
                {t("Cancel")}
              </button>
              {provider === "gitlab" && (
                <button
                  disabled={busy || testing || !token.trim()}
                  onClick={() => void testConnection()}
                  type="button"
                >
                  {testing ? t("Testing…") : t("Test connection")}
                </button>
              )}
              <button
                className="code-primary"
                disabled={
                  busy ||
                  testing ||
                  (provider === "github" ? !organization.trim() : !token.trim())
                }
                onClick={() => void connect()}
              >
                {busy ? t("Connecting…") : t("Connect")}
              </button>
            </footer>
          </div>
        )}
      </section>
      {connections[0] && (
        <>
          <IntegrationOptions
            provider={provider}
            connection={connections[0]}
            onChange={setting}
          />
          <IntegrationDeliveryHistory
            data={data}
            connection={connections[0]}
            onReload={onReload}
          />
        </>
      )}
      <Dialog
        open={Boolean(disconnecting)}
        onOpenChange={(open) => !open && setDisconnecting(undefined)}
      >
        <DialogContent className="code-disconnect-dialog">
          <DialogTitle>{t(`Disconnect ${title}?`)}</DialogTitle>
          <p>
            {t(
              "Reviews remain in Flow, but new pull and merge request updates will stop syncing.",
            )}
          </p>
          <footer>
            <button onClick={() => setDisconnecting(undefined)}>
              {t("Cancel")}
            </button>
            <button
              className="danger"
              disabled={busy}
              onClick={() => void disconnect()}
            >
              {t("Disconnect")}
            </button>
          </footer>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function IntegrationDeliveryHistory({
  data,
  connection,
  onReload,
}: {
  data: BootstrapData;
  connection: IntegrationConnection;
  onReload: () => Promise<void>;
}) {
  const { t, formatDate } = useI18n();
  const deliveries = data.integrationDeliveries
    .filter((item) => item.connectionId === connection.id)
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    .slice(0, 50);
  const retry = async (id: string) => {
    try {
      await retryIntegrationDelivery(id);
      await onReload();
      toast.success(t("Delivery queued for retry"));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("Could not retry delivery"),
      );
    }
  };
  return (
    <section className="code-integration-section">
      <div className="code-section-title">
        <div>
          <h2>{t("Delivery history")}</h2>
          <p>{t("Webhook delivery attempts and provider responses")}</p>
        </div>
      </div>
      {deliveries.map((item) => {
        const StatusIcon =
          item.status === "delivered"
            ? CheckCircle2
            : item.status === "failed"
              ? AlertCircle
              : Clock;
        return (
          <div className="code-delivery-row" key={item.id}>
            <StatusIcon data-status={item.status} />
            <div>
              <strong>{t(integrationEventLabel(item.eventType))}</strong>
              <span>
                {t(integrationDeliveryStatusLabel(item.status))} ·{" "}
                {item.attempts}{" "}
                {t(item.attempts === 1 ? "attempt" : "attempts")} ·{" "}
                {formatDate(item.updatedAt, {
                  dateStyle: "medium",
                  timeStyle: "short",
                })}
              </span>
              {item.lastError && <small>{item.lastError}</small>}
            </div>
            {item.status === "failed" && (
              <button
                className="code-manage"
                onClick={() => void retry(item.id)}
              >
                <RefreshCw />
                {t("Retry")}
              </button>
            )}
          </div>
        );
      })}
      {!deliveries.length && (
        <div className="code-delivery-empty">
          <Clock />
          <strong>{t("No deliveries yet")}</strong>
          <span>{t("Incoming webhook activity will appear here.")}</span>
        </div>
      )}
    </section>
  );
}

function integrationDeliveryStatusLabel(status: string) {
  if (status === "delivered") return "Delivered";
  if (status === "failed") return "Failed";
  if (status === "queued") return "Queued";
  if (status === "processing") return "Processing";
  return status;
}

function integrationEventLabel(eventType: string) {
  if (eventType === "pull_request") return "Pull request";
  if (eventType === "merge_request") return "Merge request";
  if (eventType === "issue") return "Issue";
  if (eventType === "push") return "Push";
  if (eventType === "ping") return "Ping";
  return eventType;
}

function IntegrationOptions({
  provider,
  connection,
  onChange,
}: {
  provider: "github" | "gitlab";
  connection: IntegrationConnection;
  onChange: (
    connection: IntegrationConnection,
    key: string,
    value: string,
  ) => Promise<void>;
}) {
  const { t } = useI18n();
  const options = [
    [
      "privateLinkbacks",
      provider === "github"
        ? "Private repositories"
        : "Private/Internal repositories",
    ],
    ["publicLinkbacks", "Public repositories"],
    ["includeDescriptions", "Include issue descriptions in linkbacks"],
    ["magicWords", "Link commits to issues with magic words"],
    ["reviewGuides", "Generate Pull Request guides"],
    ["autoLink", "Automatically link Flow issues"],
  ] as const;
  return (
    <>
      <section className="code-integration-section">
        <h2>{t("Branch format")}</h2>
        <div className="code-setting-row">
          <div>
            <strong>{t("Format")}</strong>
            <span>
              {t(
                "Keep generated branch names consistent across the workspace.",
              )}
            </span>
          </div>
          <SettingsSelect
            label={t("Format")}
            value={
              connection.config?.branchFormat || "username/identifier-title"
            }
            onChange={(value) =>
              void onChange(connection, "branchFormat", value)
            }
            options={[
              "username/identifier-title",
              "identifier-title",
              "identifier/title",
            ]}
          />
        </div>
      </section>
      <section className="code-integration-section">
        <h2>{t("Linkbacks")}</h2>
        {options.map(([key, label]) => {
          const checked =
            connection.config?.[key] ??
            (key === "privateLinkbacks" ||
            key === "includeDescriptions" ||
            key === "reviewGuides"
              ? "true"
              : "false");
          return (
            <div className="code-setting-row" key={key}>
              <div>
                <strong>{t(label)}</strong>
                {key === "reviewGuides" && (
                  <span>
                    {t("Generate guided reviews for new pull requests")}
                  </span>
                )}
              </div>
              <Toggle
                checked={checked === "true"}
                label={t(label)}
                onChange={(value) => onChange(connection, key, String(value))}
                size="regular"
              />
            </div>
          );
        })}
      </section>
    </>
  );
}
