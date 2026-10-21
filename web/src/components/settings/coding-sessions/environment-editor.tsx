import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Info, Sparkles } from "lucide-react";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { useI18n } from "@/i18n/i18n";
import {
  addCodingEnvironmentSecrets,
  archiveCodingEnvironment,
  configureCodingEnvironmentWithAgent,
  createCodingEnvironment,
  deleteCodingEnvironmentSecret,
  fetchCodingEnvironment,
  fixCodingEnvironmentWithAgent,
  updateCodingEnvironment,
} from "@/lib/api";
import { ApiError } from "@/lib/api-client";
import { toast } from "@/lib/toast";
import type { BootstrapData, CodeHostKind, CodingEnvironment, CodingEnvironmentSnapshot } from "@/types/flow";
import type { GitLabCredentialMode } from "@/types/coding-sessions";
import { IntegrationBrandIcon } from "../integration-brand-icon";
import { SettingsCrumb } from "../settings-primitives";
import {
  DEPENDENCY_OPTIONS,
  HARNESS_OPTIONS,
  NETWORK_OPTIONS,
  codeHostLabel,
  draftFromEnvironment,
  draftToInput,
  emptyDraft,
  modelOptions,
  repositoryConflict,
  sandboxSizeOptions,
  validateDraft,
  type EnvironmentDraft,
} from "./coding-sessions-model";
import { CodingButton, CodingCard, CodingCodeEditor, CodingRow, CodingSection, CodingSelect } from "./coding-settings-primitives";
import { DebugTerminal } from "./debug-terminal";
import { EnvImportDialog } from "./env-import-dialog";
import { EnvVarsEditor, FilesEditor, OwnersEditor, ToolsEditor, type EnvRow } from "./environment-fields";
import { ProposalPanel } from "./proposal-panel";
import { RepositoryPicker } from "./repository-picker";
import { SnapshotPanel } from "./snapshot-panel";
import { errorMessage, useCodingEnvironments, useCodingModels, useCodingStatus } from "./use-coding-settings";
import "../feature-settings.css";
import "./coding-sessions.css";

export const PROPOSAL_POLL_MS = 5000;

type Props = {
  data: BootstrapData;
  /** An environment id, or "new". */
  environmentId: string;
  disabled?: boolean;
  onBack: () => void;
  /** Replace the URL with the saved environment (after create). */
  onOpen: (id: string) => void;
};

const repoName = (repository: string) => repository.trim().split("/").pop() || "";
const rowsFromDraft = (draft: EnvironmentDraft): EnvRow[] => draft.variables.map((variable) => ({ ...variable, secret: false }));
/** What "dirty" compares: the request body plus the owners, which are sent separately. */
const fingerprint = (draft: EnvironmentDraft) => JSON.stringify({ ...draftToInput(draft), ownerIds: draft.ownerIds });

/**
 * The environment editor (`/settings/ai/coding-sessions/environments/:id` and `/new`), laid out
 * like the reference app's: header actions (Configure with agent, Save changes), the
 * repository/agent/model/size/tools card, then Files, Environment variables, Prepare script,
 * Permissions, Custom guidance, Network, Debug and Snapshots sections.
 */
export function EnvironmentEditorPage({ data, environmentId, disabled, onBack, onOpen }: Props) {
  const { t } = useI18n();
  const isNew = environmentId === "new";
  const { status } = useCodingStatus();
  const models = useCodingModels();
  const { environments } = useCodingEnvironments(false);
  const [environment, setEnvironment] = useState<CodingEnvironment>();
  const [loadError, setLoadError] = useState<string>();
  const [draftState, setDraft] = useState<EnvironmentDraft>(() => emptyDraft());
  const [envRows, setEnvRows] = useState<EnvRow[]>([]);
  const [baseline, setBaseline] = useState<string>(() => fingerprint(emptyDraft()));
  const [baselineOwners, setBaselineOwners] = useState<string>("[]");
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const [importOpen, setImportOpen] = useState(false);
  const [agentRunning, setAgentRunning] = useState(false);
  const statusSeeded = useRef(false);

  const applyEnvironment = useCallback((next: CodingEnvironment) => {
    const nextDraft = draftFromEnvironment(next);
    setEnvironment(next);
    setDraft(nextDraft);
    setEnvRows(rowsFromDraft(nextDraft));
    setBaseline(fingerprint(nextDraft));
    setBaselineOwners(JSON.stringify(nextDraft.ownerIds));
    setConflict(false);
  }, []);

  const load = useCallback(async () => {
    if (isNew) return;
    try {
      applyEnvironment(await fetchCodingEnvironment(environmentId));
      setLoadError(undefined);
    } catch (reason) {
      setLoadError(errorMessage(reason, "Could not load the environment"));
    }
  }, [applyEnvironment, environmentId, isNew]);
  useEffect(() => {
    void load();
  }, [load]);

  // A new environment starts on the first connected code host once the status is known.
  useEffect(() => {
    if (!isNew || !status || statusSeeded.current) return;
    statusSeeded.current = true;
    setDraft((current) => (current.repository ? current : { ...current, codeHost: emptyDraft(status).codeHost, hostName: emptyDraft(status).hostName }));
  }, [isNew, status]);

  // While an agent configures the environment, poll until its proposal arrives.
  useEffect(() => {
    if (!agentRunning || isNew) return;
    const timer = setInterval(async () => {
      try {
        const next = await fetchCodingEnvironment(environmentId);
        if (next.pendingProposal) {
          setAgentRunning(false);
          setEnvironment(next);
        }
      } catch {
        // keep polling
      }
    }, PROPOSAL_POLL_MS);
    return () => clearInterval(timer);
  }, [agentRunning, environmentId, isNew]);

  // The variables live in envRows (with their Secret checkbox); the draft is derived from them.
  const draft = useMemo<EnvironmentDraft>(() => {
    const variables = envRows.filter((row) => !row.secret).map(({ name, value }) => ({ name, value }));
    // There is no visible Name field: the environment is named after its repository.
    return { ...draftState, variables, name: draftState.name.trim() || repoName(draftState.repository) };
  }, [draftState, envRows]);
  const pendingSecrets = useMemo(() => envRows.filter((row) => row.secret).map(({ name, value }) => ({ name, value })), [envRows]);

  const update = (patch: Partial<EnvironmentDraft>) => setDraft((current) => ({ ...current, ...patch }));
  const savedSecretNames = (environment?.secrets ?? []).map((secret) => secret.name);
  // The name follows the repository, so "Choose a repository" covers a missing name.
  const errors = validateDraft(draft, pendingSecrets, savedSecretNames).filter((error) => draft.repository.trim() || error !== "Name is required");
  const repoInUse = repositoryConflict(environments, { id: environment?.id, codeHost: draft.codeHost, hostName: draft.hostName, repository: draft.repository });
  const dirty = fingerprint(draft) !== baseline || pendingSecrets.some((secret) => secret.name.trim());
  const archived = Boolean(environment?.archivedAt);
  const locked = disabled || archived || saving;
  const canManageOwners = data.viewerRole === "admin" || data.viewerRole === "owner";

  // Leaving with unsaved changes asks first, in the app and when the tab closes.
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty && !saving;
  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (!dirtyRef.current) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, []);
  const leave = async () => {
    if (dirtyRef.current && !(await confirmAction(t("Discard changes?"), { confirmLabel: t("Confirm"), danger: true }))) return;
    onBack();
  };

  const hostsOfKind = useMemo(() => (status?.codeHosts ?? []).filter((host) => host.codeHost === draft.codeHost), [status, draft.codeHost]);
  const connectedKinds = useMemo(() => [...new Set((status?.codeHosts ?? []).filter((host) => host.connected).map((host) => host.codeHost))], [status]);

  const save = async () => {
    // Like the reference app, "Save changes" stays enabled; with nothing changed it does nothing (no request, no toast).
    if (!isNew && !dirty) return;
    if (errors.length || repoInUse) return;
    setSaving(true);
    setSaveError(undefined);
    const secrets = pendingSecrets.filter((secret) => secret.name.trim()).map((secret) => ({ name: secret.name.trim(), value: secret.value }));
    const owners = JSON.stringify(draft.ownerIds) !== baselineOwners ? { ownerIds: draft.ownerIds } : {};
    try {
      if (isNew) {
        const created = await createCodingEnvironment({ ...draftToInput(draft), ...owners, ...(secrets.length ? { secrets } : {}) });
        setEnvRows([]);
        dirtyRef.current = false;
        toast.success(t("Environment created"));
        onOpen(created.id);
        return;
      }
      let saved = await updateCodingEnvironment(environmentId, { ...draftToInput(draft), ...owners, version: draft.version });
      if (secrets.length) saved = await addCodingEnvironmentSecrets(environmentId, secrets);
      applyEnvironment(saved);
      toast.success(t("Environment saved"));
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 409) {
        if (reason.code === "repo_in_use" || /repo/i.test(reason.message)) toast.error(t("Another active environment already uses this repository"));
        else setConflict(true);
      } else if (reason instanceof ApiError && reason.status === 400) setSaveError(errorMessage(reason, "Invalid environment"));
      else toast.error(errorMessage(reason, "Could not save the environment"));
    } finally {
      setSaving(false);
    }
  };

  const archive = async () => {
    if (!environment) return;
    const confirmed = await confirmAction(t("Archive {name}?").replace("{name}", environment.name), {
      description: t("Running sessions continue, but new coding sessions can't use this environment. You can unarchive it later."),
      confirmLabel: t("Archive"),
    });
    if (!confirmed) return;
    try {
      await archiveCodingEnvironment(environment.id);
      toast.success(t("Environment archived"));
      onBack();
    } catch (reason) {
      toast.error(errorMessage(reason, "Could not archive the environment"));
    }
  };

  const startAgent = async (kind: "configure" | "fix", snapshotId?: string) => {
    if (!environment) return;
    try {
      if (kind === "configure") await configureCodingEnvironmentWithAgent(environment.id);
      else await fixCodingEnvironmentWithAgent(environment.id, snapshotId);
      setAgentRunning(true);
      toast.success(t("Flow Agent is working on this environment. Its proposal will appear here."));
    } catch (reason) {
      toast.error(errorMessage(reason, "Could not start the agent"));
    }
  };

  if (loadError)
    return (
      <div className="feature-settings cs-settings">
        <SettingsCrumb pinned onClick={onBack}>{t("Coding sessions")}</SettingsCrumb>
        <header className="feature-header"><h1>{t("Edit environment")}</h1></header>
        <p className="cs-inline-error" role="alert">{loadError}</p>
      </div>
    );
  if (!isNew && !environment) return <div className="feature-settings cs-settings" aria-busy="true"><SettingsCrumb pinned onClick={onBack}>{t("Coding sessions")}</SettingsCrumb></div>;

  const harnessOptions = [{ value: "", label: "Workspace default" }, ...HARNESS_OPTIONS.map((option) => ({ ...option, translate: option.value === "openSource" }))];
  const environmentHarness = draft.harness || "codex";
  const sizes = sandboxSizeOptions(status);
  const problem = saveError ?? (errors.length > 0 && dirty ? t(errors[0]) : undefined);
  const owners = draft.ownerIds.length ? draft.ownerIds : [environment?.createdBy ?? data.viewer.id];

  return (
    <div className="feature-settings cs-settings cs-editor" data-testid="coding-environment-editor">
      <SettingsCrumb pinned onClick={() => void leave()}>{t("Coding sessions")}</SettingsCrumb>
      <header className="feature-header cs-editor-header">
        <div className="cs-editor-title">
          <h1>{t(isNew ? "New environment" : "Edit environment")}</h1>
          <div className="cs-editor-actions">
            {!isNew && (
              <CodingButton className="cs-agent-button" disabled={disabled || archived || dirty || agentRunning || Boolean(environment?.pendingProposal)} onClick={() => void startAgent("configure")}>
                <Sparkles size={16} />
                Configure with agent
              </CodingButton>
            )}
            <CodingButton primary disabled={locked || (isNew && !dirty) || (dirty && errors.length > 0) || Boolean(repoInUse)} onClick={() => void save()}>
              {isNew ? "Create environment" : "Save changes"}
            </CodingButton>
          </div>
        </div>
        <p>{t("Configure this coding environment.")}</p>
        {problem && <p className="cs-inline-error cs-editor-problem" role="alert">{problem}</p>}
      </header>
      {archived && <p className="cs-banner" role="status">{t("This environment is archived. Unarchive it from Coding sessions to edit it.")}</p>}
      {conflict && (
        <p className="cs-banner is-error" role="alert">
          {t("This environment was changed by someone else.")}{" "}
          <button type="button" className="cs-inline-link" onClick={() => void load()}>{t("Reload")}</button>
        </p>
      )}

      <CodingCard className="cs-editor-card">
        <CodingRow
          title="Repository"
          description={repoInUse
            ? t("{name} already uses this repository. Only one active environment per repository is allowed.").replace("{name}", repoInUse.name)
            : isNew ? "Choose the repository for this environment" : "Create a new environment to use a different repository"}
          warning={Boolean(repoInUse)}
        >
          {isNew ? (
            <>
              {connectedKinds.length > 1 && (
                <CodingSelect
                  label="Code host"
                  value={draft.codeHost}
                  disabled={locked}
                  options={connectedKinds.map((kind) => ({ value: kind, label: codeHostLabel(kind), translate: false }))}
                  onChange={(value) => {
                    const codeHost = value as CodeHostKind;
                    const host = status?.codeHosts?.find((item) => item.codeHost === codeHost && item.connected) ?? status?.codeHosts?.find((item) => item.codeHost === codeHost);
                    update({ codeHost, hostName: host?.hostName ?? (codeHost === "gitlab" ? "gitlab.com" : "github.com"), repository: "", repositoryId: undefined, defaultBranch: undefined });
                  }}
                />
              )}
              {hostsOfKind.length > 1 && (
                <CodingSelect label="Host" value={draft.hostName} disabled={locked} options={hostsOfKind.map((host) => ({ value: host.hostName, label: host.hostName, translate: false }))} onChange={(hostName) => update({ hostName, repository: "", repositoryId: undefined })} />
              )}
              <RepositoryPicker
                codeHost={draft.codeHost}
                hostName={draft.hostName}
                value={draft.repository}
                disabled={locked}
                onChange={(repository) => update({ repository: repository.fullName, repositoryId: repository.id, defaultBranch: repository.defaultBranch || undefined, name: repoName(repository.fullName) })}
              />
            </>
          ) : (
            <span className="cs-repo-value" data-testid="environment-repository">
              <IntegrationBrandIcon provider={draft.codeHost} size={16} />
              <span data-i18n-ignore>{draft.hostName && draft.hostName !== "github.com" && draft.hostName !== "gitlab.com" ? `${draft.hostName}/` : ""}{draft.repository}</span>
            </span>
          )}
        </CodingRow>
        {draft.codeHost === "gitlab" && (
          <p className="cs-card-text cs-gitlab-note" data-testid="gitlab-credential-note">
            {t(gitlabCredentialNote(status?.codeHosts?.find((item) => item.codeHost === "gitlab")?.credentialMode))}
          </p>
        )}
        <CodingRow title="Agent" description="Choose which agent Flow uses in coding sessions">
          <CodingSelect label="Environment agent" value={draft.harness} disabled={locked} options={harnessOptions} onChange={(value) => update({ harness: value as EnvironmentDraft["harness"], model: "auto" })} />
        </CodingRow>
        <CodingRow title="Model" description="May affect quality and cost of code changes">
          <CodingSelect label="Environment model" value={draft.model} disabled={locked} options={modelOptions(models, environmentHarness)} menuClassName="cs-model-menu" onChange={(model) => update({ model })} />
        </CodingRow>
        <CodingRow title="Sandbox size" description="Set CPU and memory for new coding sessions in this environment">
          <CodingSelect label="Sandbox size" value={draft.sandboxSize} disabled={locked} options={sizes.map((size) => ({ value: size.value, label: size.label, detail: size.detail }))} onChange={(value) => update({ sandboxSize: value as EnvironmentDraft["sandboxSize"] })} />
        </CodingRow>
        <div className="feature-row cs-row cs-tools-row">
          <div>
            <strong>{t("Tool versions")}</strong>
            <span>{t("Add the tools that Flow should install in this environment")}</span>
          </div>
          <aside>
            <ToolsEditor tools={draft.tools} disabled={locked} onChange={(tools) => update({ tools })} />
          </aside>
        </div>
      </CodingCard>

      {environment?.pendingProposal ? (
        <CodingCard className="cs-proposal-card">
          <ProposalPanel environment={environment} proposal={environment.pendingProposal} disabled={disabled} onApplied={(next) => applyEnvironment(next)} />
        </CodingCard>
      ) : agentRunning ? (
        <CodingCard className="cs-proposal-card">
          <CodingRow title="Configure with agent" description="Flow Agent is working. Its proposal will appear here." icon={<Sparkles size={16} />} />
        </CodingCard>
      ) : null}

      <CodingSection title="Files" description="Add files to the sandbox’s home directory" className="is-compact">
        <FilesEditor files={draft.files} disabled={locked} onChange={(files) => update({ files })} />
      </CodingSection>

      <CodingSection title="Environment variables" description="Secret values are encrypted before saving. Saved secret rows are masked and can’t be changed." className="is-compact">
        <EnvVarsEditor
          rows={envRows}
          saved={environment?.secrets ?? []}
          disabled={locked}
          onChange={setEnvRows}
          onImport={() => setImportOpen(true)}
          onDeleteSecret={environment ? async (name) => {
            try {
              const next = await deleteCodingEnvironmentSecret(environment.id, name);
              setEnvironment(next);
            } catch (reason) {
              toast.error(errorMessage(reason, "Could not delete the secret"));
            }
          } : undefined}
        />
      </CodingSection>

      <CodingSection
        title="Prepare script"
        titleAccessory={
          <span className="cs-info" tabIndex={0} role="img" aria-label={t("Runs in the repository with bash after tools are installed")} title={t("Runs in the repository with bash after tools are installed")}>
            <Info size={14} aria-hidden="true" />
          </span>
        }
        description="Runs while Flow prepares the environment"
      >
        <div className="feature-card">
          <CodingCodeEditor aria-label={t("Prepare script")} placeholder="npm ci" value={draft.prepareScript} disabled={locked} onChange={(prepareScript) => update({ prepareScript })} />
        </div>
      </CodingSection>

      <CodingSection title="Permissions" description="Environment owners can change settings, open terminals, and manage other owners. Workspace owners and admins can manage any environment.">
        <OwnersEditor ownerIds={owners} users={data.users} canManage={canManageOwners && !locked} onChange={(ownerIds) => update({ ownerIds })} />
      </CodingSection>

      <CodingSection title="Custom guidance" description="Add instructions for the agent to follow in this environment">
        <div className="feature-card cs-guidance">
          <textarea aria-label={t("Custom guidance")} value={draft.customGuidance} disabled={locked} onChange={(event) => update({ customGuidance: event.target.value })} />
        </div>
      </CodingSection>

      <CodingSection title="Network" description="Outbound access from the sandbox. Model providers are always reached through Flow.">
        <CodingCard>
          <CodingRow title="Network access" description={NETWORK_OPTIONS.find((option) => option.value === draft.network)?.description}>
            <CodingSelect label="Network access" value={draft.network} disabled={locked} options={NETWORK_OPTIONS} onChange={(value) => update({ network: value as EnvironmentDraft["network"] })} />
          </CodingRow>
          {draft.network === "extended" && (
            <div className="cs-domains">
              <label htmlFor="cs-domains">{t("Allowed domains")}</label>
              <textarea id="cs-domains" className="cs-code" rows={3} value={draft.allowedDomains} disabled={locked} placeholder=".example.com" onChange={(event) => update({ allowedDomains: event.target.value })} />
              <p className="cs-field-hint">{t("One per line. A leading dot matches subdomains, for example .example.com.")}</p>
            </div>
          )}
          <CodingRow title="Dependency management" description={DEPENDENCY_OPTIONS.find((option) => option.value === draft.dependencyManagement)?.description}>
            <CodingSelect label="Dependency management" value={draft.dependencyManagement} disabled={locked} options={DEPENDENCY_OPTIONS} onChange={(value) => update({ dependencyManagement: value as EnvironmentDraft["dependencyManagement"] })} />
          </CodingRow>
        </CodingCard>
      </CodingSection>

      {environment && !archived && (
        <>
          <CodingSection title="Debug" description="Create a temporary sandbox for this environment that is destroyed after 20 minutes. Billed at the same rate as sandbox runtime.">
            <CodingCard>
              <DebugTerminal environmentId={environment.id} disabled={disabled} available={status?.debugTerminal ?? true} />
            </CodingCard>
          </CodingSection>

          <SnapshotPanel
            environmentId={environment.id}
            initial={environment.latestSnapshot}
            disabled={disabled}
            onFixWithAgent={(snapshotId) => void startAgent("fix", snapshotId)}
            onChange={(snapshot?: CodingEnvironmentSnapshot) => setEnvironment((current) => (current ? { ...current, latestSnapshot: snapshot } : current))}
          />

          <CodingSection title="Danger zone">
            <CodingCard>
              <CodingRow title="Archive environment" description="New coding sessions can't use an archived environment. Running sessions continue.">
                <CodingButton danger disabled={disabled} onClick={() => void archive()}>Archive</CodingButton>
              </CodingRow>
            </CodingCard>
          </CodingSection>
        </>
      )}

      <EnvImportDialog
        environmentId={environment?.id}
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onImport={({ variables, secrets }) => {
          const existing = new Set([...envRows.map((row) => row.name), ...savedSecretNames]);
          setEnvRows([
            ...envRows.filter((row) => row.name.trim() || row.value),
            ...variables.filter((variable) => !existing.has(variable.name)).map((variable) => ({ ...variable, secret: false })),
            ...secrets.filter((secret) => !existing.has(secret.name)).map((secret) => ({ ...secret, secret: true })),
          ]);
        }}
      />
      {!canManageOwners && <p className="cs-admin-hint">{t("Only workspace admins can change environments.")}</p>}
    </div>
  );
}

/** Requirements and risks of the configured FLOW_CODING_GITLAB_CREDENTIAL_MODE (track J's copy). */
function gitlabCredentialNote(mode: GitLabCredentialMode | undefined): string {
  switch (mode) {
    case "oauth":
      return "Sessions use the workspace's GitLab OAuth access token, refreshed by Flow. It is not limited to one project: it reaches every project the authorizing user can access.";
    case "connection":
      return "Sessions use the GitLab connection token itself. It is long-lived and broad, and code the agent runs in the sandbox can read it. Prefer project access tokens.";
    default:
      return "Sessions push with a short-lived project access token that Flow creates and revokes for each session. The GitLab connection's token needs the api scope and the Maintainer role on the project. On gitlab.com, project access tokens require Premium or Ultimate.";
  }
}
