import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { FileUp, Sparkles } from "lucide-react";
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
import type { BootstrapData, CodeHostKind, CodingEnvironment, CodingEnvironmentSnapshot, CodingEnvVar } from "@/types/flow";
import type { GitLabCredentialMode } from "@/types/coding-sessions";
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
import { CodingButton, CodingCard, CodingField, CodingRow, CodingSection, CodingSelect } from "./coding-settings-primitives";
import { DebugTerminal } from "./debug-terminal";
import { EnvImportDialog } from "./env-import-dialog";
import { FilesEditor, SecretsEditor, ToolsEditor, VariablesEditor } from "./environment-fields";
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

/** The environment editor (`/settings/ai/coding-sessions/environments/:id`). */
export function EnvironmentEditorPage({ data, environmentId, disabled, onBack, onOpen }: Props) {
  const { t } = useI18n();
  const isNew = environmentId === "new";
  const { status } = useCodingStatus();
  const models = useCodingModels();
  const { environments } = useCodingEnvironments(false);
  const [environment, setEnvironment] = useState<CodingEnvironment>();
  const [loadError, setLoadError] = useState<string>();
  const [draft, setDraft] = useState<EnvironmentDraft>(() => emptyDraft());
  const [baseline, setBaseline] = useState<string>(() => JSON.stringify(draftToInput(emptyDraft())));
  const [pendingSecrets, setPendingSecrets] = useState<CodingEnvVar[]>([]);
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
    setBaseline(JSON.stringify(draftToInput(nextDraft)));
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

  const update = (patch: Partial<EnvironmentDraft>) => setDraft((current) => ({ ...current, ...patch }));
  const savedSecretNames = (environment?.secrets ?? []).map((secret) => secret.name);
  const errors = validateDraft(draft, pendingSecrets, savedSecretNames);
  const repoInUse = repositoryConflict(environments, { id: environment?.id, codeHost: draft.codeHost, hostName: draft.hostName, repository: draft.repository });
  const dirty = JSON.stringify(draftToInput(draft)) !== baseline || pendingSecrets.some((secret) => secret.name.trim());
  const archived = Boolean(environment?.archivedAt);
  const locked = disabled || archived || saving;

  const hostsOfKind = useMemo(() => (status?.codeHosts ?? []).filter((host) => host.codeHost === draft.codeHost), [status, draft.codeHost]);
  const codeHostOptions = useMemo(() => {
    const kinds = new Set<CodeHostKind>((status?.codeHosts ?? []).map((host) => host.codeHost));
    if (!kinds.size) kinds.add("github").add("gitlab");
    kinds.add(draft.codeHost);
    return [...kinds].map((kind) => ({ value: kind, label: codeHostLabel(kind), translate: false }));
  }, [status, draft.codeHost]);

  const save = async () => {
    if (errors.length || repoInUse) return;
    setSaving(true);
    setSaveError(undefined);
    const secrets = pendingSecrets.filter((secret) => secret.name.trim()).map((secret) => ({ name: secret.name.trim(), value: secret.value }));
    try {
      if (isNew) {
        const created = await createCodingEnvironment({ ...draftToInput(draft), ...(secrets.length ? { secrets } : {}) });
        setPendingSecrets([]);
        toast.success(t("Environment created"));
        onOpen(created.id);
        return;
      }
      let saved = await updateCodingEnvironment(environmentId, { ...draftToInput(draft), version: draft.version });
      if (secrets.length) saved = await addCodingEnvironmentSecrets(environmentId, secrets);
      setPendingSecrets([]);
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
        <header className="feature-header"><h1>{t("Environment")}</h1></header>
        <p className="cs-inline-error" role="alert">{loadError}</p>
      </div>
    );
  if (!isNew && !environment) return <div className="feature-settings cs-settings" aria-busy="true"><SettingsCrumb pinned onClick={onBack}>{t("Coding sessions")}</SettingsCrumb></div>;

  const harnessOptions = [{ value: "", label: "Workspace default" }, ...HARNESS_OPTIONS.map((option) => ({ ...option, translate: option.value === "openSource" }))];
  const environmentHarness = draft.harness || "codex";
  const sizes = sandboxSizeOptions(status);
  const latest = environment?.latestSnapshot;

  return (
    <div className="feature-settings cs-settings cs-editor" data-testid="coding-environment-editor">
      <SettingsCrumb pinned onClick={onBack}>{t("Coding sessions")}</SettingsCrumb>
      <header className="feature-header">
        <h1 data-i18n-ignore={isNew ? undefined : true}>{isNew ? t("New environment") : environment?.name}</h1>
        <p>{t("Configure the sandbox that coding sessions use for one repository.")}</p>
      </header>
      {archived && <p className="cs-banner" role="status">{t("This environment is archived. Unarchive it from Coding sessions to edit it.")}</p>}
      {conflict && (
        <p className="cs-banner is-error" role="alert">
          {t("This environment was changed by someone else.")}{" "}
          <button type="button" className="cs-inline-link" onClick={() => void load()}>{t("Reload")}</button>
        </p>
      )}

      <CodingCard>
        <div className="cs-form-row">
          <CodingField label="Name" htmlFor="cs-env-name">
            <input id="cs-env-name" className="cs-input" value={draft.name} disabled={locked} placeholder={t("e.g. Web app")} onChange={(event) => update({ name: event.target.value })} />
          </CodingField>
        </div>
        <CodingRow title="Code host" description="Where the repository lives">
          {hostsOfKind.length > 1 && (
            <CodingSelect label="Host" value={draft.hostName} disabled={locked || !isNew} options={hostsOfKind.map((host) => ({ value: host.hostName, label: host.hostName, translate: false }))} onChange={(hostName) => update({ hostName, repository: "", repositoryId: undefined })} />
          )}
          <CodingSelect
            label="Code host"
            value={draft.codeHost}
            disabled={locked || !isNew}
            options={codeHostOptions}
            onChange={(value) => {
              const codeHost = value as CodeHostKind;
              const host = status?.codeHosts?.find((item) => item.codeHost === codeHost && item.connected) ?? status?.codeHosts?.find((item) => item.codeHost === codeHost);
              update({ codeHost, hostName: host?.hostName ?? (codeHost === "gitlab" ? "gitlab.com" : "github.com"), repository: "", repositoryId: undefined, defaultBranch: undefined });
            }}
          />
        </CodingRow>
        {draft.codeHost === "gitlab" && (
          <p className="cs-card-text cs-gitlab-note" data-testid="gitlab-credential-note">
            {t(gitlabCredentialNote(status?.codeHosts?.find((item) => item.codeHost === "gitlab")?.credentialMode))}
          </p>
        )}
        <CodingRow
          title="Repository"
          description={repoInUse ? t("{name} already uses this repository. Only one active environment per repository is allowed.").replace("{name}", repoInUse.name) : "Only one active environment per repository"}
          warning={Boolean(repoInUse)}
        >
          <RepositoryPicker
            codeHost={draft.codeHost}
            hostName={draft.hostName}
            value={draft.repository}
            disabled={locked || !isNew}
            onChange={(repository) => update({ repository: repository.fullName, repositoryId: repository.id, defaultBranch: repository.defaultBranch || undefined, name: draft.name || repository.fullName.split("/").pop() || "" })}
          />
        </CodingRow>
      </CodingCard>

      <CodingSection title="Agent and model" description="Overrides the workspace defaults for sessions in this environment.">
        <CodingCard>
          <CodingRow title="Agent">
            <CodingSelect label="Environment agent" value={draft.harness} disabled={locked} options={harnessOptions} onChange={(value) => update({ harness: value as EnvironmentDraft["harness"], model: "auto" })} />
          </CodingRow>
          <CodingRow title="Model" description="May affect quality and cost of code changes">
            <CodingSelect label="Environment model" value={draft.model} disabled={locked} options={modelOptions(models, environmentHarness).map((option) => ({ ...option, translate: option.value === "auto" }))} onChange={(model) => update({ model })} />
          </CodingRow>
          <CodingRow title="Sandbox size" description="CPU and memory for each sandbox. Larger sizes cost more per hour.">
            <CodingSelect label="Sandbox size" value={draft.sandboxSize} disabled={locked} options={sizes.map((size) => ({ value: size.value, label: size.label, detail: size.detail }))} onChange={(value) => update({ sandboxSize: value as EnvironmentDraft["sandboxSize"] })} />
          </CodingRow>
        </CodingCard>
      </CodingSection>

      <CodingSection title="Setup" description="Runs when a sandbox starts without a snapshot, and when a snapshot is built.">
        <CodingCard className="cs-form-card">
          <CodingField label="Runtimes and tools" hint="Installed with mise, for example node 22 or python 3.12.">
            <ToolsEditor tools={draft.tools} disabled={locked} onChange={(tools) => update({ tools })} />
          </CodingField>
          <CodingField label="Files" hint="Written to the sandbox home directory before setup.">
            <FilesEditor files={draft.files} disabled={locked} onChange={(files) => update({ files })} />
          </CodingField>
          <div className="cs-field">
            <div className="cs-field-heading">
              <label>{t("Environment variables")}</label>
              <button type="button" className="cs-text-button" disabled={locked} onClick={() => setImportOpen(true)}>
                <FileUp size={13} />
                {t("Import from .env")}
              </button>
            </div>
            <VariablesEditor variables={draft.variables} disabled={locked} onChange={(variables) => update({ variables })} />
            <p className="cs-field-hint">{t("Visible to the agent and in sandbox logs.")}</p>
          </div>
          <CodingField label="Environment secrets" hint="Encrypted on save and only available while setup runs. Saved values are never shown and can't be edited. Anything the prepare script writes to disk is kept in snapshots.">
            <SecretsEditor
              saved={environment?.secrets ?? []}
              pending={pendingSecrets}
              disabled={locked}
              onPendingChange={setPendingSecrets}
              onDelete={environment ? async (name) => {
                try {
                  const next = await deleteCodingEnvironmentSecret(environment.id, name);
                  setEnvironment(next);
                } catch (reason) {
                  toast.error(errorMessage(reason, "Could not delete the secret"));
                }
              } : undefined}
            />
          </CodingField>
          <CodingField label="Prepare script" htmlFor="cs-prepare" hint="Runs in the repository with bash after tools are installed, for example npm ci.">
            <textarea id="cs-prepare" className="cs-code" spellCheck={false} rows={6} value={draft.prepareScript} disabled={locked} placeholder={"npm ci\nnpm run build"} onChange={(event) => update({ prepareScript: event.target.value })} />
          </CodingField>
          <div className="cs-field cs-field-inline">
            <div>
              <label>{t("Dependency management")}</label>
              <p className="cs-field-hint">{t(DEPENDENCY_OPTIONS.find((option) => option.value === draft.dependencyManagement)?.description ?? "")}</p>
            </div>
            <CodingSelect label="Dependency management" value={draft.dependencyManagement} disabled={locked} options={DEPENDENCY_OPTIONS} onChange={(value) => update({ dependencyManagement: value as EnvironmentDraft["dependencyManagement"] })} />
          </div>
          <CodingField label="Custom guidance" htmlFor="cs-guidance" hint="Instructions for the agent, such as how to run tests or start the app.">
            <textarea id="cs-guidance" className="cs-textarea" rows={4} value={draft.customGuidance} disabled={locked} onChange={(event) => update({ customGuidance: event.target.value })} />
          </CodingField>
        </CodingCard>
      </CodingSection>

      <CodingSection title="Network" description="Outbound access from the sandbox. Model providers are always reached through Flow.">
        <CodingCard className="cs-form-card">
          <div className="cs-field cs-field-inline">
            <div>
              <label>{t("Network access")}</label>
              <p className="cs-field-hint">{t(NETWORK_OPTIONS.find((option) => option.value === draft.network)?.description ?? "")}</p>
            </div>
            <CodingSelect label="Network access" value={draft.network} disabled={locked} options={NETWORK_OPTIONS} onChange={(value) => update({ network: value as EnvironmentDraft["network"] })} />
          </div>
          {draft.network === "extended" && (
            <CodingField label="Allowed domains" htmlFor="cs-domains" hint="One per line. A leading dot matches subdomains, for example .example.com.">
              <textarea id="cs-domains" className="cs-code" rows={3} value={draft.allowedDomains} disabled={locked} onChange={(event) => update({ allowedDomains: event.target.value })} />
            </CodingField>
          )}
        </CodingCard>
      </CodingSection>

      <div className="cs-savebar">
        {saveError ? <span className="cs-inline-error" role="alert">{saveError}</span> : errors.length > 0 && dirty ? <span className="cs-inline-error" role="alert">{t(errors[0])}</span> : <span />}
        <span />
        <CodingButton onClick={onBack}>Cancel</CodingButton>
        <CodingButton primary disabled={locked || !dirty || errors.length > 0 || Boolean(repoInUse)} onClick={() => void save()}>
          {isNew ? "Create environment" : "Save changes"}
        </CodingButton>
      </div>

      {environment && !archived && (
        <>
          <CodingSection title="Snapshot" description="A prepared copy of the repository, tools and dependencies, so sessions start quickly.">
            <CodingCard>
              <SnapshotPanel
                environmentId={environment.id}
                initial={latest}
                disabled={disabled}
                onFixWithAgent={(snapshotId) => void startAgent("fix", snapshotId)}
                onChange={(snapshot?: CodingEnvironmentSnapshot) => setEnvironment((current) => (current ? { ...current, latestSnapshot: snapshot } : current))}
              />
            </CodingCard>
          </CodingSection>

          <CodingSection title="Agent-assisted setup" description="Let Flow Agent inspect the repository and propose tools, a prepare script and guidance.">
            <CodingCard>
              {environment.pendingProposal ? (
                <ProposalPanel
                  environment={environment}
                  proposal={environment.pendingProposal}
                  disabled={disabled}
                  onApplied={(next) => applyEnvironment(next)}
                />
              ) : (
                <CodingRow title="Configure with agent" description={agentRunning ? "Flow Agent is working. Its proposal will appear here." : "The agent runs the setup in a sandbox until it succeeds. You review the result before it is applied."} icon={<Sparkles size={16} />}>
                  {latest?.status === "failed" && <CodingButton disabled={disabled || agentRunning} onClick={() => void startAgent("fix", latest.id)}>Fix with agent</CodingButton>}
                  <CodingButton disabled={disabled || agentRunning} onClick={() => void startAgent("configure")}>Configure with agent</CodingButton>
                </CodingRow>
              )}
            </CodingCard>
          </CodingSection>

          <CodingSection title="Debug terminal">
            <CodingCard>
              <DebugTerminal environmentId={environment.id} disabled={disabled} available={status?.debugTerminal ?? true} />
            </CodingCard>
          </CodingSection>

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
          const existing = new Set([...draft.variables.map((variable) => variable.name), ...pendingSecrets.map((secret) => secret.name), ...savedSecretNames]);
          update({ variables: [...draft.variables.filter((variable) => variable.name.trim() || variable.value), ...variables.filter((variable) => !existing.has(variable.name))] });
          setPendingSecrets([...pendingSecrets.filter((secret) => secret.name.trim()), ...secrets.filter((secret) => !existing.has(secret.name))]);
        }}
      />
      {data.viewerRole !== "admin" && data.viewerRole !== "owner" && <p className="cs-admin-hint">{t("Only workspace admins can change environments.")}</p>}
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
