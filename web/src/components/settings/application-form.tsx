import { useId, useRef, useState } from "react";
import { Copy, KeyRound, TriangleAlert } from "lucide-react";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOptionalTranslate } from "@/i18n/i18n";
import { validateOAuthAppManifest } from "@/lib/api";
import {
  OAUTH_SCOPE_OPTIONS,
  WEBHOOK_RESOURCE_TYPE_OPTIONS,
  parseManifestText,
  type ApplicationFormState,
} from "@/lib/oauth-app-manifest";
import { toast } from "@/lib/toast";
import type { OAuthAppManifest, OAuthAppManifestIssue } from "@/types/flow";

import {
  SettingsRow as Row,
  SettingsSection as Section,
  SettingsToggle as Toggle,
} from "./settings-primitives";
import "./application-pages.css";

export function ManifestIssues({ issues }: { issues: OAuthAppManifestIssue[] }) {
  const t = useOptionalTranslate();
  if (!issues.length) return null;
  return (
    <div className="application-issues" role="alert">
      <p className="application-issues-title">
        <TriangleAlert size={14} />
        {t("The manifest has problems")}
      </p>
      <ul>
        {issues.map((issue, index) => (
          <li key={`${issue.path}-${index}`}>
            <code data-i18n-ignore>{issue.path || "manifest"}</code>
            <span data-i18n-ignore>{issue.message}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** The client secret is only returned when an application is created; show it once, copyable. */
export function ClientSecretNotice({ secret, kind = "client" }: { secret: string; kind?: "client" | "webhook" }) {
  const t = useOptionalTranslate();
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret);
      toast.success(t("Copied to clipboard"));
    } catch {
      toast.error(t("Could not copy to clipboard"));
    }
  };
  return (
    <div className="application-secret-box" role="status">
      <p className="application-secret-title">
        <KeyRound size={14} />
        {kind === "webhook" ? t("Webhook signing secret") : t("Client secret")}
      </p>
      <div className="application-secret-value">
        <code data-i18n-ignore aria-label={kind === "webhook" ? t("Webhook signing secret") : t("Client secret")}>
          {secret}
        </code>
        <button type="button" className="settings-icon-action" aria-label={kind === "webhook" ? t("Copy webhook signing secret") : t("Copy client secret")} onClick={() => void copy()}>
          <Copy size={14} />
        </button>
      </div>
      <p className="application-secret-warning">
        {t("Copy this secret now. For security it will not be shown again.")}
      </p>
    </div>
  );
}

function CheckList({
  options,
  value,
  onChange,
  disabled,
  locked = [],
}: {
  options: { value: string; label: string; description?: string; badge?: string; ignoreI18n?: boolean }[];
  value: string[];
  onChange: (next: string[]) => void;
  disabled?: boolean;
  locked?: string[];
}) {
  return (
    <div className="application-check-grid">
      {options.map((option) => (
        <label key={option.value} className="application-check">
          <input
            type="checkbox"
            aria-label={option.label}
            checked={value.includes(option.value)}
            disabled={disabled || locked.includes(option.value)}
            onChange={(event) =>
              onChange(event.target.checked ? [...value, option.value] : value.filter((item) => item !== option.value))
            }
          />
          <span>
            <strong {...(option.ignoreI18n ? { "data-i18n-ignore": true } : {})}>{option.label}</strong>
            {option.badge ? <em className="application-check-badge">{option.badge}</em> : null}
            {option.description ? <small>{option.description}</small> : null}
          </span>
        </label>
      ))}
    </div>
  );
}

export function ApplicationFormFields({
  state,
  onChange,
  disabled,
}: {
  state: ApplicationFormState;
  onChange: (next: ApplicationFormState) => void;
  disabled?: boolean;
}) {
  const t = useOptionalTranslate();
  const id = useId();
  const set = <K extends keyof ApplicationFormState>(key: K, value: ApplicationFormState[K]) =>
    onChange({ ...state, [key]: value });
  const knownScopes = new Set(OAUTH_SCOPE_OPTIONS.map((option) => option.scope));
  const extraScopes = state.scopes.filter((scope) => !knownScopes.has(scope));
  return (
    <>
      <Section title={t("Application details")}>
        <label className="application-field">
          {t("Name")}
          <input
            className="settings-input"
            value={state.name}
            disabled={disabled}
            onChange={(event) => set("name", event.target.value)}
          />
        </label>
        <label className="application-field">
          {t("Developer name")}
          <input
            className="settings-input"
            value={state.developerName}
            disabled={disabled}
            onChange={(event) => set("developerName", event.target.value)}
          />
        </label>
        <label className="application-field">
          {t("Developer URL")}
          <input
            className="settings-input"
            value={state.developerUrl}
            disabled={disabled}
            placeholder="https://example.com"
            onChange={(event) => set("developerUrl", event.target.value)}
          />
        </label>
        <label className="application-field">
          {t("Description")}
          <textarea
            className="settings-input"
            value={state.description}
            disabled={disabled}
            onChange={(event) => set("description", event.target.value)}
          />
        </label>
        <label className="application-field">
          {t("Icon URL")}
          <input
            className="settings-input"
            value={state.iconUrl}
            disabled={disabled}
            placeholder="https://…"
            onChange={(event) => set("iconUrl", event.target.value)}
          />
        </label>
      </Section>
      <Section title={t("OAuth")}>
        <div className="application-field">
          <label htmlFor={`${id}-redirects`}>{t("Redirect URIs")}</label>
          <textarea
            id={`${id}-redirects`}
            className="settings-input"
            value={state.redirectUris}
            disabled={disabled}
            onChange={(event) => set("redirectUris", event.target.value)}
            placeholder="https://app.example.com/oauth/callback"
          />
          <small>{t("One URI per line.")}</small>
        </div>
        <Row title={t("Public application")} description={t("Allow installation in other workspaces")}>
          <Toggle
            label={t("Public application")}
            checked={state.distribution === "public"}
            disabled={disabled}
            onChange={(value) => set("distribution", value ? "public" : "private")}
          />
        </Row>
        <Row title={t("Client credentials")} description={t("Allow server-to-server access as this application.")}>
          <Toggle
            label={t("Client credentials")}
            checked={state.clientCredentials}
            disabled={disabled}
            onChange={(value) => set("clientCredentials", value)}
          />
        </Row>
      </Section>
      <Section title={t("Scopes")} description={t("Permissions the application can request.")}>
        <CheckList
          disabled={disabled}
          locked={["read"]}
          value={state.scopes}
          onChange={(next) => set("scopes", next)}
          options={[
            ...OAUTH_SCOPE_OPTIONS.map((option) => ({
              value: option.scope,
              label: t(option.label),
              description: t(option.description),
              badge: option.userOnly ? t("User actor only") : undefined,
            })),
            ...extraScopes.map((scope) => ({ value: scope, label: scope, ignoreI18n: true })),
          ]}
        />
      </Section>
      <Section title={t("Agent")} description={t("Let people assign work to this application or mention it.")}>
        <Row title={t("Assignable")} description={t("Can be assigned and delegated issues (app:assignable)")}>
          <Toggle label={t("Assignable")} checked={state.assignable} disabled={disabled} onChange={(value) => set("assignable", value)} />
        </Row>
        <Row title={t("Mentionable")} description={t("Can be mentioned in comments and documents (app:mentionable)")}>
          <Toggle label={t("Mentionable")} checked={state.mentionable} disabled={disabled} onChange={(value) => set("mentionable", value)} />
        </Row>
      </Section>
      <Section title={t("Webhook")} description={t("Receive HTTP requests when workspace data changes.")}>
        <Row title={t("Webhooks enabled")} description={t("Deliver events to the URL below")}>
          <Toggle label={t("Webhooks enabled")} checked={state.webhookEnabled} disabled={disabled} onChange={(value) => set("webhookEnabled", value)} />
        </Row>
        <div className="application-field">
          <label htmlFor={`${id}-webhook-url`}>{t("Webhook URL")}</label>
          <input
            id={`${id}-webhook-url`}
            className="settings-input"
            value={state.webhookUrl}
            disabled={disabled}
            placeholder="https://example.com/webhooks/flow"
            onChange={(event) => set("webhookUrl", event.target.value)}
          />
          <small>{t("Must be an HTTPS URL.")}</small>
        </div>
        <fieldset className="application-fieldset">
          <legend>{t("Resource types")}</legend>
          <CheckList
            disabled={disabled}
            value={state.webhookResourceTypes}
            onChange={(next) => set("webhookResourceTypes", next)}
            options={WEBHOOK_RESOURCE_TYPE_OPTIONS.map((type) => ({ value: type, label: type, ignoreI18n: true }))}
          />
        </fieldset>
      </Section>
    </>
  );
}

/**
 * Paste or upload a manifest, validate it with the server and hand it to the page.
 * `onApply` returns false to keep the dialog open (for example when the manifest has issues).
 */
export function ManifestImportDialog({
  open,
  onClose,
  onApply,
  applyLabel,
}: {
  open: boolean;
  onClose: () => void;
  onApply: (manifest: OAuthAppManifest, valid: boolean, issues: OAuthAppManifestIssue[]) => boolean | void | Promise<boolean | void>;
  applyLabel?: string;
}) {
  const t = useOptionalTranslate();
  const [text, setText] = useState("");
  const [issues, setIssues] = useState<OAuthAppManifestIssue[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const reset = () => {
    setText("");
    setIssues([]);
    setError("");
  };
  const close = () => {
    reset();
    onClose();
  };
  const submit = async () => {
    setError("");
    setIssues([]);
    const parsed = parseManifestText(text);
    if ("error" in parsed) {
      setError(t(parsed.error));
      return;
    }
    setBusy(true);
    try {
      const result = await validateOAuthAppManifest(parsed.manifest);
      setIssues(result.issues ?? []);
      const keepOpen = (await onApply(parsed.manifest, result.valid, result.issues ?? [])) === false;
      if (result.valid && !keepOpen) close();
    } catch (value) {
      setError(value instanceof Error ? value.message : t("Could not validate manifest"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(value) => !value && !busy && close()}>
      <DialogContent className="application-import-dialog" aria-describedby={undefined} closeLabel={t("Close")}>
        <DialogTitle>{t("Import manifest")}</DialogTitle>
        <p className="application-muted">{t("Paste an OAuth application manifest or upload a .json file.")}</p>
        <textarea
          className="settings-input application-manifest-text"
          aria-label={t("Manifest JSON")}
          value={text}
          placeholder='{"oauth": {"client_name": "My app"}}'
          onChange={(event) => setText(event.target.value)}
        />
        <input
          ref={fileRef}
          type="file"
          accept="application/json,.json"
          hidden
          aria-label={t("Manifest file")}
          onChange={(event) => {
            const file = event.target.files?.[0];
            if (!file) return;
            void file.text().then(setText);
            event.target.value = "";
          }}
        />
        {error && (
          <p className="application-error" role="alert">
            {error}
          </p>
        )}
        <ManifestIssues issues={issues} />
        <footer>
          <button type="button" className="settings-action" onClick={() => fileRef.current?.click()}>
            {t("Upload file")}
          </button>
          <button type="button" className="settings-action" disabled={busy} onClick={close}>
            {t("Cancel")}
          </button>
          <button type="button" className="settings-action primary" disabled={busy || !text.trim()} onClick={() => void submit()}>
            {applyLabel ?? t("Validate & import")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}
