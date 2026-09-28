import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AppWindow, Plus, Radio } from "lucide-react";
import { toast } from "sonner";

import {
  createWebhook,
  decideOAuthSyncGroupRequest,
  deleteOAuthApplication,
  fetchOAuthApplicationFailures,
  fetchOAuthSyncGroupRequests,
  updateOAuthApplication,
  updateWebhook,
} from "@/lib/api";
import type {
  BootstrapData,
  OAuthApplication,
  OAuthSyncGroupRequest,
  Webhook,
  WebhookFailureEvent,
} from "@/types/flow";
import { applicationEditPath } from "@/lib/app-routes";

import { ApplicationHeader } from "./application-header";
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
  const app = data.oauthApplications.find((item) => item.id === applicationId);
  const [failures, setFailures] = useState<WebhookFailureEvent[]>([]);
  const [syncGroups, setSyncGroups] = useState<OAuthSyncGroupRequest[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
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
        <Row title="Client ID" description={app.clientId}>
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
          title="Scopes"
          description={app.scopes.join(", ") || "No scopes"}
        />
        <Row
          title="Redirect URIs"
          description={
            app.redirectUris.length
              ? app.redirectUris.join(" · ")
              : "No redirect URIs"
          }
        />
      </Section>
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
  const app = data.oauthApplications.find((item) => item.id === applicationId);
  const linkedWebhooks = useMemo(
    () => data.webhooks.filter((item) => item.applicationId === applicationId),
    [data.webhooks, applicationId],
  );
  const [name, setName] = useState(app?.name ?? "");
  const [description, setDescription] = useState(app?.description ?? "");
  const [logoUrl, setLogoUrl] = useState(app?.logoUrl ?? "");
  const [redirects, setRedirects] = useState(app?.redirectUris.join("\n") ?? "");
  const [scopes, setScopes] = useState(app?.scopes.join(", ") ?? "read");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!app) return;
    setName(app.name);
    setDescription(app.description ?? "");
    setLogoUrl(app.logoUrl ?? "");
    setRedirects(app.redirectUris.join("\n"));
    setScopes(app.scopes.join(", "));
  }, [app]);

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

  const save = async () => {
    setBusy(true);
    try {
      await updateOAuthApplication(app.id, {
        name: name.trim(),
        description: description.trim(),
        logoUrl: logoUrl.trim(),
        redirectUris: redirects.split(/\s+/).filter(Boolean),
        scopes: scopes
          .split(",")
          .map((item) => item.trim())
          .filter(Boolean),
      });
      await onReload();
      toast.success("Application saved");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not save application",
      );
    } finally {
      setBusy(false);
    }
  };

  const addWebhook = async () => {
    try {
      await createWebhook({
        name: `${app.name} webhook`,
        url: "https://example.com/hooks/flow",
        resourceTypes: ["issues"],
        teamIds: [],
        enabled: true,
        applicationId: app.id,
      });
      await onReload();
      toast.success("Webhook created");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not create webhook",
      );
    }
  };

  return (
    <div className="application-page">
      <ApplicationHeader
        app={{ ...app, name: name || app.name, logoUrl: logoUrl || app.logoUrl }}
        eyebrow="Edit application"
        actions={
          <>
            <ActionButton onClick={onBack}>Cancel</ActionButton>
            <ActionButton primary disabled={busy || !name.trim()} onClick={() => void save()}>
              Save
            </ActionButton>
          </>
        }
      />
      <Section title="Details">
        <label className="application-field">
          Name
          <input
            className="settings-input"
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label className="application-field">
          Description
          <textarea
            className="settings-input"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </label>
        <label className="application-field">
          Logo URL
          <input
            className="settings-input"
            value={logoUrl}
            placeholder="https://…"
            onChange={(event) => setLogoUrl(event.target.value)}
          />
        </label>
        <label className="application-field">
          Redirect URIs
          <textarea
            className="settings-input"
            value={redirects}
            onChange={(event) => setRedirects(event.target.value)}
            placeholder="https://app.example.com/oauth/callback"
          />
        </label>
        <label className="application-field">
          Scopes
          <input
            className="settings-input"
            value={scopes}
            onChange={(event) => setScopes(event.target.value)}
          />
        </label>
      </Section>
      <Section title="Webhooks">
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
          <ActionButton onClick={() => void addWebhook()}>
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
