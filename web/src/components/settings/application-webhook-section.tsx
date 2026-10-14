import { Copy, MoreHorizontal, RefreshCw, ToggleLeft, ToggleRight } from "lucide-react";

import { confirmAction } from "@/components/ui/action-dialog-service";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { FlowTooltip } from "@/components/ui/tooltip";
import { useI18n } from "@/i18n/i18n";
import { fetchApplicationWebhookSecret, rotateApplicationWebhookSecret, updateApplicationWebhook } from "@/lib/api";
import { APPLICATION_WEBHOOK_EVENT_GROUPS } from "@/lib/oauth-app-manifest";
import { toast } from "@/lib/toast";
import type { OAuthApplication, WebhookFailureEvent } from "@/types/flow";

import { SettingsRow as Row, SettingsSection as Section } from "./settings-primitives";
import "./oauth-application-settings.css";

/** The reference app masks the signing secret with 32 bullets; Copy fetches the real secret. */
const MASKED_SECRET = "•".repeat(32);

const EVENT_LABELS = new Map(APPLICATION_WEBHOOK_EVENT_GROUPS.flatMap((group) => group.events.map((event) => [event.type, event.label] as const)));
const EVENT_ORDER = APPLICATION_WEBHOOK_EVENT_GROUPS.flatMap((group) => group.events.map((event) => event.type));

/**
 * The reference app's "Webhooks" and "Webhook delivery failures" sections of an OAuth application's page: delivery status (with
 * Disable / Enable webhooks), the URL, the masked signing secret (rotate copies the new one) and the events.
 */
export function ApplicationWebhookSection({ app, failures, onReload }: { app: OAuthApplication; failures: WebhookFailureEvent[]; onReload: () => Promise<void> }) {
  const { t } = useI18n();
  const copy = async (value: string | (() => Promise<string>)) => {
    try {
      await navigator.clipboard.writeText(typeof value === "string" ? value : await value());
      toast.success(t("Copied to clipboard"));
    } catch {
      toast.error(t("Could not copy to clipboard"));
    }
  };
  const rotate = async () => {
    const confirmed = await confirmAction(t("Rotate your signing secret?"), {
      description: t("The current signing secret will be invalidated. You will need to update any services that verify webhook signatures. You cannot undo this action."),
      confirmLabel: t("Rotate"),
    });
    if (!confirmed) return;
    try {
      const next = await rotateApplicationWebhookSecret(app.id);
      if (next.secret) await navigator.clipboard.writeText(next.secret);
      toast.success(t("Secret rotated and copied to clipboard"), { description: t("Your new signing secret has been copied to your clipboard.") });
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t("Could not rotate secret"));
    }
  };
  const setEnabled = async (enabled: boolean) => {
    try {
      await updateApplicationWebhook(app.id, { enabled });
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t("Could not update webhook"));
    }
  };
  const types = app.webhookResourceTypes ?? [];
  const events = [...types].sort((a, b) => order(a) - order(b)).map((type) => t(EVENT_LABELS.get(type) ?? type));
  return (
    <>
      <Section className="oauth-app-section" title={t("Webhooks")}>
        <Row
          title={t("Delivery status")}
          description={
            <span className="oauth-app-webhook-status" data-enabled={app.webhookEnabled ? "true" : "false"}>
              <i aria-hidden="true" />
              {t(app.webhookEnabled ? "Enabled" : "Disabled")}
            </span>
          }
        >
          <DropdownMenu>
            <DropdownMenuTrigger className="oauth-app-icon-button is-small" aria-label={t("Webhook options")}>
              <MoreHorizontal aria-hidden="true" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" sideOffset={4} className="oauth-app-menu">
              {app.webhookEnabled ? (
                <DropdownMenuItem onSelect={() => void setEnabled(false)}><ToggleLeft aria-hidden="true" />{t("Disable webhooks")}</DropdownMenuItem>
              ) : (
                <DropdownMenuItem onSelect={() => void setEnabled(true)}><ToggleRight aria-hidden="true" />{t("Enable webhooks")}</DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </Row>
        <Row title={t("URL")} description={<span className="oauth-app-mono" data-i18n-ignore>{app.webhookUrl}</span>}>
          <FlowTooltip label={t("Copy to clipboard")}>
            <button type="button" className="oauth-app-icon-button" aria-label={t("Copy webhook URL")} onClick={() => void copy(app.webhookUrl ?? "")}><Copy aria-hidden="true" /></button>
          </FlowTooltip>
        </Row>
        <Row title={t("Signing secret")} description={<span className="oauth-app-mono oauth-app-secret" aria-label={t("Signing secret (hidden)")} data-i18n-ignore>{MASKED_SECRET}</span>}>
          <div className="oauth-app-actions">
            <FlowTooltip label={t("Rotate secret")}>
              <button type="button" className="oauth-app-icon-button" aria-label={t("Rotate webhook signing secret")} onClick={() => void rotate()}><RefreshCw aria-hidden="true" /></button>
            </FlowTooltip>
            <FlowTooltip label={t("Copy to clipboard")}>
              <button type="button" className="oauth-app-icon-button" aria-label={t("Copy webhook signing secret")} onClick={() => void copy(async () => (await fetchApplicationWebhookSecret(app.id)).secret)}><Copy aria-hidden="true" /></button>
            </FlowTooltip>
          </div>
        </Row>
        <Row title={t("Events")} description={events.length ? events.join(t(", ")) : t("No events")} />
      </Section>
      <Section className="oauth-app-section" title={t("Webhook delivery failures")} description={t("Failed webhook deliveries are logged after exhausting all retries.")}>
        {failures.length ? (
          failures.map((item) => (
            <Row key={item.id} title={<span data-i18n-ignore>{item.executionId}</span>} description={<span data-i18n-ignore>{`${item.url} · ${item.httpStatus ?? "—"} · ${item.responseOrError.slice(0, 120)}`}</span>} />
          ))
        ) : (
          <Row className="oauth-app-empty-row" title={t("No failures recorded for this webhook")} />
        )}
      </Section>
    </>
  );
}

function order(type: string) {
  const index = EVENT_ORDER.indexOf(type);
  return index < 0 ? EVENT_ORDER.length : index;
}
