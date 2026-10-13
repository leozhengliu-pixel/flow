import { useEffect, useState } from "react";
import { Copy, RotateCw } from "lucide-react";
import { toast } from "@/lib/toast";
import { FlowTooltip } from "@/components/ui/tooltip";
import { confirmAction } from "@/components/ui/action-dialog-service";
import {
  fetchApplicationWebhook,
  rotateApplicationWebhookSecret,
  updateApplicationWebhook,
} from "@/lib/api";
import {
  WEBHOOK_RESOURCE_TYPES,
  isAllowedWebhookUrl,
} from "@/lib/webhook-resources";
import type { ApplicationWebhookSettings, OAuthApplication } from "@/types/flow";
import {
  SettingsRow,
  SettingsSection,
  SettingsToggle,
} from "./settings-primitives";
import "./application-webhook-settings.css";

/** Stored values are payload type names ("Issue") or ids ("issues"). */
function selectedIDs(values: string[]): string[] {
  return WEBHOOK_RESOURCE_TYPES.filter(
    (item) => values.includes(item.id) || values.includes(item.type),
  ).map((item) => item.id);
}

/**
 * The developer's webhook for an OAuth application: one URL, its events and
 * a signing secret. Every workspace that installs the application gets a
 * managed webhook delivering these events, removed again on uninstall (with
 * an OAuthApp revoked event).
 */
export function ApplicationWebhookSettingsSection({
  app,
  onReload,
}: {
  app: OAuthApplication;
  onReload: () => Promise<void>;
}) {
  const [settings, setSettings] = useState<ApplicationWebhookSettings | null>(
    null,
  );
  const [url, setUrl] = useState(app.webhookUrl ?? "");
  const [types, setTypes] = useState<string[]>(
    selectedIDs(app.webhookResourceTypes ?? []),
  );
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    fetchApplicationWebhook(app.id)
      .then((value) => {
        if (!active) return;
        setSettings(value);
        setUrl(value.url ?? "");
        setTypes(selectedIDs(value.resourceTypes ?? []));
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [app.id]);

  const enabled = settings?.enabled ?? Boolean(app.webhookEnabled);

  const save = async (next: { enabled?: boolean }) => {
    const target = url.trim();
    const willEnable = next.enabled ?? enabled;
    if (willEnable && !isAllowedWebhookUrl(target)) {
      setError("Please enter a publicly reachable HTTPS URL.");
      return;
    }
    if (willEnable && types.length === 0) {
      setError("Select at least one event.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await updateApplicationWebhook(app.id, {
        enabled: willEnable,
        url: target,
        resourceTypes: types,
      });
      setSettings(result);
      if (result.secret) setSecret(result.secret);
      toast.success("Application webhook saved");
      await onReload();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not save webhook",
      );
    } finally {
      setBusy(false);
    }
  };

  const rotate = async () => {
    if (
      !(await confirmAction("Rotate webhook signing secret?", {
        description:
          "Deliveries to every installing workspace are signed with the new secret right away.",
        confirmLabel: "Rotate",
        danger: true,
      }))
    )
      return;
    setBusy(true);
    try {
      const result = await rotateApplicationWebhookSecret(app.id);
      setSettings(result);
      setSecret(result.secret ?? "");
      await onReload();
    } catch (reason) {
      toast.error(
        reason instanceof Error ? reason.message : "Could not rotate secret",
      );
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret);
      toast.success("The signing secret has been copied to your clipboard.");
    } catch {
      toast.error("Could not copy to clipboard");
    }
  };

  return (
    <SettingsSection
      title="Application webhook"
      description="Created in every workspace that installs this application and removed when it is uninstalled."
    >
      <SettingsRow
        title="Webhooks"
        description="Send events from installing workspaces to your endpoint."
      >
        <SettingsToggle
          label="Application webhook enabled"
          checked={enabled}
          disabled={busy}
          onChange={(value) => void save({ enabled: value })}
        />
      </SettingsRow>
      <div className="application-webhook-form">
        <label className="application-webhook-field">
          <span>Webhook URL</span>
          <input
            value={url}
            placeholder="https://example.com/webhooks/flow"
            aria-label="Webhook URL"
            onChange={(event) => setUrl(event.target.value)}
          />
        </label>
        <fieldset className="application-webhook-events">
          <legend>Events</legend>
          {WEBHOOK_RESOURCE_TYPES.map((item) => (
            <label key={item.id}>
              <input
                type="checkbox"
                checked={types.includes(item.id)}
                onChange={(event) =>
                  setTypes((current) =>
                    event.target.checked
                      ? [...current, item.id]
                      : current.filter((value) => value !== item.id),
                  )
                }
              />
              <span>{item.label}</span>
            </label>
          ))}
        </fieldset>
        {error ? (
          <p className="application-webhook-error" role="alert">
            {error}
          </p>
        ) : null}
        <div className="application-webhook-actions">
          <span className="application-muted">
            {settings ? (
              <>
                <span>Installed workspaces</span>{" "}
                <span data-i18n-ignore>{settings.installations}</span>
              </>
            ) : null}
          </span>
          <button
            type="button"
            className="settings-action"
            disabled={busy}
            onClick={() => void save({})}
          >
            Save webhook
          </button>
        </div>
      </div>
      <SettingsRow
        title="Signing secret"
        description="Verify the Flow-Signature header (HMAC-SHA256 of the raw body) with this secret."
      >
        <div className="application-credential-secret">
          <code data-i18n-ignore>
            {secret ||
              (settings?.secretPrefix
                ? `${settings.secretPrefix}…`
                : "Created when the webhook is enabled")}
          </code>
          {settings?.signingEnabled ? (
            <FlowTooltip label="Rotate secret">
              <button
                type="button"
                className="settings-icon-action"
                aria-label="Rotate webhook signing secret"
                disabled={busy}
                onClick={() => void rotate()}
              >
                <RotateCw size={14} />
              </button>
            </FlowTooltip>
          ) : null}
          {secret ? (
            <FlowTooltip label="Copy to clipboard">
              <button
                type="button"
                className="settings-icon-action"
                aria-label="Copy webhook signing secret"
                onClick={() => void copy()}
              >
                <Copy size={14} />
              </button>
            </FlowTooltip>
          ) : null}
        </div>
      </SettingsRow>
    </SettingsSection>
  );
}
