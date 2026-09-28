import { useMemo, useState } from "react";
import { toast } from "sonner";

import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@/components/ui/dialog";
import { SelectControl } from "@/components/ui/select-control";
import { useI18n } from "@/i18n/i18n";
import { batchUpdateIssues, updateTeam } from "@/lib/api";
import { teamHierarchy } from "@/lib/team-hierarchy";
import type { BootstrapData, Team, WorkflowState } from "@/types/flow";

import "./retire-team-form.css";

type IssueAction = "move" | "cancel";
type SubTeamAction = "retire" | "detach";

function openIssuesForTeam(data: BootstrapData, teamId: string) {
  return data.issues.filter(
    (issue) =>
      issue.team.id === teamId &&
      !issue.archivedAt &&
      issue.state.type !== "completed" &&
      issue.state.type !== "canceled",
  );
}

function canceledStateForTeam(
  data: BootstrapData,
  teamId: string,
): WorkflowState | undefined {
  const states = data.states.filter(
    (state) =>
      state.type === "canceled" &&
      !state.reserved &&
      (state.teamId === teamId || !state.teamId),
  );
  return (
    states.find((state) => state.teamId === teamId) ??
    states.find((state) => !state.teamId)
  );
}

function message(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

/**
 * LS-0534 RetireTeamForm — IssueMoveTeam | Cancel + targetTeamId + batch
 * progress + handleSubTeamsOnRetirement.
 */
export function RetireTeamForm({
  open,
  onOpenChange,
  data,
  team,
  onReload,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const hierarchy = useMemo(
    () => teamHierarchy(data.teams, data.teamSettings),
    [data.teams, data.teamSettings],
  );
  const openIssues = useMemo(
    () => openIssuesForTeam(data, team.id),
    [data, team.id],
  );
  const descendantIds = useMemo(() => {
    const all = hierarchy.subtree(team.id);
    return [...all].filter((id) => id !== team.id);
  }, [hierarchy, team.id]);
  const activeDescendants = descendantIds.filter((id) => {
    const item = data.teams.find((candidate) => candidate.id === id);
    return item && !item.retiredAt;
  });

  const teamOptions = useMemo(() => {
    const memberships = new Set(
      data.teamMembers
        .filter((member) => member.userId === data.viewer.id)
        .map((member) => member.teamId),
    );
    const yours = data.teams.filter(
      (item) =>
        !item.retiredAt &&
        item.id !== team.id &&
        memberships.has(item.id),
    );
    const others = data.teams.filter(
      (item) =>
        !item.retiredAt &&
        item.id !== team.id &&
        !memberships.has(item.id),
    );
    return [
      ...yours.map((item) => ({
        value: item.id,
        label: item.name,
        entityName: true,
        groupLabel: t("Your teams"),
      })),
      ...others.map((item) => ({
        value: item.id,
        label: item.name,
        entityName: true,
        groupLabel: t("Other teams"),
      })),
    ];
  }, [data.teamMembers, data.teams, data.viewer.id, t, team.id]);

  const [issueAction, setIssueAction] = useState<IssueAction>(
    openIssues.length ? "move" : "cancel",
  );
  const [targetTeamId, setTargetTeamId] = useState(
    () => teamOptions[0]?.value ?? "",
  );
  const [subTeamAction, setSubTeamAction] =
    useState<SubTeamAction>("retire");
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<{
    total: number;
    done: number;
    failed: number;
    label: string;
  } | null>(null);

  const targetTeam = data.teams.find((item) => item.id === targetTeamId);
  const canSubmit =
    !busy &&
    (openIssues.length === 0 ||
      issueAction === "cancel" ||
      Boolean(targetTeamId));

  const issueSummary =
    openIssues.length === 0
      ? t("No open issues to process")
      : issueAction === "cancel"
        ? t(`${openIssues.length} issue${openIssues.length === 1 ? "" : "s"} will be canceled`)
        : targetTeam
          ? t(
              `${openIssues.length} issue${openIssues.length === 1 ? "" : "s"} will be moved to ${targetTeam.name}`,
            )
          : t(
              `${openIssues.length} issue${openIssues.length === 1 ? "" : "s"} will be moved to selected team`,
            );

  const subTeamSummary =
    activeDescendants.length === 0
      ? null
      : subTeamAction === "retire"
        ? t(
            `${activeDescendants.length} sub-team${activeDescendants.length === 1 ? "" : "s"} will be retired`,
          )
        : t(
            `${activeDescendants.length} direct sub-team${activeDescendants.length === 1 ? "" : "s"} will be removed from parent`,
          );

  const processIssues = async () => {
    if (!openIssues.length) return { success: 0, failed: 0 };
    const ids = openIssues.map((issue) => issue.id);
    const chunkSize = 25;
    let success = 0;
    let failed = 0;
    const label =
      issueAction === "cancel" ? t("canceled") : t("moved");
    setProgress({
      total: ids.length,
      done: 0,
      failed: 0,
      label: t("Processing issues…"),
    });

    if (issueAction === "cancel") {
      const canceled = canceledStateForTeam(data, team.id);
      if (!canceled) throw new Error(t("No canceled workflow state found"));
      for (let index = 0; index < ids.length; index += chunkSize) {
        const chunk = ids.slice(index, index + chunkSize);
        try {
          await batchUpdateIssues(chunk, { stateId: canceled.id });
          success += chunk.length;
        } catch {
          failed += chunk.length;
        }
        setProgress({
          total: ids.length,
          done: success,
          failed,
          label: t("Processing issues…"),
        });
      }
    } else {
      for (let index = 0; index < ids.length; index += chunkSize) {
        const chunk = ids.slice(index, index + chunkSize);
        try {
          await batchUpdateIssues(chunk, { teamId: targetTeamId });
          success += chunk.length;
        } catch {
          failed += chunk.length;
        }
        setProgress({
          total: ids.length,
          done: success,
          failed,
          label: t("Processing issues…"),
        });
      }
    }

    if (failed) {
      toast.error(
        t(`Failed to process issues`),
        {
          description: t(
            `${label.charAt(0).toUpperCase()}${label.slice(1)} ${success} issue${success === 1 ? "" : "s"}, ${failed} failed`,
          ),
        },
      );
    }
    return { success, failed };
  };

  const submit = async () => {
    if (!canSubmit) return;
    setBusy(true);
    try {
      const result = await processIssues();
      if (result.failed && result.success === 0 && openIssues.length) {
        setBusy(false);
        return;
      }
      await updateTeam(data.workspace.urlKey, team.id, {
        retired: true,
        subTeamAction,
      });
      toast.success(t(`${team.name} retired`));
      onOpenChange(false);
      await onReload();
    } catch (error) {
      toast.error(t("Failed to retire team"), {
        description: message(error),
      });
    } finally {
      setBusy(false);
      setProgress(null);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent
        className="retire-team-dialog"
        aria-describedby="retire-team-description"
      >
        <DialogTitle>{t("Retire team")}</DialogTitle>
        <p id="retire-team-description" className="retire-team-lead">
          {t(
            "Retiring a team prevents new issues from being created and existing issues from being edited. All issues and their history are preserved, and the team can be restored at any time.",
          )}
        </p>

        {openIssues.length > 0 && (
          <section className="retire-team-section">
            <h3>{t("Handle issues")}</h3>
            <p className="retire-team-hint">
              {t(teamHierarchyTriageHint(data, team.id))}
            </p>
            <div className="retire-team-choices" role="radiogroup" aria-label={t("Handle issues action")}>
              <label className="retire-team-choice">
                <input
                  type="radio"
                  name="issue-action"
                  checked={issueAction === "move"}
                  disabled={busy}
                  onChange={() => setIssueAction("move")}
                />
                <span>{t("Move to team")}</span>
              </label>
              <label className="retire-team-choice">
                <input
                  type="radio"
                  name="issue-action"
                  checked={issueAction === "cancel"}
                  disabled={busy}
                  onChange={() => setIssueAction("cancel")}
                />
                <span>{t("Cancel issues")}</span>
              </label>
            </div>
            {issueAction === "move" && (
              <div className="retire-team-target">
                <SelectControl
                  label={t("Target team")}
                  value={targetTeamId}
                  onChange={setTargetTeamId}
                  options={teamOptions}
                  disabled={busy || !teamOptions.length}
                />
              </div>
            )}
            <p className="retire-team-summary">{issueSummary}</p>
          </section>
        )}

        {activeDescendants.length > 0 && (
          <section className="retire-team-section">
            <h3>{t("Handle sub-teams")}</h3>
            <p className="retire-team-hint">
              {t(
                "Retire all sub-teams in the hierarchy, or keep them active by removing from this team",
              )}
            </p>
            <div className="retire-team-choices" role="radiogroup" aria-label={t("Handle sub-teams action")}>
              <label className="retire-team-choice">
                <input
                  type="radio"
                  name="sub-team-action"
                  checked={subTeamAction === "retire"}
                  disabled={busy}
                  onChange={() => setSubTeamAction("retire")}
                />
                <span>{t("Retire teams")}</span>
              </label>
              <label className="retire-team-choice">
                <input
                  type="radio"
                  name="sub-team-action"
                  checked={subTeamAction === "detach"}
                  disabled={busy}
                  onChange={() => setSubTeamAction("detach")}
                />
                <span>{t("Remove from parent")}</span>
              </label>
            </div>
            {subTeamSummary && (
              <p className="retire-team-summary">{subTeamSummary}</p>
            )}
          </section>
        )}

        {progress && (
          <div
            className="retire-team-progress"
            role="status"
            aria-live="polite"
          >
            <div className="retire-team-progress-label">
              {progress.label}{" "}
              <span>
                {progress.done + progress.failed}/{progress.total}
              </span>
            </div>
            <div className="retire-team-progress-track">
              <div
                className="retire-team-progress-bar"
                style={{
                  width: `${
                    progress.total
                      ? ((progress.done + progress.failed) / progress.total) *
                        100
                      : 0
                  }%`,
                }}
              />
            </div>
          </div>
        )}

        <footer className="retire-team-footer">
          <button
            type="button"
            className="settings-action"
            disabled={busy}
            onClick={() => onOpenChange(false)}
          >
            {t("Cancel")}
          </button>
          <button
            type="button"
            className="settings-action danger"
            disabled={!canSubmit}
            onClick={() => void submit()}
          >
            {busy ? t("Retiring…") : t("Retire team")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function teamHierarchyTriageHint(data: BootstrapData, teamId: string) {
  const triage = data.teamSettings?.[teamId]?.triageEnabled;
  return triage
    ? "Cancel triage, active, and backlog issues, or move them to another team"
    : "Cancel active and backlog issues, or move them to another team";
}
