import type { ChatBindingBlockedReason } from "@/types/chat-integrations";

// Plain sentences for a group binding that sends nothing (its server-computed blocked reason) and for its last skipped update.
type Translate = (key: string) => string;

/** Why a binding sends nothing now (its server-computed blocked reason), as a sentence. */
export function chatBindingBlockedText(reason: ChatBindingBlockedReason, t: Translate): string {
  switch (reason) {
    case "hipaa": return t("Not sending updates: group notifications are off while HIPAA compliance is on.");
    case "revoked": return t("Not sending updates: this binding was revoked.");
    case "team_inactive": return t("Not sending updates: the team is archived or no longer available.");
    case "team_private_needs_authorization": return t("Not sending updates: this team is private. Edit the binding and approve sharing it again.");
    case "team_members_only_needs_authorization": return t("Not sending updates: this team is limited to its members, so sharing it with a group needs approval. Edit the binding and approve sharing it.");
    case "authorizer_lost_access": return t("Not sending updates: the person who approved sharing this private team can no longer manage it.");
    case "project_out_of_scope": return t("Not sending updates: a project of this binding is archived, left the team, or is shared with a team this group can't see.");
    case "template_not_permitted": return t("Not sending updates: the default template is missing or no longer fits this binding.");
    case "issue_restricted": return t("Not sending updates: the issue has its own access restrictions.");
    case "initiative_out_of_scope": return t("Not sending updates: a contributing team can't be shared with this group.");
  }
}

/** Why the binding's last skipped update was not sent, completing "Last update skipped on {date}: …". */
export function chatBindingSkipText(reason: ChatBindingBlockedReason, t: Translate): string {
  switch (reason) {
    case "hipaa": return t("HIPAA compliance is on");
    case "revoked": return t("the binding was revoked");
    case "team_inactive": return t("the team was unavailable");
    case "team_private_needs_authorization": return t("the team is private and sharing it wasn't approved");
    case "team_members_only_needs_authorization": return t("the team is limited to its members and sharing it wasn't approved");
    case "authorizer_lost_access": return t("the approver could no longer manage the team");
    case "project_out_of_scope": return t("a project couldn't be shared with this group");
    case "template_not_permitted": return t("the default template no longer fit");
    case "issue_restricted": return t("the issue has its own access restrictions");
    case "initiative_out_of_scope": return t("a contributing team couldn't be shared with this group");
  }
}
