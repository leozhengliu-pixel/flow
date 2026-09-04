import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  Bot,
  CalendarDays,
  Check,
  ChevronDown,
  CircleAlert,
  Clipboard,
  ExternalLink,
  GitFork,
  KeyRound,
  LoaderCircle,
  Mail,
  MessageCircle,
  MessageSquare,
  Monitor,
  ShieldCheck,
  Plus,
  Smartphone,
} from "lucide-react";
import * as Popover from "@radix-ui/react-popover";
import { toast } from "sonner";
import { NavLink } from "react-router-dom";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  deletePushSubscription,
  addCommitSigningKey,
  beginPasskeyRegistration,
  createAPIKey,
  deletePasskey,
  fetchAccountIdentities,
  fetchAccountSessions,
  fetchCommitSigningKey,
  fetchPasskeys,
  finishPasskeyRegistration,
  listPushSubscriptions,
  logoutAccount,
  removeMember,
  removeCommitSigningKey,
  revokeAccountSession,
  revokeAPIKey,
  rotateAPIKey,
  revokeOtherAccountSessions,
  revokeOAuthAuthorization,
  updateAccountProfile,
  updateAPIKey,
  updateNotificationPreferences,
  unlinkAccountIdentity,
  updatePasskey,
} from "@/lib/api";
import type { SettingsPageId } from "@/lib/app-routes";
import { agentSkillPath, newAgentSkillPath } from "@/lib/app-routes";
import type {
  APIKey,
  AccountSessionInfo,
  BootstrapData,
  CommitSigningKey,
  NotificationPreferences,
  OAuthAuthorization,
  Passkey,
} from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import {
  SettingsPageTitle,
  SettingsRow,
  SettingsSection,
  SettingsSelect,
  SettingsToggle,
  type SettingsSelectOption,
} from "./settings-primitives";

import "./personal-settings.css";

export type PersonalSettingsValues = Record<string, string | boolean>;

type Props = {
  page: SettingsPageId;
  apiKeyMode?: "new" | "detail" | "edit";
  apiKeyId?: string;
  signingKeyMode?: "new";
  data: BootstrapData;
  values: PersonalSettingsValues;
  setValue: (key: string, value: string | boolean) => void;
  onNavigate: (page: SettingsPageId) => void;
  onCreateAPIKey?: () => void;
  onCreateSigningKey?: () => void;
  onOpenAPIKey?: (key: APIKey) => void;
  onEditAPIKey?: (key: APIKey) => void;
  onLogout?: () => Promise<void>;
  onReload: () => Promise<void>;
  onBack: () => void;
  onCustomizeSidebar: () => void;
};

type PersonalTranslate = (source: string) => string;

// Keeps the one-time secret available during the create -> detail navigation
// even when a browser blocks sessionStorage. It is deleted as soon as the
// detail page consumes it and is never sent back to the API.
const pendingAPIKeySecrets = new Map<string, string>();

// Personal settings are kept local until the shared catalog owns every key.
// This avoids falling back to partially translated legacy DOM mutations.
const PERSONAL_ZH: Record<string, string> = {
  Preferences: "偏好设置",
  Profile: "个人资料",
  Notifications: "通知",
  "Code & reviews": "代码与评审",
  "Security & access": "安全与访问",
  "Connected accounts": "已连接账户",
  "Agent personalization": "智能助手个性化",
  General: "通用",
  "Interface and theme": "界面与主题",
  "Desktop application": "桌面应用",
  "Automations and workflows": "自动化与工作流",
  Language: "语言",
  "Choose the language used throughout the application":
    "选择整个应用使用的语言",
  "Default home view": "默认主页视图",
  "Select which view to display when launching Flow":
    "选择启动 Flow 时显示的视图",
  "Flow Agent (default)": "Flow Agent（默认）",
  Inbox: "收件箱",
  "My issues": "我的事项",
  "Display names": "显示名称",
  "Select how names are displayed in the Flow interface":
    "选择名称在 Flow 界面中的显示方式",
  "Full name": "全名",
  "First name": "名字",
  Username: "用户名",
  username: "用户名",
  "One word, like a nickname or first name": "一个单词，例如昵称或名字",
  "First day of the week": "每周第一天",
  "Used for date pickers": "用于日期选择器",
  Monday: "星期一",
  Saturday: "星期六",
  Sunday: "星期日",
  "Convert text emoticons into emojis": "将文本表情转换为 Emoji",
  "Strings like :) will be converted to 🙂": "类似 :) 的文本会转换为 🙂",
  "Send comments on…": "提交评论快捷键…",
  "Choose which key press is used to submit comments":
    "选择提交评论所使用的按键",
  "App sidebar": "应用侧边栏",
  "Customize sidebar item visibility, ordering, and badge style":
    "自定义侧边栏项目的可见性、顺序和徽标样式",
  Customize: "自定义",
  "Font size": "字体大小",
  "Adjust the size of text across the app": "调整整个应用的文字大小",
  Small: "小",
  Default: "默认",
  Large: "大",
  "Use pointer cursors": "使用指针光标",
  "Change the cursor to a pointer when hovering over interactive elements":
    "悬停在交互元素上时使用指针光标",
  "Underline links": "为链接添加下划线",
  "Always underline links in text content": "始终为正文中的链接添加下划线",
  "Disable animated images & emoji": "停用动画图片和 Emoji",
  "When enabled, GIFs and animated emojis remain static until hovered":
    "启用后，GIF 和动态 Emoji 在悬停前保持静止",
  "Interface theme": "界面主题",
  "Select or customize your interface color scheme": "选择或自定义界面配色方案",
  "System preference": "跟随系统",
  Light: "浅色",
  Dark: "深色",
  "Open in desktop app": "在桌面应用中打开",
  "Automatically open links in desktop app when possible":
    "尽可能自动在桌面应用中打开链接",
  "Auto-assign to self": "自动指派给自己",
  "When creating new issues, always assign them to yourself by default":
    "创建新事项时默认指派给自己",
  "On move to started status, assign to yourself":
    "移至“已开始”状态时指派给自己",
  "When you move an unassigned issue to started, it will be automatically assigned to you":
    "将未指派事项移至已开始状态时，自动指派给自己",
  "Profile picture": "头像",
  Email: "邮箱",
  Title: "职位",
  "Your job title or role": "你的职位或角色",
  "Software engineer": "软件工程师",
  Cancel: "取消",
  "Security settings": "安全设置",
  Breadcrumb: "面包屑导航",
  Create: "创建",
  Update: "更新",
  "Saving…": "正在保存…",
  "Profile updated": "个人资料已更新",
  "Could not update profile": "无法更新个人资料",
  "Workspace access": "工作区访问权限",
  "Remove yourself from workspace": "将自己移出工作区",
  "Leave workspace": "离开工作区",
  "You will lose access to this workspace. An administrator must invite you again to restore access.":
    "你将失去此工作区的访问权限。管理员需要重新邀请你才能恢复访问。",
  "Could not leave workspace": "无法离开工作区",
  "Push notifications": "推送通知",
  "Choose which notifications are pushed to your devices. All notifications will still appear in your inbox.":
    "选择要推送到设备的通知。所有通知仍会显示在收件箱中。",
  Desktop: "桌面端",
  Mobile: "移动端",
  Enabled: "已启用",
  Disabled: "已停用",
  "Not available in Flow web": "Flow 网页版暂不可用",
  "Enabled for all notifications": "已为所有通知启用",
  "Not connected": "未连接",
  "Updates from Flow": "Flow 更新",
  "Subscribe to product announcements and important changes from the Flow team":
    "订阅 Flow 团队的产品公告和重要变更",
  "Show updates in sidebar": "在侧边栏显示更新",
  "Highlight new features and improvements in the app sidebar":
    "在应用侧边栏突出显示新功能和改进",
  "Changelog newsletter": "更新日志简报",
  "Receive an email twice a month highlighting new features and improvements":
    "每月接收两封介绍新功能和改进的邮件",
  Marketing: "营销",
  "Marketing and onboarding": "营销与引导",
  "Occasional updates to help you get the most out of Flow":
    "偶尔发送帮助你更好使用 Flow 的更新",
  "Other updates": "其他更新",
  "Invite accepted": "邀请已接受",
  "Email when invitees accept an invite": "受邀者接受邀请时发送邮件",
  "Privacy and legal updates": "隐私与法律更新",
  "Email when privacy policies or terms of service change":
    "隐私政策或服务条款变更时发送邮件",
  "Data processing agreement (DPA)": "数据处理协议（DPA）",
  "Data processing agreement": "数据处理协议",
  "Email when our DPA changes": "DPA 变更时发送邮件",
  "Email notifications": "邮件通知",
  "Desktop notifications": "桌面通知",
  "Enable email notifications": "启用邮件通知",
  "Enable desktop notifications": "启用桌面通知",
  "Notification sounds": "通知声音",
  "Web browser": "网页浏览器",
  Remove: "移除",
  "No browser push devices": "没有浏览器推送设备",
  "Browser devices appear here after notification permission is granted.":
    "授予通知权限后，浏览器设备会显示在这里。",
  "Could not save notifications": "无法保存通知设置",
  "Could not remove device": "无法移除设备",
  "Review GitHub pull requests and agent code diffs in Flow":
    "在 Flow 中评审 GitHub 拉取请求和 Agent 代码差异",
  "Enable code reviews": "启用代码评审",
  "Review GitHub pull requests, accessible from the sidebar":
    "从侧边栏评审 GitHub 拉取请求",
  "Auto-convert draft pull requests": "自动转换草稿拉取请求",
  "Automatically mark your drafts as ready upon approval or requesting a review":
    "批准或请求评审时，自动将草稿标记为可评审",
  "Merge strategy": "合并策略",
  "Choose the default merge strategy for pull requests":
    "选择拉取请求的默认合并策略",
  "Squash and merge": "压缩并合并",
  "Merge commit": "创建合并提交",
  "Rebase and merge": "变基并合并",
  "Code theme": "代码主题",
  "Select the syntax highlighting theme used in code diffs and viewers":
    "选择代码差异和查看器使用的语法高亮主题",
  "Flow Light": "Flow 浅色",
  "Flow Dark": "Flow 深色",
  "GitHub Light": "GitHub 浅色",
  "GitHub Dark": "GitHub 深色",
  Font: "字体",
  "Code font": "代码字体",
  Regular: "常规",
  "12px, Regular, Default": "12px，常规，默认",
  "13px, Regular, Default": "13px，常规，默认",
  "14px, Regular, Default": "14px，常规，默认",
  "Choose which review activity appears in your Flow inbox and push notifications":
    "选择要显示在 Flow 收件箱和推送通知中的评审活动",
  "Comments & reviews": "评论与评审",
  "Comments, mentions, and submitted reviews": "评论、提及和已提交的评审",
  All: "全部",
  "Exclude Bots": "排除机器人",
  "Mentions only": "仅提及",
  "Review requests": "评审请求",
  "Requests for your personal review": "请求你个人评审",
  "GitHub team review requests": "GitHub 团队评审请求",
  "Requests for review from your GitHub teams with 10 or fewer members":
    "来自成员不超过 10 人的 GitHub 团队的评审请求",
  "Checks & merge queue": "检查与合并队列",
  "Check failures and merge queue updates": "检查失败和合并队列更新",
  "Signed commits": "签名提交",
  "Require signed commits": "要求签名提交",
  "Users must upload a signing key before starting a coding session":
    "用户必须先上传签名密钥才能开始编码会话",
  "No signing key added": "尚未添加签名密钥",
  "Add key": "添加密钥",
  "External tools": "外部工具",
  "Configure coding tools": "配置编码工具",
  "Configure the external coding tools you can open issues in":
    "配置可用于打开事项的外部编码工具",
  "Git attachment format": "Git 附件格式",
  "The format of GitHub/GitLab attachments on issues":
    "事项中 GitHub/GitLab 附件的格式",
  "Issue title": "标题",
  "Title and URL": "标题和 URL",
  "On git branch copy, move issue to started status":
    "复制 Git 分支时将事项移至已开始状态",
  "After copying the git branch name, issue status is moved to the team’s first started workflow status.":
    "复制 Git 分支名称后，将事项状态移至团队的第一个已开始工作流状态。",
  "On open in coding tool, move issue to started status":
    "在编码工具中打开时将事项移至已开始状态",
  "After opening an issue in a coding tool or copying as prompt, issue status is moved to the team’s first started workflow status.":
    "在编码工具中打开事项或复制为提示词后，将事项状态移至团队的第一个已开始工作流状态。",
  Sessions: "会话",
  "Devices logged into your account": "已登录你账户的设备",
  "No sessions": "没有会话",
  "Current session": "当前会话",
  "Log out": "退出登录",
  "Could not log out": "退出登录失败",
  "Signed-in session": "已登录会话",
  "Last active": "上次活动时间",
  expires: "到期时间",
  teams: "个团队",
  "other session": "个其他会话",
  "other sessions": "个其他会话",
  "Revoke all": "全部撤销",
  Revoke: "撤销",
  Passkeys: "通行密钥",
  "Passkeys are a secure way to sign in to your Flow account":
    "通行密钥是一种安全登录 Flow 账户的方式",
  "No passkeys registered": "尚未注册通行密钥",
  "Passkey enrollment is not available on this server.":
    "此服务器暂不支持注册通行密钥。",
  "Passkey enrollment is not available on this browser.":
    "当前浏览器暂不支持注册通行密钥。",
  "Passkeys require a secure connection": "通行密钥需要安全连接。",
  "Passkeys are unavailable on IP addresses; use localhost or HTTPS":
    "IP 地址无法使用通行密钥，请改用 localhost 或 HTTPS。",
  "Passkey registration was canceled": "通行密钥注册已取消",
  "Passkey added": "通行密钥已添加",
  "Unable to add passkey": "无法添加通行密钥",
  "Passkey name": "通行密钥名称",
  "Never used": "从未使用",
  Rename: "重命名",
  "Could not rename passkey": "无法重命名通行密钥",
  "Press Enter to save or Escape to cancel":
    "按 Enter 保存，按 Escape 取消",
  "Revoke passkey?": "撤销通行密钥？",
  "This passkey will no longer be able to sign in to Flow.":
    "此通行密钥将无法再登录 Flow。",
  "Unable to revoke passkey": "无法撤销通行密钥",
  "Personal API keys": "个人 API 密钥",
  "Use Flow’s API to build your own integrations":
    "使用 Flow API 构建自己的集成",
  "No API keys created": "尚未创建 API 密钥",
  "Key name": "密钥名称",
  "A descriptive name for this API key…": "为此 API 密钥填写描述性名称…",
  "Key name is required": "请输入密钥名称",
  "Key name must be at least 2 characters": "密钥名称至少需要 2 个字符",
  "An API key with this name already exists": "已存在同名 API 密钥",
  "Create API key": "创建 API 密钥",
  "API key created": "API 密钥已创建",
  "API key": "API 密钥",
  "When using the API key all actions are attributed to you as an individual":
    "使用 API 密钥时，所有操作都会归属于你个人",
  "Only enable the minimum permissions required for your use case":
    "仅启用用例所需的最低权限",
  Permissions: "权限",
  "Full access": "完全访问",
  "No permissions": "无权限",
  "Only select permissions…": "仅选择权限…",
  Read: "读取",
  "Read all workspace data available to you": "读取你可访问的全部工作区数据",
  Write: "写入",
  "Read and write all workspace data available to you":
    "读取和写入你可访问的全部工作区数据",
  "Create issues": "创建事项",
  "Create and update issues": "创建和更新事项",
  "Create comments": "创建评论",
  "Create issue comments": "创建事项评论",
  Admin: "管理员",
  "Access admin-only API features": "访问仅限管理员的 API 功能",
  "Team access": "团队访问权限",
  "Set limits around which teams can be accessed via this API key":
    "限制此 API 密钥可访问的团队",
  "All teams you have access to": "你有权访问的所有团队",
  Teams: "团队",
  "Only select teams…": "仅选择团队…",
  "Select teams…": "选择团队…",
  "Remove team": "移除团队",
  "No teams found": "未找到团队",
  "No teams selected": "未选择团队",
  "Toggle menu": "切换菜单",
  "Copy this key now. It will not be shown again.":
    "请立即复制此密钥，关闭后将不再显示。",
  "This secret is shown once. Store it somewhere safe before leaving this page.":
    "此密钥只显示一次，请在离开页面前将其保存到安全位置。",
  "This secret is shown once. Copy it before closing.":
    "此密钥只显示一次，请在关闭前复制。",
  "This API key will not be visible in the future. Please copy it now.":
    "此 API 密钥之后将不再显示，请立即复制。",
  "Copy to clipboard": "复制到剪贴板",
  Copy: "复制",
  Copied: "已复制",
  Done: "完成",
  "Could not copy API key": "无法复制 API 密钥",
  "Personal API key copied to clipboard": "个人 API 密钥已复制到剪贴板",
  "Select at least one permission": "至少选择一项权限",
  "Select at least one team": "至少选择一个团队",
  "Could not create API key": "无法创建 API 密钥",
  Created: "创建于",
  Edit: "编辑",
  Save: "保存",
  "Edit API key": "编辑 API 密钥",
  "API key updated": "API 密钥已更新",
  "Could not update API key": "无法更新 API 密钥",
  "API key revoked": "API 密钥已撤销",
  "active API key": "个有效 API 密钥",
  "Manage API keys": "管理 API 密钥",
  "New API key": "新建 API 密钥",
  "New passkey": "新建通行密钥",
  Rotate: "轮换",
  "Could not rotate API key": "无法轮换 API 密钥",
  "Could not revoke API key": "无法撤销 API 密钥",
  "Revoke API key?": "撤销 API 密钥？",
  "Applications using this key will no longer access Flow data.":
    "使用此密钥的应用将无法再访问 Flow 数据。",
  "Last used": "上次使用",
  "Commit signing key": "提交签名密钥",
  "Coding sessions use this key to sign your commits":
    "编码会话使用此密钥为提交签名",
  "Add commit signing key": "添加提交签名密钥",
  "Paste an unencrypted SSH or PGP private key. The private material is validated and never stored.":
    "粘贴未加密的 SSH 或 PGP 私钥。私钥只会被验证，不会被保存。",
  "Private key": "私钥",
  "Private key is required": "请输入私钥",
  "A descriptive name for this key…": "为此密钥填写描述性名称…",
  "Paste your private key here…": "在此粘贴私钥…",
  "Paste an unencrypted SSH or GPG private key or drop a key file here":
    "粘贴未加密的 SSH 或 GPG 私钥，或将密钥文件拖到这里",
  "You can also drop a key file here": "也可以将密钥文件拖到这里",
  "Upload key": "上传密钥",
  "Signing key uploaded": "签名密钥已上传",
  "Signing key removed": "签名密钥已移除",
  "Unable to upload signing key": "无法上传签名密钥",
  "Unable to remove signing key": "无法移除签名密钥",
  "Unable to read signing key": "无法读取签名密钥",
  "Authorized applications": "已授权应用",
  "OAuth applications you’ve approved": "你已授权的 OAuth 应用",
  "No authorized applications": "没有已授权应用",
  "Revoke authorized application?": "撤销已授权应用？",
  "This application will no longer access your Flow account.":
    "此应用将无法再访问你的 Flow 账户。",
  "Could not revoke authorized application": "无法撤销已授权应用",
  "Revoke all other sessions?": "撤销所有其他会话？",
  "Every other device will need to sign in again.":
    "所有其他设备都需要重新登录。",
  "Could not load security information": "无法加载安全信息",
  "Other sessions revoked": "已撤销其他会话",
  "Could not revoke sessions": "无法撤销会话",
  "Show all": "显示全部",
  "Show less": "收起",
  "Session revoked": "会话已撤销",
  "Revoke access": "撤销访问权限",
  "Log out?": "退出登录？",
  "You will be logged out from this session": "你将从此会话退出登录",
  "This device will no longer be able to access your account":
    "此设备将无法再访问你的账户",
  Device: "设备",
  "IP address": "IP 地址",
  "Last location": "上次位置",
  "Original sign in": "首次登录",
  Unknown: "未知",
  "API key not found": "未找到 API 密钥",
  "This API key may have been revoked or removed":
    "此 API 密钥可能已被撤销或移除",
  "Connect your user accounts to sync attribution of your actions between apps":
    "连接用户账户，以同步你在不同应用中的操作归属",
  "Sync your message attribution, and receive notifications in Slack":
    "同步消息归属并在 Slack 中接收通知",
  "Sync your calendar out-of-office status to Flow":
    "将日历中的外出状态同步到 Flow",
  "Preview issues, projects, and views within Notion":
    "在 Notion 中预览事项、项目和视图",
  "Review code in Flow and sync attribution of your git-related actions":
    "在 Flow 中评审代码并同步 Git 相关操作的归属",
  Connected: "已连接",
  Connect: "连接",
  "Sign-in identities": "登录身份",
  "Identity provider accounts connected to your Flow profile":
    "连接到 Flow 个人资料的身份提供商账户",
  "last used": "上次使用时间",
  Unlink: "解除关联",
  "No sign-in identities connected": "未连接登录身份",
  "Could not load connected accounts": "无法加载已连接账户",
  "Could not unlink identity": "无法解除身份关联",
  "Your personal settings for Flow Agent": "Flow Agent 的个人设置",
  Guidance: "指引",
  "Provide personal instructions and context for Flow Agent when responding to conversations":
    "为 Flow Agent 回复会话时提供个人指引和上下文",
  "AI prompt rules": "AI 提示规则",
  "Enter personal guidance for Flow Agent (optional)…":
    "输入 Flow Agent 的个人指引（可选）…",
  Skills: "技能",
  "Reusable prompts auto-selected by the agent or invoked via slash commands":
    "由 Agent 自动选择或通过斜杠命令调用的可复用提示",
  "Create skill": "创建技能",
  "No skills created": "尚未创建技能",
  "MCP connectors": "MCP 连接器",
  "Add MCP connectors for use with Flow Agent. Workspace admins can manage available connectors in security settings.":
    "添加供 Flow Agent 使用的 MCP 连接器。工作区管理员可在安全设置中管理可用连接器。",
  "Agent MCP access disabled in this workspace":
    "此工作区已停用 Agent MCP 访问",
  Configure: "配置",
  "?": "？",
};

export function PersonalSettings(props: Props) {
  const { locale } = useI18n();
  const p = useCallback<PersonalTranslate>(
    (source) => (locale === "zh-CN" ? (PERSONAL_ZH[source] ?? source) : source),
    [locale],
  );
  let content: ReactNode;
  if (props.page === "preferences") content = <Preferences {...props} p={p} />;
  else if (props.page === "profile") content = <Profile {...props} p={p} />;
  else if (props.page === "notifications")
    content = <Notifications {...props} p={p} />;
  else if (props.page === "code-and-reviews")
    content = <CodeReviews {...props} p={p} />;
  else if (props.page === "account-security")
    content = <Security {...props} p={p} />;
  else if (props.page === "connections")
    content = <Connections {...props} p={p} />;
  else content = <Agents {...props} p={p} />;
  return <div className="personal-settings">{content}</div>;
}

type PersonalProps = Props & { p: PersonalTranslate };

function PersonalPageTitle({
  children,
  description,
}: {
  children: ReactNode;
  description?: ReactNode;
}) {
  return (
    <SettingsPageTitle
      className="personal-page-header"
      description={description}
    >
      {children}
    </SettingsPageTitle>
  );
}
function PersonalSection({
  action,
  className,
  headerClassName,
  id,
  title,
  description,
  children,
}: {
  action?: ReactNode;
  className?: string;
  headerClassName?: string;
  id?: string;
  title?: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <SettingsSection
      action={action}
      className={`personal-section${className ? ` ${className}` : ""}`}
      description={description}
      id={id}
      headerClassName={headerClassName}
      title={title}
    >
      {children}
    </SettingsSection>
  );
}
function PersonalRow(props: React.ComponentProps<typeof SettingsRow>) {
  return <SettingsRow {...props} />;
}
function PersonalSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: SettingsSelectOption[];
  onChange: (value: string) => void;
}) {
  return (
    <SettingsSelect
      entityName={isBusinessName}
      label={label}
      onChange={onChange}
      options={options}
      value={value}
    />
  );
}
function Action({
  children,
  onClick,
  danger,
  primary,
  disabled,
  label,
  title,
}: {
  children: ReactNode;
  onClick?: () => void;
  danger?: boolean;
  primary?: boolean;
  disabled?: boolean;
  label?: string;
  title?: string;
}) {
  return (
    <button
      aria-label={label}
      title={title}
      disabled={disabled}
      className={`personal-action${danger ? " danger" : ""}${primary ? " primary" : ""}`}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

function Preferences({
  values,
  setValue,
  p,
  onCustomizeSidebar,
}: PersonalProps) {
  const { locale, setLocale } = useI18n();
  return (
    <>
      <PersonalPageTitle>{p("Preferences")}</PersonalPageTitle>
      <PersonalSection title={p("General")}>
        <PersonalRow
          title={p("Language")}
          description={p("Choose the language used throughout the application")}
        >
          <PersonalSelect
            label={p("Language")}
            value={locale === "zh-CN" ? "简体中文" : "English"}
            options={localizedOptions(p, ["English", "简体中文"])}
            onChange={(value) => {
              const next = value === "简体中文" ? "zh-CN" : "en-US";
              setLocale(next);
              setValue("language", next);
            }}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Default home view")}
          description={p("Select which view to display when launching Flow")}
        >
          <PersonalSelect
            label={p("Default home view")}
            value={String(values.homeView)}
            options={localizedOptions(p, [
              "Flow Agent (default)",
              "Inbox",
              "My issues",
            ])}
            onChange={(v) => setValue("homeView", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Display names")}
          description={p(
            "Select how names are displayed in the Flow interface",
          )}
        >
          <PersonalSelect
            label={p("Display names")}
            value={String(values.displayNames)}
            options={localizedOptions(p, [
              "Full name",
              "First name",
              "Username",
            ])}
            onChange={(v) => setValue("displayNames", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("First day of the week")}
          description={p("Used for date pickers")}
        >
          <PersonalSelect
            label={p("First day of the week")}
            value={String(values.firstDay)}
            options={localizedOptions(p, ["Monday", "Saturday", "Sunday"])}
            onChange={(v) => setValue("firstDay", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Convert text emoticons into emojis")}
          description={p("Strings like :) will be converted to 🙂")}
        >
          <SettingsToggle
            label={p("Convert text emoticons into emojis")}
            checked={Boolean(values.emoticons)}
            onChange={(v) => setValue("emoticons", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Send comments on…")}
          description={p("Choose which key press is used to submit comments")}
        >
          <PersonalSelect
            label={p("Send comments on…")}
            value={String(values.sendComments)}
            options={localizedOptions(p, ["Enter", "⌘ Enter"])}
            onChange={(v) => setValue("sendComments", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={p("Interface and theme")}>
        <PersonalRow
          title={p("App sidebar")}
          description={p(
            "Customize sidebar item visibility, ordering, and badge style",
          )}
        >
          <Action onClick={onCustomizeSidebar}>{p("Customize")}</Action>
        </PersonalRow>
        <PersonalRow
          title={p("Font size")}
          description={p("Adjust the size of text across the app")}
        >
          <PersonalSelect
            label={p("Font size")}
            value={String(values.fontSize)}
            options={localizedOptions(p, ["Small", "Default", "Large"])}
            onChange={(v) => setValue("fontSize", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Use pointer cursors")}
          description={p(
            "Change the cursor to a pointer when hovering over interactive elements",
          )}
        >
          <SettingsToggle
            label={p("Use pointer cursors")}
            checked={Boolean(values.pointerCursor)}
            onChange={(v) => setValue("pointerCursor", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Underline links")}
          description={p("Always underline links in text content")}
        >
          <SettingsToggle
            label={p("Underline links")}
            checked={Boolean(values.underlineLinks)}
            onChange={(v) => setValue("underlineLinks", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Disable animated images & emoji")}
          description={p(
            "When enabled, GIFs and animated emojis remain static until hovered",
          )}
        >
          <SettingsToggle
            label={p("Disable animated images & emoji")}
            checked={Boolean(values.disableAnimatedImages)}
            onChange={(v) => setValue("disableAnimatedImages", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection>
        <PersonalRow
          title={p("Interface theme")}
          description={p("Select or customize your interface color scheme")}
        >
          <PersonalSelect
            label={p("Interface theme")}
            value={String(values.interfaceTheme)}
            options={localizedOptions(p, [
              "System preference",
              "Light",
              "Dark",
            ])}
            onChange={(v) => setValue("interfaceTheme", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={p("Desktop application")}>
        <PersonalRow
          title={p("Open in desktop app")}
          description={p(
            "Automatically open links in desktop app when possible",
          )}
        >
          <SettingsToggle
            label={p("Open in desktop app")}
            checked={Boolean(values.desktopLinks)}
            onChange={(v) => setValue("desktopLinks", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={p("Automations and workflows")}>
        <PersonalRow
          title={p("Auto-assign to self")}
          description={p(
            "When creating new issues, always assign them to yourself by default",
          )}
        >
          <SettingsToggle
            label={p("Auto-assign to self")}
            checked={Boolean(values.autoAssign)}
            onChange={(v) => setValue("autoAssign", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("On move to started status, assign to yourself")}
          description={p(
            "When you move an unassigned issue to started, it will be automatically assigned to you",
          )}
        >
          <SettingsToggle
            label={p("On move to started status, assign to yourself")}
            checked={Boolean(values.assignStarted)}
            onChange={(v) => setValue("assignStarted", v)}
          />
        </PersonalRow>
      </PersonalSection>
    </>
  );
}

function Profile({ data, onReload, onBack, p }: PersonalProps) {
  const current = data.userSettings[data.viewer.id];
  const [displayName, setDisplayName] = useState(data.viewer.displayName);
  const [username, setUsername] = useState(
    current?.username || data.viewer.name,
  );
  const [jobTitle, setJobTitle] = useState(current?.jobTitle || "");
  const [busy, setBusy] = useState(false);
  const [leaveOpen, setLeaveOpen] = useState(false);
  const dirty =
    displayName !== data.viewer.displayName ||
    username !== (current?.username || data.viewer.name) ||
    jobTitle !== (current?.jobTitle || "");
  const save = async () => {
    setBusy(true);
    try {
      await updateAccountProfile({
        displayName,
        username,
        jobTitle,
        avatarUrl: data.viewer.avatarUrl,
      });
      await onReload();
      toast.success(p("Profile updated"));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : p("Could not update profile"),
      );
    } finally {
      setBusy(false);
    }
  };
  const leave = async () => {
    setBusy(true);
    try {
      await removeMember(data.workspace.urlKey, data.viewer.id);
      setLeaveOpen(false);
      onBack();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : p("Could not leave workspace"),
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <PersonalPageTitle>{p("Profile")}</PersonalPageTitle>
      <PersonalSection>
        <PersonalRow title={p("Profile picture")}>
          <span className="personal-avatar" data-i18n-ignore>
            {initials(displayName)}
          </span>
        </PersonalRow>
        <PersonalRow title={p("Email")}>
          <span className="personal-static" data-i18n-ignore>
            {data.viewer.email}
          </span>
        </PersonalRow>
        <PersonalRow title={p("Full name")}>
          <input
            data-i18n-ignore
            className="personal-input"
            aria-label={p("Full name")}
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Title")}
          description={p("Your job title or role")}
        >
          <input
            data-i18n-ignore
            className="personal-input"
            aria-label={p("Title")}
            placeholder={p("Software engineer")}
            value={jobTitle}
            onChange={(e) => setJobTitle(e.target.value)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Username")}
          description={p("One word, like a nickname or first name")}
        >
          <input
            data-i18n-ignore
            className="personal-input"
            aria-label={p("Username")}
            placeholder={p("username")}
            value={username}
            onChange={(e) => setUsername(e.target.value.replace(/\s+/g, ""))}
          />
        </PersonalRow>
      </PersonalSection>
      {dirty && (
        <div className="personal-save-bar">
          <Action
            onClick={() => {
              setDisplayName(data.viewer.displayName);
              setUsername(current?.username || data.viewer.name);
              setJobTitle(current?.jobTitle || "");
            }}
          >
            {p("Cancel")}
          </Action>
          <Action
            primary
            disabled={busy || !displayName.trim() || !username.trim()}
            onClick={() => void save()}
          >
            {busy ? p("Saving…") : p("Update")}
          </Action>
        </div>
      )}
      <PersonalSection title={p("Workspace access")}>
        <PersonalRow title={p("Remove yourself from workspace")} danger>
          <Action danger onClick={() => setLeaveOpen(true)}>
            {p("Leave workspace")}
          </Action>
        </PersonalRow>
      </PersonalSection>
      <Dialog open={leaveOpen} onOpenChange={setLeaveOpen}>
        <DialogContent className="personal-dialog">
          <DialogTitle>
            {p("Leave workspace")}{" "}
            <span data-i18n-ignore>{data.workspace.name}</span>
            {p("?")}
          </DialogTitle>
          <p>
            {p(
              "You will lose access to this workspace. An administrator must invite you again to restore access.",
            )}
          </p>
          <footer>
            <Action onClick={() => setLeaveOpen(false)}>{p("Cancel")}</Action>
            <Action danger disabled={busy} onClick={() => void leave()}>
              {p("Leave workspace")}
            </Action>
          </footer>
        </DialogContent>
      </Dialog>
    </>
  );
}

function Notifications({ data, values, setValue, onReload, p }: PersonalProps) {
  const { formatDate } = useI18n();
  const initial = useMemo(
    () =>
      data.notificationPreferences?.[data.viewer.id] ??
      defaultNotificationPreferences(data.viewer.id),
    [data.notificationPreferences, data.viewer.id],
  );
  const [preferences, setPreferences] = useState(initial);
  const [pushSubscriptions, setPushSubscriptions] = useState(
    data.pushSubscriptions ?? [],
  );
  const [channel, setChannel] = useState<
    "desktop" | "mobile" | "email" | "slack" | null
  >(null);
  useEffect(() => setPreferences(initial), [initial]);
  useEffect(() => {
    void listPushSubscriptions()
      .then(setPushSubscriptions)
      .catch(() => undefined);
  }, []);
  const save = async (next: NotificationPreferences) => {
    const before = preferences;
    setPreferences(next);
    try {
      setPreferences(await updateNotificationPreferences(next));
      await onReload();
    } catch (error) {
      setPreferences(before);
      toast.error(
        error instanceof Error
          ? error.message
          : p("Could not save notifications"),
      );
    }
  };
  return (
    <>
      <PersonalPageTitle>{p("Notifications")}</PersonalPageTitle>
      <PersonalSection
        title={p("Push notifications")}
        description={p(
          "Choose which notifications are pushed to your devices. All notifications will still appear in your inbox.",
        )}
      >
        <NotificationChannel
          icon={<Monitor />}
          title={p("Desktop")}
          status={p(preferences.desktop.enabled ? "Enabled" : "Disabled")}
          onClick={() => setChannel("desktop")}
        />
        <NotificationChannel
          icon={<Smartphone />}
          title={p("Mobile")}
          status={p("Not available in Flow web")}
          disabled
        />
        <NotificationChannel
          icon={<Mail />}
          title={p("Email")}
          status={p(
            preferences.email.enabled
              ? "Enabled for all notifications"
              : "Disabled",
          )}
          onClick={() => setChannel("email")}
        />
        <NotificationChannel
          icon={<MessageCircle />}
          title="Slack"
          status={p("Not connected")}
          entityName
          disabled
        />
      </PersonalSection>
      <PersonalSection
        title={p("Updates from Flow")}
        description={p(
          "Subscribe to product announcements and important changes from the Flow team",
        )}
      >
        <PersonalRow
          title={p("Show updates in sidebar")}
          description={p(
            "Highlight new features and improvements in the app sidebar",
          )}
        >
          <SettingsToggle
            label={p("Show updates in sidebar")}
            checked={Boolean(values.changelogUpdates)}
            onChange={(v) => setValue("changelogUpdates", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Changelog newsletter")}
          description={p(
            "Receive an email twice a month highlighting new features and improvements",
          )}
        >
          <SettingsToggle
            label={p("Changelog newsletter")}
            checked={Boolean(values.changelogNewsletter)}
            onChange={(v) => setValue("changelogNewsletter", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={p("Marketing")}>
        <PersonalRow
          title={p("Marketing and onboarding")}
          description={p(
            "Occasional updates to help you get the most out of Flow",
          )}
        >
          <SettingsToggle
            label={p("Marketing and onboarding")}
            checked={Boolean(values.marketingUpdates)}
            onChange={(v) => setValue("marketingUpdates", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={p("Other updates")}>
        <PersonalRow
          title={p("Invite accepted")}
          description={p("Email when invitees accept an invite")}
        >
          <SettingsToggle
            label={p("Invite accepted")}
            checked={Boolean(values.inviteAcceptedUpdates)}
            onChange={(v) => setValue("inviteAcceptedUpdates", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Privacy and legal updates")}
          description={p(
            "Email when privacy policies or terms of service change",
          )}
        >
          <SettingsToggle
            label={p("Privacy and legal updates")}
            checked={Boolean(values.privacyUpdates)}
            onChange={(v) => setValue("privacyUpdates", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Data processing agreement (DPA)")}
          description={p("Email when our DPA changes")}
        >
          <SettingsToggle
            label={p("Data processing agreement")}
            checked={Boolean(values.dpaUpdates)}
            onChange={(v) => setValue("dpaUpdates", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <Dialog
        open={channel !== null}
        onOpenChange={(open) => {
          if (!open) setChannel(null);
        }}
      >
        <DialogContent className="personal-dialog notification-dialog">
          <DialogTitle>
            {channel === "email"
              ? p("Email notifications")
              : p("Desktop notifications")}
          </DialogTitle>
          {channel && channel !== "mobile" && channel !== "slack" && (
            <>
              <PersonalRow title={p(`Enable ${channel} notifications`)}>
                <SettingsToggle
                  label={p(`Enable ${channel} notifications`)}
                  checked={preferences[channel].enabled}
                  onChange={(enabled) =>
                    void save({
                      ...preferences,
                      [channel]: { ...preferences[channel], enabled },
                    })
                  }
                />
              </PersonalRow>
              {channel === "desktop" && (
                <>
                  <PersonalRow title={p("Notification sounds")}>
                    <SettingsToggle
                      label={p("Notification sounds")}
                      checked={preferences.soundEnabled}
                      onChange={(soundEnabled) =>
                        void save({ ...preferences, soundEnabled })
                      }
                    />
                  </PersonalRow>
                  {pushSubscriptions.map((item) => (
                    <PersonalRow
                      key={item.id}
                      icon={<Monitor />}
                      title={
                        <span data-i18n-ignore>
                          {item.userAgent || p("Web browser")}
                        </span>
                      }
                      description={`${p("Enabled")} ${formatDate(item.createdAt, { dateStyle: "medium" })}`}
                    >
                      <Action
                        danger
                        onClick={() =>
                          void deletePushSubscription(item.id)
                            .then(() =>
                              listPushSubscriptions().then(
                                setPushSubscriptions,
                              ),
                            )
                            .catch((error) =>
                              toast.error(
                                error instanceof Error
                                  ? error.message
                                  : p("Could not remove device"),
                              ),
                            )
                        }
                      >
                        {p("Remove")}
                      </Action>
                    </PersonalRow>
                  ))}
                  {!pushSubscriptions.length && (
                    <PersonalRow
                      title={p("No browser push devices")}
                      description={p(
                        "Browser devices appear here after notification permission is granted.",
                      )}
                    />
                  )}
                </>
              )}
              <footer>
                <Action onClick={() => setChannel(null)}>{p("Done")}</Action>
              </footer>
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

function NotificationChannel({
  icon,
  title,
  status,
  onClick,
  disabled,
  entityName,
}: {
  icon: ReactNode;
  title: string;
  status: string;
  onClick?: () => void;
  disabled?: boolean;
  entityName?: boolean;
}) {
  return (
    <button className="personal-channel" disabled={disabled} onClick={onClick}>
      <span className="personal-channel-icon">{icon}</span>
      <span>
        <strong data-i18n-ignore={entityName || undefined}>{title}</strong>
        <small>{status}</small>
      </span>
      <ChevronDown size={14} />
    </button>
  );
}

function CodeReviews({
  values,
  setValue,
  onNavigate,
  onCreateSigningKey,
  p,
}: PersonalProps) {
  return (
    <>
      <PersonalPageTitle
        description={p(
          "Review GitHub pull requests and agent code diffs in Flow",
        )}
      >
        {p("Code & reviews")}
      </PersonalPageTitle>
      <PersonalSection>
        <PersonalRow
          title={p("Enable code reviews")}
          description={p(
            "Review GitHub pull requests, accessible from the sidebar",
          )}
        >
          <SettingsToggle
            label={p("Enable code reviews")}
            checked={Boolean(values.codeReviewsEnabled)}
            onChange={(v) => setValue("codeReviewsEnabled", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Auto-convert draft pull requests")}
          description={p(
            "Automatically mark your drafts as ready upon approval or requesting a review",
          )}
        >
          <SettingsToggle
            label={p("Auto-convert draft pull requests")}
            checked={Boolean(values.autoConvertDrafts)}
            onChange={(v) => setValue("autoConvertDrafts", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Merge strategy")}
          description={p("Choose the default merge strategy for pull requests")}
        >
          <PersonalSelect
            label={p("Merge strategy")}
            value={String(values.mergeStrategy)}
            options={localizedOptions(p, [
              "Squash and merge",
              "Merge commit",
              "Rebase and merge",
            ])}
            onChange={(v) => setValue("mergeStrategy", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection>
        <PersonalRow
          title={p("Code theme")}
          description={p(
            "Select the syntax highlighting theme used in code diffs and viewers",
          )}
        >
          <PersonalSelect
            label={p("Code theme")}
            value={String(values.codeTheme)}
            options={localizedOptions(p, [
              "Flow Light",
              "Flow Dark",
              "GitHub Light",
              "GitHub Dark",
            ])}
            onChange={(v) => setValue("codeTheme", v)}
          />
        </PersonalRow>
        <PersonalRow title={p("Font")}>
          <PersonalSelect
            label={p("Code font")}
            value={String(values.codeFont)}
            options={localizedOptions(p, [
              "12px, Regular, Default",
              "13px, Regular, Default",
              "14px, Regular, Default",
            ])}
            onChange={(v) => setValue("codeFont", v)}
          />
        </PersonalRow>
        <pre className="personal-code-preview" data-i18n-ignore>
          <code>
            <span>const</span> config = {"{"}\n apiUrl:{" "}
            <i>"https://api.example.com"</i>,\n timeout: <b>5000</b>,\n debug:{" "}
            <em>true</em>\n{"}"};
          </code>
        </pre>
      </PersonalSection>
      <PersonalSection
        title={p("Notifications")}
        description={p(
          "Choose which review activity appears in your Flow inbox and push notifications",
        )}
      >
        <PersonalRow
          title={p("Comments & reviews")}
          description={p("Comments, mentions, and submitted reviews")}
        >
          <PersonalSelect
            label={p("Comments & reviews")}
            value={String(values.reviewCommentsFilter)}
            options={localizedOptions(p, [
              "All",
              "Exclude Bots",
              "Mentions only",
            ])}
            onChange={(v) => setValue("reviewCommentsFilter", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Review requests")}
          description={p("Requests for your personal review")}
        >
          <SettingsToggle
            label={p("Review requests")}
            checked={Boolean(values.reviewRequests)}
            onChange={(v) => setValue("reviewRequests", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("GitHub team review requests")}
          description={p(
            "Requests for review from your GitHub teams with 10 or fewer members",
          )}
        >
          <SettingsToggle
            label={p("GitHub team review requests")}
            checked={Boolean(values.githubTeamReviewRequests)}
            onChange={(v) => setValue("githubTeamReviewRequests", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("Checks & merge queue")}
          description={p("Check failures and merge queue updates")}
        >
          <SettingsToggle
            label={p("Checks & merge queue")}
            checked={Boolean(values.checksMergeQueue)}
            onChange={(v) => setValue("checksMergeQueue", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={p("Signed commits")}>
        <PersonalRow
          title={p("Require signed commits")}
          description={p(
            "Users must upload a signing key before starting a coding session",
          )}
        >
          <SettingsToggle
            label={p("Require signed commits")}
            checked={Boolean(values.requireSignedCommits)}
            onChange={(v) => setValue("requireSignedCommits", v)}
            disabled
          />
        </PersonalRow>
        <PersonalRow
          title={p("Commit signing key")}
          description={p("Coding sessions use this key to sign your commits")}
        >
          <Action
            onClick={() =>
              onCreateSigningKey
                ? onCreateSigningKey()
                : onNavigate("account-security")
            }
          >
            {p("Add key")}
          </Action>
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={p("External tools")}>
        <PersonalRow
          title={p("Configure coding tools")}
          description={p(
            "Configure the external coding tools you can open issues in",
          )}
        >
          <ExternalLink size={16} />
        </PersonalRow>
        <PersonalRow
          title={p("Git attachment format")}
          description={p("The format of GitHub/GitLab attachments on issues")}
        >
          <PersonalSelect
            label={p("Git attachment format")}
            value={String(values.gitAttachmentFormat)}
            options={[
              { value: "Title", label: p("Issue title") },
              { value: "URL", label: "URL", entityName: true },
              { value: "Title and URL", label: p("Title and URL") },
            ]}
            onChange={(v) => setValue("gitAttachmentFormat", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("On git branch copy, move issue to started status")}
          description={p(
            "After copying the git branch name, issue status is moved to the team’s first started workflow status.",
          )}
        >
          <SettingsToggle
            label={p("On git branch copy, move issue to started status")}
            checked={Boolean(values.gitBranchMoveStarted)}
            onChange={(v) => setValue("gitBranchMoveStarted", v)}
          />
        </PersonalRow>
        <PersonalRow
          title={p("On open in coding tool, move issue to started status")}
          description={p(
            "After opening an issue in a coding tool or copying as prompt, issue status is moved to the team’s first started workflow status.",
          )}
        >
          <SettingsToggle
            label={p("On open in coding tool, move issue to started status")}
            checked={Boolean(values.codingToolMoveStarted)}
            onChange={(v) => setValue("codingToolMoveStarted", v)}
          />
        </PersonalRow>
      </PersonalSection>
    </>
  );
}

function Security(props: PersonalProps) {
  if (props.signingKeyMode === "new") {
    return (
      <CommitSigningKeyCreatePage
        onCancel={() => props.onNavigate("account-security")}
        onReload={props.onReload}
        p={props.p}
      />
    );
  }
  if (props.apiKeyMode === "new") {
    return (
      <APIKeyCreatePage
        data={props.data}
        onCreated={
          props.onOpenAPIKey
            ? (created, secret) => {
                pendingAPIKeySecrets.set(created.id, secret);
                try {
                  window.sessionStorage.setItem(
                    `flow.api-key-secret:${created.id}`,
                    secret,
                  );
                } catch {
                  // The detail page still renders the created metadata when
                  // session storage is unavailable.
                }
                props.onOpenAPIKey?.(created);
              }
            : undefined
        }
        onCancel={() => props.onNavigate("account-security")}
        onReload={props.onReload}
        p={props.p}
      />
    );
  }
  if (props.apiKeyMode === "edit") {
    const key = (props.data.apiKeys ?? []).find(
      (item) => item.id === props.apiKeyId && !item.revokedAt,
    );
    return key ? (
      <APIKeyCreatePage
        data={props.data}
        apiKey={key}
        onCancel={() => {
          if (props.onOpenAPIKey) props.onOpenAPIKey(key);
          else props.onNavigate("account-security");
        }}
        onSaved={(updated) => {
          if (props.onOpenAPIKey) props.onOpenAPIKey(updated);
          else props.onNavigate("account-security");
        }}
        onReload={props.onReload}
        p={props.p}
      />
    ) : (
      <div className="settings-not-found">
        <strong>{props.p("API key not found")}</strong>
        <span>{props.p("This API key may have been revoked or removed")}</span>
      </div>
    );
  }
  if (props.apiKeyMode === "detail") {
    const key = (props.data.apiKeys ?? []).find(
      (item) => item.id === props.apiKeyId && !item.revokedAt,
    );
    return key ? (
      <APIKeyDetailPage
        data={props.data}
        apiKey={key}
        onBack={() => props.onNavigate("account-security")}
        onEdit={
          props.onEditAPIKey
            ? () => props.onEditAPIKey?.(key)
            : undefined
        }
        onReload={props.onReload}
        p={props.p}
      />
    ) : (
      <div className="settings-not-found">
        <strong>{props.p("API key not found")}</strong>
        <span>{props.p("This API key may have been revoked or removed")}</span>
      </div>
    );
  }
  return <SecurityOverview {...props} />;
}

function SecurityOverview({
  data,
  onNavigate,
  onCreateAPIKey,
  onCreateSigningKey,
  onOpenAPIKey,
  onLogout,
  onReload,
  p,
}: PersonalProps) {
  const { formatDate, locale } = useI18n();
  const [sessions, setSessions] = useState<
    Awaited<ReturnType<typeof fetchAccountSessions>>
  >([]);
  const [passkeys, setPasskeys] = useState<Passkey[]>([]);
  const [signingKey, setSigningKey] = useState<CommitSigningKey | null>(null);
  const [passkeyBusy, setPasskeyBusy] = useState(false);
  const [editingPasskeyId, setEditingPasskeyId] = useState<string | null>(null);
  const [passkeyNameDraft, setPasskeyNameDraft] = useState("");
  const [passkeyRevoke, setPasskeyRevoke] = useState<Passkey | null>(null);
  const [signingKeyOpen, setSigningKeyOpen] = useState(false);
  const [signingKeyName, setSigningKeyName] = useState("");
  const [signingKeyValue, setSigningKeyValue] = useState("");
  const [signingKeyBusy, setSigningKeyBusy] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [selectedSession, setSelectedSession] = useState<AccountSessionInfo | null>(null);
  const [showAllSessions, setShowAllSessions] = useState(false);
  const [sessionAction, setSessionAction] = useState<AccountSessionInfo | null>(null);
  const [sessionActionBusy, setSessionActionBusy] = useState(false);
  useEffect(() => {
    void fetchAccountSessions()
      .then(setSessions)
      .catch((error) =>
        toast.error(
          error instanceof Error
            ? error.message
            : p("Could not load security information"),
        ),
      );
  }, [p]);
  useEffect(() => {
    void fetchPasskeys()
      .then(setPasskeys)
      .catch(() => setPasskeys([]));
    void fetchCommitSigningKey()
      .then(setSigningKey)
      .catch(() => setSigningKey(null));
  }, []);
  // The account endpoint does not guarantee ordering; the settings page
  // presents the most recently active devices first, just like the native
  // security surface.
  const other = [...sessions]
    .filter((item) => !item.current)
    .sort(
      (left, right) =>
        new Date(right.lastSeenAt).getTime() -
        new Date(left.lastSeenAt).getTime(),
    );
  const visibleOther = showAllSessions ? other : other.slice(0, 5);
  const apiKeys = (data.apiKeys ?? []).filter(
    (item) => item.creatorId === data.viewer.id && !item.revokedAt,
  );
  const isWorkspaceAdmin =
    data.viewerRole === "admin" || data.viewerRole === "owner";
  const canCreateAPIKey =
    data.viewerRole !== "guest" &&
    (data.workspaceSettings?.apiKeyPermission !== "admins" || isWorkspaceAdmin);
  const apiKeyCreateDisabledReason =
    data.viewerRole === "guest"
      ? p("Guest users cannot create personal API keys")
      : p("Personal API keys are not enabled for this workspace");
  const [revealedSecret, setRevealedSecret] = useState<{
    name: string;
    secret: string;
  } | null>(null);
  const [rotatingKeyId, setRotatingKeyId] = useState<string | null>(null);
  const [revokeKey, setRevokeKey] = useState<APIKey | null>(null);
  const [revokeAuthorization, setRevokeAuthorization] =
    useState<OAuthAuthorization | null>(null);
  const revoke = async () => {
    try {
      await revokeOtherAccountSessions();
      setSessions(await fetchAccountSessions());
      setConfirmOpen(false);
      toast.success(p("Other sessions revoked"));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : p("Could not revoke sessions"),
      );
    }
  };
  const confirmSessionAction = async () => {
    const session = sessionAction;
    if (!session || sessionActionBusy) return;
    setSessionActionBusy(true);
    try {
      if (session.current) {
        setSessionAction(null);
        setSelectedSession(null);
        await (onLogout ? onLogout() : logoutAccount());
      } else {
        await revokeAccountSession(session.id);
        setSessions(await fetchAccountSessions());
        setSessionAction(null);
        setSelectedSession(null);
        toast.success(p("Session revoked"));
      }
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : p(session.current ? "Could not log out" : "Could not revoke session"),
      );
    } finally {
      setSessionActionBusy(false);
    }
  };
  const passkeyUnavailableReason = (() => {
    if (typeof window === "undefined") {
      return p("Passkey enrollment is not available on this server.");
    }
    if (
      !("PublicKeyCredential" in window) ||
      !("credentials" in navigator) ||
      typeof navigator.credentials?.create !== "function"
    ) {
      return p("Passkey enrollment is not available on this browser.");
    }
    // WebAuthn forbids an IP literal as an RP ID. Chromium treats HTTP IP
    // origins as secure contexts, so isSecureContext alone is not enough to
    // prevent a registration that the server can never verify.
    const host = window.location.hostname;
    const isIPv4 = /^(?:\d{1,3}\.){3}\d{1,3}$/.test(host);
    const isIPv6 = host.includes(":");
    if (isIPv4 || isIPv6) {
      return p(
        "Passkeys are unavailable on IP addresses; use localhost or HTTPS",
      );
    }
    if (!window.isSecureContext) {
      return p("Passkeys require a secure connection");
    }
    return undefined;
  })();
  const canUsePasskeys = !passkeyUnavailableReason;
  const registerPasskey = async () => {
    if (!canUsePasskeys || passkeyBusy) return;
    setPasskeyBusy(true);
    try {
      const started = await beginPasskeyRegistration();
      const creationOptions = toCredentialCreationOptions(started.options);
      const credential = await navigator.credentials.create({
        publicKey: creationOptions,
      });
      if (!(credential instanceof PublicKeyCredential)) {
        throw new Error(p("Passkey registration was canceled"));
      }
      const response = credential.response as AuthenticatorAttestationResponse;
      const saved = await finishPasskeyRegistration({
        registrationId: started.registrationId,
        name: "Passkey",
        credential: serializeCreationCredential(credential, response),
      });
      setPasskeys((current) => [...current, saved]);
      toast.success(p("Passkey added"));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : p("Unable to add passkey"),
      );
    } finally {
      setPasskeyBusy(false);
    }
  };
  const saveSigningKey = async () => {
    if (!signingKeyName.trim() || !signingKeyValue.trim() || signingKeyBusy)
      return;
    setSigningKeyBusy(true);
    try {
      const saved = await addCommitSigningKey({
        name: signingKeyName.trim(),
        privateKey: signingKeyValue,
      });
      setSigningKey(saved);
      setSigningKeyOpen(false);
      setSigningKeyName("");
      setSigningKeyValue("");
      toast.success(p("Signing key uploaded"));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : p("Unable to upload signing key"),
      );
    } finally {
      setSigningKeyBusy(false);
    }
  };
  return (
    <>
      <div className="personal-security-page">
      <PersonalPageTitle>{p("Security & access")}</PersonalPageTitle>
      <PersonalSection
        id="sessions"
        className="personal-security-section"
        title={p("Sessions")}
        description={p("Devices logged into your account")}
      >
        {sessions.filter((item) => item.current).length ? (
          sessions
            .filter((item) => item.current)
            .map((item) => (
            <PersonalRow
              key={item.id}
              className="personal-security-device-row"
              role="button"
              tabIndex={0}
              onClick={(event) => {
                if (event.target instanceof Element && event.target.closest("button")) return;
                setSelectedSession(item);
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  setSelectedSession(item);
                }
              }}
              icon={<BrowserSessionIcon browserType={item.browserType} />}
              title={
                <span data-i18n-ignore>
                  {item.name || p("Current session")}
                </span>
              }
              description={
                <span>
                  <span className="personal-security-current-dot" aria-hidden="true" />
                  <span className="personal-security-current-label">
                    {p("Current session")}
                  </span>
                  {item.location ? ` · ${item.location}` : ""}
                  {item.countryCodes?.length
                    ? ` · (${item.countryCodes.join(", ")})`
                    : ""}
                </span>
              }
            >
              <Action
                onClick={() => setSessionAction(item)}
              >
                {p("Log out")}
              </Action>
            </PersonalRow>
          ))
        ) : (
          <PersonalRow
            className="personal-security-empty-row"
            title={p("No sessions")}
          />
        )}
      </PersonalSection>
      {other.length > 0 && (
        <PersonalSection
          id="sessions-other"
          className="personal-security-section"
          title={`${other.length} ${p(other.length === 1 ? "other session" : "other sessions")}`}
        >
          <div className="personal-section-action">
            <Action danger onClick={() => setConfirmOpen(true)}>
              {p("Revoke all")}
            </Action>
          </div>
          {visibleOther.map((item) => (
            <PersonalRow
              key={item.id}
              className="personal-security-device-row"
              role="button"
              tabIndex={0}
              onClick={(event) => {
                if (event.target instanceof Element && event.target.closest("button")) return;
                setSelectedSession(item);
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  setSelectedSession(item);
                }
              }}
              icon={<BrowserSessionIcon browserType={item.browserType} />}
              title={
                <span data-i18n-ignore>
                  {item.name || p("Signed-in session")}
                </span>
              }
              description={`${item.location ? `${item.location} · ` : ""}${p("Last active")} ${formatDate(item.lastSeenAt, { dateStyle: "medium", timeStyle: "short" })}`}
            >
              <Action danger onClick={() => setSessionAction(item)}>
                {p("Revoke")}
              </Action>
            </PersonalRow>
          ))}
          {other.length > 5 && (
            <div className="personal-security-show-all">
              <button
                type="button"
                className="personal-action"
                onClick={() => setShowAllSessions((current) => !current)}
              >
                {p(showAllSessions ? "Show less" : "Show all")}
              </button>
            </div>
          )}
        </PersonalSection>
      )}
      <PersonalSection
        id="passkeys"
        className="personal-security-section"
        title={
          passkeys.length
            ? `${passkeys.length} ${
                locale === "zh-CN"
                  ? "个通行密钥"
                  : passkeys.length === 1
                    ? "passkey"
                    : "passkeys"
              }`
            : p("Passkeys")
        }
        description={p(
          "Passkeys are a secure way to sign in to your Flow account",
        )}
      >
        {passkeys.length === 0 ? (
          <PersonalRow
            className="personal-security-empty-row"
            title={p("No passkeys registered")}
            description={
              canUsePasskeys
                ? undefined
                : passkeyUnavailableReason
            }
          >
            <Action
              onClick={() => void registerPasskey()}
              disabled={!canUsePasskeys || passkeyBusy}
              title={passkeyUnavailableReason}
            >
              {p("New passkey")}
            </Action>
          </PersonalRow>
        ) : (
          <>
            {passkeys.map((item) =>
              editingPasskeyId === item.id ? (
                <PersonalRow
                  key={item.id}
                  className="personal-security-api-key-row"
                  icon={<KeyRound />}
                  title={
                    <input
                      className="personal-inline-input"
                      aria-label={p("Passkey name")}
                      maxLength={64}
                      value={passkeyNameDraft}
                      onChange={(event) => setPasskeyNameDraft(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === "Escape") setEditingPasskeyId(null);
                        if (event.key === "Enter") {
                          event.preventDefault();
                          void updatePasskey(item.id, passkeyNameDraft.trim() || item.name)
                            .then((updated) => {
                              setPasskeys((current) => current.map((key) => key.id === updated.id ? updated : key));
                              setEditingPasskeyId(null);
                            })
                            .catch((error) => toast.error(error instanceof Error ? error.message : p("Could not rename passkey")));
                        }
                      }}
                      autoFocus
                    />
                  }
                  description={p("Press Enter to save or Escape to cancel")}
                >
                  <Action onClick={() => setEditingPasskeyId(null)}>{p("Cancel")}</Action>
                </PersonalRow>
              ) : (
                <PersonalRow
                  key={item.id}
                  className="personal-security-api-key-row"
                  icon={<KeyRound />}
                  title={<span data-i18n-ignore>{item.name}</span>}
                  description={
                    item.lastUsedAt
                      ? `${p("Last used")} ${formatSecurityRelativeDate(
                          item.lastUsedAt,
                          locale,
                        )}`
                      : p("Never used")
                  }
                >
                  <span className="personal-security-key-actions">
                    <Action
                      onClick={() => {
                        setEditingPasskeyId(item.id);
                        setPasskeyNameDraft(item.name);
                      }}
                    >
                      {p("Rename")}
                    </Action>
                    <Action danger onClick={() => setPasskeyRevoke(item)}>
                      {p("Revoke")}
                    </Action>
                  </span>
                </PersonalRow>
              ),
            )}
            <div className="personal-security-section-action">
              <Action onClick={() => void registerPasskey()} disabled={!canUsePasskeys || passkeyBusy} title={passkeyUnavailableReason}>
                {p("New passkey")}
              </Action>
            </div>
          </>
        )}
      </PersonalSection>
      <PersonalSection
        id="personal-api-keys"
        className="personal-security-section"
        title={
          apiKeys.length
            ? `${apiKeys.length} ${
                locale === "zh-CN"
                  ? "个 API 密钥"
                  : apiKeys.length === 1
                    ? "API key"
                    : "API keys"
              }`
            : p("Personal API keys")
        }
        description={p("Use Flow’s API to build your own integrations")}
      >
        {apiKeys.map((item) => (
          <PersonalRow
            key={item.id}
            className="personal-security-api-key-row"
            role="button"
            tabIndex={0}
            onClick={(event) => {
              if (event.target instanceof Element && event.target.closest("button")) return;
              onOpenAPIKey?.(item);
            }}
            onKeyDown={(event) => {
              if ((event.key === "Enter" || event.key === " ") && onOpenAPIKey) {
                event.preventDefault();
                onOpenAPIKey(item);
              }
            }}
            icon={<KeyRound />}
            title={<span data-i18n-ignore>{item.name}</span>}
            description={
              <APIKeyMetadata
                apiKey={item}
                data={data}
                formatDate={formatDate}
                locale={locale}
                p={p}
              />
            }
          >
            <span className="personal-security-key-actions">
              <Action
                disabled={rotatingKeyId === item.id}
                onClick={() => {
                  setRotatingKeyId(item.id);
                  void rotateAPIKey(item.id)
                    .then(async (result) => {
                      setRevealedSecret({ name: item.name, secret: result.secret });
                      await onReload().catch(() => undefined);
                    })
                    .catch((error) => toast.error(error instanceof Error ? error.message : p("Could not rotate API key")))
                    .finally(() => setRotatingKeyId(null));
                }}
              >
                {p("Rotate")}
              </Action>
              <Action
                danger
                onClick={() => setRevokeKey(item)}
              >
                {p("Revoke")}
              </Action>
            </span>
          </PersonalRow>
        ))}
        {!apiKeys.length && (
          <PersonalRow
            className="personal-security-empty-row"
            title={p("No API keys created")}
          >
            <Action
              disabled={!canCreateAPIKey}
              title={!canCreateAPIKey ? apiKeyCreateDisabledReason : undefined}
              onClick={() =>
                onCreateAPIKey ? onCreateAPIKey() : onNavigate("api")
              }
            >
              {p("New API key")}
            </Action>
          </PersonalRow>
        )}
        {apiKeys.length > 0 && (
          <div className="personal-security-section-action">
            <Action
              disabled={!canCreateAPIKey}
              title={!canCreateAPIKey ? apiKeyCreateDisabledReason : undefined}
              onClick={() =>
                onCreateAPIKey ? onCreateAPIKey() : onNavigate("api")
              }
            >
              {p("New API key")}
            </Action>
          </div>
        )}
      </PersonalSection>
      <PersonalSection
        id="commit-signing-key"
        className="personal-security-section"
        title={p("Commit signing key")}
        description={p("Coding sessions use this key to sign your commits")}
      >
        <PersonalRow
          className="personal-security-empty-row"
          title={
            signingKey ? (
              <span data-i18n-ignore>{signingKey.name}</span>
            ) : (
              p("No signing key added")
            )
          }
          description={
            signingKey
              ? `${signingKey.type.toUpperCase()} · ${signingKey.fingerprint}`
              : undefined
          }
        >
          {signingKey ? (
            <Action
              danger
              onClick={() =>
                void removeCommitSigningKey()
                  .then(() => setSigningKey(null))
                  .then(() => toast.success(p("Signing key removed")))
                  .catch((error) =>
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : p("Unable to remove signing key"),
                    ),
                  )
              }
            >
              {p("Remove")}
            </Action>
          ) : (
            <Action
              onClick={() =>
                onCreateSigningKey
                  ? onCreateSigningKey()
                  : setSigningKeyOpen(true)
              }
            >
              {p("Add key")}
            </Action>
          )}
        </PersonalRow>
      </PersonalSection>
      <PersonalSection
        id="authorized-applications"
        className="personal-security-section"
        title={p("Authorized applications")}
        description={p("OAuth applications you’ve approved")}
      >
        {data.oauthAuthorizations
          .filter((item) => item.userId === data.viewer.id && !item.revokedAt)
          .map((item) => (
            <PersonalRow
              key={item.id}
              icon={<ShieldCheck />}
              title={<span data-i18n-ignore>{item.clientName}</span>}
              description={
                <span
                  data-i18n-ignore
                >{`MCP · ${item.scopes.join(", ")}`}</span>
              }
            >
              <Action
                danger
                onClick={() => setRevokeAuthorization(item)}
              >
                {p("Revoke")}
              </Action>
            </PersonalRow>
          ))}
        {!data.oauthAuthorizations.some(
          (item) => item.userId === data.viewer.id && !item.revokedAt,
        ) && (
          <PersonalRow
            className="personal-security-empty-row"
            title={p("No authorized applications")}
          />
        )}
      </PersonalSection>
      <Dialog
        open={Boolean(selectedSession)}
        onOpenChange={(open) => !open && setSelectedSession(null)}
      >
        <DialogContent className="personal-dialog personal-session-dialog">
          {selectedSession && (
            <>
              <header className="personal-session-dialog-header">
                <span className="personal-session-dialog-icon">
                  <BrowserSessionIcon browserType={selectedSession.browserType} />
                </span>
                <div>
                  <DialogTitle>
                    {selectedSession.name || p("Signed-in session")}
                  </DialogTitle>
                  <p>
                    {selectedSession.current
                      ? p("Current session")
                      : p("Signed-in session")}
                  </p>
                </div>
              </header>
              <dl className="personal-session-details">
                <div>
                  <dt>{p("Device")}</dt>
                  <dd>
                    {selectedSession.name ||
                      [selectedSession.browserType, selectedSession.operatingSystem]
                        .filter(Boolean)
                        .join(" on ") ||
                      p("Unknown")}
                  </dd>
                </div>
                <div>
                  <dt>{p("IP address")}</dt>
                  <dd>{selectedSession.ip || p("Unknown")}</dd>
                </div>
                <div>
                  <dt>{p("Last location")}</dt>
                  <dd>{selectedSession.location || p("Unknown")}</dd>
                </div>
                <div>
                  <dt>{p("Original sign in")}</dt>
                  <dd>
                    {formatDate(selectedSession.createdAt, {
                      dateStyle: "medium",
                      timeStyle: "short",
                    })}
                  </dd>
                </div>
              </dl>
              <footer>
                <Action
                  danger
                  disabled={sessionActionBusy}
                  onClick={() => {
                    setSessionAction(selectedSession);
                    setSelectedSession(null);
                  }}
                >
                  {selectedSession.current ? p("Log out") : p("Revoke access")}
                </Action>
              </footer>
            </>
          )}
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(sessionAction)}
        onOpenChange={(open) => !open && setSessionAction(null)}
      >
        <DialogContent className="personal-dialog">
          <DialogTitle>
            {sessionAction?.current ? p("Log out?") : p("Revoke access")}
          </DialogTitle>
          <p>
            {sessionAction?.current
              ? p("You will be logged out from this session")
              : p("This device will no longer be able to access your account")}
          </p>
          <footer>
            <Action
              disabled={sessionActionBusy}
              onClick={() => setSessionAction(null)}
            >
              {p("Cancel")}
            </Action>
            <Action
              danger
              disabled={sessionActionBusy}
              onClick={() => void confirmSessionAction()}
            >
              {sessionAction?.current ? p("Log out") : p("Revoke")}
            </Action>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent className="personal-dialog">
          <DialogTitle>{p("Revoke all other sessions?")}</DialogTitle>
          <p>{p("Every other device will need to sign in again.")}</p>
          <footer>
            <Action onClick={() => setConfirmOpen(false)}>{p("Cancel")}</Action>
            <Action danger onClick={() => void revoke()}>
              {p("Revoke all")}
            </Action>
          </footer>
        </DialogContent>
      </Dialog>
      <APIKeySecretDialog
        secret={revealedSecret}
        onClose={() => setRevealedSecret(null)}
        p={p}
      />
      <Dialog open={Boolean(revokeKey)} onOpenChange={(open) => !open && setRevokeKey(null)}>
        <DialogContent className="personal-dialog">
          <DialogTitle>
            {p("Revoke API key?")}
          </DialogTitle>
          <p>{p("Applications using this key will no longer access Flow data.")}</p>
          <footer>
            <Action onClick={() => setRevokeKey(null)}>{p("Cancel")}</Action>
            <Action
              danger
              onClick={() => {
                if (!revokeKey) return;
                const id = revokeKey.id;
                void revokeAPIKey(id)
                  .then(onReload)
                  .then(() => setRevokeKey(null))
                  .catch((error) =>
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : p("Could not revoke API key"),
                    ),
                  );
              }}
            >
              {p("Revoke")}
            </Action>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(revokeAuthorization)}
        onOpenChange={(open) => !open && setRevokeAuthorization(null)}
      >
        <DialogContent className="personal-dialog">
          <DialogTitle>{p("Revoke authorized application?")}</DialogTitle>
          <p>{p("This application will no longer access your Flow account.")}</p>
          <footer>
            <Action onClick={() => setRevokeAuthorization(null)}>
              {p("Cancel")}
            </Action>
            <Action
              danger
              onClick={() => {
                if (!revokeAuthorization) return;
                const id = revokeAuthorization.id;
                void revokeOAuthAuthorization(id)
                  .then(onReload)
                  .then(() => setRevokeAuthorization(null))
                  .catch((error) =>
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : p("Could not revoke authorized application"),
                    ),
                  );
              }}
            >
              {p("Revoke")}
            </Action>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(passkeyRevoke)}
        onOpenChange={(open) => !open && setPasskeyRevoke(null)}
      >
        <DialogContent className="personal-dialog">
          <DialogTitle>{p("Revoke passkey?")}</DialogTitle>
          <p>{p("This passkey will no longer be able to sign in to Flow.")}</p>
          <footer>
            <Action onClick={() => setPasskeyRevoke(null)}>{p("Cancel")}</Action>
            <Action
              danger
              onClick={() => {
                if (!passkeyRevoke) return;
                const id = passkeyRevoke.id;
                void deletePasskey(id)
                  .then(() => setPasskeys((current) => current.filter((item) => item.id !== id)))
                  .then(() => setPasskeyRevoke(null))
                  .catch((error) =>
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : p("Unable to revoke passkey"),
                    ),
                  );
              }}
            >
              {p("Revoke")}
            </Action>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog open={signingKeyOpen} onOpenChange={setSigningKeyOpen}>
        <DialogContent className="personal-dialog personal-signing-key-dialog">
          <DialogTitle>{p("Add commit signing key")}</DialogTitle>
          <p>{p("Paste an unencrypted SSH or PGP private key. The private material is validated and never stored.")}</p>
          <label>
            {p("Key name")}
            <input
              className="personal-input"
              value={signingKeyName}
              onChange={(event) => setSigningKeyName(event.target.value)}
              placeholder={p("A descriptive name for this key…")}
              autoFocus
            />
          </label>
          <label>
            {p("Private key")}
            <textarea
              className="personal-signing-key-input"
              value={signingKeyValue}
              onChange={(event) => setSigningKeyValue(event.target.value)}
              placeholder={p("Paste your private key here…")}
              spellCheck={false}
              onDragOver={(event) => event.preventDefault()}
              onDrop={(event) => {
                event.preventDefault();
                const file = event.dataTransfer.files[0];
                if (file) void file.text().then(setSigningKeyValue);
              }}
            />
            <small className="personal-signing-key-help">
              {p("You can also drop a key file here")}
            </small>
          </label>
          <footer>
            <Action onClick={() => setSigningKeyOpen(false)} disabled={signingKeyBusy}>
              {p("Cancel")}
            </Action>
            <Action
              primary
              disabled={signingKeyBusy || !signingKeyName.trim() || !signingKeyValue.trim()}
              onClick={() => void saveSigningKey()}
            >
              {p("Upload key")}
            </Action>
          </footer>
        </DialogContent>
      </Dialog>
      </div>
    </>
  );
}

type CommitSigningKeyCreateProps = {
  onCancel: () => void;
  onReload: () => Promise<void>;
  p: PersonalTranslate;
};

/**
 * The signing-key editor is a real settings route, not a confirmation modal.
 * Keeping the upload state local means a private key is never placed in
 * bootstrap data, browser history, or a React prop owned by the overview.
 */
function CommitSigningKeyCreatePage({
  onCancel,
  onReload,
  p,
}: CommitSigningKeyCreateProps) {
  const [name, setName] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [dragActive, setDragActive] = useState(false);
  const [error, setError] = useState("");
  const cancelRef = useRef(onCancel);
  cancelRef.current = onCancel;

  // Match the native flow: opening the add route while a key already exists
  // returns to the security inventory instead of allowing a second key.
  useEffect(() => {
    let mounted = true;
    void fetchCommitSigningKey()
      .then((existing) => {
        if (!mounted) return;
        if (existing) {
          cancelRef.current();
          return;
        }
        setLoading(false);
      })
      .catch((requestError) => {
        if (!mounted) return;
        setLoading(false);
        setError(
          requestError instanceof Error
            ? requestError.message
            : p("Unable to read signing key"),
        );
      });
    return () => {
      mounted = false;
    };
  }, [p]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) {
        event.preventDefault();
        cancelRef.current();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [busy]);

  const readKeyFile = useCallback(
    async (file: File) => {
      try {
        const contents = await file.text();
        setPrivateKey(contents);
        setName((current) =>
          current.trim() ? current : keyNameFromFilename(file.name),
        );
        setError("");
      } catch (requestError) {
        setError(
          requestError instanceof Error
            ? requestError.message
            : p("Unable to read signing key"),
        );
      }
    },
    [p],
  );

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy || loading) return;
    const trimmedName = name.trim();
    const trimmedKey = privateKey.trim();
    if (!trimmedName) {
      setError(p("Key name is required"));
      return;
    }
    if (!trimmedKey) {
      setError(p("Private key is required"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      await addCommitSigningKey({ name: trimmedName, privateKey: trimmedKey });
      toast.success(p("Signing key uploaded"));
      await onReload().catch(() => undefined);
      cancelRef.current();
    } catch (requestError) {
      setError(
        requestError instanceof Error
          ? requestError.message
          : p("Unable to upload signing key"),
      );
    } finally {
      setBusy(false);
    }
  };

  const disabled = loading || busy;
  return (
    <div className="personal-signing-key-page">
      <nav
        className="personal-signing-key-breadcrumb"
        aria-label={p("Breadcrumb")}
      >
        <button type="button" onClick={onCancel} disabled={busy}>
          <ChevronDown aria-hidden="true" />
          {p("Security settings")}
        </button>
      </nav>
      <header className="personal-signing-key-header">
        <h1>{p("Add commit signing key")}</h1>
        <p>{p("Coding sessions use this key to sign your commits")}</p>
      </header>
      <section className="personal-signing-key-card">
        <form onSubmit={submit}>
          <label className="personal-signing-key-field">
            <span>{p("Key name")}</span>
            <input
              aria-label={p("Key name")}
              autoComplete="off"
              autoFocus
              disabled={disabled}
              maxLength={255}
              onChange={(event) => {
                setName(event.target.value);
                if (error) setError("");
              }}
              placeholder={p("A descriptive name for this key…")}
              value={name}
            />
          </label>
          <label
            className={`personal-signing-key-field personal-signing-key-drop${dragActive ? " is-drag-active" : ""}`}
            onDragEnter={(event) => {
              event.preventDefault();
              setDragActive(true);
            }}
            onDragOver={(event) => event.preventDefault()}
            onDragLeave={(event) => {
              if (event.currentTarget === event.target) setDragActive(false);
            }}
            onDrop={(event) => {
              event.preventDefault();
              setDragActive(false);
              const file = event.dataTransfer.files[0];
              if (file) void readKeyFile(file);
            }}
          >
            <span>{p("Private key")}</span>
            <textarea
              aria-label={p("Private key")}
              disabled={disabled}
              onChange={(event) => {
                setPrivateKey(event.target.value);
                if (error) setError("");
              }}
              placeholder={p("Paste your private key here…")}
              rows={8}
              spellCheck={false}
              value={privateKey}
            />
            <small>
              {p(
                "Paste an unencrypted SSH or GPG private key or drop a key file here",
              )}
            </small>
          </label>
          {error && (
            <p className="personal-signing-key-error" role="alert">
              {error}
            </p>
          )}
          <footer>
            <button
              className="personal-signing-key-button"
              type="button"
              onClick={onCancel}
              disabled={busy}
            >
              {p("Cancel")}
            </button>
            <button
              className="personal-signing-key-button primary"
              type="submit"
              disabled={disabled || !name.trim() || !privateKey.trim()}
            >
              {p("Upload key")}
            </button>
          </footer>
        </form>
      </section>
    </div>
  );
}

function keyNameFromFilename(filename: string) {
  const basename = filename.trim().split(/[\\/]/).pop() ?? "";
  return basename.replace(/\.(pem|key|txt|asc|gpg|pgp)$/i, "");
}

type APIKeyCreateProps = {
  data: BootstrapData;
  apiKey?: APIKey;
  onCancel: () => void;
  onCreated?: (key: APIKey, secret: string) => void;
  onSaved?: (key: APIKey) => void;
  onReload: () => Promise<void>;
  p: PersonalTranslate;
};

const API_KEY_SCOPES = [
  {
    value: "read",
    label: "Read",
    description: "Read all workspace data available to you",
  },
  {
    value: "write",
    label: "Write",
    description: "Read and write all workspace data available to you",
  },
  {
    value: "create_issues",
    label: "Create issues",
    description: "Create and update issues",
  },
  {
    value: "create_comments",
    label: "Create comments",
    description: "Create issue comments",
  },
  {
    value: "admin",
    label: "Admin",
    description: "Access admin-only API features",
  },
] as const;

function APIKeyCreatePage({
  data,
  apiKey,
  onCancel,
  onCreated,
  onSaved,
  onReload,
  p,
}: APIKeyCreateProps) {
  const editing = Boolean(apiKey);
  // Preserve the server's tri-state policy: null means unrestricted, while
  // an explicit [] means deny-all. Treat an omitted value like null for
  // compatibility with older in-memory fixtures.
  const initialScopes = apiKey?.scopes;
  const initialTeamIds = apiKey?.teamIds ?? [];
  const [name, setName] = useState(apiKey?.name ?? "");
  const [permissionMode, setPermissionMode] = useState<
    "fullAccess" | "selectScope"
  >(apiKey && initialScopes !== null && initialScopes !== undefined
    ? "selectScope"
    : "fullAccess");
  const [selectedScopes, setSelectedScopes] = useState<string[]>(
    initialScopes ?? [],
  );
  const [teamMode, setTeamMode] = useState<"all" | "selectTeams">(
    apiKey?.teamRestriction === "selected" || initialTeamIds.length
      ? "selectTeams"
      : "all",
  );
  const [selectedTeams, setSelectedTeams] = useState<string[]>(initialTeamIds);
  const [teamQuery, setTeamQuery] = useState("");
  const [teamMenuOpen, setTeamMenuOpen] = useState(false);
  const [teamActiveIndex, setTeamActiveIndex] = useState(0);
  const teamPickerRef = useRef<HTMLDivElement | null>(null);
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const teamOptions = useMemo(
    () =>
      data.teams.filter((team) =>
        team.name.toLowerCase().includes(teamQuery.trim().toLowerCase()),
      ),
    [data.teams, teamQuery],
  );
  const isAdmin = data.viewerRole === "admin" || data.viewerRole === "owner";
  const availableScopes = isAdmin
    ? API_KEY_SCOPES
    : API_KEY_SCOPES.filter((scope) => scope.value !== "admin");
  const scopeValues =
    permissionMode === "fullAccess"
      ? []
      : selectedScopes;
  const selectedTeamRecords = data.teams.filter((team) =>
    selectedTeams.includes(team.id),
  );
  useEffect(() => {
    setTeamActiveIndex((current) =>
      teamOptions.length ? Math.min(current, teamOptions.length - 1) : 0,
    );
  }, [teamOptions.length]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        if (teamMenuOpen) {
          setTeamMenuOpen(false);
          return;
        }
        onCancel();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onCancel, teamMenuOpen]);

  useEffect(() => {
    if (!teamMenuOpen) return;
    const handlePointerDown = (event: PointerEvent) => {
      if (
        event.target instanceof Node &&
        !teamPickerRef.current?.contains(event.target)
      ) {
        setTeamMenuOpen(false);
        setTeamQuery("");
      }
    };
    document.addEventListener("pointerdown", handlePointerDown);
    return () => document.removeEventListener("pointerdown", handlePointerDown);
  }, [teamMenuOpen]);

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy || secret) return;
    const trimmedName = name.trim();
    if (!trimmedName) {
      setError(p("Key name is required"));
      return;
    }
    if (trimmedName.length < 2) {
      setError(p("Key name must be at least 2 characters"));
      return;
    }
    if (
      (data.apiKeys ?? []).some(
        (item) =>
          item.id !== apiKey?.id &&
          !item.revokedAt &&
          item.creatorId === data.viewer.id &&
          item.name.trim().toLowerCase() === trimmedName.toLowerCase(),
      )
    ) {
      setError(p("An API key with this name already exists"));
      return;
    }
    if (permissionMode === "selectScope" && !scopeValues.length) {
      setError(p("Select at least one permission"));
      return;
    }
    if (teamMode === "selectTeams" && !selectedTeams.length) {
      setError(p("Select at least one team"));
      return;
    }
    setError("");
    setBusy(true);
    try {
      if (apiKey) {
        const updated = await updateAPIKey(apiKey.id, {
          name: trimmedName,
          scopes: permissionMode === "fullAccess" ? null : scopeValues,
          teamIds: teamMode === "selectTeams" ? selectedTeams : null,
          teamRestriction: teamMode === "selectTeams" ? "selected" : "all",
        });
        await onReload().catch(() => undefined);
        toast.success(p("API key updated"));
        onSaved?.(updated);
        return;
      }
      const result = await createAPIKey({
        name: trimmedName,
        // The public API represents full access with an omitted/null scope;
        // retaining that distinction keeps the detail view and authorization
        // behavior aligned with the account security flow.
        scopes: permissionMode === "fullAccess" ? undefined : scopeValues,
        teamIds: teamMode === "selectTeams" ? selectedTeams : [],
        teamRestriction: teamMode === "selectTeams" ? "selected" : "all",
      });
      try {
        await navigator.clipboard?.writeText(result.secret);
        toast.success(p("Personal API key copied to clipboard"));
      } catch {
        // Clipboard permissions are optional; the one-time value remains
        // visible in the confirmation state so it can be copied manually.
      }
      await onReload().catch(() => undefined);
      if (onCreated) onCreated(result.key, result.secret);
      else setSecret(result.secret);
    } catch (requestError) {
      setError(
        apiKeyErrorMessage(
          requestError,
          p,
          editing ? p("Could not update API key") : p("Could not create API key"),
        ),
      );
    } finally {
      setBusy(false);
    }
  };

  const copySecret = async () => {
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret);
      toast.success(p("Copied"));
    } catch {
      setError(p("Could not copy API key"));
    }
  };

  return (
    <div className="personal-api-key-page">
      <nav className="personal-api-key-breadcrumb" aria-label={p("Breadcrumb")}>
        <button type="button" onClick={onCancel}>
          <ChevronDown aria-hidden="true" />
          {p("Security settings")}
        </button>
      </nav>
      <header className="personal-api-key-header">
        <h1>
          {secret
            ? p("API key created")
            : editing
              ? p("Edit API key")
              : p("Create API key")}
        </h1>
        <p>
          {secret
            ? p("This secret is shown once. Store it somewhere safe before leaving this page.")
            : p("When using the API key all actions are attributed to you as an individual")}
        </p>
      </header>
      <section className="personal-api-key-card">
        {secret ? (
          <div className="personal-api-key-secret">
            <div className="personal-api-key-secret-warning" role="status">
              <CircleAlert aria-hidden="true" />
              <span>{p("Copy this key now. It will not be shown again.")}</span>
            </div>
            <label>
              {p("API key")}
              <div className="personal-api-key-secret-input">
                <input readOnly value={secret} aria-label={p("API key")} />
                <button type="button" onClick={() => void copySecret()} aria-label={p("Copy") }>
                  <Clipboard aria-hidden="true" />
                </button>
              </div>
            </label>
            <footer>
              <button type="button" className="personal-api-key-button primary" onClick={onCancel}>
                {p("Done")}
              </button>
            </footer>
          </div>
        ) : (
          <form onSubmit={(event) => void submit(event)}>
            <label className="personal-api-key-field">
              <span>{p("Key name")}</span>
              <input
                id="label"
                autoFocus
                autoComplete="off"
                maxLength={40}
                value={name}
                onChange={(event) => {
                  setName(event.target.value);
                  if (error) setError("");
                }}
                placeholder={p("A descriptive name for this API key…")}
                aria-label={p("Key name")}
              />
            </label>
            <fieldset className="personal-api-key-fieldset">
              <legend>{p("Permissions")}</legend>
              <p>{p("Only enable the minimum permissions required for your use case")}</p>
              <label className="personal-api-key-radio">
                <input
                  type="radio"
                  name="api-key-permission-mode"
                  value="fullAccess"
                  checked={permissionMode === "fullAccess"}
                  onChange={() => {
                    setPermissionMode("fullAccess");
                    setSelectedScopes([]);
                  }}
                />
                <span>{p("Full access")}</span>
              </label>
              <label className="personal-api-key-radio">
                <input
                  type="radio"
                  name="api-key-permission-mode"
                  value="selectScope"
                  checked={permissionMode === "selectScope"}
                  onChange={() => setPermissionMode("selectScope")}
                />
                <span>{p("Only select permissions…")}</span>
              </label>
              {permissionMode === "selectScope" && (
                <div className="personal-api-key-scope-list">
                  {availableScopes.map((scope) => (
                    <label className="personal-api-key-scope" key={scope.value}>
                      <input
                        id={`scope-${scope.value === "create_issues" ? "issues:create" : scope.value === "create_comments" ? "comments:create" : scope.value}`}
                        type="checkbox"
                        checked={
                          selectedScopes.includes(scope.value) ||
                          (selectedScopes.includes("write") &&
                            (scope.value === "create_issues" ||
                              scope.value === "create_comments"))
                        }
                        disabled={
                          selectedScopes.includes("write") &&
                            (scope.value === "create_issues" ||
                              scope.value === "create_comments")
                        }
                        onChange={(event) => {
                          if (scope.value === "write") {
                            setSelectedScopes((current) =>
                              event.target.checked
                                ? [
                                    ...current.filter(
                                      (value) =>
                                        value !== "create_issues" &&
                                        value !== "create_comments",
                                    ),
                                    "write",
                                  ]
                                : current.filter(
                                    (value) =>
                                      value !== "write" &&
                                      value !== "create_issues" &&
                                      value !== "create_comments",
                                  ),
                            );
                            return;
                          }
                          setSelectedScopes((current) =>
                            event.target.checked
                              ? [...current, scope.value]
                              : current.filter((value) => value !== scope.value),
                          );
                        }}
                      />
                      <strong>{p(scope.label)}</strong>
                      <small>{p(scope.description)}</small>
                    </label>
                  ))}
                </div>
              )}
            </fieldset>
            <fieldset className="personal-api-key-fieldset">
              <legend>{p("Team access")}</legend>
              <p>{p("Set limits around which teams can be accessed via this API key")}</p>
              <label className="personal-api-key-radio">
                <input
                  type="radio"
                  name="api-key-team-mode"
                  value="all"
                  checked={teamMode === "all"}
                  onChange={() => {
                    setTeamMode("all");
                    setSelectedTeams([]);
                    setTeamMenuOpen(false);
                  }}
                />
                <span>{p("All teams you have access to")}</span>
              </label>
              <label className="personal-api-key-radio">
                <input
                  type="radio"
                  name="api-key-team-mode"
                  value="selectTeams"
                  checked={teamMode === "selectTeams"}
                  onChange={() => setTeamMode("selectTeams")}
                />
                <span>{p("Only select teams…")}</span>
              </label>
              {teamMode === "selectTeams" && (
                <div
                  className="personal-api-key-team-picker"
                  ref={teamPickerRef}
                >
                  <div className="personal-api-key-team-control">
                    {selectedTeamRecords.map((team) => (
                      <span className="personal-api-key-team-chip" key={team.id}>
                        <span data-i18n-ignore>{team.name}</span>
                        <button
                          type="button"
                          aria-label={`${p("Remove team")} ${team.name}`}
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() =>
                            setSelectedTeams((current) =>
                              current.filter((id) => id !== team.id),
                            )
                          }
                        >
                          <span aria-hidden="true">×</span>
                        </button>
                      </span>
                    ))}
                    <input
                      id="team"
                      role="combobox"
                      aria-expanded={teamMenuOpen}
                      aria-controls="api-key-team-options"
                      aria-label={p("Select teams…")}
                      placeholder={p("Select teams…")}
                      value={teamQuery}
                      onFocus={() => {
                        setTeamMenuOpen(true);
                        setTeamActiveIndex(0);
                      }}
                      aria-activedescendant={
                        teamMenuOpen && teamOptions[teamActiveIndex]
                          ? `api-key-team-${teamOptions[teamActiveIndex].id}`
                          : undefined
                      }
                      onKeyDown={(event) => {
                        if (event.key === "ArrowDown") {
                          event.preventDefault();
                          if (teamMenuOpen && teamOptions.length) {
                            setTeamActiveIndex((current) =>
                              Math.min(current + 1, teamOptions.length - 1),
                            );
                          }
                          setTeamMenuOpen(true);
                        } else if (event.key === "ArrowUp") {
                          event.preventDefault();
                          if (teamMenuOpen && teamOptions.length) {
                            setTeamActiveIndex((current) => Math.max(current - 1, 0));
                          }
                          setTeamMenuOpen(true);
                        } else if (event.key === "Home") {
                          event.preventDefault();
                          setTeamActiveIndex(0);
                        } else if (event.key === "End") {
                          event.preventDefault();
                          setTeamActiveIndex(Math.max(teamOptions.length - 1, 0));
                        } else if (
                          event.key === "Enter" &&
                          teamMenuOpen &&
                          teamOptions[teamActiveIndex]
                        ) {
                          event.preventDefault();
                          const first = teamOptions[teamActiveIndex];
                          setSelectedTeams((current) =>
                            current.includes(first.id)
                              ? current.filter((id) => id !== first.id)
                              : [...current, first.id],
                          );
                          setTeamQuery("");
                        } else if (
                          event.key === "Backspace" &&
                          !teamQuery &&
                          selectedTeams.length
                        ) {
                          setSelectedTeams((current) => current.slice(0, -1));
                        } else if (event.key === "Escape") {
                          event.preventDefault();
                          setTeamMenuOpen(false);
                          setTeamQuery("");
                        }
                      }}
                      onChange={(event) => {
                        setTeamQuery(event.target.value);
                        setTeamMenuOpen(true);
                      }}
                    />
                    <button
                      type="button"
                      aria-label={p("Toggle menu")}
                      onClick={() => setTeamMenuOpen((open) => !open)}
                    >
                      <ChevronDown aria-hidden="true" />
                    </button>
                  </div>
                  {teamMenuOpen && (
                    <div
                      id="api-key-team-options"
                      className="personal-api-key-team-menu"
                      role="listbox"
                    >
                      {teamOptions.length ? (
                        teamOptions.map((team, index) => {
                          const checked = selectedTeams.includes(team.id);
                          return (
                            <button
                              type="button"
                              role="option"
                              id={`api-key-team-${team.id}`}
                              aria-selected={checked}
                              data-highlighted={index === teamActiveIndex || undefined}
                              key={team.id}
                              onMouseDown={(event) => event.preventDefault()}
                              onPointerMove={() => setTeamActiveIndex(index)}
                              onClick={() => {
                                setSelectedTeams((current) =>
                                  checked
                                    ? current.filter((id) => id !== team.id)
                                    : [...current, team.id],
                                );
                                setTeamQuery("");
                              }}
                            >
                              <span>{team.name}</span>
                              {checked && <Check aria-hidden="true" />}
                            </button>
                          );
                        })
                      ) : (
                        <span className="personal-api-key-team-empty">{p("No teams found")}</span>
                      )}
                    </div>
                  )}
                </div>
              )}
            </fieldset>
            {error && (
              <p className="personal-api-key-error" role="alert">
                {error}
              </p>
            )}
            <footer className="personal-api-key-footer">
              <button type="button" className="personal-api-key-button" onClick={onCancel}>
                {p("Cancel")}
              </button>
              <button
                type="submit"
                className="personal-api-key-button primary"
                disabled={busy}
                aria-busy={busy}
              >
                {busy && <LoaderCircle aria-hidden="true" />}
                {editing ? p("Save") : p("Create")}
              </button>
            </footer>
          </form>
        )}
      </section>
    </div>
  );
}

function APIKeySecretDialog({
  secret,
  onClose,
  p,
}: {
  secret: { name: string; secret: string } | null;
  onClose: () => void;
  p: PersonalTranslate;
}) {
  const [copied, setCopied] = useState(false);
  useEffect(() => setCopied(false), [secret]);
  if (!secret) return null;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret.secret);
      setCopied(true);
      toast.success(p("Copied"));
    } catch {
      toast.error(p("Could not copy API key"));
    }
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="personal-dialog personal-api-key-dialog">
        <DialogTitle>{p("API key created")}</DialogTitle>
        <p>{p("This secret is shown once. Copy it before closing.")}</p>
        <label>
          <span className="sr-only">{p("API key")}</span>
          <input
            className="personal-input"
            readOnly
            value={secret.secret}
            onFocus={(event) => event.currentTarget.select()}
            aria-label={p("API key")}
          />
        </label>
        <footer>
          <Action onClick={() => void copy()}>
            {copied ? <Check size={14} /> : <Clipboard size={14} />}
            {copied ? p("Copied") : p("Copy")}
          </Action>
          <Action primary onClick={onClose}>{p("Done")}</Action>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

type APIKeyDetailPageProps = {
  data: BootstrapData;
  apiKey: APIKey;
  onBack: () => void;
  onEdit?: () => void;
  onReload: () => Promise<void>;
  p: PersonalTranslate;
};

function APIKeyDetailPage({
  data,
  apiKey,
  onBack,
  onEdit,
  onReload,
  p,
}: APIKeyDetailPageProps) {
  const { formatDate } = useI18n();
  const [current, setCurrent] = useState(() => ({
    ...apiKey,
    // Do not coerce null to []; null is full access, [] is deny-all.
    scopes: apiKey.scopes,
    teamIds: apiKey.teamIds ?? [],
  }));
  const [editingName, setEditingName] = useState(false);
  const [nameDraft, setNameDraft] = useState(apiKey.name);
  const [busy, setBusy] = useState(false);
  const [revokeOpen, setRevokeOpen] = useState(false);
  const [secret, setSecret] = useState<{ name: string; secret: string } | null>(null);
  const [newlyCreatedSecret, setNewlyCreatedSecret] = useState("");
  useEffect(() => {
    try {
      const key =
        pendingAPIKeySecrets.get(apiKey.id) ??
        window.sessionStorage.getItem(`flow.api-key-secret:${apiKey.id}`);
      if (key) {
        setNewlyCreatedSecret(key);
        pendingAPIKeySecrets.delete(apiKey.id);
        window.sessionStorage.removeItem(`flow.api-key-secret:${apiKey.id}`);
      }
    } catch {
      const key = pendingAPIKeySecrets.get(apiKey.id);
      if (key) {
        setNewlyCreatedSecret(key);
        pendingAPIKeySecrets.delete(apiKey.id);
      }
    }
  }, [apiKey.id]);
  const scopeList = current.scopes ?? [];
  const teamIdList = current.teamIds ?? [];
  const teams = data.teams.filter((team) => teamIdList.includes(team.id));
  const selectedTeamAccess =
    current.teamRestriction === "selected" || teamIdList.length > 0;
  const isFullAccess =
    current.scopes == null ||
    (scopeList.includes("write") &&
      (scopeList.includes("admin") ||
        (scopeList.includes("read") && scopeList.length === 2)));
  const scopeDescription: Record<string, string> = {
    read: "Read all workspace data available to you",
    write: "Read and write all workspace data available to you",
    create_issues: "Create and update issues",
    create_comments: "Create issue comments",
    admin: "Access admin-only API features",
  };
  const saveName = async () => {
    const next = nameDraft.trim();
    if (!next || next.length < 2 || next.length > 40 || busy) return;
    setBusy(true);
    try {
      const updated = await updateAPIKey(current.id, { name: next });
      const normalized = { ...updated, scopes: updated.scopes, teamIds: updated.teamIds ?? [] };
      setCurrent(normalized);
      setNameDraft(normalized.name);
      setEditingName(false);
      await onReload().catch(() => undefined);
      toast.success(p("API key updated"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : p("Could not update API key"));
    } finally {
      setBusy(false);
    }
  };
  const rotate = async () => {
    if (busy) return;
    setBusy(true);
    try {
      const result = await rotateAPIKey(current.id);
      const normalized = {
        ...result.key,
        scopes: result.key.scopes,
        teamIds: result.key.teamIds ?? [],
      };
      setCurrent(normalized);
      setSecret({ name: normalized.name, secret: result.secret });
      await onReload().catch(() => undefined);
      try {
        await navigator.clipboard?.writeText(result.secret);
        toast.success(p("Personal API key copied to clipboard"));
      } catch {
        // Keep the one-time secret visible in the dialog when clipboard access is unavailable.
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : p("Could not rotate API key"));
    } finally {
      setBusy(false);
    }
  };
  const revoke = async () => {
    if (busy) return;
    setBusy(true);
    try {
      await revokeAPIKey(current.id);
      await onReload().catch(() => undefined);
      setRevokeOpen(false);
      toast.success(p("API key revoked"));
      onBack();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : p("Could not revoke API key"));
    } finally {
      setBusy(false);
    }
  };
  const copyNewSecret = async () => {
    if (!newlyCreatedSecret) return;
    try {
      await navigator.clipboard.writeText(newlyCreatedSecret);
      toast.success(p("Copied"));
    } catch {
      toast.error(p("Could not copy API key"));
    }
  };
  return (
    <div className="personal-api-key-page personal-api-key-detail-page">
      <nav className="personal-api-key-breadcrumb" aria-label={p("Breadcrumb")}>
        <button type="button" onClick={onBack}>
          <ChevronDown aria-hidden="true" />
          {p("Security settings")}
        </button>
      </nav>
      <header className="personal-api-key-header personal-api-key-detail-header">
        <div>
          <h1 data-i18n-ignore>{current.name}</h1>
          <p>
            {p("Created")} {formatDate(current.createdAt, { dateStyle: "medium" })}
          </p>
        </div>
        <div className="personal-api-key-detail-actions">
          <Action
            onClick={() => {
              if (onEdit) onEdit();
              else setEditingName(true);
            }}
            disabled={busy}
          >
            {p("Edit")}
          </Action>
          <Action onClick={() => void rotate()} disabled={busy}>{p("Rotate")}</Action>
          <Action danger onClick={() => setRevokeOpen(true)} disabled={busy}>{p("Revoke")}</Action>
        </div>
      </header>
      <section className="personal-api-key-card personal-api-key-detail-card">
        <div className="personal-api-key-detail-row">
          <div>
            <strong>{p("Key name")}</strong>
            <span data-i18n-ignore>{current.name}</span>
          </div>
          <Action
            onClick={() => {
              if (onEdit) onEdit();
              else {
                setNameDraft(current.name);
                setEditingName(true);
              }
            }}
            disabled={busy}
          >
            {p("Edit")}
          </Action>
        </div>
        {current.prefix && (
          <div className="personal-api-key-detail-row">
            <div>
              <strong>{p("API key")}</strong>
              {newlyCreatedSecret ? (
                <div className="personal-api-key-new-secret">
                  <div className="personal-api-key-secret-inline">
                    <span className="personal-api-key-mono" data-i18n-ignore>
                      {newlyCreatedSecret}
                    </span>
                    <button
                      type="button"
                      aria-label={p("Copy to clipboard")}
                      onClick={() => void copyNewSecret()}
                    >
                      <Clipboard aria-hidden="true" />
                    </button>
                  </div>
                  <small>{p("This API key will not be visible in the future. Please copy it now.")}</small>
                </div>
              ) : (
                <span className="personal-api-key-mono" data-i18n-ignore>
                  {current.prefix}…
                </span>
              )}
            </div>
          </div>
        )}
        <div className="personal-api-key-detail-row personal-api-key-detail-column">
          <strong>{p("Permissions")}</strong>
          {isFullAccess ? (
            <span>{p("Full access")}</span>
          ) : scopeList.length ? (
            <div className="personal-api-key-scope-summary">
              {scopeList.map((scope) => (
                <div key={scope}>
                  <span>{p(scope === "create_issues" ? "Create issues" : scope === "create_comments" ? "Create comments" : scope === "admin" ? "Admin" : scope === "write" ? "Write" : "Read")}</span>
                  <small>{p(scopeDescription[scope] ?? scope)}</small>
                </div>
              ))}
            </div>
          ) : (
            <span>{p("No permissions")}</span>
          )}
        </div>
        <div className="personal-api-key-detail-row personal-api-key-detail-column">
          <strong>{p("Team access")}</strong>
          {selectedTeamAccess ? (
            <div className="personal-api-key-detail-teams">
              {teams.length ? (
                teams.map((team) => (
                  <span key={team.id} data-i18n-ignore>
                    {team.name}
                  </span>
                ))
              ) : (
                <span>{p("No teams selected")}</span>
              )}
            </div>
          ) : (
            <span>{p("All teams you have access to")}</span>
          )}
        </div>
        {current.lastUsedAt && (
          <div className="personal-api-key-detail-row">
            <div><strong>{p("Last used")}</strong><span>{formatDate(current.lastUsedAt, { dateStyle: "medium", timeStyle: "short" })}</span></div>
          </div>
        )}
        {current.expiresAt && (
          <div className="personal-api-key-detail-row">
            <div><strong>{p("expires")}</strong><span>{formatDate(current.expiresAt, { dateStyle: "medium", timeStyle: "short" })}</span></div>
          </div>
        )}
      </section>
      <Dialog open={editingName} onOpenChange={setEditingName}>
        <DialogContent className="personal-dialog personal-api-key-dialog">
          <DialogTitle>{p("Edit API key")}</DialogTitle>
          <label>
            <span>{p("Key name")}</span>
            <input
              className="personal-input"
              value={nameDraft}
              maxLength={40}
              autoFocus
              onChange={(event) => setNameDraft(event.target.value)}
              onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); void saveName(); } }}
            />
          </label>
          <footer>
            <Action onClick={() => setEditingName(false)} disabled={busy}>{p("Cancel")}</Action>
            <Action primary onClick={() => void saveName()} disabled={busy || nameDraft.trim().length < 2}>{p("Save")}</Action>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog open={revokeOpen} onOpenChange={setRevokeOpen}>
        <DialogContent className="personal-dialog">
          <DialogTitle>{p("Revoke API key?")}</DialogTitle>
          <p>{p("Applications using this key will no longer access Flow data.")}</p>
          <footer>
            <Action onClick={() => setRevokeOpen(false)} disabled={busy}>{p("Cancel")}</Action>
            <Action danger onClick={() => void revoke()} disabled={busy}>{p("Revoke")}</Action>
          </footer>
        </DialogContent>
      </Dialog>
      <APIKeySecretDialog secret={secret} onClose={() => setSecret(null)} p={p} />
    </div>
  );
}

function Connections({ data, onNavigate, p }: PersonalProps) {
  const integrations = useMemo(
    () =>
      new Map(data.integrationConnections.map((item) => [item.provider, item])),
    [data.integrationConnections],
  );
  const { formatDate } = useI18n();
  const [identities, setIdentities] = useState<
    Awaited<ReturnType<typeof fetchAccountIdentities>>
  >([]);
  useEffect(() => {
    void fetchAccountIdentities()
      .then(setIdentities)
      .catch((error) =>
        toast.error(
          error instanceof Error
            ? error.message
            : p("Could not load connected accounts"),
        ),
      );
  }, [p]);
  const entries = [
    {
      provider: "slack",
      name: "Slack",
      description: p(
        "Sync your message attribution, and receive notifications in Slack",
      ),
      icon: <MessageCircle />,
    },
    {
      provider: "google-calendar",
      name: "Google Calendar",
      description: p("Sync your calendar out-of-office status to Flow"),
      icon: <CalendarDays />,
    },
    {
      provider: "notion",
      name: "Notion",
      description: p("Preview issues, projects, and views within Notion"),
      icon: <MessageSquare />,
    },
    {
      provider: "github",
      name: "GitHub",
      description: p(
        "Review code in Flow and sync attribution of your git-related actions",
      ),
      icon: <GitFork />,
    },
  ];
  return (
    <>
      <PersonalPageTitle
        description={p(
          "Connect your user accounts to sync attribution of your actions between apps",
        )}
      >
        {p("Connected accounts")}
      </PersonalPageTitle>
      <div className="personal-connection-list">
        {entries.map((item) => {
          const connection = integrations.get(item.provider);
          return (
            <div className="settings-card" key={item.provider}>
              <PersonalRow
                icon={item.icon}
                title={<span data-i18n-ignore>{item.name}</span>}
                description={item.description}
              >
                <Action onClick={() => onNavigate("integrations")}>
                  {p(connection ? "Connected" : "Connect")}
                </Action>
              </PersonalRow>
            </div>
          );
        })}
      </div>
      <PersonalSection
        title={p("Sign-in identities")}
        description={p(
          "Identity provider accounts connected to your Flow profile",
        )}
      >
        {identities.map((item) => (
          <PersonalRow
            key={item.id}
            icon={<ShieldCheck />}
            title={
              <span data-i18n-ignore>{item.username || item.subject}</span>
            }
            description={
              <span>
                <span data-i18n-ignore>
                  {item.provider.toUpperCase()} · {item.issuer}
                </span>
                {` · ${p("last used")} ${formatDate(item.lastLoginAt, { dateStyle: "medium" })}`}
              </span>
            }
          >
            <Action
              danger
              onClick={() =>
                void unlinkAccountIdentity(item.id)
                  .then(() => fetchAccountIdentities().then(setIdentities))
                  .catch((error) =>
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : p("Could not unlink identity"),
                    ),
                  )
              }
            >
              {p("Unlink")}
            </Action>
          </PersonalRow>
        ))}
        {!identities.length && (
          <div className="personal-empty">
            <ShieldCheck />
            <h3>{p("No sign-in identities connected")}</h3>
          </div>
        )}
      </PersonalSection>
    </>
  );
}

function Agents({ data, values, setValue, onNavigate, p }: PersonalProps) {
  const [draft, setDraft] = useState(String(values.agentInstructions || ""));
  const dirty = draft !== String(values.agentInstructions || "");
  return (
    <>
      <PersonalPageTitle
        description={p("Your personal settings for Flow Agent")}
      >
        {p("Agent personalization")}
      </PersonalPageTitle>
      <PersonalSection
        title={p("Guidance")}
        description={p(
          "Provide personal instructions and context for Flow Agent when responding to conversations",
        )}
      >
        <textarea
          className="personal-agent-guidance"
          aria-label={p("AI prompt rules")}
          placeholder={p("Enter personal guidance for Flow Agent (optional)…")}
          maxLength={4000}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => {
            if (dirty) setValue("agentInstructions", draft);
          }}
        />
      </PersonalSection>
      <PersonalSection
        title={p("Skills")}
        description={p(
          "Reusable prompts auto-selected by the agent or invoked via slash commands",
        )}
      >
        <div className="personal-agent-skills">
          {data.agentSkills.map((skill) => (
            <NavLink
              data-i18n-ignore
              key={skill.id}
              to={agentSkillPath(data.workspace.urlKey, skill.id)}
            >
              <Bot />
              <span>
                <strong>{skill.name}</strong>
                <small>{skill.instructions}</small>
              </span>
            </NavLink>
          ))}
          <NavLink
            className="personal-agent-add-row"
            to={newAgentSkillPath(data.workspace.urlKey)}
          >
            <span>
              {p(
                data.agentSkills.length ? "Create skill" : "No skills created",
              )}
            </span>
            <Plus />
          </NavLink>
        </div>
      </PersonalSection>
      <PersonalSection
        title={p("MCP connectors")}
        description={p(
          "Add MCP connectors for use with Flow Agent. Workspace admins can manage available connectors in security settings.",
        )}
      >
        <PersonalRow title={p("Agent MCP access disabled in this workspace")}>
          <Action onClick={() => onNavigate("security")}>
            {p("Configure")}
          </Action>
        </PersonalRow>
      </PersonalSection>
    </>
  );
}

function defaultNotificationPreferences(
  userId: string,
): NotificationPreferences {
  const categories = {
    assignments: true,
    statusChanges: true,
    comments: true,
    mentions: true,
    reactions: true,
    subscriptions: true,
    documents: true,
    updates: true,
    reminders: true,
    loops: true,
    integrations: true,
    billing: true,
    customerRequests: true,
    triage: true,
  };
  return {
    userId,
    inbox: { enabled: true, categories },
    email: { enabled: true, categories: { ...categories } },
    desktop: { enabled: false, categories: { ...categories } },
    emailFormat: "digest",
    delayLowPriority: true,
    immediateUrgent: true,
    soundEnabled: true,
    updatedAt: new Date().toISOString(),
  };
}
function initials(value: string) {
  return (
    value
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((part) => part[0]?.toUpperCase())
      .join("") || "?"
  );
}

function apiKeyPermissionsLabel(
  key: { scopes?: string[] | null },
  p: PersonalTranslate,
) {
  // Null/omitted means unrestricted; an explicit empty list means deny-all.
  if (key.scopes == null) return p("Full access");
  const scopes = key.scopes.map((scope) =>
    scope === "issues:create" || scope === "issue:create"
      ? "create_issues"
      : scope === "comments:create" || scope === "comment:create"
        ? "create_comments"
        : scope,
  );
  if (
    scopes.includes("write") &&
    (scopes.includes("admin") ||
      (scopes.includes("read") && scopes.length === 2))
  ) {
    return p("Full access");
  }
  const labels: Record<string, string> = {
    read: "Read",
    write: "Write",
    create_issues: "Create issues",
    create_comments: "Create comments",
    admin: "Admin",
  };
  return scopes.map((scope) => p(labels[scope] ?? scope)).join(", ") || p("No permissions");
}

function apiKeyErrorMessage(
  error: unknown,
  p: PersonalTranslate,
  fallback: string,
) {
  const message = error instanceof Error ? error.message : "";
  const normalized = message.toLowerCase();
  if (normalized.includes("name is required")) return p("Key name is required");
  if (normalized.includes("name must be between") || normalized.includes("at least 2")) {
    return p("Key name must be at least 2 characters");
  }
  if (normalized.includes("name already exists")) {
    return p("An API key with this name already exists");
  }
  if (normalized.includes("selected team") || normalized.includes("team access")) {
    return p("Select at least one team");
  }
  if (normalized.includes("permission") || normalized.includes("scope")) {
    return p("Select at least one permission");
  }
  return message || fallback;
}

function apiKeyTeamAccessLabel(
  key: { teamIds?: string[] | null; teamRestriction?: "all" | "selected" },
  p: PersonalTranslate,
) {
  const teamIds = key.teamIds ?? [];
  const selected =
    key.teamRestriction === "selected" ||
    (!key.teamRestriction && teamIds.length > 0);
  return selected
    ? `${teamIds.length} ${p("teams")}`
    : p("All teams you have access to");
}

function formatSecurityRelativeDate(value: string, locale: "en-US" | "zh-CN") {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return "—";
  const seconds = (timestamp - Date.now()) / 1000;
  const absolute = Math.abs(seconds);
  const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ["year", 31_536_000],
    ["month", 2_592_000],
    ["week", 604_800],
    ["day", 86_400],
    ["hour", 3_600],
    ["minute", 60],
    ["second", 1],
  ];
  const [unit, divisor] =
    units.find(([, size]) => absolute >= size) ?? units[units.length - 1];
  const amount = Math.round(seconds / divisor);
  return new Intl.RelativeTimeFormat(locale, { numeric: "auto" }).format(
    amount,
    unit,
  );
}

function APIKeyMetadata({
  apiKey,
  data,
  formatDate,
  locale,
  p,
}: {
  apiKey: APIKey;
  data: BootstrapData;
  formatDate: (value: Date | string | number, options?: Intl.DateTimeFormatOptions) => string;
  locale: "en-US" | "zh-CN";
  p: PersonalTranslate;
}) {
  // Null/omitted means unrestricted; preserve an explicit [] as deny-all.
  const scopes = (apiKey.scopes ?? []).map((scope) =>
    scope === "issues:create" || scope === "issue:create"
      ? "create_issues"
      : scope === "comments:create" || scope === "comment:create"
        ? "create_comments"
        : scope,
  );
  const selectedTeams = data.teams.filter((team) =>
    (apiKey.teamIds ?? []).includes(team.id),
  );
  const scopeLabels: Record<string, string> = {
    read: "Read",
    write: "Write",
    create_issues: "Create issues",
    create_comments: "Create comments",
    admin: "Admin",
  };
  const scopeDescriptions: Record<string, string> = {
    read: "Read all workspace data available to you",
    write: "Read and write all workspace data available to you",
    create_issues: "Create and update issues",
    create_comments: "Create issue comments",
    admin: "Access admin-only API features",
  };
  const separator = (
    <span className="personal-security-metadata-separator" aria-hidden="true">
      ·
    </span>
  );
  const scopeText = apiKeyPermissionsLabel(apiKey, p);
  const teamText = apiKeyTeamAccessLabel(apiKey, p);
  const selectedRestriction =
    apiKey.teamRestriction === "selected" ||
    (!apiKey.teamRestriction && (apiKey.teamIds ?? []).length > 0);
  return (
    <span className="personal-security-api-key-metadata">
      <span>
        {p("Created")} {formatDate(apiKey.createdAt, { dateStyle: "medium" })}
      </span>
      {separator}
      <span>
        {apiKey.lastUsedAt
          ? `${p("Last used")} ${formatSecurityRelativeDate(apiKey.lastUsedAt, locale)}`
          : p("Never used")}
      </span>
      {separator}
      {scopes.length > 0 ? (
        <Popover.Root>
          <Popover.Trigger asChild>
            <button
              type="button"
              className="personal-security-metadata-trigger"
              aria-label={`${p("Permissions")}: ${scopeText}`}
              onPointerDown={(event) => event.stopPropagation()}
              onClick={(event) => event.stopPropagation()}
              onKeyDown={(event) => event.stopPropagation()}
            >
              <span>{scopeText}</span>
              <ChevronDown aria-hidden="true" />
            </button>
          </Popover.Trigger>
          <Popover.Portal>
            <Popover.Content
              align="start"
              side="bottom"
              sideOffset={4}
              collisionPadding={8}
              className="personal-security-metadata-popover"
              aria-label={p("Permissions")}
              onPointerDown={(event) => event.stopPropagation()}
            >
              <strong>{p("Permissions")}</strong>
              <div className="personal-security-metadata-list">
                {scopes.map((scope) => (
                  <div key={scope}>
                    <span>{p(scopeLabels[scope] ?? scope)}</span>
                    <small>{p(scopeDescriptions[scope] ?? scope)}</small>
                  </div>
                ))}
              </div>
            </Popover.Content>
          </Popover.Portal>
        </Popover.Root>
      ) : (
        <span>{scopeText}</span>
      )}
      {separator}
      {selectedRestriction && selectedTeams.length > 0 ? (
        <Popover.Root>
          <Popover.Trigger asChild>
            <button
              type="button"
              className="personal-security-metadata-trigger"
              aria-label={`${p("Teams")}: ${teamText}`}
              onPointerDown={(event) => event.stopPropagation()}
              onClick={(event) => event.stopPropagation()}
              onKeyDown={(event) => event.stopPropagation()}
            >
              <span>{teamText}</span>
              <ChevronDown aria-hidden="true" />
            </button>
          </Popover.Trigger>
          <Popover.Portal>
            <Popover.Content
              align="start"
              side="bottom"
              sideOffset={4}
              collisionPadding={8}
              className="personal-security-metadata-popover"
              aria-label={p("Teams")}
              onPointerDown={(event) => event.stopPropagation()}
            >
              <strong>{p("Teams")}</strong>
              <div className="personal-security-metadata-list">
                {selectedTeams.map((team) => (
                  <div key={team.id}>
                    <span data-i18n-ignore>{team.name}</span>
                  </div>
                ))}
              </div>
            </Popover.Content>
          </Popover.Portal>
        </Popover.Root>
      ) : (
        <span>{teamText}</span>
      )}
      {apiKey.expiresAt && (
        <>
          {separator}
          <span>
            {p("expires")} {formatDate(apiKey.expiresAt, { dateStyle: "medium" })}
          </span>
        </>
      )}
    </span>
  );
}

function BrowserSessionIcon({ browserType }: { browserType?: string }) {
  if (browserType === "Microsoft Edge") return <Monitor aria-hidden="true" />;
  // Use a fixed 16px viewport so browser icons align with the session rows.
  return (
    <svg className="personal-browser-icon" aria-hidden="true" viewBox="0 0 16 16">
      <path d="M8 11.5724C7.3 11.5724 6.64829 11.3793 6.06896 10.9931C5.48965 10.6069 5.05516 10.1241 4.76551 9.5207L1.86896 4.5C1.26551 5.5862 1 6.76896 1 8C1 9.76206 1.5793 11.2828 2.7138 12.5862C3.84829 13.8897 5.27241 14.6621 6.96209 14.9276L8.98968 11.4034C8.79655 11.4759 8.43449 11.5724 8 11.5724Z" />
      <path d="M5.80345 5.10345C6.45516 4.59655 7.1793 4.3793 8 4.3793H14.0103C13.3828 3.31725 12.5379 2.54481 11.4759 1.94136C10.4138 1.3138 9.25516 1 8 1C6.9138 1 5.87586 1.24139 4.93449 1.7C3.99313 2.15861 3.1 2.83449 2.47241 3.70345L4.5 7.03449C4.6931 6.26206 5.15171 5.61035 5.80345 5.10345Z" />
      <path d="M14.469 5.36896H10.4138C11.1138 6.06896 11.5966 6.9862 11.5966 8C11.5966 8.74829 11.3793 9.42414 10.969 10.0517L8.07241 15C9.97931 14.9759 11.6207 14.3 12.9724 12.9241C14.3241 11.5483 15 9.9069 15 8C15 7.1069 14.8552 6.16551 14.469 5.36896Z" />
      <path d="M8 5.34484C9.44829 5.34484 10.6552 6.55174 10.6552 8C10.6552 9.44826 9.44829 10.6552 8 10.6552C6.55171 10.6552 5.34484 9.44829 5.34484 8C5.34484 6.55171 6.55171 5.34484 8 5.34484Z" />
    </svg>
  );
}

function decodeBase64Url(value: string): ArrayBuffer {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes.buffer;
}

function encodeBase64Url(value: ArrayBufferLike): string {
  const bytes = new Uint8Array(value);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/g, "");
}

function toCredentialCreationOptions(raw: unknown): PublicKeyCredentialCreationOptions {
  const wrapper = (raw ?? {}) as {
    publicKey?: Record<string, unknown>;
  };
  const source = wrapper.publicKey ?? (raw as Record<string, unknown>) ?? {};
  const user = (source.user ?? {}) as Record<string, unknown>;
  const excludeCredentials = Array.isArray(source.excludeCredentials)
    ? source.excludeCredentials.map((item) => {
        const descriptor = item as Record<string, unknown>;
        return {
          ...descriptor,
          id: decodeBase64Url(String(descriptor.id ?? "")),
        } as PublicKeyCredentialDescriptor;
      })
    : undefined;
  return {
    ...source,
    challenge: decodeBase64Url(String(source.challenge ?? "")),
    user: {
      ...user,
      id: decodeBase64Url(String(user.id ?? "")),
    },
    excludeCredentials,
  } as PublicKeyCredentialCreationOptions;
}

function serializeCreationCredential(
  credential: PublicKeyCredential,
  response: AuthenticatorAttestationResponse,
) {
  return {
    id: credential.id,
    type: credential.type,
    rawId: encodeBase64Url(credential.rawId),
    response: {
      clientDataJSON: encodeBase64Url(response.clientDataJSON),
      attestationObject: encodeBase64Url(response.attestationObject),
      transports: response.getTransports?.() ?? [],
    },
  };
}
function localizedOptions(
  p: PersonalTranslate,
  values: string[],
): SettingsSelectOption[] {
  return values.map((value) => ({
    value,
    label: p(value),
    entityName: isBusinessName(value),
  }));
}
function isBusinessName(value: string) {
  return /Flow|GitHub|GitLab|Slack|Notion|Google Calendar|MCP/.test(value);
}
