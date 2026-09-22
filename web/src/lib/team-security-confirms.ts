import { confirmChoiceAction } from "@/components/ui/action-dialog-service";
import type { TeamSettings } from "@/types/flow";

export type TeamPermissionKey =
  | "settingsPermission"
  | "labelPermission"
  | "templatePermission"
  | "agentSkillPermission"
  | "loopPermission"
  | "memberPermission";

const PERMISSION_RANK: Record<string, number> = {
  owners: 0,
  teamMembers: 1,
  allMembers: 2,
};

const MEMBERSHIP_RANK: Record<string, number> = {
  owners: 0,
  members: 1,
  open: 2,
};

export function isLessRestrictivePermission(
  previous: string,
  next: string,
) {
  return (PERMISSION_RANK[next] ?? 0) > (PERMISSION_RANK[previous] ?? 0);
}

export function isLessRestrictiveMembership(
  previous: string,
  next: string,
) {
  return (MEMBERSHIP_RANK[next] ?? 0) > (MEMBERSHIP_RANK[previous] ?? 0);
}

/** Linear applyToSubTeams / onlyThisTeam confirm when relaxing a permission. */
export async function confirmApplyPermissionToSubTeams(): Promise<
  "applyToSubTeams" | "onlyThisTeam" | null
> {
  const choice = await confirmChoiceAction("Update permission?", {
    description:
      "This permission is being made less restrictive. This change can also be applied to all sub-teams.",
    confirmLabel: "Update",
    danger: false,
    defaultChoice: "applyToSubTeams",
    choices: [
      { id: "applyToSubTeams", label: "Apply to all sub-teams" },
      { id: "onlyThisTeam", label: "Only this team" },
    ],
  });
  if (choice !== "applyToSubTeams" && choice !== "onlyThisTeam") return null;
  return choice;
}

/** Linear allowSubTeams / dontAllowSubTeams when opening membership. */
export async function confirmAllowSubTeamsMembership(): Promise<
  "allowSubTeams" | "dontAllowSubTeams" | null
> {
  const choice = await confirmChoiceAction("Allow members to join?", {
    description:
      "Members will be able to join this team without an invite. This change can also be applied to all sub-teams.",
    confirmLabel: "Allow",
    danger: false,
    defaultChoice: "allowSubTeams",
    choices: [
      {
        id: "allowSubTeams",
        label: "Allow members to join all sub-teams",
      },
      {
        id: "dontAllowSubTeams",
        label: "Keep restricting membership for sub-teams",
      },
    ],
  });
  if (choice !== "allowSubTeams" && choice !== "dontAllowSubTeams")
    return null;
  return choice;
}

export function getIssueSharingAudience() {
  return "anyone with the link";
}

export type IssueSharingPermission = NonNullable<
  TeamSettings["issueSharingPermission"]
>;
