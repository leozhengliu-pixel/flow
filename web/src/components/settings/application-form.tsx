import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Copy, Eye, EyeOff, TriangleAlert } from "lucide-react";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { FlowTooltip } from "@/components/ui/tooltip";
import { ApplicationIconPlaceholder } from "@/components/ui/application-icon-placeholder";
import { useOptionalTranslate } from "@/i18n/i18n";
import { uploadOAuthApplicationIcon, validateOAuthAppManifest } from "@/lib/api";
import { APPLICATION_WEBHOOK_EVENT_GROUPS, parseManifestText, type ApplicationFormErrors, type ApplicationFormState } from "@/lib/oauth-app-manifest";
import { toast } from "@/lib/toast";
import type { OAuthAppManifest, OAuthAppManifestIssue } from "@/types/flow";

import { SettingsToggle as Toggle } from "./settings-primitives";
import "./application-pages.css";
import "./oauth-application-settings.css";

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

/**
 * The reference app's "Webhook signing secret" field: a read-only input with Copy and Show/Hide. A secret the form just generated
 * is shown in the clear; the application's saved secret starts masked.
 */
function WebhookSecretField({ id, secret, isNew }: { id: string; secret: string; isNew: boolean }) {
  const t = useOptionalTranslate();
  const [visible, setVisible] = useState(isNew);
  const copy = async () => {
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret);
      toast.info(t("Copied to clipboard"), { description: t("The signing secret has been copied to your clipboard.") });
    } catch {
      toast.error(t("Could not copy to clipboard"));
    }
  };
  return (
    <div className="oauth-app-field">
      <label htmlFor={id} className="oauth-app-field-heading">
        <span className="oauth-app-field-label">{t("Webhook signing secret")}</span>
      </label>
      <div className="oauth-app-field-control oauth-app-secret-field">
        <input id={id} className="oauth-app-input" type={visible ? "text" : "password"} readOnly value={secret} data-i18n-ignore aria-busy={!secret || undefined} />
        <div className="oauth-app-secret-field-actions">
          <FlowTooltip label={t("Copy to clipboard")}>
            <button type="button" className="oauth-app-icon-button is-small" aria-label={t("Copy to clipboard")} disabled={!secret} onClick={() => void copy()}>
              <Copy aria-hidden="true" />
            </button>
          </FlowTooltip>
          <FlowTooltip label={t(visible ? "Hide" : "Show")}>
            <button type="button" className="oauth-app-icon-button is-small" aria-label={t(visible ? "Hide" : "Show")} aria-pressed={visible} onClick={() => setVisible((value) => !value)}>
              {/* The reference app: an open eye while the secret shows, a crossed-out eye while it is masked. */}
              {visible ? <Eye aria-hidden="true" /> : <EyeOff aria-hidden="true" />}
            </button>
          </FlowTooltip>
        </div>
      </div>
    </div>
  );
}

/**
 * The reference app's "Create new application" / "Edit application" card: the icon picker, the labelled fields, the Public /
 * Client credentials / Webhooks switches (with the webhook URL and event groups when webhooks are on) and the
 * footer buttons.
 */
export function ApplicationForm({
  state,
  onChange,
  errors,
  disabled,
  onSubmit,
  actions,
}: {
  state: ApplicationFormState;
  onChange: (next: ApplicationFormState) => void;
  errors: ApplicationFormErrors;
  disabled?: boolean;
  onSubmit: () => void;
  actions: ReactNode;
}) {
  const t = useOptionalTranslate();
  const id = useId();
  const set = <K extends keyof ApplicationFormState>(key: K, value: ApplicationFormState[K]) => onChange({ ...state, [key]: value });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    onSubmit();
  };
  return (
    <form className="settings-card oauth-app-form" onSubmit={submit} noValidate>
      <ApplicationIconPicker url={state.iconUrl} disabled={disabled} onChange={(url) => set("iconUrl", url)} />
      <FormField id={`${id}-name`} label={t("Application name")} hint={t("User visible name for your application")} error={errors.name}>
        <input id={`${id}-name`} className="oauth-app-input" value={state.name} disabled={disabled} placeholder="Bug Reporter" data-i18n-ignore onChange={(event) => set("name", event.target.value)} />
      </FormField>
      <FormField id={`${id}-developer`} label={t("Developer name")} hint={t("The person or company developing this application")} error={errors.developerName}>
        <input id={`${id}-developer`} className="oauth-app-input" value={state.developerName} disabled={disabled} placeholder="Bug Reporter Corp" onChange={(event) => set("developerName", event.target.value)} />
      </FormField>
      <FormField id={`${id}-developer-url`} label={t("Developer URL")} hint={t("Homepage or documentation for your application. Required for public applications.")} error={errors.developerUrl}>
        <input id={`${id}-developer-url`} className="oauth-app-input" value={state.developerUrl} disabled={disabled} placeholder="https://bugreporter.com/flow-app" onChange={(event) => set("developerUrl", event.target.value)} />
      </FormField>
      <FormField id={`${id}-description`} label={t("Description")} hint={t("Describe your application")}>
        <textarea id={`${id}-description`} className="oauth-app-input is-multiline" value={state.description} disabled={disabled} placeholder={t("Allows a user to create Flow issues from within the Bug Reporter application.")} onChange={(event) => set("description", event.target.value)} />
      </FormField>
      <FormField id={`${id}-redirects`} label={t("Redirect URIs")} hint={t("All OAuth redirect URIs, separated with newlines")} error={errors.redirectUris} errorBelow>
        <textarea id={`${id}-redirects`} className="oauth-app-input is-multiline" value={state.redirectUris} disabled={disabled} placeholder={"https://bugreporter.com/callback\nhttps://bugreporter.com/callback2"} onChange={(event) => set("redirectUris", event.target.value)} />
      </FormField>
      <FormField
        id={`${id}-github`}
        label={t("GitHub username")}
        hint={fillCodes(t("For {actor} only, add a GitHub username to associate commits and comments in GitHub with your application (use {suffix} suffix for GitHub Apps, like {example})"), { actor: "actor=app", suffix: "[bot]", example: "my-app[bot]" })}
        error={errors.githubUsername}
        errorBelow
      >
        <input id={`${id}-github`} className="oauth-app-input" value={state.githubUsername} disabled={disabled} placeholder="github-username" data-i18n-ignore autoComplete="off" spellCheck={false} onChange={(event) => set("githubUsername", event.target.value)} />
      </FormField>
      <SwitchRow title={t("Public")} description={t("Allow this application to be installed by other workspaces")}>
        <Toggle label={t("Public")} checked={state.distribution === "public"} disabled={disabled} onChange={(value) => set("distribution", value ? "public" : "private")} />
      </SwitchRow>
      <SwitchRow title={t("Client credentials")} description={fillCode(t("Support generating OAuth access tokens using the {code} grant type."), "client_credentials")}>
        <Toggle label={t("Client credentials")} checked={state.clientCredentials} disabled={disabled} onChange={(value) => set("clientCredentials", value)} />
      </SwitchRow>
      <SwitchRow title={t("Webhooks")} description={t("Webhooks allow you to receive HTTP push notifications on your specified URL.")}>
        <Toggle label={t("Webhooks")} checked={state.webhookEnabled} disabled={disabled} onChange={(value) => set("webhookEnabled", value)} />
      </SwitchRow>
      {state.webhookEnabled && (
        <>
          <FormField id={`${id}-webhook-url`} label={t("Webhook URL")} error={errors.webhookUrl}>
            <input id={`${id}-webhook-url`} className="oauth-app-input" value={state.webhookUrl} disabled={disabled} placeholder="https://example.com/webhook" onChange={(event) => set("webhookUrl", event.target.value)} />
          </FormField>
          <WebhookSecretField key={state.webhookSecretIsNew ? "new" : "saved"} id={`${id}-webhook-secret`} secret={state.webhookSecret} isNew={state.webhookSecretIsNew} />
          {APPLICATION_WEBHOOK_EVENT_GROUPS.map((group) => (
            <fieldset key={group.title} className="oauth-app-events" disabled={disabled}>
              <legend>
                <span className="oauth-app-field-label">{t(group.title)}</span>
                {group.hint && <span className="oauth-app-field-hint"><span aria-hidden="true">-</span> {fillCode(t(group.hint), "actor=app")}</span>}
              </legend>
              <div className="oauth-app-event-grid">
                {group.events.map((event) => (
                  <label key={event.type}>
                    <input
                      type="checkbox"
                      checked={state.webhookResourceTypes.includes(event.type)}
                      onChange={(change) => set("webhookResourceTypes", change.target.checked ? [...state.webhookResourceTypes, event.type] : state.webhookResourceTypes.filter((item) => item !== event.type))}
                    />
                    <span>{t(event.label)}</span>
                  </label>
                ))}
              </div>
            </fieldset>
          ))}
        </>
      )}
      <footer className="oauth-app-form-actions">{actions}</footer>
    </form>
  );
}

function FormField({ id, label, hint, error, errorBelow, children }: { id: string; label: string; hint?: ReactNode; error?: string; errorBelow?: boolean; children: ReactNode }) {
  const t = useOptionalTranslate();
  return (
    <div className={`oauth-app-field${error ? " is-invalid" : ""}`}>
      <label htmlFor={id} className="oauth-app-field-heading">
        <span className="oauth-app-field-label">{label}</span>
        {hint && <span className="oauth-app-field-hint"><span aria-hidden="true">-</span> {hint}</span>}
      </label>
      <div className="oauth-app-field-control">
        {children}
        {error && !errorBelow && <span className="oauth-app-required" role="alert"><span>{t(error)}</span></span>}
      </div>
      {error && errorBelow && <span className="oauth-app-required is-below" role="alert"><span>{t(error)}</span></span>}
    </div>
  );
}

function SwitchRow({ title, description, children }: { title: string; description: ReactNode; children: ReactNode }) {
  return (
    <div className="oauth-app-switch">
      <div>
        <span className="oauth-app-switch-title">{title}</span>
        <span className="oauth-app-switch-description">{description}</span>
      </div>
      {children}
    </div>
  );
}

/** Renders `{code}` in a translated sentence as the reference app's inline code chip. */
function fillCode(text: string, code: string) {
  return fillCodes(text, { code });
}

/** Renders each `{name}` placeholder in a translated sentence as the reference app's inline code chip. */
function fillCodes(text: string, codes: Record<string, string>) {
  const parts = text.split(/\{(\w+)\}/);
  if (parts.length === 1) return text;
  return <>{parts.map((part, index) => (index % 2 === 0 ? part : part in codes ? <code key={index} className="oauth-app-code" data-i18n-ignore>{codes[part]}</code> : `{${part}}`))}</>;
}

/** The reference app's "Application icon · At least 256x256px" tile: click to upload an image. */
function ApplicationIconPicker({ url, disabled, onChange }: { url: string; disabled?: boolean; onChange: (url: string) => void }) {
  const t = useOptionalTranslate();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const upload = async (file: File) => {
    setBusy(true);
    try {
      onChange((await uploadOAuthApplicationIcon(file)).url);
    } catch (error) {
      toast.error(error instanceof Error && error.message ? error.message : t("Could not upload icon"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="oauth-app-icon-row">
      <button type="button" className="oauth-app-icon-tile" aria-label={t("Upload application icon")} disabled={disabled || busy} onClick={() => input.current?.click()}>
        {url ? <img src={url} alt="" /> : <ApplicationIconPlaceholder />}
      </button>
      <input ref={input} type="file" hidden accept="image/png,image/jpeg,image/webp,image/svg+xml" aria-label={t("Application icon")} onChange={(event) => {
        const file = event.target.files?.[0];
        event.target.value = "";
        if (file) void upload(file);
      }} />
      <div className="oauth-app-icon-copy">
        <span className="oauth-app-field-label">{t("Application icon")}</span>
        <span className="oauth-app-icon-hint">{t("At least 256x256px")}</span>
      </div>
    </div>
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
