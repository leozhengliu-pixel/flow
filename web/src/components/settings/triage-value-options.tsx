import { TeamIcon } from "@/components/issue/issue-icons";
import { UserAvatar } from "@/components/ui/user-avatar";
import type { BootstrapData, TriageValueRuleKind } from "@/types/flow";

export type TriageValueOption = { value: string; label: string; icon: React.ReactNode };

/** The values a per-value rule can name for a row: users, projects, labels or teams of the workspace. */
export function triageValueOptions(data: BootstrapData, kind: TriageValueRuleKind): TriageValueOption[] {
  switch (kind) {
    case "assignee":
      return data.users
        .filter((user) => !user.app && user.active !== false)
        .map((user) => ({ value: user.id, label: user.displayName || user.name, icon: <UserAvatar className="feature-value-avatar" name={user.displayName || user.name} avatarUrl={user.avatarUrl} /> }));
    case "project":
      return (data.projects ?? []).filter((project) => !project.archivedAt).map((project) => ({ value: project.id, label: project.name, icon: <span className="feature-value-dot" style={{ background: project.color }} aria-hidden="true" /> }));
    case "label":
      return (data.labels ?? []).filter((label) => !label.archivedAt).map((label) => ({ value: label.id, label: label.name, icon: <span className="feature-value-dot" style={{ background: label.color }} aria-hidden="true" /> }));
    case "team":
      return data.teams.filter((team) => !team.retiredAt && !team.archivedAt).map((team) => ({ value: team.id, label: team.name, icon: <TeamIcon team={team} size={14} /> }));
  }
}
