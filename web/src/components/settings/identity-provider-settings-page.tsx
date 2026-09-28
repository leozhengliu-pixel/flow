import { useEffect, useState } from "react";
import { KeyRound, Plus, ShieldCheck } from "lucide-react";
import { toast } from "sonner";

import {
  createSCIMToken,
  deleteIdentityProvider,
  listSCIMTokens,
  rotateSCIMToken,
  revokeSCIMToken,
  updateIdentityProvider,
  updateWorkspacePreferences,
  verifyIdentityProvider,
} from "@/lib/api";
import type {
  BootstrapData,
  IdentityProvider,
  SCIMToken,
  WorkspaceSettings,
} from "@/types/flow";

import {
  SettingsPageTitle as PageTitle,
  SettingsRow as Row,
  SettingsSection as Section,
  SettingsSelect as Select,
  SettingsToggle as Toggle,
} from "./settings-primitives";
import "./identity-provider-settings-page.css";

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

export function IdentityProviderSettingsPage({
  data,
  providerId,
  onReload,
  onBack,
}: {
  data: BootstrapData;
  providerId: string;
  onReload: () => Promise<void>;
  onBack: () => void;
}) {
  const provider = data.identityProviders.find((item) => item.id === providerId);
  const [settings, setSettings] = useState(data.workspaceSettings);
  const [tokens, setTokens] = useState<SCIMToken[]>([]);
  const [roleGroups, setRoleGroups] = useState({
    admin: data.workspaceSettings.scimRoleGroups?.admin ?? "",
    member: data.workspaceSettings.scimRoleGroups?.member ?? "",
    guest: data.workspaceSettings.scimRoleGroups?.guest ?? "",
  });
  const [busy, setBusy] = useState("");
  const [revealedSecret, setRevealedSecret] = useState("");

  useEffect(() => setSettings(data.workspaceSettings), [data.workspaceSettings]);
  useEffect(() => {
    setRoleGroups({
      admin: data.workspaceSettings.scimRoleGroups?.admin ?? "",
      member: data.workspaceSettings.scimRoleGroups?.member ?? "",
      guest: data.workspaceSettings.scimRoleGroups?.guest ?? "",
    });
  }, [data.workspaceSettings.scimRoleGroups]);

  useEffect(() => {
    void listSCIMTokens()
      .then(setTokens)
      .catch((error) =>
        toast.error(
          error instanceof Error ? error.message : "Could not load SCIM tokens",
        ),
      );
  }, [providerId]);

  if (!provider) {
    return (
      <>
        <PageTitle description="SAML authentication and SCIM provisioning">
          Identity provider
        </PageTitle>
        <div className="settings-empty">
          <ShieldCheck size={28} />
          <h3>Identity provider not found</h3>
          <ActionButton onClick={onBack}>Back to Security</ActionButton>
        </div>
      </>
    );
  }

  const saveSettings = async (next: WorkspaceSettings, message: string) => {
    setSettings(next);
    try {
      await updateWorkspacePreferences(next);
      await onReload();
      toast.success(message);
    } catch (error) {
      setSettings(data.workspaceSettings);
      toast.error(
        error instanceof Error ? error.message : "Could not save settings",
      );
    }
  };

  const mutateProvider = async (
    action: () => Promise<unknown>,
    message: string,
  ) => {
    setBusy(provider.id);
    try {
      await action();
      await onReload();
      toast.success(message);
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : "Identity provider operation failed",
      );
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="identity-provider-page">
      <PageTitle
        description={`${provider.type.toUpperCase()} · ${provider.domains.join(", ") || "No discovery domains"}`}
      >
        {provider.name}
      </PageTitle>
      <div className="identity-provider-toolbar">
        <ActionButton onClick={onBack}>Back</ActionButton>
        <ActionButton
          disabled={busy === provider.id}
          onClick={() =>
            void mutateProvider(
              () => verifyIdentityProvider(provider.id),
              "SSO connection verified",
            )
          }
        >
          Test SSO connection
        </ActionButton>
        <ActionButton
          danger
          onClick={() =>
            void mutateProvider(async () => {
              await deleteIdentityProvider(provider.id);
              onBack();
            }, "Provider removed")
          }
        >
          Remove
        </ActionButton>
      </div>

      <Section title="SAML authentication">
        <Row title="Enabled" description="Allow members to sign in with this provider.">
          <Toggle
            label={`${provider.name} enabled`}
            checked={provider.enabled}
            onChange={(enabled) =>
              void mutateProvider(
                () => updateIdentityProvider(provider.id, { enabled }),
                "Identity provider updated",
              )
            }
          />
        </Row>
        <Row
          title="Require this provider"
          description="Members must authenticate with this identity provider."
        >
          <Toggle
            label="Require this provider"
            checked={provider.enforced}
            onChange={(enforced) =>
              void mutateProvider(
                () => updateIdentityProvider(provider.id, { enforced }),
                "Identity provider updated",
              )
            }
          />
        </Row>
        <Row title="Issuer / metadata" description={provider.issuer} />
        <Row
          title="Discovery status"
          description={
            provider.lastVerifiedAt
              ? `${provider.discoveryStatus} · verified ${new Date(provider.lastVerifiedAt).toLocaleString()}`
              : provider.discoveryStatus
          }
        />
        {provider.type === "oidc" ? (
          <Row title="Client ID" description={provider.clientId || "—"} />
        ) : null}
      </Section>

      <Section title="SCIM provisioning">
        <Row
          title="Enable SCIM"
          description="Allow directory sync using SCIM tokens for this workspace."
        >
          <Toggle
            label="Enable SCIM"
            checked={Boolean(settings.scimEnabled)}
            onChange={(scimEnabled) =>
              void saveSettings({ ...settings, scimEnabled }, "SCIM updated")
            }
          />
        </Row>
        {tokens.map((token) => (
          <Row
            key={token.id}
            title={token.name}
            description={`Created ${new Date(token.createdAt).toLocaleString()}`}
          >
            <div className="settings-inline-actions">
              <ActionButton
                onClick={() =>
                  void rotateSCIMToken(token.id)
                    .then(async (rotated) => {
                      setRevealedSecret(rotated.secret ?? "");
                      setTokens(await listSCIMTokens());
                      toast.success("SCIM token rotated");
                    })
                    .catch((error) =>
                      toast.error(
                        error instanceof Error
                          ? error.message
                          : "Could not rotate token",
                      ),
                    )
                }
              >
                Rotate
              </ActionButton>
              <ActionButton
                danger
                onClick={() =>
                  void revokeSCIMToken(token.id)
                    .then(async () => {
                      setTokens(await listSCIMTokens());
                      toast.success("SCIM token revoked");
                    })
                    .catch((error) =>
                      toast.error(
                        error instanceof Error
                          ? error.message
                          : "Could not revoke token",
                      ),
                    )
                }
              >
                Revoke
              </ActionButton>
            </div>
          </Row>
        ))}
        {!tokens.length ? (
          <div className="settings-empty compact">
            <KeyRound size={22} />
            <h3>No SCIM tokens</h3>
            <p>Create a token to connect your identity provider directory.</p>
          </div>
        ) : null}
        <div className="settings-section-action">
          <ActionButton
            onClick={() =>
              void createSCIMToken({ name: `${provider.name} SCIM` })
                .then(async (created) => {
                  setRevealedSecret(created.secret ?? "");
                  setTokens(await listSCIMTokens());
                  toast.success("SCIM token created");
                })
                .catch((error) =>
                  toast.error(
                    error instanceof Error
                      ? error.message
                      : "Could not create SCIM token",
                  ),
                )
            }
          >
            <Plus size={14} />
            New SCIM token
          </ActionButton>
        </div>
        {revealedSecret ? (
          <div className="identity-provider-secret">
            <p>Copy this SCIM token now. It won’t be shown again.</p>
            <input
              className="settings-input"
              readOnly
              value={revealedSecret}
              onFocus={(event) => event.currentTarget.select()}
            />
            <ActionButton
              onClick={() => {
                void navigator.clipboard.writeText(revealedSecret);
                toast.success("Copied");
              }}
            >
              Copy token
            </ActionButton>
          </div>
        ) : null}
      </Section>

      <Section title="Role groups">
        <p className="identity-provider-help">
          Map IdP group display names to workspace roles. Latest matching group
          push wins when a user belongs to multiple groups.
        </p>
        {(["admin", "member", "guest"] as const).map((role) => (
          <label key={role} className="identity-provider-role-row">
            <span>{role}</span>
            <input
              className="settings-input"
              data-i18n-ignore
              value={roleGroups[role]}
              placeholder={`${role}-group`}
              onChange={(event) =>
                setRoleGroups((current) => ({
                  ...current,
                  [role]: event.target.value,
                }))
              }
            />
          </label>
        ))}
        <div className="settings-section-action">
          <ActionButton
            primary
            onClick={() =>
              void saveSettings(
                {
                  ...settings,
                  scimRoleGroups: {
                    admin: roleGroups.admin.trim(),
                    member: roleGroups.member.trim(),
                    guest: roleGroups.guest.trim(),
                  },
                },
                "Role groups saved",
              )
            }
          >
            Save role groups
          </ActionButton>
        </div>
        <Row title="Default SCIM role">
          <Select
            label="Default SCIM role"
            value={settings.scimDefaultRole || "member"}
            options={["member", "guest", "admin"]}
            onChange={(value) =>
              void saveSettings(
                { ...settings, scimDefaultRole: value },
                "Default SCIM role saved",
              )
            }
          />
        </Row>
      </Section>
    </div>
  );
}

