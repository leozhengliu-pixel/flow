import { useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { Archive, ArrowUpRight, ChevronRight, MoreHorizontal, Plus, TriangleAlert, X } from "lucide-react";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { AgentCodingGlyph, LoopsGlyph } from "@/components/ui/agent-glyph";
import { useI18n } from "@/i18n/i18n";
import { archiveCodingEnvironment, createCodingEnvironment, fetchCommitSigningKey, unarchiveCodingEnvironment } from "@/lib/api";
import { codingEnvironmentIdFromPath, codingEnvironmentPath, settingsPath, type IntegrationProvider, type SettingsPageId } from "@/lib/app-routes";
import { toast } from "@/lib/toast";
import type { BootstrapData, CodingEnvironment, CodingHarness, CodingSettings, CodingStatus, WorkspaceSettings } from "@/types/flow";
import { IntegrationBrandIcon } from "../integration-brand-icon";
import { SettingsCrumb } from "../settings-primitives";
import {
  HARNESS_OPTIONS,
  SNAPSHOT_STATUS_LABEL,
  TRUSTED_SOURCE_OPTIONS,
  hasConnectedCodeHost,
  modelOptions,
  reasoningOptions,
  repositoryConflict,
  trustedSourceLabel,
} from "./coding-sessions-model";
import { CodingButton, CodingCard, CodingLinkRow, CodingRow, CodingSection, CodingSelect, CodingToggle } from "./coding-settings-primitives";
import { EnvironmentEditorPage } from "./environment-editor";
import { RepositoryPickerPopover, type PickedRepository, type RepositoryHost } from "./repository-picker";
import { errorMessage, useCodingEnvironments, useCodingModels, useCodingPolicy, useCodingStatus } from "./use-coding-settings";
import "../feature-settings.css";
import "./coding-sessions.css";

export const CODING_SESSIONS_DOCS_URL = "https://flow.app/docs/coding-sessions";

type Props = {
  data: BootstrapData;
  settings: WorkspaceSettings;
  /** "coding-environments" is the legacy list page (redirects) and the environment editor route. */
  page: "coding-sessions" | "coding-environments";
  /** Not an admin, or a workspace save is in flight. */
  disabled?: boolean;
  onToggleEnabled: (value: boolean) => void;
  onNavigateSettings?: (page: SettingsPageId) => void;
  onOpenIntegration?: (provider: IntegrationProvider | string) => void;
};

/** Settings › AI & agents › Coding sessions, and the environment editor below it. */
export function CodingSessionsSettings(props: Props) {
  const location = useLocation();
  const navigate = useNavigate();
  const workspace = props.data.workspace.urlKey;
  const environmentId = props.page === "coding-environments" ? codingEnvironmentIdFromPath(location.pathname) : undefined;
  const redirect = props.page === "coding-environments" && !environmentId;
  useEffect(() => {
    if (redirect) navigate(settingsPath(workspace, "coding-sessions") + "#environments", { replace: true });
  }, [redirect, navigate, workspace]);
  if (redirect) return null;
  if (environmentId)
    return (
      <EnvironmentEditorPage
        key={environmentId}
        data={props.data}
        environmentId={environmentId}
        disabled={props.disabled}
        onBack={() => navigate(settingsPath(workspace, "coding-sessions"))}
        onOpen={(id) => navigate(codingEnvironmentPath(workspace, id), { replace: true })}
      />
    );
  return <CodingSessionsOverview {...props} onOpenEnvironment={(id) => navigate(codingEnvironmentPath(workspace, id))} />;
}

function CodingSessionsOverview({ data, settings: workspaceSettings, disabled, onToggleEnabled, onNavigateSettings, onOpenIntegration, onOpenEnvironment }: Props & { onOpenEnvironment: (id: string) => void }) {
  const { t } = useI18n();
  const enabled = workspaceSettings.featureFlags["coding-sessions"] ?? true;
  const { status, loaded: statusLoaded } = useCodingStatus();
  const models = useCodingModels();
  const { settings, update, error: policyError } = useCodingPolicy(workspaceSettings.codingAgentSettings, enabled);
  const [showArchived, setShowArchived] = useState(false);
  const { environments, archived, loaded, error, reload } = useCodingEnvironments(true);
  // Without a reachable status API the code host is unknown; don't block the toggle on it.
  const needsCodeAccess = statusLoaded && status !== undefined && !hasConnectedCodeHost(status);
  const active = enabled && !needsCodeAccess;
  const controlsDisabled = disabled || !active;

  useEffect(() => {
    if (loaded && window.location.hash === "#environments") document.getElementById("environments")?.scrollIntoView?.({ block: "start" });
  }, [loaded]);

  return (
    <div className="feature-settings cs-settings" data-testid="coding-sessions-settings">
      <SettingsCrumb pinned onClick={() => onNavigateSettings?.("ai")}>{t("AI & agents")}</SettingsCrumb>
      <header className="feature-header cs-header">
        <div>
          <h1>{t("Coding sessions")}</h1>
          <p>
            {t("Let Flow Agent write code and open pull requests.")}{" "}
            <a className="feature-docs-link" href={CODING_SESSIONS_DOCS_URL} rel="noreferrer" target="_blank">
              {t("Docs")}
              <ArrowUpRight size={11} />
            </a>
          </p>
        </div>
        <button type="button" className="cs-header-link" onClick={() => onNavigateSettings?.("usage")}>
          {t("Manage credits")}
          <ChevronRight size={14} />
        </button>
      </header>

      <CodingCard>
        {needsCodeAccess ? (
          <CodingRow title="Enable coding sessions" description="Needs code access via GitHub integration" warning>
            <EnableIntegrationButton disabled={disabled} onOpenIntegration={onOpenIntegration} />
          </CodingRow>
        ) : (
          <CodingRow title="Enable coding sessions" description="Assign or ask Flow to make code changes">
            <CodingToggle checked={enabled} disabled={disabled} label="Enable coding sessions" onChange={onToggleEnabled} />
          </CodingRow>
        )}
        <PolicyRows settings={settings} update={update} models={models} status={status} disabled={controlsDisabled} />
      </CodingCard>
      {policyError && <p className="cs-inline-error cs-policy-error" role="alert">{policyError}</p>}

      {settings.requireSignedCommits && <SigningKeyNotice onOpen={() => onNavigateSettings?.("account-security")} />}

      <CodingSection
        id="environments"
        title="Environments"
        description="Create reusable environments for coding sessions."
      >
        <CodingCard className="cs-environments">
          {error && <p className="cs-inline-error" role="alert">{error}</p>}
          {environments.length > 0 && (
            <div className="feature-row cs-row cs-environments-header">
              <h3>{t(environments.length === 1 ? "1 environment" : "{count} environments").replace("{count}", String(environments.length))}</h3>
              <aside>
                <NewEnvironmentButton status={status} environments={environments} disabled={controlsDisabled} onOpenEnvironment={onOpenEnvironment} />
              </aside>
            </div>
          )}
          {environments.map((environment) => (
            <EnvironmentRow key={environment.id} environment={environment} disabled={controlsDisabled} onOpen={() => onOpenEnvironment(environment.id)} onArchived={() => void reload()} />
          ))}
          {loaded && !environments.length && !error && (
            <div className="feature-row cs-row cs-empty-row">
              <div>
                <span className="cs-empty-text">{t("No environments configured")}</span>
              </div>
              <aside>
                <NewEnvironmentButton status={status} environments={environments} disabled={controlsDisabled} onOpenEnvironment={onOpenEnvironment} />
              </aside>
            </div>
          )}
        </CodingCard>
        {archived.length > 0 && (
          <div className="cs-archived-toggle">
            <button type="button" className="cs-text-button" aria-expanded={showArchived} onClick={() => setShowArchived((value) => !value)}>
              {t(showArchived ? "Hide archived environments" : "Show archived environments")}
            </button>
          </div>
        )}
        {showArchived && archived.length > 0 && (
          <CodingCard className="cs-environments is-archived">
            {archived.map((environment) => (
              <div key={environment.id} className="feature-row cs-row">
                <div>
                  <strong data-i18n-ignore>{environment.name}</strong>
                  <span data-i18n-ignore>{environment.repository}</span>
                </div>
                <aside>
                  <CodingButton
                    disabled={disabled}
                    onClick={async () => {
                      try {
                        await unarchiveCodingEnvironment(environment.id);
                        await reload();
                      } catch (reason) {
                        toast.error(errorMessage(reason, "Could not unarchive environment"));
                      }
                    }}
                  >
                    Unarchive
                  </CodingButton>
                </aside>
              </div>
            ))}
            {!archived.length && <div className="feature-row cs-row cs-empty-row"><div><span className="cs-empty-text">{t("No archived environments")}</span></div></div>}
          </CodingCard>
        )}
      </CodingSection>

      <TrustedSourcesSection settings={settings} update={update} disabled={controlsDisabled} onOpenCodeAccess={() => onOpenIntegration?.("github")} />
      <section className="feature-section cs-section cs-loop-access">
        <CodingCard>
          <CodingLinkRow
            figure
            icon={<LoopsGlyph size={16} />}
            title="Repository access for Loops"
            description="Choose which repos Loops can use for coding sessions"
            onClick={() => onNavigateSettings?.("loops")}
          />
        </CodingCard>
      </section>
      {data.viewerRole !== "admin" && data.viewerRole !== "owner" && <p className="cs-admin-hint">{t("Only workspace admins can change coding session settings.")}</p>}
    </div>
  );
}

/**
 * "New environment" opens the repository picker in place, like the reference app: choosing a
 * repository creates the environment (named after the repository) and opens its editor. A
 * repository that already has an active environment opens that one instead.
 */
function NewEnvironmentButton({ status, environments, disabled, onOpenEnvironment }: { status?: CodingStatus; environments: CodingEnvironment[]; disabled?: boolean; onOpenEnvironment: (id: string) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const connected = (status?.codeHosts ?? []).filter((host) => host.connected);
  const hosts: RepositoryHost[] = (connected.length ? connected : status?.codeHosts?.slice(0, 1) ?? [{ codeHost: "github" as const, hostName: "github.com" }]).map((host) => ({ codeHost: host.codeHost, hostName: host.hostName }));
  const create = async (repository: PickedRepository) => {
    const existing = repositoryConflict(environments, { codeHost: repository.codeHost, hostName: repository.hostName, repository: repository.fullName });
    if (existing) {
      onOpenEnvironment(existing.id);
      return;
    }
    setCreating(true);
    try {
      const created = await createCodingEnvironment({
        name: repository.fullName,
        codeHost: repository.codeHost,
        hostName: repository.hostName,
        repository: repository.fullName,
        ...(repository.id ? { repositoryId: repository.id } : {}),
        ...(repository.defaultBranch ? { defaultBranch: repository.defaultBranch } : {}),
      });
      toast.success(t("Environment created"));
      onOpenEnvironment(created.id);
    } catch (reason) {
      toast.error(errorMessage(reason, "Could not create the environment"));
    } finally {
      setCreating(false);
    }
  };
  return (
    <RepositoryPickerPopover hosts={hosts} open={open} onOpenChange={setOpen} onPick={(repository) => void create(repository)}>
      <CodingButton ghost disabled={disabled || creating} aria-haspopup="dialog">
        <Plus size={14} />
        New environment
      </CodingButton>
    </RepositoryPickerPopover>
  );
}

/** Opens Settings › Integrations › GitHub, where track D's GitHub App card installs the App. */
function EnableIntegrationButton({ disabled, onOpenIntegration }: { disabled?: boolean; onOpenIntegration?: (provider: IntegrationProvider | string) => void }) {
  const { t } = useI18n();
  return (
    <button type="button" className="feature-button primary cs-integration-button" disabled={disabled} onClick={() => onOpenIntegration?.("github")}>
      <IntegrationBrandIcon provider="github" size={16} />
      {t("Enable integration")}
      <ArrowUpRight size={12} />
    </button>
  );
}

function PolicyRows({ settings, update, models, status, disabled }: { settings: CodingSettings; update: (patch: Partial<CodingSettings>) => Promise<void>; models: ReturnType<typeof useCodingModels>; status?: CodingStatus; disabled: boolean }) {
  const { t } = useI18n();
  const models_ = useMemo(() => modelOptions(models, settings.harness), [models, settings.harness]);
  const efforts = reasoningOptions(models, settings.harness, settings.model);
  const regions = status?.regions ?? [];
  const pinned = Boolean(settings.pinnedRegion);
  return (
    <>
      <CodingRow title="Agent" description="Choose which agent Flow uses in coding sessions" disabled={disabled}>
        <CodingSelect
          label="Agent"
          value={settings.harness}
          disabled={disabled}
          options={HARNESS_OPTIONS.map((option) => ({ ...option, translate: option.value === "openSource" }))}
          onChange={(value) => {
            const harness = value as CodingHarness;
            const keepModel = modelOptions(models, harness).some((option) => option.value === settings.model);
            void update({ harness, ...(keepModel ? {} : { model: "auto" }) });
          }}
        />
      </CodingRow>
      <CodingRow title="Model" description="May affect quality and cost of code changes" disabled={disabled}>
        <CodingSelect
          label="Model"
          value={settings.model}
          disabled={disabled}
          options={models_}
          menuClassName="cs-model-menu"
          onChange={(model) => void update({ model })}
        />
      </CodingRow>
      {efforts.length > 0 && settings.model !== "auto" && (
        <CodingRow title="Reasoning effort" description="How long the agent thinks before acting" disabled={disabled}>
          <CodingSelect
            label="Reasoning effort"
            value={settings.reasoningEffort ?? ""}
            disabled={disabled}
            options={[{ value: "", label: "Model default" }, ...efforts.map((effort) => ({ value: effort, label: effort[0].toUpperCase() + effort.slice(1) }))]}
            onChange={(value) => void update({ reasoningEffort: value as CodingSettings["reasoningEffort"] })}
          />
        </CodingRow>
      )}
      <CodingRow title="Require signed commits" description="Users must upload a signing key before starting a coding session" disabled={disabled}>
        <CodingToggle checked={settings.requireSignedCommits} disabled={disabled} label="Require signed commits" onChange={(value) => void update({ requireSignedCommits: value })} />
      </CodingRow>
      <CodingRow
        title="Pin sandbox region"
        disabled={disabled}
        description={regions.length
          ? <span data-i18n-ignore>{t("Pin new coding session sandboxes to {region}.").replace("{region}", settings.pinnedRegion || regions[0])} {t("Region pinning raises the sandbox compute price.")}</span>
          : "No sandbox regions are configured on this server"}
      >
        {pinned && regions.length > 1 && (
          <CodingSelect label="Sandbox region" value={settings.pinnedRegion ?? ""} disabled={disabled} options={regions.map((region) => ({ value: region, label: region, translate: false }))} onChange={(pinnedRegion) => void update({ pinnedRegion })} />
        )}
        <CodingToggle checked={pinned} disabled={disabled || !regions.length} label="Pin sandbox region" onChange={(value) => void update({ pinnedRegion: value ? regions[0] : "" })} />
      </CodingRow>
    </>
  );
}

/**
 * One environment: a 64px row (32px tile, the name) that opens the editor; hovering shows the "…" menu with
 * "Archive environment" (confirmed first). The snapshot state stays as a badge.
 */
function EnvironmentRow({ environment, disabled, onOpen, onArchived }: { environment: CodingEnvironment; disabled?: boolean; onOpen: () => void; onArchived: () => void }) {
  const { t } = useI18n();
  const snapshot = environment.latestSnapshot;
  const host = environment.hostName && environment.hostName !== "github.com" && environment.hostName !== "gitlab.com" ? `${environment.hostName}/` : "";
  const archive = async () => {
    const confirmed = await confirmAction(t("Archive this environment?"), {
      description: t("Archived environments can’t be used for new coding sessions."),
      confirmLabel: t("Archive environment"),
      danger: true,
    });
    if (!confirmed) return;
    try {
      await archiveCodingEnvironment(environment.id);
      toast.success(t("Environment archived"));
      onArchived();
    } catch (reason) {
      toast.error(errorMessage(reason, "Could not archive the environment"));
    }
  };
  return (
    <div className="feature-row cs-row cs-environment-row">
      <button type="button" className="cs-environment-open" onClick={onOpen} aria-label={environment.name} title={`${host}${environment.repository}`} />
      <span className="feature-row-icon cs-environment-tile"><IntegrationBrandIcon provider={environment.codeHost} size={16} /></span>
      <div>
        <strong data-i18n-ignore>{environment.name}</strong>
      </div>
      <aside>
        {environment.pendingProposal && <span className="cs-badge is-accent">{t("Proposal ready")}</span>}
        {snapshot && <span className={`cs-badge is-${snapshot.status}`}>{t(SNAPSHOT_STATUS_LABEL[snapshot.status])}</span>}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button type="button" className="cs-icon-button cs-environment-menu" aria-label={t("Open menu")} disabled={disabled}>
              <MoreHorizontal size={16} />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => void archive()}>
              <Archive size={14} />
              {t("Archive environment")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </aside>
    </div>
  );
}

function TrustedSourcesSection({ settings, update, disabled, onOpenCodeAccess }: { settings: CodingSettings; update: (patch: Partial<CodingSettings>) => Promise<void>; disabled: boolean; onOpenCodeAccess: () => void }) {
  const { t } = useI18n();
  const trusted = settings.trustedExternalSources;
  const available = TRUSTED_SOURCE_OPTIONS.filter((option) => !trusted.includes(option.value));
  return (
    <CodingSection
      title="Trusted external sources"
      description={<>{t("Allow these sources to start coding sessions using repositories connected in")} <button type="button" className="cs-inline-link" onClick={onOpenCodeAccess}>{t("Code access")}</button>.</>}
      action={
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button type="button" className="cs-icon-button" aria-label={t("Add trusted source")} disabled={disabled || !available.length}>
              <Plus size={14} />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="feature-select-menu">
            {available.map((option) => (
              <DropdownMenuItem key={option.value} onSelect={() => void update({ trustedExternalSources: [...trusted, option.value] })}>
                {t(option.label)}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      }
    >
      <CodingCard>
        {trusted.length ? (
          trusted.map((source) => (
            <div key={source} className="feature-row cs-row cs-compact-row">
              <div>
                <strong>{t(trustedSourceLabel(source))}</strong>
              </div>
              <aside>
                <button type="button" className="cs-icon-button" aria-label={t("Remove {source}").replace("{source}", t(trustedSourceLabel(source)))} disabled={disabled} onClick={() => void update({ trustedExternalSources: trusted.filter((item) => item !== source) })}>
                  <X size={14} />
                </button>
              </aside>
            </div>
          ))
        ) : (
          <p className="cs-card-text">{t("No trusted sources. Issues from external sources require a member’s approval to start coding sessions.")}</p>
        )}
      </CodingCard>
    </CodingSection>
  );
}

/** The "Coding sessions" row on Settings › AI & agents: Enabled, Disabled, or ⚠ Needs code access. */
export function CodingSessionsAIRow({ enabled, onOpen }: { enabled: boolean; onOpen: () => void }) {
  const { t } = useI18n();
  const { status, loaded } = useCodingStatus();
  const needsCodeAccess = loaded && status !== undefined && !hasConnectedCodeHost(status);
  return (
    <CodingLinkRow
      icon={<AgentCodingGlyph size={18} />}
      title="Coding sessions"
      description="Assign or ask Flow to make code changes"
      detail={needsCodeAccess ? <span className="cs-row-warning"><TriangleAlert size={12} aria-hidden="true" />{t("Needs code access")}</span> : enabled ? "Enabled" : "Disabled"}
      onClick={onOpen}
    />
  );
}

/**
 * With "Require signed commits", the viewer needs a signing key whose private key Flow holds.
 * Keys uploaded before Flow signed on the user's behalf report `needsReupload` (track E).
 */
function SigningKeyNotice({ onOpen }: { onOpen: () => void }) {
  const { t } = useI18n();
  const [state, setState] = useState<"ok" | "missing" | "reupload">();
  useEffect(() => {
    let active = true;
    fetchCommitSigningKey()
      .then((key) => {
        if (!active) return;
        if (!key) setState("missing");
        else setState((key as { needsReupload?: boolean }).needsReupload ? "reupload" : "ok");
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);
  if (state !== "missing" && state !== "reupload") return null;
  return (
    <p className="cs-banner cs-signing-notice" role="status" data-testid="signing-key-notice">
      <TriangleAlert size={13} aria-hidden="true" />
      <span>{t(state === "reupload"
        ? "Re-upload your commit signing key. Your current key was uploaded without its private key, so Flow can't sign commits for your coding sessions."
        : "Upload a commit signing key. Coding sessions require signed commits in this workspace.")}</span>
      <button type="button" className="cs-inline-link" onClick={onOpen}>{t(state === "reupload" ? "Re-upload key" : "Upload key")}</button>
    </p>
  );
}
