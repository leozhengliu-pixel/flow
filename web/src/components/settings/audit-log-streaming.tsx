import { useCallback, useEffect, useState } from "react";
import { Copy } from "lucide-react";
import { toast } from "sonner";

import { confirmAction } from "@/components/ui/action-dialog-service";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  createAuditLogStream,
  deleteAuditLogStream,
  getAuditLogStream,
  updateAuditLogStream,
  type AuditLogStreamStatus,
} from "@/lib/api";
import { SettingsRow, SettingsSection, SettingsToggle } from "./settings-primitives";
import { WebhookFailureEvents } from "./webhook-failure-events";
import "./feature-settings.css";
import "./audit-log-streaming.css";

function newSigningSecret() {
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  return `flow_audit_${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`;
}

function streamDescription(webhook: AuditLogStreamStatus["webhook"]) {
  if (!webhook) return "Stream audit events to a webhook endpoint";
  if (!webhook.enabled) return `Disabled. Delivery to ${webhook.url} stopped after repeated failures.`;
  if (webhook.failingSince) return `Failing. Delivery to ${webhook.url} has been failing.`;
  return `Healthy. Streaming to ${webhook.url}`;
}

/** Audit log streaming: one signed webhook that receives every audit entry. */
export function AuditLogStreaming() {
  const [status, setStatus] = useState<AuditLogStreamStatus>();
  const [creating, setCreating] = useState(false);
  const load = useCallback(async () => {
    try {
      setStatus(await getAuditLogStream());
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not load audit log streaming");
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  const webhook = status?.webhook ?? null;
  const toggle = async () => {
    if (!webhook) {
      setCreating(true);
      return;
    }
    try {
      if (webhook.enabled) {
        const confirmed = await confirmAction("Disable audit log streaming?", {
          description: "This will delete the webhook and stop streaming audit events.",
          confirmLabel: "Disable streaming",
        });
        if (!confirmed) return;
        await deleteAuditLogStream();
        toast.success("Audit log streaming disabled");
      } else {
        await updateAuditLogStream({ enabled: true });
        toast.success("Audit log streaming re-enabled");
      }
      await load();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Could not update audit log streaming");
    }
  };
  return (
    <>
      <SettingsSection title="Streaming">
        <SettingsRow title="Stream logs" description={streamDescription(webhook)}>
          <SettingsToggle
            label="Stream logs"
            checked={webhook?.enabled === true}
            disabled={!status}
            onChange={() => void toggle()}
          />
        </SettingsRow>
      </SettingsSection>
      {webhook && (
        <WebhookFailureEvents
          webhookId={webhook.id}
          failures={status?.failures}
          description="Failed audit log deliveries are recorded after all retries are exhausted."
        />
      )}
      {creating && (
        <CreateAuditStreamDialog
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false);
            void load();
          }}
        />
      )}
    </>
  );
}

function CreateAuditStreamDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [url, setUrl] = useState("");
  const [secret] = useState(newSigningSecret);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    const value = url.trim();
    if (!/^https?:\/\/\S+$/i.test(value)) {
      setError("Enter a valid http or https URL");
      return;
    }
    setBusy(true);
    try {
      await createAuditLogStream({ url: value, secret });
      toast.success("Audit log streaming enabled");
      onCreated();
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "Failed to enable audit log streaming");
      setBusy(false);
    }
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="feature-dialog">
        <DialogTitle asChild>
          <h2>Create audit log webhook</h2>
        </DialogTitle>
        <label>
          URL
          <input
            autoFocus
            autoComplete="off"
            placeholder="https://…"
            maxLength={2048}
            value={url}
            aria-invalid={Boolean(error)}
            onChange={(event) => {
              setUrl(event.target.value);
              setError("");
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter") void submit();
            }}
          />
          {error && <span role="alert">{error}</span>}
        </label>
        <label>
          Signing secret
          <span className="audit-stream-secret">
            <input readOnly value={secret} aria-label="Signing secret" />
            <button
              type="button"
              className="feature-button"
              aria-label="Copy signing secret"
              onClick={() =>
                void navigator.clipboard.writeText(secret).then(() => toast.info("Signing secret copied to clipboard"))
              }
            >
              <Copy size={14} />
            </button>
          </span>
          <span>
            Each delivery carries an X-Flow-Signature header: the HMAC-SHA256 of the request body, signed with this
            secret. Save it now; it is not shown again.
          </span>
        </label>
        <footer className="audit-stream-footer">
          <button type="button" className="feature-button" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="feature-button primary" disabled={busy} onClick={() => void submit()}>
            Create
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}
