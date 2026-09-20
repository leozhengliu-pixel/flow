/**
 * LS-0365 JiraSyncPage — SelectProjects → ConfigureSync → StatusMapping wizard.
 */
import { ChevronLeft } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

import { useI18n } from "@/i18n/i18n";
import {
  createJiraLink,
  fetchJiraRemoteProjects,
  fetchJiraRemoteStatuses,
  updateJiraLink,
  type JiraRemoteProject,
  type JiraRemoteStatus,
} from "@/lib/api";
import type { BootstrapData, Team, WorkflowState } from "@/types/flow";

import "./feature-settings.css";
import "./jira-settings.css";

type Step = "SelectProjects" | "ConfigureSync" | "StatusMapping";
type SyncDirection = "bidirectional" | "unidirectional";

type Props = {
  data: BootstrapData;
  mode: "new" | "edit";
  jiraProjectId?: string;
  onBack: () => void;
  onReload: () => Promise<void>;
};

const STEPS: Step[] = ["SelectProjects", "ConfigureSync", "StatusMapping"];

export function JiraSyncPage({ data, mode, jiraProjectId, onBack, onReload }: Props) {
  const { t } = useI18n();
  const existing = useMemo(
    () =>
      (data.jiraLinks ?? []).find((link) => link.jiraProjectId === jiraProjectId) ??
      undefined,
    [data.jiraLinks, jiraProjectId],
  );

  const [step, setStep] = useState<Step>(mode === "edit" ? "ConfigureSync" : "SelectProjects");
  const [busy, setBusy] = useState(false);
  const [projects, setProjects] = useState<JiraRemoteProject[]>([]);
  const [projectsError, setProjectsError] = useState("");
  const [statuses, setStatuses] = useState<JiraRemoteStatus[]>([]);
  const [statusesError, setStatusesError] = useState("");
  const [statusesLoading, setStatusesLoading] = useState(false);

  const [selectedProjectId, setSelectedProjectId] = useState(
    existing?.jiraProjectId ?? jiraProjectId ?? "",
  );
  const [manualProjectId, setManualProjectId] = useState(
    existing?.jiraProjectId ?? jiraProjectId ?? "",
  );
  const [manualProjectKey, setManualProjectKey] = useState(existing?.jiraProjectKey ?? "");
  const [manualProjectName, setManualProjectName] = useState(existing?.jiraProjectName ?? "");
  const [teamId, setTeamId] = useState(existing?.teamId ?? "");
  const [syncDirection, setSyncDirection] = useState<SyncDirection>(
    existing?.syncDirection === "unidirectional" ? "unidirectional" : "bidirectional",
  );
  const [statusMap, setStatusMap] = useState<Record<string, string>>(existing?.statusMap ?? {});
  const [projectQuery, setProjectQuery] = useState("");
  const [teamQuery, setTeamQuery] = useState("");

  const teams = data.teams;
  const selectedTeam = teams.find((team) => team.id === teamId);
  const teamStates = useMemo(
    () =>
      data.states.filter(
        (state) => !selectedTeam || state.teamId === selectedTeam.id || !state.teamId,
      ),
    [data.states, selectedTeam],
  );

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const remote = await fetchJiraRemoteProjects();
        if (cancelled) return;
        setProjects(remote.projects ?? []);
        setProjectsError(remote.error ?? "");
      } catch (error) {
        if (cancelled) return;
        setProjects([]);
        setProjectsError(
          error instanceof Error ? error.message : t("Failed to load Jira projects"),
        );
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [t]);

  useEffect(() => {
    const projectId = selectedProjectId || manualProjectId;
    if (!projectId || step === "SelectProjects") return;
    let cancelled = false;
    setStatusesLoading(true);
    void (async () => {
      try {
        const remote = await fetchJiraRemoteStatuses(projectId);
        if (cancelled) return;
        setStatuses(remote.statuses ?? []);
        setStatusesError(remote.error ?? "");
        if ((remote.statuses ?? []).length) {
          setStatusMap((current) => autoMapStatuses(remote.statuses, teamStates, current));
        }
      } catch (error) {
        if (cancelled) return;
        setStatuses([]);
        setStatusesError(
          error instanceof Error
            ? error.message
            : t("Failed to load Jira statuses. Try reloading the page."),
        );
      } finally {
        if (!cancelled) setStatusesLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selectedProjectId, manualProjectId, step, teamStates, t]);

  const filteredProjects = projects.filter((project) => {
    const q = projectQuery.trim().toLowerCase();
    if (!q) return true;
    return `${project.name} ${project.key} ${project.id}`.toLowerCase().includes(q);
  });
  const filteredTeams = teams.filter((team) => {
    const q = teamQuery.trim().toLowerCase();
    if (!q) return true;
    return `${team.name} ${team.key}`.toLowerCase().includes(q);
  });

  const resolveProject = (): {
    id: string;
    key: string;
    name: string;
  } | null => {
    if (selectedProjectId) {
      const found = projects.find((project) => project.id === selectedProjectId);
      if (found) return { id: found.id, key: found.key, name: found.name };
      if (existing && existing.jiraProjectId === selectedProjectId) {
        return {
          id: existing.jiraProjectId,
          key: existing.jiraProjectKey ?? "",
          name: existing.jiraProjectName ?? "",
        };
      }
    }
    if (manualProjectId.trim()) {
      return {
        id: manualProjectId.trim(),
        key: manualProjectKey.trim(),
        name: manualProjectName.trim() || manualProjectId.trim(),
      };
    }
    return null;
  };

  const continueFrom = async (current: Step) => {
    if (current === "SelectProjects") {
      const project = resolveProject();
      if (!project) {
        toast.error(t("Please select a Jira project."));
        return;
      }
      if (!teamId) {
        toast.error(t("Please select a team."));
        return;
      }
      setSelectedProjectId(project.id);
      setStep("ConfigureSync");
      return;
    }
    if (current === "ConfigureSync") {
      setStep("StatusMapping");
      return;
    }
    await submit();
  };

  const submit = async () => {
    const project = resolveProject();
    if (!project) {
      toast.error(t("Please select a Jira project."));
      setStep("SelectProjects");
      return;
    }
    if (!teamId) {
      toast.error(t("Please select a team."));
      setStep("SelectProjects");
      return;
    }
    setBusy(true);
    try {
      if (mode === "edit" && existing) {
        await updateJiraLink(existing.id, {
          syncDirection,
          statusMap,
        });
        toast.success(t("Jira sync settings updated"));
      } else {
        await createJiraLink({
          jiraProjectId: project.id,
          jiraProjectKey: project.key,
          jiraProjectName: project.name,
          teamId,
          syncDirection,
          statusMap,
        });
        toast.success(
          t("Issues from your Jira project will now be added to your Flow team").replace(
            "{project}",
            project.name,
          ),
        );
      }
      await onReload();
      onBack();
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("Something unexpected went wrong when updating your Jira integration."),
      );
    } finally {
      setBusy(false);
    }
  };

  const title = mode === "edit" ? t("Edit Jira link") : t("Create Jira link");
  const description =
    mode === "edit"
      ? t("Configure how issues are synced between Jira and Flow")
      : t(
          "Link issues and projects from a Jira space to a Flow team, and configure how issues, projects, and statuses are synced.",
        );

  return (
    <div className="feature-integrations">
      <div className="jira-settings feature-settings">
        <header className="feature-header">
          <button type="button" className="feature-back-link" onClick={onBack}>
            <ChevronLeft size={14} />
            {t("Jira")}
          </button>
          <h1>{title}</h1>
          <p>{description}</p>
        </header>

        <WizardStep
          title={t("Select team")}
          description={t("Select a Jira project to link issues and projects to a Flow team")}
          state={stepState("SelectProjects", step)}
        >
          <div className="feature-card">
            <div className="feature-row" style={{ alignItems: "flex-start" }}>
              <div style={{ width: "100%" }}>
                <strong>{t("Jira project")}</strong>
                {mode === "edit" ? (
                  <span data-i18n-ignore>
                    {existing?.jiraProjectName ||
                      existing?.jiraProjectKey ||
                      existing?.jiraProjectId}
                    {" · "}
                    {t("Cannot change the Jira project for an existing sync")}
                  </span>
                ) : (
                  <>
                    {projectsError ? (
                      <div className="jira-callout" role="status" style={{ margin: "8px 0" }}>
                        <strong>{t("Live projects unavailable")}</strong>
                        {projectsError}
                      </div>
                    ) : null}
                    {projects.length > 0 ? (
                      <>
                        <input
                          aria-label={t("Search projects…")}
                          placeholder={t("Search projects…")}
                          value={projectQuery}
                          onChange={(event) => setProjectQuery(event.target.value)}
                          style={{
                            marginTop: 8,
                            width: "100%",
                            height: 32,
                            padding: "0 10px",
                            borderRadius: 8,
                            border: "1px solid var(--theme-border-strong)",
                            background: "var(--theme-surface-1)",
                            color: "var(--theme-text-primary)",
                          }}
                        />
                        <div style={{ marginTop: 8, maxHeight: 180, overflow: "auto" }}>
                          {filteredProjects.length ? (
                            filteredProjects.map((project) => (
                              <label
                                key={project.id}
                                style={{
                                  display: "flex",
                                  gap: 8,
                                  alignItems: "center",
                                  padding: "6px 0",
                                  fontSize: 13,
                                }}
                              >
                                <input
                                  type="radio"
                                  name="jira-project"
                                  checked={selectedProjectId === project.id}
                                  onChange={() => {
                                    setSelectedProjectId(project.id);
                                    setManualProjectId(project.id);
                                    setManualProjectKey(project.key);
                                    setManualProjectName(project.name);
                                  }}
                                />
                                <span data-i18n-ignore>
                                  {project.name} ({project.key})
                                </span>
                              </label>
                            ))
                          ) : (
                            <span>{t("No matching projects")}</span>
                          )}
                        </div>
                      </>
                    ) : (
                      <div className="jira-form" style={{ padding: "8px 0 0" }}>
                        <label>
                          {t("Jira project ID")}
                          <input
                            aria-label={t("Jira project ID")}
                            value={manualProjectId}
                            onChange={(event) => {
                              setManualProjectId(event.target.value);
                              setSelectedProjectId("");
                            }}
                            placeholder="10000"
                          />
                        </label>
                        <label>
                          {t("Project key")}
                          <input
                            aria-label={t("Project key")}
                            value={manualProjectKey}
                            onChange={(event) => setManualProjectKey(event.target.value)}
                            placeholder="OPS"
                          />
                        </label>
                        <label>
                          {t("Project name")}
                          <input
                            aria-label={t("Project name")}
                            value={manualProjectName}
                            onChange={(event) => setManualProjectName(event.target.value)}
                          />
                        </label>
                      </div>
                    )}
                  </>
                )}
              </div>
            </div>
            <div className="feature-row" style={{ alignItems: "flex-start" }}>
              <div style={{ width: "100%" }}>
                <strong>{t("Flow team")}</strong>
                {mode === "edit" ? (
                  <span data-i18n-ignore>
                    {selectedTeam?.name ?? teamId}
                    {" · "}
                    {t("Cannot change the Flow team for an existing sync")}
                  </span>
                ) : (
                  <>
                    <input
                      aria-label={t("Search teams…")}
                      placeholder={t("Search teams…")}
                      value={teamQuery}
                      onChange={(event) => setTeamQuery(event.target.value)}
                      style={{
                        marginTop: 8,
                        width: "100%",
                        height: 32,
                        padding: "0 10px",
                        borderRadius: 8,
                        border: "1px solid var(--theme-border-strong)",
                        background: "var(--theme-surface-1)",
                        color: "var(--theme-text-primary)",
                      }}
                    />
                    <div style={{ marginTop: 8, maxHeight: 180, overflow: "auto" }}>
                      {filteredTeams.length ? (
                        filteredTeams.map((team) => (
                          <TeamOption
                            key={team.id}
                            team={team}
                            checked={teamId === team.id}
                            onSelect={() => setTeamId(team.id)}
                          />
                        ))
                      ) : (
                        <span>{t("No matching teams")}</span>
                      )}
                    </div>
                  </>
                )}
              </div>
            </div>
          </div>
          {step === "SelectProjects" ? (
            <div className="feature-section-action">
              <button
                type="button"
                className="feature-button primary"
                disabled={busy}
                onClick={() => void continueFrom("SelectProjects")}
              >
                {t("Continue")}
              </button>
            </div>
          ) : null}
        </WizardStep>

        <WizardStep
          title={t("Configure sync")}
          description={t("Choose how issues and projects should be synced between Jira and Flow")}
          state={stepState("ConfigureSync", step)}
        >
          <div className="feature-card">
            <DirectionOption
              title={t("Bi-directional")}
              description={t(
                "Issues, projects, and supported properties synced in both directions",
              )}
              selected={syncDirection === "bidirectional"}
              onSelect={() => setSyncDirection("bidirectional")}
            />
            <DirectionOption
              title={t("Jira → Flow")}
              description={t(
                "Tickets and epics are synced from Jira to Flow only. Updates in Flow are synced back to Jira.",
              )}
              selected={syncDirection === "unidirectional"}
              onSelect={() => setSyncDirection("unidirectional")}
            />
          </div>
          {step === "ConfigureSync" ? (
            <div className="feature-section-action">
              <button
                type="button"
                className="feature-button primary"
                disabled={busy}
                onClick={() => void continueFrom("ConfigureSync")}
              >
                {t("Continue")}
              </button>
            </div>
          ) : null}
        </WizardStep>

        {selectedTeam ? (
          <WizardStep
            title={t("Mapped statuses")}
            description={t("Statuses sync between Jira and Flow if the names match")}
            state={stepState("StatusMapping", step)}
            faded
          >
            {statusesLoading ? (
              <div className="jira-callout">{t("Loading Jira statuses…")}</div>
            ) : null}
            {statusesError ? (
              <div className="jira-callout" role="status">
                <strong>{t("Statuses unavailable")}</strong>
                {statusesError}
              </div>
            ) : null}
            {!statusesLoading && !statuses.length ? (
              <div className="jira-callout" role="status">
                {t("No statuses found in the Jira project. The link can still be created.")}
              </div>
            ) : null}
            {statuses.length > 0 ? (
              <div className="feature-card">
                {statuses.map((status) => (
                  <div className="feature-row" key={status.id || status.name}>
                    <div>
                      <strong data-i18n-ignore>{status.name}</strong>
                      <span>{t("Jira")}</span>
                    </div>
                    <aside>
                      <select
                        aria-label={`${t("Map status")}: ${status.name}`}
                        value={statusMap[status.name] ?? ""}
                        onChange={(event) =>
                          setStatusMap((current) => ({
                            ...current,
                            [status.name]: event.target.value,
                          }))
                        }
                        className="feature-select"
                        style={{ minWidth: 140 }}
                      >
                        <option value="">{t("No match")}</option>
                        {teamStates.map((state) => (
                          <option key={state.id} value={state.id}>
                            {state.name}
                          </option>
                        ))}
                      </select>
                    </aside>
                  </div>
                ))}
              </div>
            ) : (
              <NameMatchPreview states={teamStates} />
            )}
            {step === "StatusMapping" ? (
              <div className="feature-section-action">
                <button
                  type="button"
                  className="feature-button primary"
                  disabled={busy || statusesLoading}
                  onClick={() => void continueFrom("StatusMapping")}
                >
                  {mode === "edit" ? t("Save") : t("Create link")}
                </button>
              </div>
            ) : null}
          </WizardStep>
        ) : null}

        <p className="jira-pixel-note">
          {t(
            "UI note: Linear pixel follow-up pending — compare this wizard with Linear …/integrations/jira/sync/new.",
          )}
        </p>
      </div>
    </div>
  );
}

function WizardStep({
  title,
  description,
  state,
  faded,
  children,
}: {
  title: string;
  description: string;
  state: "pending" | "active" | "done";
  faded?: boolean;
  children: React.ReactNode;
}) {
  const { t } = useI18n();
  return (
    <section
      className="feature-section"
      data-wizard-state={state}
      style={{ opacity: faded && state === "pending" ? 0.55 : 1 }}
    >
      <header>
        <h2>{title}</h2>
        <p>{description}</p>
        {state === "done" ? (
          <span style={{ fontSize: 12, color: "var(--theme-text-secondary)" }}>{t("Done")}</span>
        ) : null}
      </header>
      {state !== "pending" ? children : null}
    </section>
  );
}

function DirectionOption({
  title,
  description,
  selected,
  onSelect,
}: {
  title: string;
  description: string;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      className="feature-row"
      onClick={onSelect}
      style={{
        width: "100%",
        textAlign: "left",
        border: 0,
        background: selected ? "var(--theme-surface-hover)" : "transparent",
        cursor: "pointer",
      }}
    >
      <div>
        <strong>{title}</strong>
        <span>{description}</span>
      </div>
      <aside>
        <span
          aria-hidden
          style={{
            width: 16,
            height: 16,
            borderRadius: "50%",
            border: `2px solid ${selected ? "#5e6ad2" : "var(--theme-border-strong)"}`,
            boxShadow: selected ? "inset 0 0 0 3px #5e6ad2" : undefined,
          }}
        />
      </aside>
    </button>
  );
}

function TeamOption({
  team,
  checked,
  onSelect,
}: {
  team: Team;
  checked: boolean;
  onSelect: () => void;
}) {
  return (
    <label
      style={{
        display: "flex",
        gap: 8,
        alignItems: "center",
        padding: "6px 0",
        fontSize: 13,
      }}
    >
      <input
        type="radio"
        name="flow-team"
        checked={checked}
        onChange={onSelect}
        aria-label={`${team.name} (${team.key})`}
      />
      <span data-i18n-ignore>
        {team.name} ({team.key})
      </span>
    </label>
  );
}

function NameMatchPreview({ states }: { states: WorkflowState[] }) {
  const { t } = useI18n();
  if (!states.length) return null;
  return (
    <div className="feature-card">
      <div className="feature-row">
        <div>
          <strong>{t("Name matching")}</strong>
          <span>
            {t(
              "When live Jira statuses are available, Flow matches them to team workflow states by name.",
            )}
          </span>
        </div>
      </div>
      {states.slice(0, 8).map((state) => (
        <div className="feature-row" key={state.id}>
          <div>
            <strong data-i18n-ignore>{state.name}</strong>
            <span>{t("Flow")}</span>
          </div>
          <aside>
            <span>{t("No match")}</span>
          </aside>
        </div>
      ))}
    </div>
  );
}

function stepState(target: Step, current: Step): "pending" | "active" | "done" {
  const targetIndex = STEPS.indexOf(target);
  const currentIndex = STEPS.indexOf(current);
  if (targetIndex < currentIndex) return "done";
  if (targetIndex === currentIndex) return "active";
  return "pending";
}

function autoMapStatuses(
  jiraStatuses: JiraRemoteStatus[],
  flowStates: WorkflowState[],
  current: Record<string, string>,
) {
  const next = { ...current };
  for (const status of jiraStatuses) {
    if (next[status.name]) continue;
    const match = flowStates.find(
      (state) => state.name.trim().toLowerCase() === status.name.trim().toLowerCase(),
    );
    if (match) next[status.name] = match.id;
  }
  return next;
}

