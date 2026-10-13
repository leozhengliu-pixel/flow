/**
 * LS-0648 WebhookEditPage depth + LS-0711 oauthWebhookSecretQuery / show-once signing UX.
 * Full-page settings form (the reference app Create webhook layout) — not a Dialog modal.
 * Existing webhooks also show the signing state, a test event and the
 * delivery log (status, response, duration, attempts, redeliver).
 */
import { useState } from "react";
import { ChevronDown } from "lucide-react";
import { toast } from "@/lib/toast";

import { ViewGlyph } from "@/components/views/view-icon-picker";
import {
  createWebhook,
  deleteWebhook,
  revokeWebhookSecret,
  rotateWebhookSecret,
  sendWebhookTestEvent,
  updateWebhook,
} from "@/lib/api";
import {
  WEBHOOK_RESOURCE_TYPES,
  isAllowedWebhookUrl,
  webhookSigningStatus,
} from "@/lib/webhook-resources";
import type { BootstrapData, Webhook, WebhookDelivery } from "@/types/flow";
import { WebhookDeliveries } from "./webhook-deliveries";

import "./webhook-edit-page.css";

const RESOURCES = WEBHOOK_RESOURCE_TYPES.filter((item) => !item.applicationOnly);

type ActionButtonProps = {
  children: React.ReactNode;
  onClick?: () => void;
  primary?: boolean;
  danger?: boolean;
  disabled?: boolean;
};

function ActionButton({
  children,
  onClick,
  primary,
  danger,
  disabled,
}: ActionButtonProps) {
  return (
    <button
      type="button"
      className={`webhook-edit-action${primary ? " is-primary" : ""}${danger ? " is-danger" : ""}`}
      disabled={disabled}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

function signingSummary(current: Webhook | null, secretPrefix: string) {
  if (!current) return "A signing secret is created when you save.";
  if (current.managedBy === "application")
    return "Signed with the application's webhook signing secret.";
  const status = webhookSigningStatus(current);
  if (status === "revoked")
    return "Signing secret revoked. Deliveries are sent unsigned.";
  if (status === "rotationRequired")
    return "Deliveries are not signed yet. Rotate the secret to start signing them.";
  return secretPrefix ? `Secret prefix ${secretPrefix}…` : "Signing secret on file";
}

export function WebhookEditPage({
  data,
  webhook,
  onClose,
  onSaved,
}: {
  data: BootstrapData;
  webhook: Webhook | null;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [name, setName] = useState(webhook?.name ?? "");
  const [url, setUrl] = useState(webhook?.url ?? "");
  const [resourceTypes, setResourceTypes] = useState<string[]>(
    webhook?.resourceTypes ?? ["issues"],
  );
  const [teamIds, setTeamIds] = useState<string[]>(webhook?.teamIds ?? []);
  const [urlError, setUrlError] = useState("");
  const [revealedSecret, setRevealedSecret] = useState<string | null>(null);
  const [secretPrefix, setSecretPrefix] = useState(webhook?.secretPrefix ?? "");
  const [busy, setBusy] = useState(false);
  const [current, setCurrent] = useState<Webhook | null>(webhook);
  const [testResult, setTestResult] = useState<WebhookDelivery | null>(null);
  const [deliveryRefresh, setDeliveryRefresh] = useState(0);

  const isCreate = !current;
  const managed = current?.managedBy === "application";
  const pageTitle = isCreate ? "Create webhook" : "Edit webhook";
  const primaryLabel = isCreate ? "Create webhook" : "Save";

  const validateUrl = (value: string) => {
    const trimmed = value.trim();
    if (!trimmed) {
      setUrlError("Required");
      return false;
    }
    if (!/^https:\/\//i.test(trimmed)) {
      setUrlError("Please enter a valid HTTPS URL.");
      return false;
    }
    if (!isAllowedWebhookUrl(trimmed)) {
      setUrlError("Please enter a publicly reachable HTTPS URL.");
      return false;
    }
    setUrlError("");
    return true;
  };

  const save = async () => {
    if (!managed && !validateUrl(url)) return;
    setBusy(true);
    try {
      if (current) {
        const updated = await updateWebhook(
          current.id,
          managed
            ? { name: name.trim(), enabled: current.enabled }
            : {
                name: name.trim(),
                url: url.trim(),
                resourceTypes,
                teamIds,
                enabled: current.enabled,
              },
        );
        setCurrent(updated);
        setSecretPrefix(updated.secretPrefix ?? secretPrefix);
        await onSaved();
        toast.success("Webhook updated");
        onClose();
      } else {
        const created = await createWebhook({
          name: name.trim(),
          url: url.trim(),
          resourceTypes,
          teamIds,
          enabled: true,
        });
        setCurrent(created);
        setSecretPrefix(created.secretPrefix ?? "");
        if (created.secret) {
          setRevealedSecret(created.secret);
          toast.success("Webhook created");
          // Keep page open so the secret can be copied once (LS-0711).
          await onSaved();
        } else {
          await onSaved();
          toast.success("Webhook created");
          onClose();
        }
      }
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not save webhook",
      );
    } finally {
      setBusy(false);
    }
  };

  const copySecret = async () => {
    if (!revealedSecret) return;
    try {
      await navigator.clipboard.writeText(revealedSecret);
      toast.success("The signing secret has been copied to your clipboard.");
    } catch {
      toast.error("Could not copy to clipboard");
    }
  };

  const rotate = async () => {
    if (!current) return;
    setBusy(true);
    try {
      const rotated = await rotateWebhookSecret(current.id);
      setCurrent(rotated);
      setSecretPrefix(rotated.secretPrefix ?? "");
      setRevealedSecret(rotated.secret);
      await onSaved();
      toast.success("Signing secret rotated");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not rotate secret",
      );
    } finally {
      setBusy(false);
    }
  };

  const revoke = async () => {
    if (!current) return;
    setBusy(true);
    try {
      await revokeWebhookSecret(current.id);
      setSecretPrefix("");
      setRevealedSecret(null);
      setCurrent({
        ...current,
        secretPrefix: undefined,
        signingEnabled: false,
        signingStatus: "revoked",
        secretRevokedAt: new Date().toISOString(),
      });
      await onSaved();
      toast.success("Signing secret revoked");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not revoke secret",
      );
    } finally {
      setBusy(false);
    }
  };

  const setEnabled = async (enabled: boolean) => {
    if (!current) return;
    setBusy(true);
    try {
      const updated = await updateWebhook(current.id, { enabled });
      setCurrent(updated);
      await onSaved();
      toast.success(enabled ? "Webhook enabled" : "Webhook disabled");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not update webhook",
      );
    } finally {
      setBusy(false);
    }
  };

  const upgradePayload = async () => {
    if (!current) return;
    setBusy(true);
    try {
      const updated = await updateWebhook(current.id, { payloadVersion: 2 });
      setCurrent(updated);
      await onSaved();
      toast.success("Webhook now uses the current payload format");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not update webhook",
      );
    } finally {
      setBusy(false);
    }
  };

  const sendTest = async () => {
    if (!current) return;
    setBusy(true);
    try {
      const result = await sendWebhookTestEvent(current.id);
      setTestResult(result);
      setDeliveryRefresh((value) => value + 1);
      if (result.status === "succeeded") toast.success("Test event delivered");
      else toast.error("Test event failed");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not send test event",
      );
    } finally {
      setBusy(false);
    }
  };

  const canSave =
    Boolean(name.trim()) &&
    (managed || isAllowedWebhookUrl(url.trim())) &&
    resourceTypes.length > 0 &&
    !busy;

  return (
    <div className="webhook-edit-page" data-testid="webhook-edit-page">
      <nav className="webhook-edit-breadcrumb" aria-label="Breadcrumb">
        <button type="button" onClick={onClose}>
          <ChevronDown aria-hidden="true" />
          API settings
        </button>
      </nav>
      <header className="webhook-edit-header">
        <h1>{pageTitle}</h1>
      </header>
      <section className="webhook-edit-card" aria-label={pageTitle}>
        {managed ? (
          <div className="webhook-edit-banner" role="note">
            <p>
              This webhook is managed by an installed application. Its developer
              sets the URL and events; uninstall the application to remove it.
            </p>
          </div>
        ) : null}
        {current && !current.enabled && current.disabledReason === "deliveryFailures" ? (
          <div className="webhook-edit-banner is-danger" role="alert">
            <p>
              This webhook was disabled because deliveries kept failing. Fix the
              endpoint, then enable it again.
            </p>
            <ActionButton disabled={busy} onClick={() => void setEnabled(true)}>
              Enable webhook
            </ActionButton>
          </div>
        ) : null}
        {current && current.enabled && current.failingSince ? (
          <div className="webhook-edit-banner is-warning" role="status">
            <p>
              Recent deliveries are failing. The webhook is disabled if they keep
              failing for 24 hours.
            </p>
          </div>
        ) : null}
        <label className="webhook-edit-field">
          <span>Name</span>
          <input
            autoFocus
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="A descriptive name"
            aria-label="Name"
          />
        </label>
        <label className="webhook-edit-field">
          <span>Endpoint URL</span>
          <input
            value={url}
            disabled={managed}
            onChange={(event) => {
              setUrl(event.target.value);
              if (urlError) validateUrl(event.target.value);
            }}
            onBlur={() => !managed && validateUrl(url)}
            placeholder="https://example.com/webhooks/flow"
            aria-invalid={Boolean(urlError)}
            aria-label="Endpoint URL"
          />
          {urlError ? (
            <span className="webhook-edit-error" role="alert">
              {urlError}
            </span>
          ) : null}
        </label>

        <section className="webhook-signing" aria-label="Signing">
          <header>
            <h3>Signing secret</h3>
            <p>
              Every delivery carries a Flow-Signature header: the HMAC-SHA256 of
              the raw body, keyed with this secret. The full secret is shown
              only once after create or rotate.
            </p>
          </header>
          {revealedSecret ? (
            <div className="webhook-signing-reveal">
              <label>
                Signing secret
                <input readOnly value={revealedSecret} aria-label="Signing secret" />
              </label>
              <ActionButton onClick={() => void copySecret()}>
                Copy secret
              </ActionButton>
            </div>
          ) : (
            <div className="webhook-signing-meta">
              <span>{signingSummary(current, secretPrefix)}</span>
              {current && !managed ? (
                <div className="webhook-signing-actions">
                  <ActionButton disabled={busy} onClick={() => void rotate()}>
                    Rotate
                  </ActionButton>
                  <ActionButton
                    danger
                    disabled={busy || !secretPrefix}
                    onClick={() => void revoke()}
                  >
                    Revoke
                  </ActionButton>
                </div>
              ) : null}
            </div>
          )}
        </section>

        {current && (current.payloadVersion ?? 1) < 2 && !managed ? (
          <div className="webhook-edit-banner" role="note">
            <p>
              This webhook uses the legacy payload format (type "issues",
              action "created"). Switch to the current format to receive
              payloads (type "Issue", action "create", actor,
              url and updatedFrom).
            </p>
            <ActionButton disabled={busy} onClick={() => void upgradePayload()}>
              Use current format
            </ActionButton>
          </div>
        ) : null}

        <fieldset
          className="webhook-edit-fieldset webhook-edit-resources"
          disabled={managed}
        >
          <legend>Data change events</legend>
          <div className="webhook-edit-resource-grid">
            {RESOURCES.map((resource) => (
              <label key={resource.id} className="webhook-edit-check">
                <input
                  type="checkbox"
                  checked={resourceTypes.includes(resource.id)}
                  onChange={(event) =>
                    setResourceTypes((currentTypes) =>
                      event.target.checked
                        ? [...currentTypes, resource.id]
                        : currentTypes.filter((item) => item !== resource.id),
                    )
                  }
                />
                <span>{resource.label}</span>
              </label>
            ))}
          </div>
        </fieldset>

        <fieldset
          className="webhook-edit-fieldset webhook-edit-teams"
          disabled={managed}
        >
          <legend>Team</legend>
          <div className="webhook-edit-team-list">
            <label className="webhook-edit-check">
              <input
                type="checkbox"
                checked={!teamIds.length}
                onChange={() => setTeamIds([])}
              />
              <span>All teams</span>
            </label>
            {data.teams.map((team) => (
              <label key={team.id} className="webhook-edit-check">
                <input
                  type="checkbox"
                  checked={teamIds.includes(team.id)}
                  onChange={(event) =>
                    setTeamIds((currentIds) =>
                      event.target.checked
                        ? [...currentIds, team.id]
                        : currentIds.filter((id) => id !== team.id),
                    )
                  }
                />
                <ViewGlyph color={team.color} icon={team.icon || "Team"} />
                <span data-i18n-ignore>{team.name}</span>
              </label>
            ))}
          </div>
        </fieldset>

        {current && !revealedSecret ? (
          <div className="webhook-signing-meta">
            <span className="webhook-test-result" role="status">
              {testResult ? (
                <>
                  <span>
                    {testResult.status === "succeeded"
                      ? "Test event delivered"
                      : "Test event failed"}
                  </span>{" "}
                  <span data-i18n-ignore>
                    {testResult.status === "succeeded"
                      ? `HTTP ${testResult.responseStatus ?? 200} · ${testResult.durationMs} ms`
                      : testResult.error ||
                        `HTTP ${testResult.responseStatus ?? "—"}`}
                  </span>
                </>
              ) : (
                "Send a WebhookTest event to check the endpoint."
              )}
            </span>
            <ActionButton disabled={busy} onClick={() => void sendTest()}>
              Send test event
            </ActionButton>
          </div>
        ) : null}

        {current && (
          <WebhookDeliveries
            webhookId={current.id}
            refreshKey={deliveryRefresh}
            canRedeliver={current.enabled}
          />
        )}

        <footer className="webhook-edit-footer">
          {current && !revealedSecret && !managed && (
            <ActionButton
              danger
              disabled={busy}
              onClick={() =>
                void deleteWebhook(current.id).then(async () => {
                  await onSaved();
                  onClose();
                })
              }
            >
              Delete
            </ActionButton>
          )}
          {current && !revealedSecret && current.enabled ? (
            <ActionButton disabled={busy} onClick={() => void setEnabled(false)}>
              Disable
            </ActionButton>
          ) : null}
          {current &&
          !revealedSecret &&
          !current.enabled &&
          current.disabledReason !== "deliveryFailures" ? (
            <ActionButton disabled={busy} onClick={() => void setEnabled(true)}>
              Enable
            </ActionButton>
          ) : null}
          <div className="webhook-edit-footer-spacer" />
          {revealedSecret ? (
            <ActionButton primary onClick={onClose}>
              Done
            </ActionButton>
          ) : (
            <>
              <ActionButton disabled={busy} onClick={onClose}>
                Cancel
              </ActionButton>
              <ActionButton
                primary
                disabled={!canSave}
                onClick={() => void save()}
              >
                {primaryLabel}
              </ActionButton>
            </>
          )}
        </footer>
      </section>
    </div>
  );
}
