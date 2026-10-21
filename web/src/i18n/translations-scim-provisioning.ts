/**
 * Chinese copy for SCIM provisioning: the deprovisioning setting and team
 * groups on the identity provider page, the "Managed by SCIM" badge and
 * locked actions in Settings › Members, and SCIM entries in the audit log.
 * Merged into zhCN by translations.ts.
 */
export const scimProvisioningZhCN: Record<string, string> = {
  "Use the work email as userName. While SCIM is on, the role, name, email and status of provisioned members are managed in your identity provider; turn SCIM off or revoke every token to manage them in Flow.":
    "请使用工作邮箱作为 userName。SCIM 开启期间，已配置成员的角色、姓名、邮箱和状态由身份提供商管理；如需在 Flow 中管理，请关闭 SCIM 或撤销所有令牌。",
  "When a user is deprovisioned": "用户被取消配置时",
  "Suspend": "暂停",
  "Suspended in the workspace. Their work stays and provisioning them again restores them.":
    "在工作区中暂停。其工作内容会保留，再次配置即可恢复。",
  "Removed from the workspace and its teams. Their account and work stay; provisioning them again adds them back.":
    "从工作区及其团队中移除。账户和工作内容会保留，再次配置会重新加入。",
  "Deprovisioning setting saved": "取消配置设置已保存",
  "Team groups": "团队群组",
  "Map an IdP group display name to a team. Members of the group are added to the team and removed when they leave the group; members added in Flow are kept.":
    "将身份提供商群组的显示名称映射到团队。群组成员会被加入团队，离开群组时会被移出；在 Flow 中添加的成员会被保留。",
  "Save team groups": "保存团队群组",
  "Team groups saved": "团队群组已保存",
  "Role groups saved": "角色组已保存",
  "Default SCIM role saved": "默认 SCIM 角色已保存",
  "SCIM updated": "SCIM 已更新",
  "SCIM token created": "SCIM 令牌已创建",
  "SCIM token rotated": "SCIM 令牌已轮换",
  "SCIM token revoked": "SCIM 令牌已撤销",
  "Map IdP group display names to workspace roles. Latest matching group push wins when a user belongs to multiple groups.":
    "将身份提供商群组的显示名称映射到工作区角色。用户属于多个群组时，以最近一次推送的匹配群组为准。",
  // Settings › Members
  "Managed by SCIM": "由 SCIM 管理",
  "Managed by SCIM. Change their role, name, email or status in your identity provider.":
    "由 SCIM 管理。请在身份提供商中更改其角色、姓名、邮箱或状态。",
  // Audit log resource types
  "workspace member": "工作区成员",
  "scim group": "SCIM 群组",
  "scim token": "SCIM 令牌",
  "scim settings": "SCIM 设置",
};
