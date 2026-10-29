/**
 * LS-0557 SlackUpdates — post org project / initiative updates to a Slack channel.
 * Shared by Project updates + Initiatives settings. REST authorize/disconnect only.
 */
import { ArrowUpRight } from "lucide-react";
import { useState } from "react";
import { toast } from "@/lib/toast";

import { IntegrationBrandIcon } from "@/components/settings/integration-brand-icon";
import { useI18n } from "@/i18n/i18n";
import {
  authorizeIntegration,
  disconnectIntegrationConnection,
} from "@/lib/api";
import type { BootstrapData } from "@/types/flow";
import type { ChatIntegrationProvider } from "@/types/chat-integrations";
import { AppLink } from "@/components/ui/app-link";
import { integrationSettingsPath } from "@/lib/app-routes";
import { useChatPlatform } from "@/lib/chat-platform";
import { chatProviderName } from "@/lib/integration-catalog";


import { findSlackUpdatesConnection, SCOPE_BY_KIND, type SlackUpdatesKind } from "./slack-updates-model";

type SlackUpdatesProps = {
  data: BootstrapData;
  kind: SlackUpdatesKind;
  disabled?: boolean;
  onReload: () => Promise<void>;
  /** `card` matches Initiatives FeatureCard; `row` matches Project updates rows. */
  variant?: "card" | "row";
};

/** Project or initiative updates on the deployment's chat platform: a Slack channel, or Lark/Feishu groups. */
export function SlackUpdates(props: SlackUpdatesProps) {
  const chat = useChatPlatform(props.data);
  if (chat.provider) return <ChatGroupUpdates {...props} provider={chat.provider} />;
  return <SlackChannelUpdates {...props} />;
}

/**
 * On Lark or Feishu, updates go to the groups each team's owners bind: the
 * same row, linking to the integration where the groups are managed.
 */
function ChatGroupUpdates({ data, kind, disabled = false, variant = "card", provider }: SlackUpdatesProps & { provider: ChatIntegrationProvider }) {
  const { t } = useI18n();
  const name = chatProviderName(provider, t);
  const format = (text: string) => text.replaceAll("{name}", name);
  const heading = format(t("{name} notifications"));
  const hint = format(t(kind === "project"
    ? "Project updates are posted to the {name} groups of the project's teams that share project updates, and never when a private team would be disclosed"
    : "Initiative updates are posted to the {name} groups connected to the initiative and to its lead team's groups that share initiative updates, and never with a private team's content"));
  const title = format(t(kind === "project" ? "Send project updates to {name} groups" : "Send initiative updates to {name} groups"));
  const description = format(t(kind === "project" ? "Team owners choose the groups in each team's {name} notifications" : "An initiative's owner connects its groups from the initiative's notifications; team owners choose their teams' groups"));
  const href = integrationSettingsPath(data.workspace.urlKey, provider);
  const action = <AppLink className={variant === "row" ? "iu-slack-action" : "feature-button"} href={href} aria-disabled={disabled || undefined}>{t("Open settings")}<ArrowUpRight size={variant === "row" ? 14 : 11} /></AppLink>;
  if (variant === "row") {
    return <section className="iu-section slack-updates-row" data-kind={kind} data-provider={provider}>
      <h2>{heading}</h2>
      <p>{hint}</p>
      <div className="iu-card iu-slack-row">
        <span className="iu-slack-icon"><IntegrationBrandIcon provider={provider} /></span>
        <span className="iu-slack-copy"><strong>{title}</strong><small>{description}</small></span>
        {action}
      </div>
    </section>;
  }
  return <div className={`slack-updates feature-subsection${disabled ? " is-disabled" : ""}`} data-kind={kind} data-provider={provider} aria-disabled={disabled}>
    <header><h3>{heading}</h3><p>{hint}</p></header>
    <div className="feature-card">
      <div className="initiative-integration-row">
        <IntegrationBrandIcon provider={provider} />
        <div><strong>{title}</strong><span>{description}</span></div>
        {action}
      </div>
    </div>
  </div>;
}

function SlackChannelUpdates({
  data,
  kind,
  disabled = false,
  onReload,
  variant = "card",
}: SlackUpdatesProps) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const connection = findSlackUpdatesConnection(data, kind);
  const connected = connection?.status === "connected" ? connection : undefined;
  const creator = connected
    ? (data.users ?? []).find((user) => user.id === connected.connectedBy)
    : undefined;
  const canManage = ["owner", "admin"].includes(data.viewerRole);
  const noun = kind === "project" ? "project" : "initiative";

  const toggle = async () => {
    if (!canManage || disabled) return;
    setBusy(true);
    try {
      if (connected) {
        await disconnectIntegrationConnection("slack", connected.id);
      } else {
        await authorizeIntegration("slack", {
          name: "Slack",
          config: {
            scope: SCOPE_BY_KIND[kind],
            ...(kind === "project" ? { source: "project-updates" } : {}),
          },
        });
      }
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("Could not update Slack channel"),
      );
    } finally {
      setBusy(false);
    }
  };

  const title = connected
    ? connected.name || connected.config?.channelName || "Slack"
    : t(`Send ${noun} updates to a Slack channel`);
  const description = connected
    ? t(`Broadcasting ${noun} updates`)
    : t(`Connect a channel to send all ${noun} updates to`);
  const permissionHint =
    kind === "initiative"
      ? t(
          "Updates are only posted to Slack for workspace-level initiatives or initiatives led by a public team",
        )
      : t(
          "Updates are only posted to Slack when associated with at least one non-private team",
        );

  const actionLabel = connected ? t("Disconnect") : t("Connect");
  const enabledBy =
    connected && creator
      ? t("Enabled by {name} on {date}")
          .replace("{name}", creator.displayName || creator.name)
          .replace("{date}", new Date(connected.createdAt).toLocaleDateString())
      : undefined;

  if (variant === "row") {
    return (
      <section className="iu-section slack-updates-row" data-kind={kind}>
        <h2>{t("Slack notifications")}</h2>
        <p>{permissionHint}</p>
        <div className="iu-card iu-slack-row">
          <span className="iu-slack-icon">
            <IntegrationBrandIcon provider="slack" />
          </span>
          <span className="iu-slack-copy">
            <strong>{title}</strong>
            <small>{description}</small>
          </span>
          <svg
            className="iu-slack-info"
            viewBox="0 0 16 16"
            role="img"
            aria-label={t("Slack channel updates")}
          >
            <title>{enabledBy ?? permissionHint}</title>
            <circle cx="8" cy="8" r="7" fill="currentColor" />
            <path
              d="M8 7.2v3.6M8 4.8v1"
              stroke="var(--theme-surface-2)"
              strokeWidth="1.6"
              strokeLinecap="round"
            />
          </svg>
          <button
            type="button"
            className="iu-slack-action"
            disabled={disabled || busy || !canManage}
            title={enabledBy}
            onClick={() => void toggle()}
          >
            {connected ? (
              actionLabel
            ) : (
              <>
                {actionLabel}
                <ArrowUpRight size={14} />
              </>
            )}
          </button>
        </div>
      </section>
    );
  }

  return (
    <div
      className={`slack-updates feature-subsection${disabled ? " is-disabled" : ""}`}
      data-kind={kind}
      aria-disabled={disabled}
    >
      <header>
        <h3>{t("Slack notifications")}</h3>
        <p>{permissionHint}</p>
      </header>
      <div className="feature-card">
        <div className="initiative-integration-row">
          <IntegrationBrandIcon provider="slack" />
          <div>
            <strong>{title}</strong>
            <span>{description}</span>
          </div>
          <button
            type="button"
            className="feature-button"
            disabled={disabled || busy || !canManage}
            title={enabledBy}
            onClick={() => void toggle()}
          >
            {connected ? (
              actionLabel
            ) : (
              <>
                {actionLabel}
                <ArrowUpRight size={11} />
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
