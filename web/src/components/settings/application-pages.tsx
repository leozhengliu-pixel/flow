import { useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { AppWindow, Download, Plus, Radio, Upload } from "lucide-react";
import { toast } from "@/lib/toast";

import {
  OAuthManifestError,
  createOAuthApplicationFromManifest,
  decideOAuthSyncGroupRequest,
  deleteOAuthApplication,
  exportOAuthApplicationManifest,
  fetchOAuthApplicationFailures,
  fetchOAuthApplicationInstalls,
  fetchOAuthSyncGroupRequests,
  revokeOAuthAuthorization,
  updateOAuthApplicationFromManifest,
  updateWebhook,
  type OAuthApplicationInstalls,
  type OAuthManifestSaveResult,
} from "@/lib/api";
import { saveApplication } from "@/lib/application-agents";
import { useOptionalTranslate } from "@/i18n/i18n";
import {
  applyManifestToForm,
  formStateFromApplication,
  formStateFromManifest,
  manifestFileName,
  manifestFromFormState,
  manifestFromSearch,
} from "@/lib/oauth-app-manifest";
import type {
  BootstrapData,
  OAuthAppManifestIssue,
  OAuthApplication,
  OAuthSyncGroupRequest,
  Webhook,
  WebhookFailureEvent,
} from "@/types/flow";
import {
  applicationEditPath,
  applicationSettingsPath,
  webhookSettingsPath,
} from "@/lib/app-routes";

import { ApplicationHeader } from "./application-header";
import { ApplicationCredentials } from './application-credentials';
import { ApplicationWebhookSettingsSection } from "./application-webhook-settings";
import {
  ApplicationFormFields,
  ClientSecretNotice,
  ManifestImportDialog,
  ManifestIssues,
} from "./application-form";
import {
  SettingsPageTitle as PageTitle,
  SettingsRow as Row,
  SettingsSection as Section,
  SettingsToggle as Toggle,
} from "./settings-primitives";
import "./application-pages.css";

function ActionButton({
  children,
  onClick,
  primary,
  danger,
  disabled,
}: {
  children: React.ReactNode;
  onClick?: () => void;
  primary?: boolean;
  danger?: boolean;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      className={`settings-action${primary ? " primary" : ""}${danger ? " danger" : ""}`}
      disabled={disabled}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

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
  const t = useOptionalTranslate();
  const app = data.oauthApplications.find((item) => item.id === applicationId);
  const [failures, setFailures] = useState<WebhookFailureEvent[]>([]);
  const [syncGroups, setSyncGroups] = useState<OAuthSyncGroupRequest[]>([]);
  const [installs, setInstalls] = useState<OAuthApplicationInstalls>({
    authorizations: [],
    installations: [],
  });
  const [loading, setLoading] = useState(true);

  const loadInstalls = () =>
    fetchOAuthApplicationInstalls(applicationId)
      .then((value) =>
        setInstalls({
          authorizations: value.authorizations ?? [],
          installations: value.installations ?? [],
        }),
      )
      .catch((error) =>
        toast.error(
          error instanceof Error ? error.message : t("Could not load installs"),
        ),
      );

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    void fetchOAuthApplicationInstalls(applicationId)
      .then((value) => {
        if (cancelled) return;
        setInstalls({
          authorizations: value.authorizations ?? [],
          installations: value.installations ?? [],
        });
      })
      .catch(() => undefined);
    Promise.all([
      fetchOAuthApplicationFailures(applicationId),
      fetchOAuthSyncGroupRequests(applicationId),
    ])
      .then(([nextFailures, nextGroups]) => {
        if (cancelled) return;
        setFailures(nextFailures);
        setSyncGroups(nextGroups);
      })
      .catch((error) =>
        toast.error(
          error instanceof Error ? error.message : "Could not load application",
        ),
      )
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [applicationId]);

  if (!app) {
    return (
      <>
        <PageTitle description="OAuth application details">
          Application
        </PageTitle>
        <div className="settings-empty">
          <AppWindow size={28} />
          <h3>Application not found</h3>
          <ActionButton onClick={onBack}>Back to applications</ActionButton>
        </div>
      </>
    );
  }

  const pendingGroups = syncGroups.filter((item) => item.status === "pending");

  return (
    <div className="application-page">
      <ApplicationHeader
        app={app}
        actions={
          <>
            <ActionButton onClick={onBack}>Back</ActionButton>
            <ActionButton
              primary
              onClick={() =>
                navigate(applicationEditPath(data.workspace.urlKey, app.id))
              }
            >
              Edit
            </ActionButton>
          </>
        }
      />
      <Section title="Client">
        <Row title="Client ID" description={<span data-i18n-ignore>{app.clientId}</span>}>
          <ActionButton
            onClick={() => {
              void navigator.clipboard.writeText(app.clientId);
              toast.success("Copied client ID");
            }}
          >
            Copy
          </ActionButton>
        </Row>
        <Row
          title="Developer"
          description={
            app.developerName || app.developerUrl ? (
              <span data-i18n-ignore>
                {app.developerName}
                {app.developerName && app.developerUrl ? " · " : ""}
                {app.developerUrl}
              </span>
            ) : (
              t("No developer information")
            )
          }
        />
        <Row
          title="Distribution"
          description={
            app.publicEnabled
              ? t("Public: can be installed in other workspaces")
              : t("Private: only this workspace")
          }
        />
        <Row
          title="Grant types"
          description={
            <Chips
              values={
                app.supportsClientCredentials
                  ? ["authorization_code", "client_credentials"]
                  : ["authorization_code"]
              }
            />
          }
        />
        <Row
          title="Scopes"
          description={
            app.scopes.length ? <Chips values={app.scopes} /> : t("No scopes")
          }
        />
        <Row
          title="Redirect URIs"
          description={
            app.redirectUris.length ? (
              <span data-i18n-ignore>{app.redirectUris.join(" · ")}</span>
            ) : (
              t("No redirect URIs")
            )
          }
        />
        <Row title="Webhook" description={webhookSummary(app, t)} />
      </Section>
      <Section
        title="Installs"
        description="Authorizations and installations of this application in the workspace."
      >
        {installs.authorizations.filter((item) => !item.revokedAt).map((item) => (
          <Row
            key={item.id}
            title={
              <span data-i18n-ignore>
                {data.users.find((user) => user.id === item.userId)
                  ?.displayName ?? t("Unknown user")}
              </span>
            }
            description={
              <>
                <Chips values={item.scopes} />
                <span>
                  {item.lastUsedAt
                    ? `${t("Last used")} ${new Date(item.lastUsedAt).toLocaleDateString()}`
                    : t("Never used")}
                </span>
              </>
            }
          >
            <ActionButton
              danger
              onClick={() =>
                void revokeOAuthAuthorization(item.id)
                  .then(async () => {
                    toast.success(t("Authorization revoked"));
                    await loadInstalls();
                  })
                  .catch((error) =>
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : t("Could not revoke authorization"),
                    ),
                  )
              }
            >
              Revoke
            </ActionButton>
          </Row>
        ))}
        {installs.installations.filter((item) => item.active).map((item) => (
          <Row
            key={item.id}
            title={<span data-i18n-ignore>{item.name}</span>}
            description={
              <>
                <span>{t("Application installation")}</span>
                <Chips values={item.scopes} />
              </>
            }
          >
            <ActionButton
              danger
              onClick={() =>
                void saveApplication(data.workspace.urlKey, {
                  ...item,
                  active: false,
                })
                  .then(async () => {
                    toast.success(t("Installation revoked"));
                    await loadInstalls();
                  })
                  .catch((error) =>
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : t("Could not revoke installation"),
                    ),
                  )
              }
            >
              Revoke
            </ActionButton>
          </Row>
        ))}
        {!loading &&
          !installs.authorizations.some((item) => !item.revokedAt) &&
          !installs.installations.some((item) => item.active) && (
            <div className="settings-empty compact">
              <AppWindow size={22} />
              <h3>No installs yet</h3>
              <p>People who authorize this application will appear here.</p>
            </div>
          )}
      </Section>
      <ApplicationCredentials app={app} onReload={onReload}/>
      <ApplicationWebhookSettingsSection app={app} onReload={onReload} />
      <Section title="Delivery failures">
        {loading ? (
          <p className="application-muted">Loading failures…</p>
        ) : failures.length ? (
          failures.map((item) => (
            <Row
              key={item.id}
              title={item.executionId}
              description={`${item.url} · ${item.httpStatus ?? "—"} · ${item.responseOrError.slice(0, 120)}`}
            />
          ))
        ) : (
          <div className="settings-empty compact">
            <Radio size={22} />
            <h3>No delivery failures</h3>
            <p>Outbound webhook deliveries for this application are healthy.</p>
          </div>
        )}
      </Section>
      <Section title="Sync group requests">
        {pendingGroups.length ? (
          pendingGroups.map((item) => (
            <Row
              key={item.id}
              title={item.groupName}
              description={`Requested ${new Date(item.createdAt).toLocaleString()}`}
            >
              <div className="settings-inline-actions">
                <ActionButton
                  primary
                  onClick={() =>
                    void decideOAuthSyncGroupRequest(app.id, item.id, {
                      status: "approved",
                    })
                      .then(async () => {
                        toast.success("Sync group approved");
                        await onReload();
                        setSyncGroups((current) =>
                          current.map((entry) =>
                            entry.id === item.id
                              ? { ...entry, status: "approved" }
                              : entry,
                          ),
                        );
                      })
                      .catch((error) =>
                        toast.error(
                          error instanceof Error
                            ? error.message
                            : "Could not approve request",
                        ),
                      )
                  }
                >
                  Approve
                </ActionButton>
                <ActionButton
                  danger
                  onClick={() =>
                    void decideOAuthSyncGroupRequest(app.id, item.id, {
                      status: "denied",
                    })
                      .then(async () => {
                        toast.success("Sync group denied");
                        await onReload();
                        setSyncGroups((current) =>
                          current.map((entry) =>
                            entry.id === item.id
                              ? { ...entry, status: "denied" }
                              : entry,
                          ),
                        );
                      })
                      .catch((error) =>
                        toast.error(
                          error instanceof Error
                            ? error.message
                            : "Could not deny request",
                        ),
                      )
                  }
                >
                  Deny
                </ActionButton>
              </div>
            </Row>
          ))
        ) : (
          <div className="settings-empty compact">
            <AppWindow size={22} />
            <h3>No pending sync groups</h3>
            <p>
              Directory sync requests from this OAuth application will appear
              here when the model is present.
            </p>
          </div>
        )}
      </Section>
    </div>
  );
}

function Chips({ values }: { values: string[] }) {
  return (
    <span className="application-chips" data-i18n-ignore>
      {values.map((value) => (
        <span key={value} className="application-chip">
          {value}
        </span>
      ))}
    </span>
  );
}

function webhookSummary(app: OAuthApplication, t: (source: string) => string) {
  if (!app.webhookEnabled || !app.webhookUrl) return t("Webhooks are off");
  return (
    <>
      <span data-i18n-ignore>{app.webhookUrl}</span>
      {app.webhookResourceTypes?.length ? (
        <Chips values={app.webhookResourceTypes} />
      ) : null}
    </>
  );
}

function errorIssues(error: unknown): OAuthAppManifestIssue[] {
  return error instanceof OAuthManifestError ? error.issues : [];
}

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
  const [state, setState] = useState(() =>
    formStateFromManifest(manifestFromSearch(location.search)),
  );
  const [issues, setIssues] = useState<OAuthAppManifestIssue[]>([]);
  const [importing, setImporting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<OAuthManifestSaveResult>();

  const create = async () => {
    setBusy(true);
    setIssues([]);
    try {
      const app = await createOAuthApplicationFromManifest(
        manifestFromFormState(state),
      );
      setCreated(app);
      await onReload();
    } catch (error) {
      const next = errorIssues(error);
      setIssues(next);
      if (!next.length)
        toast.error(
          error instanceof Error ? error.message : t("Could not create application"),
        );
    } finally {
      setBusy(false);
    }
  };

  if (created) {
    return (
      <div className="application-page">
        <PageTitle description="Your OAuth application is ready.">
          Application created
        </PageTitle>
        <Section title={created.name}>
          <Row
            title="Client ID"
            description={<span data-i18n-ignore>{created.clientId}</span>}
          />
        </Section>
        {created.clientSecret && (
          <ClientSecretNotice secret={created.clientSecret} />
        )}
        {created.webhookSecret && (
          <ClientSecretNotice kind="webhook" secret={created.webhookSecret} />
        )}
        <div className="settings-section-action">
          <ActionButton
            primary
            onClick={() =>
              navigate(applicationSettingsPath(data.workspace.urlKey, created.id))
            }
          >
            Open application
          </ActionButton>
        </div>
      </div>
    );
  }

  return (
    <div className="application-page">
      <PageTitle
        description="Register an application to use Flow's API with OAuth."
        action={
          <div className="settings-inline-actions">
            <ActionButton onClick={() => setImporting(true)}>
              <Upload size={14} />
              Import manifest
            </ActionButton>
          </div>
        }
      >
        New OAuth application
      </PageTitle>
      <ManifestIssues issues={issues} />
      <ApplicationFormFields state={state} onChange={setState} disabled={busy} />
      <div className="settings-section-action application-form-actions">
        <ActionButton onClick={onBack}>Cancel</ActionButton>
        <ActionButton
          primary
          disabled={busy || !state.name.trim()}
          onClick={() => void create()}
        >
          Create
        </ActionButton>
      </div>
      <ManifestImportDialog
        open={importing}
        onClose={() => setImporting(false)}
        onApply={(manifest, valid, found) => {
          setState((current) => applyManifestToForm(current, manifest));
          setIssues(found);
          return valid;
        }}
      />
    </div>
  );
}

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
  const linkedWebhooks = useMemo(
    () => data.webhooks.filter((item) => item.applicationId === applicationId),
    [data.webhooks, applicationId],
  );
  const [state, setState] = useState(() =>
    app ? formStateFromApplication(app) : formStateFromManifest({}),
  );
  const [issues, setIssues] = useState<OAuthAppManifestIssue[]>([]);
  const [importing, setImporting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [webhookSecret, setWebhookSecret] = useState("");

  useEffect(() => {
    if (app) setState(formStateFromApplication(app));
    // Reset the form only when a different or newly saved version of the app arrives.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app?.id, app?.updatedAt]);

  if (!app) {
    return (
      <>
        <PageTitle description="Edit OAuth application">Edit application</PageTitle>
        <div className="settings-empty">
          <AppWindow size={28} />
          <h3>Application not found</h3>
          <ActionButton onClick={onBack}>Back</ActionButton>
        </div>
      </>
    );
  }

  const save = async (manifest = manifestFromFormState(state)) => {
    setBusy(true);
    setIssues([]);
    try {
      const saved = await updateOAuthApplicationFromManifest(app.id, manifest);
      if (saved?.webhookSecret) setWebhookSecret(saved.webhookSecret);
      await onReload();
      toast.success(t("Application saved"));
      return true;
    } catch (error) {
      const next = errorIssues(error);
      setIssues(next);
      if (!next.length)
        toast.error(
          error instanceof Error ? error.message : t("Could not save application"),
        );
      return false;
    } finally {
      setBusy(false);
    }
  };

  const exportManifest = async () => {
    try {
      const manifest = await exportOAuthApplicationManifest(app.id);
      const text = JSON.stringify(manifest, null, 2);
      try {
        await navigator.clipboard.writeText(text);
      } catch {
        // Clipboard access is optional; the file download below still happens.
      }
      const url = URL.createObjectURL(new Blob([text], { type: "application/json" }));
      const link = document.createElement("a");
      link.href = url;
      link.download = manifestFileName(app.name);
      link.click();
      URL.revokeObjectURL(url);
      toast.success(t("Manifest exported and copied"));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("Could not export manifest"),
      );
    }
  };

  return (
    <div className="application-page">
      <ApplicationHeader
        app={{ ...app, name: state.name || app.name, logoUrl: state.iconUrl || app.logoUrl }}
        eyebrow="Edit application"
        actions={
          <>
            <ActionButton onClick={() => void exportManifest()}>
              <Download size={14} />
              Export manifest
            </ActionButton>
            <ActionButton onClick={() => setImporting(true)}>
              <Upload size={14} />
              Import manifest
            </ActionButton>
            <ActionButton onClick={onBack}>Cancel</ActionButton>
            <ActionButton primary disabled={busy || !state.name.trim()} onClick={() => void save()}>
              Save
            </ActionButton>
          </>
        }
      />
      <ManifestIssues issues={issues} />
      {webhookSecret && <ClientSecretNotice kind="webhook" secret={webhookSecret} />}
      <ApplicationFormFields state={state} onChange={setState} disabled={busy} />
      <Section title="Linked webhooks">
        <div className="application-webhook-grid">
          {linkedWebhooks.map((item) => (
            <WebhookCard
              key={item.id}
              webhook={item}
              onReload={onReload}
            />
          ))}
          {!linkedWebhooks.length && (
            <div className="settings-empty compact">
              <Radio size={22} />
              <h3>No webhooks for this application</h3>
              <p>Attach HTTPS endpoints to receive workspace events.</p>
            </div>
          )}
        </div>
        <div className="settings-section-action">
          <ActionButton
            onClick={() =>
              navigate(webhookSettingsPath(data.workspace.urlKey, "new"))
            }
          >
            <Plus size={14} />
            New webhook
          </ActionButton>
        </div>
      </Section>
      <Section title="Danger zone">
        <Row
          title="Delete application"
          description="Revokes the client credentials immediately."
        >
          <ActionButton
            danger
            onClick={() =>
              void deleteOAuthApplication(app.id).then(async () => {
                await onReload();
                onBack();
              })
            }
          >
            Delete
          </ActionButton>
        </Row>
      </Section>
      <ManifestImportDialog
        open={importing}
        onClose={() => setImporting(false)}
        applyLabel={t("Validate & save")}
        onApply={async (manifest, valid, found) => {
          setIssues(found);
          if (!valid) return false;
          const saved = await save(manifest);
          return saved ? undefined : false;
        }}
      />
    </div>
  );
}

function WebhookCard({
  webhook,
  onReload,
}: {
  webhook: Webhook;
  onReload: () => Promise<void>;
}) {
  return (
    <div className="application-webhook-card">
      <div>
        <strong data-i18n-ignore>{webhook.name}</strong>
        <p data-i18n-ignore>{webhook.url}</p>
        <small>{webhook.resourceTypes.join(", ") || "all resources"}</small>
      </div>
      <Toggle
        label={`${webhook.name} enabled`}
        checked={webhook.enabled}
        onChange={(value) =>
          void updateWebhook(webhook.id, { enabled: value }).then(onReload)
        }
      />
    </div>
  );
}
