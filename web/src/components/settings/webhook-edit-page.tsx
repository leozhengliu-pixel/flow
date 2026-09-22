/**
 * LS-0648 WebhookEditPage depth + LS-0711 oauthWebhookSecretQuery / show-once signing UX.
 * Dialog chrome (Flow API settings) with Linear-aligned HTTPS + Signing secret controls.
 */
import { useState } from "react";
import { toast } from "sonner";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import {
  createWebhook,
  deleteWebhook,
  revokeWebhookSecret,
  rotateWebhookSecret,
  updateWebhook,
} from "@/lib/api";
import type { BootstrapData, Webhook } from "@/types/flow";
import { WebhookFailureEvents } from "./webhook-failure-events";

import "./webhook-edit-page.css";

const RESOURCES = [
  "issues",
  "comments",
  "projects",
  "cycles",
  "documents",
  "customers",
] as const;

function title(resource: string) {
  return resource.charAt(0).toUpperCase() + resource.slice(1).replace(/_/g, " ");
}

function isAllowedWebhookUrl(value: string): boolean {
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== "https:") return false;
    const host = parsed.hostname.toLowerCase();
    if (
      host === "localhost" ||
      host === "127.0.0.1" ||
      host === "0.0.0.0" ||
      host.endsWith(".local")
    ) {
      return false;
    }
    return Boolean(host);
  } catch {
    return false;
  }
}

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
      setUrlError("Please enter a HTTPS URL.");
      return false;
    }
    setUrlError("");
    return true;
  };

  const save = async () => {
    if (!validateUrl(url)) return;
    setBusy(true);
    try {
      const input = {
        name: name.trim(),
        url: url.trim(),
        resourceTypes,
        teamIds,
        enabled: current?.enabled ?? true,
      };
      if (current) {
        const updated = await updateWebhook(current.id, input);
        setCurrent(updated);
        setSecretPrefix(updated.secretPrefix ?? secretPrefix);
        await onSaved();
        toast.success("Webhook updated");
        onClose();
      } else {
        const created = await createWebhook(input);
        setCurrent(created);
        setSecretPrefix(created.secretPrefix ?? "");
        if (created.secret) {
          setRevealedSecret(created.secret);
          toast.success("Webhook created");
          // Keep dialog open so the secret can be copied once (LS-0711).
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
      setCurrent({ ...current, secretPrefix: undefined, secretRevokedAt: new Date().toISOString() });
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

  const canSave =
    Boolean(name.trim()) &&
    isAllowedWebhookUrl(url.trim()) &&
    resourceTypes.length > 0 &&
    !busy;

  return (
    <Dialog open onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="settings-invite-dialog settings-webhook-dialog webhook-edit-dialog">
        <DialogTitle>
          {current ? "Edit webhook" : "Create webhook"}
        </DialogTitle>
        <p className="webhook-edit-crumb">API settings</p>
        <label>
          Name
          <input
            autoFocus
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label>
          Endpoint URL
          <input
            value={url}
            onChange={(event) => {
              setUrl(event.target.value);
              if (urlError) validateUrl(event.target.value);
            }}
            onBlur={() => validateUrl(url)}
            placeholder="https://example.com/webhooks/flow"
            aria-invalid={Boolean(urlError)}
          />
          {urlError ? (
            <span className="webhook-edit-error" role="alert">
              {urlError}
            </span>
          ) : null}
        </label>
        <fieldset className="settings-check-list">
          <legend>Resources</legend>
          {RESOURCES.map((resource) => (
            <label key={resource}>
              <input
                type="checkbox"
                checked={resourceTypes.includes(resource)}
                onChange={(event) =>
                  setResourceTypes((currentTypes) =>
                    event.target.checked
                      ? [...currentTypes, resource]
                      : currentTypes.filter((item) => item !== resource),
                  )
                }
              />
              {title(resource)}
            </label>
          ))}
        </fieldset>
        <fieldset className="settings-check-list">
          <legend>Teams</legend>
          <label>
            <input
              type="checkbox"
              checked={!teamIds.length}
              onChange={() => setTeamIds([])}
            />
            All teams
          </label>
          {data.teams.map((team) => (
            <label key={team.id}>
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
        </fieldset>

        <section className="webhook-signing" aria-label="Signing">
          <header>
            <h3>Signing</h3>
            <p>
              Use the signing secret to verify that deliveries come from Flow.
              The full secret is shown only once after create or rotate.
            </p>
          </header>
          {revealedSecret ? (
            <div className="webhook-signing-reveal">
              <label>
                Signing secret
                <input readOnly value={revealedSecret} />
              </label>
              <ActionButton onClick={() => void copySecret()}>
                Copy secret
              </ActionButton>
            </div>
          ) : (
            <div className="webhook-signing-meta">
              <span>
                {secretPrefix
                  ? `Secret prefix ${secretPrefix}…`
                  : current?.secretRevokedAt
                    ? "Signing secret revoked"
                    : current
                      ? "Signing secret on file"
                      : "A signing secret is created when you save."}
              </span>
              {current ? (
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

        {current && <WebhookFailureEvents webhookId={current.id} />}

        <footer>
          {current && (
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
          <ActionButton disabled={busy} onClick={onClose}>
            {revealedSecret ? "Done" : "Cancel"}
          </ActionButton>
          {!(current && revealedSecret) && (
            <ActionButton
              primary
              disabled={!canSave}
              onClick={() => void save()}
            >
              Save
            </ActionButton>
          )}
        </footer>
      </DialogContent>
    </Dialog>
  );
}
