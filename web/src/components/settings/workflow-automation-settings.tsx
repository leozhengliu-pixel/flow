import { useEffect, useState } from "react";
import { Play, Plus, RefreshCw, Trash2, Workflow } from "lucide-react";
import { toast } from "sonner";

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
  SettingsSection,
  SettingsToggle,
} from "./settings-primitives";

export function WorkflowAutomationSettings({ data }: { data: BootstrapData }) {
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
  const refresh = async () => {
    const [nextDefinitions, nextRuns] = await Promise.all([
      listWorkflowDefinitions(),
      listWorkflowRuns(),
    ]);
    setDefinitions(nextDefinitions);
    setRuns(nextRuns);
  };
  useEffect(() => {
    void refresh().catch((error) => toast.error(message(error)));
  }, []);
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
      toast.error(message(error));
    }
  };
  const run = async (id: string) => {
    try {
      await runWorkflowDefinition(id);
      await refresh();
    } catch (error) {
      toast.error(message(error));
    }
  };
  return (
    <>
      <SettingsPageTitle description="Run durable automations on a schedule or when issues are created.">
        Workflows
      </SettingsPageTitle>
      <SettingsSection
        title="Definitions"
        action={
          <button className="settings-action" onClick={() => setCreating(true)}>
            <Plus size={13} />
            New workflow
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
                placeholder="Workflow name"
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
              <select
                className="settings-select native"
                value={trigger}
                onChange={(event) =>
                  setTrigger(
                    event.target.value as WorkflowDefinition["trigger"],
                  )
                }
              >
                <option value="manual">Manual</option>
                <option value="schedule">Schedule</option>
                <option value="issueCreated">Issue created</option>
              </select>
              {trigger === "schedule" && (
                <input
                  className="settings-input"
                  aria-label="Cron schedule"
                  value={schedule}
                  onChange={(event) => setSchedule(event.target.value)}
                  placeholder="0 9 * * *"
                />
              )}
              <select
                className="settings-select native"
                value={teamId}
                onChange={(event) => setTeamId(event.target.value)}
              >
                <option value="">Workspace</option>
                {data.teams.map((team) => (
                  <option key={team.id} value={team.id}>
                    {team.name}
                  </option>
                ))}
              </select>
              <select
                className="settings-select native"
                value={actionType}
                onChange={(event) =>
                  setActionType(event.target.value as WorkflowAction["type"])
                }
              >
                <option value="notify">Notify creator</option>
                <option value="createIssue">Create issue</option>
              </select>
              <footer>
                <button
                  type="button"
                  className="settings-action"
                  onClick={() => setCreating(false)}
                >
                  Cancel
                </button>
                <button
                  className="settings-action primary"
                  disabled={!name.trim()}
                >
                  Create
                </button>
              </footer>
            </form>
          )}
          {definitions.map((item) => (
            <div className="automation-rule-row" key={item.id}>
              <Workflow size={17} />
              <span>
                <strong>{item.name}</strong>
                <small>
                  {item.trigger}
                  {item.schedule ? ` · ${item.schedule}` : ""}
                  {item.nextRunAt
                    ? ` · Next ${new Date(item.nextRunAt).toLocaleString()}`
                    : ""}
                  {item.lastRunStatus ? ` · ${item.lastRunStatus}` : ""}
                </small>
              </span>
              <SettingsToggle
                label={`${item.name} enabled`}
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
                    .catch((error) => toast.error(message(error)))
                }
              />
              <button
                className="settings-icon-action"
                aria-label={`Run ${item.name}`}
                onClick={() => void run(item.id)}
              >
                <Play size={14} />
              </button>
              <button
                className="settings-icon-action danger"
                aria-label={`Delete ${item.name}`}
                onClick={() =>
                  void deleteWorkflowDefinition(item.id)
                    .then(refresh)
                    .catch((error) => toast.error(message(error)))
                }
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))}
          {!definitions.length && !creating && (
            <div className="settings-empty compact">
              <Workflow size={24} />
              <h3>No workflows</h3>
              <p>Create a manual, scheduled, or issue-triggered workflow.</p>
            </div>
          )}
        </div>
      </SettingsSection>
      <SettingsSection title="Run history">
        <div className="team-setting-list">
          {runs.slice(0, 50).map((run) => (
            <div className="automation-rule-row" key={run.id}>
              <span>
                <strong>
                  {definitions.find((item) => item.id === run.workflowId)
                    ?.name ?? "Deleted workflow"}
                </strong>
                <small>
                  {run.status} · Attempt {run.attempt} ·{" "}
                  {new Date(run.startedAt).toLocaleString()}
                  {run.error ? ` · ${run.error}` : ""}
                </small>
              </span>
              {run.status === "failed" && (
                <button
                  className="settings-action"
                  onClick={() =>
                    void retryWorkflowRun(run.id)
                      .then(refresh)
                      .catch((error) => toast.error(message(error)))
                  }
                >
                  <RefreshCw size={13} />
                  Retry
                </button>
              )}
            </div>
          ))}
          {!runs.length && (
            <div className="settings-empty compact">
              <p>No workflow runs yet.</p>
            </div>
          )}
        </div>
      </SettingsSection>
    </>
  );
}
function message(error: unknown) {
  return error instanceof Error ? error.message : "Workflow request failed";
}
