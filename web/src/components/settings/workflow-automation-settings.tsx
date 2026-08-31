import { useCallback, useEffect, useState } from "react";
import { Play, Plus, RefreshCw, Trash2, Workflow } from "lucide-react";
import { toast } from "sonner";

import { useI18n } from "@/i18n/i18n";

import {
  createWorkflowDefinition,
  deleteWorkflowDefinition,
  listWorkflowDefinitions,
  listWorkflowRuns,
  retryWorkflowRun,
  runWorkflowDefinition,
  updateWorkflowDefinition,
} from "@/lib/api";
import type {
  BootstrapData,
  WorkflowAction,
  WorkflowDefinition,
  WorkflowRun,
} from "@/types/flow";
import {
  SettingsPageTitle,
  SettingsSelect,
  SettingsSection,
  SettingsToggle,
} from "./settings-primitives";

export function WorkflowAutomationSettings({ data }: { data: BootstrapData }) {
  const { formatDate, t } = useI18n();
  const [definitions, setDefinitions] = useState<WorkflowDefinition[]>(
    data.workflowDefinitions ?? [],
  );
  const [runs, setRuns] = useState<WorkflowRun[]>(data.workflowRuns ?? []);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [trigger, setTrigger] =
    useState<WorkflowDefinition["trigger"]>("manual");
  const [schedule, setSchedule] = useState("0 9 * * *");
  const [actionType, setActionType] =
    useState<WorkflowAction["type"]>("notify");
  const [teamId, setTeamId] = useState("");
  const refresh = useCallback(async () => {
    const [nextDefinitions, nextRuns] = await Promise.all([
      listWorkflowDefinitions(),
      listWorkflowRuns(),
    ]);
    setDefinitions(nextDefinitions);
    setRuns(nextRuns);
  }, []);
  useEffect(() => {
    void refresh().catch((error) => toast.error(message(error, t)));
  }, [refresh, t]);
  const create = async () => {
    const config: Record<string, string> = {};
    if (actionType === "createIssue") {
      config.teamId = teamId || data.teams[0]?.id || "";
      config.title = "Scheduled issue";
    }
    if (actionType === "notify") config.recipientId = data.viewer.id;
    try {
      await createWorkflowDefinition({
        name: name.trim(),
        trigger,
        schedule: trigger === "schedule" ? schedule : undefined,
        teamId: teamId || undefined,
        actions: [{ type: actionType, config }],
        maxAttempts: 3,
      });
      setCreating(false);
      setName("");
      await refresh();
    } catch (error) {
      toast.error(message(error, t));
    }
  };
  const run = async (id: string) => {
    try {
      await runWorkflowDefinition(id);
      await refresh();
    } catch (error) {
      toast.error(message(error, t));
    }
  };
  return (
    <>
      <SettingsPageTitle description={t("Run durable automations on a schedule or when issues are created.")}>
        {t("Workflows")}
      </SettingsPageTitle>
      <SettingsSection
        title={t("Definitions")}
        action={
          <button className="settings-action" onClick={() => setCreating(true)}>
            <Plus size={13} />
            {t("New workflow")}
          </button>
        }
      >
        <div className="team-setting-list">
          {creating && (
            <form
              className="agent-skill-editor"
              onSubmit={(event) => {
                event.preventDefault();
                void create();
              }}
            >
              <input
                autoFocus
                className="settings-input"
                aria-label={t("Workflow name")}
                placeholder={t("Workflow name")}
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
              <SettingsSelect
                label={t("Trigger")}
                value={trigger}
                onChange={(value) =>
                  setTrigger(value as WorkflowDefinition["trigger"])
                }
                options={[
                  { value: "manual", label: t("Manual") },
                  { value: "schedule", label: t("Schedule") },
                  { value: "issueCreated", label: t("Issue created") },
                ]}
              />
              {trigger === "schedule" && (
                <input
                  className="settings-input"
                  aria-label={t("Cron schedule")}
                  value={schedule}
                  onChange={(event) => setSchedule(event.target.value)}
                  placeholder="0 9 * * *"
                />
              )}
              <SettingsSelect
                label={t("Team")}
                value={teamId}
                onChange={setTeamId}
                options={[
                  { value: "", label: t("Workspace") },
                  ...data.teams.map((team) => ({
                    value: team.id,
                    label: team.name,
                    entityName: true,
                  })),
                ]}
              />
              <SettingsSelect
                label={t("Action")}
                value={actionType}
                onChange={(value) =>
                  setActionType(value as WorkflowAction["type"])
                }
                options={[
                  { value: "notify", label: t("Notify creator") },
                  { value: "createIssue", label: t("Create issue") },
                ]}
              />
              <footer>
                <button
                  type="button"
                  className="settings-action"
                  onClick={() => setCreating(false)}
                >
                  {t("Cancel")}
                </button>
                <button
                  className="settings-action primary"
                  disabled={!name.trim()}
                >
                  {t("Create")}
                </button>
              </footer>
            </form>
          )}
          {definitions.map((item) => (
            <div className="automation-rule-row" key={item.id}>
              <Workflow size={17} />
              <span>
                <strong data-i18n-ignore>{item.name}</strong>
                <small>
                  {t(workflowTriggerLabel(item.trigger))}
                  {item.schedule ? ` · ${item.schedule}` : ""}
                  {item.nextRunAt
                    ? ` · ${t("Next")} ${formatDate(item.nextRunAt, { dateStyle: "medium", timeStyle: "short" })}`
                    : ""}
                  {item.lastRunStatus ? ` · ${t(workflowRunStatusLabel(item.lastRunStatus))}` : ""}
                </small>
              </span>
              <SettingsToggle
                label={t("Workflow enabled")}
                checked={item.enabled}
                onChange={(enabled) =>
                  void updateWorkflowDefinition(item.id, {
                    name: item.name,
                    description: item.description,
                    teamId: item.teamId,
                    trigger: item.trigger,
                    schedule: item.schedule,
                    conditions: item.conditions,
                    actions: item.actions,
                    enabled,
                    maxAttempts: item.maxAttempts,
                  })
                    .then(refresh)
                    .catch((error) => toast.error(message(error, t)))
                }
              />
              <button
                className="settings-icon-action"
                aria-label={`${t("Run workflow")}: ${item.name}`}
                onClick={() => void run(item.id)}
              >
                <Play size={14} />
              </button>
              <button
                className="settings-icon-action danger"
                aria-label={`${t("Delete workflow")}: ${item.name}`}
                onClick={() =>
                  void deleteWorkflowDefinition(item.id)
                    .then(refresh)
                    .catch((error) => toast.error(message(error, t)))
                }
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))}
          {!definitions.length && !creating && (
            <div className="settings-empty compact">
              <Workflow size={24} />
              <h3>{t("No workflows")}</h3>
              <p>{t("Create a manual, scheduled, or issue-triggered workflow.")}</p>
            </div>
          )}
        </div>
      </SettingsSection>
      <SettingsSection title={t("Run history")}>
        <div className="team-setting-list">
          {runs.slice(0, 50).map((run) => (
            <div className="automation-rule-row" key={run.id}>
              <span>
                <strong>
                  {definitions.find((item) => item.id === run.workflowId)
                    ?.name ?? t("Deleted workflow")}
                </strong>
                <small>
                  {t(workflowRunStatusLabel(run.status))} · {t("Attempt")} {run.attempt} ·{" "}
                  {formatDate(run.startedAt, { dateStyle: "medium", timeStyle: "short" })}
                  {run.error ? ` · ${run.error}` : ""}
                </small>
              </span>
              {run.status === "failed" && (
                <button
                  className="settings-action"
                  onClick={() =>
                    void retryWorkflowRun(run.id)
                      .then(refresh)
                      .catch((error) => toast.error(message(error, t)))
                  }
                >
                  <RefreshCw size={13} />
                  {t("Retry")}
                </button>
              )}
            </div>
          ))}
          {!runs.length && (
            <div className="settings-empty compact">
              <p>{t("No workflow runs yet.")}</p>
            </div>
          )}
        </div>
      </SettingsSection>
    </>
  );
}
function workflowTriggerLabel(trigger: WorkflowDefinition["trigger"]) {
  if (trigger === "schedule") return "Schedule";
  if (trigger === "issueCreated") return "Issue created";
  return "Manual";
}

function workflowRunStatusLabel(status: string) {
  if (status === "queued") return "Queued";
  if (status === "running") return "Running";
  if (status === "completed") return "Completed";
  if (status === "failed") return "Failed";
  return status;
}

function message(error: unknown, t: (value: string) => string) {
  return error instanceof Error ? error.message : t("Workflow request failed");
}
