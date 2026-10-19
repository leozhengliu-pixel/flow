import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { Download, LayoutGrid, MoreHorizontal, Pencil, Trash2, Upload } from "lucide-react";
import { toast } from "@/lib/toast";

import {
  OAuthManifestError,
  createOAuthApplicationFromManifest,
  decideOAuthSyncGroupRequest,
  deleteOAuthApplication,
  exportOAuthApplicationManifest,
  fetchApplicationWebhookSecret,
  fetchOAuthApplicationFailures,
  fetchOAuthSyncGroupRequests,
  updateOAuthApplicationFromManifest,
} from "@/lib/api";
import { useI18n, useOptionalTranslate } from "@/i18n/i18n";
import {
  OAUTH_SCOPE_OPTIONS,
  applyManifestToForm,
  formStateFromApplication,
  formStateFromManifest,
  githubUsernameError,
  manifestFileName,
  manifestFromFormState,
  manifestFromSearch,
  validateApplicationForm,
  type ApplicationFormErrors,
  type ApplicationFormState,
} from "@/lib/oauth-app-manifest";
import type {
  BootstrapData,
  OAuthAppManifestIssue,
  OAuthSyncGroupRequest,
  WebhookFailureEvent,
} from "@/types/flow";
import { applicationEditPath, applicationSettingsPath, installedApplicationPath, settingsPath } from "@/lib/app-routes";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";

import { ApplicationCredentials } from "./application-credentials";
import { ApplicationWebhookSection } from "./application-webhook-section";
import { OAuthAppAvatar } from "./oauth-applications-section";
import { fillNodes } from "@/lib/fill-nodes";
import {
  ApplicationForm,
  ManifestImportDialog,
  ManifestIssues,
} from "./application-form";
import {
  SettingsCrumb,
  SettingsPageTitle as PageTitle,
  SettingsRow as Row,
  SettingsSection as Section,
} from "./settings-primitives";
import "./application-pages.css";
import "./oauth-application-settings.css";

/** Every scope an application can request:, a new application is not limited to a configured subset. */
const ALL_APPLICATION_SCOPES = [...OAUTH_SCOPE_OPTIONS.map((option) => option.scope)];

function newApplicationState(search: string): ApplicationFormState {
  const manifest = manifestFromSearch(search);
  const state = formStateFromManifest(manifest);
  if (manifest.oauth?.scopes || manifest.agent) return state;
  return { ...state, scopes: ALL_APPLICATION_SCOPES, assignable: true, mentionable: true };
}

function errorIssues(error: unknown): OAuthAppManifestIssue[] {
  return error instanceof OAuthManifestError ? error.issues : [];
}

/** The reference app's "Create new application" page (Settings › API › New OAuth application). */
export function ApplicationCreatePage({
  data,
  onReload,
  onBack,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
  onBack: () => void;
}) {
  const t = useOptionalTranslate();
  const navigate = useNavigate();
  const location = useLocation();
  const [state, setState] = useState(() => newApplicationState(location.search));
  const [issues, setIssues] = useState<OAuthAppManifestIssue[]>([]);
  const [errors, setErrors] = useState<ApplicationFormErrors>({});
  const [attempted, setAttempted] = useState(false);
  const [busy, setBusy] = useState(false);

  const change = (next: ApplicationFormState) => {
    setState(next);
    if (attempted) setErrors(validateApplicationForm(next));
  };
  const create = async () => {
    setAttempted(true);
    const found = validateApplicationForm(state);
    setErrors(found);
    if (Object.keys(found).length || busy) return;
    setBusy(true);
    setIssues([]);
    try {
      // Like the reference app, the GitHub username is looked up on GitHub when the form is submitted.
      const githubUsername = await githubUsernameError(state.githubUsername);
      if (githubUsername) {
        setErrors({ githubUsername });
        return;
      }
      const app = await createOAuthApplicationFromManifest(manifestFromFormState(state));
      await onReload();
      toast.success(t("Application created"));
      navigate(applicationSettingsPath(data.workspace.urlKey, app.id));
    } catch (error) {
      const next = errorIssues(error);
      setIssues(next);
      if (!next.length) toast.error(error instanceof Error ? error.message : t("Could not create application"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="application-page">
      <SettingsCrumb pinned onClick={onBack}>{t("API settings")}</SettingsCrumb>
      <PageTitle>{t("Create new application")}</PageTitle>
      <ManifestIssues issues={issues} />
      <ApplicationForm
        state={state}
        onChange={change}
        errors={errors}
        disabled={busy}
        onSubmit={() => void create()}
        actions={
          <>
            <button type="button" className="oauth-app-button" onClick={onBack}>{t("Cancel")}</button>
            <button type="submit" className="oauth-app-button is-primary" disabled={busy}>{t("Create")}</button>
          </>
        }
      />
    </div>
  );
}

/** The reference app's "Edit application" page: the same form, with Save. */
export function ApplicationEditPage({
  data,
  applicationId,
  onReload,
  onBack,
}: {
  data: BootstrapData;
  applicationId: string;
  onReload: () => Promise<void>;
  onBack: () => void;
}) {
  const t = useOptionalTranslate();
  const navigate = useNavigate();
  const app = data.oauthApplications.find((item) => item.id === applicationId);
  const [state, setState] = useState(() => (app ? formStateFromApplication(app) : formStateFromManifest({})));
  const [issues, setIssues] = useState<OAuthAppManifestIssue[]>([]);
  const [errors, setErrors] = useState<ApplicationFormErrors>({});
  const [attempted, setAttempted] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (app) setState(formStateFromApplication(app));
    // Reset the form only when a different or newly saved version of the app arrives.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app?.id, app?.updatedAt]);

  // Like the reference app, the form shows the application's saved signing secret (masked until "Show").
  const savedSecretPrefix = app?.webhookSecretPrefix;
  useEffect(() => {
    if (!app || !savedSecretPrefix) return;
    let cancelled = false;
    fetchApplicationWebhookSecret(app.id)
      .then(({ secret }) => {
        if (!cancelled) setState((current) => (current.webhookSecretIsNew ? current : { ...current, webhookSecret: secret }));
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app?.id, app?.updatedAt, savedSecretPrefix]);

  if (!app) return <ApplicationNotFound onBack={onBack} />;

  const change = (next: ApplicationFormState) => {
    setState(next);
    if (attempted) setErrors(validateApplicationForm(next));
  };
  const save = async () => {
    setAttempted(true);
    const found = validateApplicationForm(state);
    setErrors(found);
    if (Object.keys(found).length || busy) return;
    setBusy(true);
    setIssues([]);
    try {
      const githubUsername = state.githubUsername.trim() !== (app.githubUsername ?? "") ? await githubUsernameError(state.githubUsername) : undefined;
      if (githubUsername) {
        setErrors({ githubUsername });
        return;
      }
      await updateOAuthApplicationFromManifest(app.id, manifestFromFormState(state));
      await onReload();
      toast.success(t("Application updated"), { description: t("“{name}” updated.").replace("{name}", state.name.trim()) });
      navigate(applicationSettingsPath(data.workspace.urlKey, app.id));
    } catch (error) {
      const next = errorIssues(error);
      setIssues(next);
      // The reference app: "Update failed" with the server's reason.
      if (!next.length) toast.error(t("Update failed"), { description: error instanceof Error && error.message ? t(error.message) : t("Could not save application") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="application-page">
      <SettingsCrumb pinned onClick={() => navigate(applicationSettingsPath(data.workspace.urlKey, app.id))}>{t("Back")}</SettingsCrumb>
      <PageTitle>{t("Edit application")}</PageTitle>
      <ManifestIssues issues={issues} />
      <ApplicationForm
        state={state}
        onChange={change}
        errors={errors}
        disabled={busy}
        onSubmit={() => void save()}
        actions={<button type="submit" className="oauth-app-button is-primary" disabled={busy}>{t("Save")}</button>}
      />
    </div>
  );
}

function ApplicationNotFound({ onBack }: { onBack: () => void }) {
  const t = useOptionalTranslate();
  return (
    <div className="application-page">
      <SettingsCrumb pinned onClick={onBack}>{t("API")}</SettingsCrumb>
      <PageTitle>{t("Application not found")}</PageTitle>
    </div>
  );
}

/**
 * The reference app's OAuth application page (Settings › API › <app>): icon, name, "Created <date> by <member>" and the menu;
 * the developer, description, redirect URIs and availability; OAuth credentials. Webhook delivery failures and
 * directory sync requests (Flow additions) follow when there are any.
 */
export function ApplicationDetailsPage({
  data,
  applicationId,
  onReload,
  onBack,
}: {
  data: BootstrapData;
  applicationId: string;
  onReload: () => Promise<void>;
  onBack: () => void;
}) {
  const navigate = useNavigate();
  const { t, formatDate } = useI18n();
  const app = data.oauthApplications.find((item) => item.id === applicationId);
  const [failures, setFailures] = useState<WebhookFailureEvent[]>([]);
  const [syncGroups, setSyncGroups] = useState<OAuthSyncGroupRequest[]>([]);
  const [importing, setImporting] = useState(false);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    Promise.all([fetchOAuthApplicationFailures(applicationId), fetchOAuthSyncGroupRequests(applicationId)])
      .then(([nextFailures, nextGroups]) => {
        if (cancelled) return;
        setFailures(nextFailures);
        setSyncGroups(nextGroups);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [applicationId]);

  if (!app) return <ApplicationNotFound onBack={onBack} />;

  const creator = data.users.find((user) => user.id === app.creatorId);
  const pendingGroups = syncGroups.filter((item) => item.status === "pending");
  const workspace = data.workspace.urlKey;

  const downloadManifest = async () => {
    try {
      const manifest = await exportOAuthApplicationManifest(app.id);
      const url = URL.createObjectURL(new Blob([JSON.stringify(manifest, null, 2)], { type: "application/json" }));
      const link = document.createElement("a");
      link.href = url;
      link.download = manifestFileName(app.name);
      link.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not export manifest"));
    }
  };

  return (
    <div className="application-page">
      <SettingsCrumb pinned onClick={onBack}>{t("API")}</SettingsCrumb>
      <header className="oauth-app-page-header">
        <OAuthAppAvatar logoUrl={app.logoUrl} name={app.name} redirectUris={app.redirectUris} size={48} />
        <div className="oauth-app-page-header-copy">
          <h1 data-i18n-ignore>{app.name}</h1>
          <p data-i18n-ignore>
            {fillNodes(t(creator ? "Created {date} by {member}" : "Created {date}"), {
              date: formatDate(app.createdAt, { month: "short", day: "numeric", year: "numeric" }),
              member: <strong key="member">{creator?.displayName || creator?.name}</strong>,
            })}
          </p>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger className="oauth-app-icon-button is-small" aria-label={t("Open menu")}>
            <MoreHorizontal aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" sideOffset={4} className="oauth-app-menu">
            <DropdownMenuItem onSelect={() => navigate(applicationEditPath(workspace, app.id))}><Pencil aria-hidden="true" />{t("Edit application")}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void downloadManifest()}><Download aria-hidden="true" />{t("Download manifest")}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setImporting(true)}><Upload aria-hidden="true" />{t("Import manifest…")}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setDeleting(true)}><Trash2 aria-hidden="true" />{t("Delete application")}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => navigate(installedApplicationPath(workspace, app.clientId))}><LayoutGrid aria-hidden="true" />{t("View in workspace")}</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </header>
      <Section className="oauth-app-section">
        <Row title={t("Developer")} description={<span data-i18n-ignore>{[app.developerName, app.developerUrl].filter(Boolean).join(" · ") || "—"}</span>} />
        {app.description && <Row title={t("Description")} description={<span data-i18n-ignore>{app.description}</span>} />}
        <Row title={t("Redirect URIs")} description={<span className="oauth-app-mono" data-i18n-ignore>{app.redirectUris.join("\n") || "—"}</span>} />
        <Row title={t("Availability")} description={app.publicEnabled ? t("Public to all workspaces") : t("Private to this workspace")} />
        {app.githubUsername && <Row title={t("GitHub username")} description={<span className="oauth-app-mono" data-i18n-ignore>{app.githubUsername}</span>} />}
      </Section>
      <ApplicationCredentials app={app} workspace={workspace} onReload={onReload} />
      {app.webhookUrl ? (
        <ApplicationWebhookSection app={app} failures={failures} onReload={onReload} />
      ) : (
        failures.length > 0 && (
          <Section className="oauth-app-section" title={t("Webhook delivery failures")} description={t("Failed webhook deliveries are logged after exhausting all retries.")}>
            {failures.map((item) => (
              <Row key={item.id} title={<span data-i18n-ignore>{item.executionId}</span>} description={<span data-i18n-ignore>{`${item.url} · ${item.httpStatus ?? "—"} · ${item.responseOrError.slice(0, 120)}`}</span>} />
            ))}
          </Section>
        )
      )}
      {pendingGroups.length > 0 && (
        <Section className="oauth-app-section" title={t("Sync group requests")}>
          {pendingGroups.map((item) => (
            <Row key={item.id} title={<span data-i18n-ignore>{item.groupName}</span>} description={t("Requested {date}").replace("{date}", formatDate(item.createdAt, { month: "short", day: "numeric", year: "numeric" }))}>
              <div className="oauth-app-actions">
                {(["approved", "denied"] as const).map((status) => (
                  <button
                    key={status}
                    type="button"
                    className={`oauth-app-button${status === "approved" ? " is-primary" : " is-ghost"}`}
                    onClick={() =>
                      void decideOAuthSyncGroupRequest(app.id, item.id, { status })
                        .then(async () => {
                          setSyncGroups((current) => current.map((entry) => (entry.id === item.id ? { ...entry, status } : entry)));
                          await onReload();
                        })
                        .catch((error) => toast.error(error instanceof Error ? error.message : t("Could not update request")))
                    }
                  >
                    {t(status === "approved" ? "Approve" : "Deny")}
                  </button>
                ))}
              </div>
            </Row>
          ))}
        </Section>
      )}
      <ManifestImportDialog
        open={importing}
        onClose={() => setImporting(false)}
        applyLabel={t("Validate & save")}
        onApply={async (manifest, valid) => {
          if (!valid) return false;
          try {
            const merged = manifestFromFormState(applyManifestToForm(formStateFromApplication(app), manifest));
            await updateOAuthApplicationFromManifest(app.id, merged);
            await onReload();
            toast.success(t("Application saved"));
          } catch (error) {
            toast.error(error instanceof Error ? error.message : t("Could not save application"));
            return false;
          }
        }}
      />
      <TypedConfirmDialog
        open={deleting}
        title={t('Delete the application "{name}"?').replace("{name}", app.name)}
        description={t("You cannot undo this action.")}
        value={app.name}
        confirmLabel={t("Delete")}
        onClose={() => setDeleting(false)}
        onConfirm={async () => {
          try {
            await deleteOAuthApplication(app.id);
            await onReload();
            navigate(settingsPath(workspace, "api"));
          } catch (error) {
            toast.error(error instanceof Error ? error.message : t("Could not delete application"));
          }
        }}
      />
    </div>
  );
}

/** The reference app's destructive confirmation that asks to type the item's name ("To confirm, type <name> below:"). */
export function TypedConfirmDialog({
  open,
  title,
  description,
  value,
  confirmLabel,
  onClose,
  onConfirm,
}: {
  open: boolean;
  title: string;
  description: string;
  value: string;
  confirmLabel: string;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}) {
  const t = useOptionalTranslate();
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const matches = typed.trim() === value.trim();
  const close = () => {
    if (busy) return;
    setTyped("");
    onClose();
  };
  const confirm = async () => {
    if (!matches || busy) return;
    setBusy(true);
    try {
      await onConfirm();
      setTyped("");
      onClose();
    } finally {
      setBusy(false);
    }
  };
  const [before, after = ""] = t("To confirm, type {name} below:").split("{name}");
  return (
    <Dialog open={open} onOpenChange={(next) => !next && close()}>
      <DialogContent className="action-dialog" overlayClassName="action-dialog-overlay" aria-describedby={undefined} closeLabel={t("Close")}>
        <DialogTitle data-i18n-ignore>{title}</DialogTitle>
        <p>{description}</p>
        <p className="oauth-app-confirm-label" data-i18n-ignore>{before}<strong>{value}</strong>{after}</p>
        <input aria-label={t("Application name")} value={typed} autoFocus onChange={(event) => setTyped(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); void confirm(); } }} />
        <footer>
          <button type="button" onClick={close}>{t("Cancel")}</button>
          <button type="button" className="danger" disabled={!matches || busy} onClick={() => void confirm()}>{confirmLabel}</button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}
