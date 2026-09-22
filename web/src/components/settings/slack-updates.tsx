/**
 * LS-0557 SlackUpdates — post org project / initiative updates to a Slack channel.
 * Shared by Project updates + Initiatives settings. REST authorize/disconnect only.
 */
import { ArrowUpRight } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { IntegrationBrandIcon } from "@/components/settings/integration-brand-icon";
import { useI18n } from "@/i18n/i18n";
import {
  authorizeIntegration,
  disconnectIntegrationConnection,
} from "@/lib/api";
import type { BootstrapData, IntegrationConnection } from "@/types/flow";

import "./slack-updates.css";

export type SlackUpdatesKind = "project" | "initiative";

const SCOPE_BY_KIND: Record<SlackUpdatesKind, string> = {
  project: "orgProjectUpdates",
  initiative: "initiative-updates",
};

export function findSlackUpdatesConnection(
  data: BootstrapData,
  kind: SlackUpdatesKind,
): IntegrationConnection | undefined {
  const scope = SCOPE_BY_KIND[kind];
  return (data.integrationConnections ?? []).find((item) => {
    if (item.provider !== "slack") return false;
    const config = item.config ?? {};
    if (kind === "project") {
      return (
        config.scope === scope ||
        config.source === "project-updates" ||
        config.scope === "project-updates"
      );
    }
    return config.scope === scope;
  });
}

export function SlackUpdates({
  data,
  kind,
  disabled = false,
  onReload,
  variant = "card",
}: {
  data: BootstrapData;
  kind: SlackUpdatesKind;
  disabled?: boolean;
  onReload: () => Promise<void>;
  /** `card` matches Initiatives FeatureCard; `row` matches Project updates rows. */
  variant?: "card" | "row";
}) {
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
          "Updates are only posted to Slack for workspace-level projects or projects owned by a public team.",
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
      <section className="slack-updates slack-updates-row" data-kind={kind}>
        <header>
          <h3>{t("Slack notifications")}</h3>
          <p>{permissionHint}</p>
        </header>
        <div className="ip-setting-row">
          <span className="ip-slack-label">
            <IntegrationBrandIcon provider="slack" />
            <span>
              <strong>{title}</strong>
              <small>{description}</small>
            </span>
          </span>
          <button
            type="button"
            className="settings-action"
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
