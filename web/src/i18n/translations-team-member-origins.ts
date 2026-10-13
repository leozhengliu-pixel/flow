/**
 * Chinese copy for team membership origins: the origin notes and sync
 * removal rules in team member settings, and the source of team member
 * changes in the audit log. Merged into zhCN by translations.ts.
 */
export const teamMemberOriginsZhCN: Record<string, string> = {
  "Synced from SCIM": "由 SCIM 同步",
  "Added by sync": "由同步添加",
  "Added via API": "通过 API 添加",
  "Joined by invitation": "通过邀请加入",
  "Added by the SCIM group mapped to this team. SCIM removes this member when they leave the group, unless they are a team owner.":
    "由映射到此团队的 SCIM 群组添加。成员离开该群组时，SCIM 会将其移出团队（团队负责人除外）。",
  "Added by a directory sync through the API. That sync may remove this member again; team owners are kept.":
    "由目录同步通过 API 添加。该同步之后可能会再次移除此成员；团队负责人会被保留。",
  "Added by an integration through the API. Syncs that remove only their own members keep this member.":
    "由集成通过 API 添加。只移除自己所添加成员的同步会保留此成员。",
  "Joined by accepting an invitation to this team. Syncs keep this member.":
    "通过接受此团队的邀请加入。同步会保留此成员。",
  "Who a sync can remove": "同步可以移除哪些成员",
  "SCIM removes only members marked “Synced from SCIM” when they leave the identity provider group, and directory syncs remove only members marked “Added by sync”.":
    "SCIM 只会在成员离开身份提供商群组时移除标记为“由 SCIM 同步”的成员；目录同步只会移除标记为“由同步添加”的成员。",
  "Team owners and members added in Flow by hand or by invitation are always kept; remove them here.":
    "团队负责人以及在 Flow 中手动添加或通过邀请加入的成员始终会被保留；如需移除，请在此处操作。",
  // Audit log resource type of team membership changes.
  "team member": "团队成员",
  "By hand": "手动",
  "Via API": "通过 API",
  "Via agent": "通过 Agent",
  "Via sync": "通过同步",
  "Via invitation": "通过邀请",
  "Via SCIM": "通过 SCIM",
};
