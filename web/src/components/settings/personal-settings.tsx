import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  Bot,
  CalendarDays,
  ChevronDown,
  Code2,
  ExternalLink,
  GitFork,
  Globe,
  KeyRound,
  Laptop,
  Mail,
  MessageCircle,
  MessageSquare,
  Monitor,
  ShieldCheck,
  Plus,
  Smartphone,
} from "lucide-react";
import { toast } from "sonner";
import { NavLink } from "react-router-dom";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  deletePushSubscription,
  fetchAccountIdentities,
  fetchAccountSessions,
  listPushSubscriptions,
  removeMember,
  revokeAccountSession,
  revokeOtherAccountSessions,
  revokeOAuthAuthorization,
  updateAccountProfile,
  updateNotificationPreferences,
  unlinkAccountIdentity,
} from "@/lib/api";
import type { SettingsPageId } from "@/lib/app-routes";
import { agentSkillPath, newAgentSkillPath } from "@/lib/app-routes";
import type { BootstrapData, NotificationPreferences } from "@/types/flow";
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
  data: BootstrapData;
  values: PersonalSettingsValues;
  setValue: (key: string, value: string | boolean) => void;
  onNavigate: (page: SettingsPageId) => void;
  onReload: () => Promise<void>;
  onBack: () => void;
  onCustomizeSidebar: () => void;
};

type PersonalTranslate = (source: string) => string;

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
  Done: "完成",
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
  "Current session": "当前会话",
  "Signed-in session": "已登录会话",
  "Last active": "上次活动时间",
  expires: "到期时间",
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
  "Personal API keys": "个人 API 密钥",
  "Use Flow’s API to build your own integrations":
    "使用 Flow API 构建自己的集成",
  "No API keys created": "尚未创建 API 密钥",
  "active API key": "个有效 API 密钥",
  "Manage API keys": "管理 API 密钥",
  "Commit signing key": "提交签名密钥",
  "Coding sessions use this key to sign your commits":
    "编码会话使用此密钥为提交签名",
  "Authorized applications": "已授权应用",
  "No authorized applications": "没有已授权应用",
  "Revoke all other sessions?": "撤销所有其他会话？",
  "Every other device will need to sign in again.":
    "所有其他设备都需要重新登录。",
  "Could not load security information": "无法加载安全信息",
  "Other sessions revoked": "已撤销其他会话",
  "Could not revoke sessions": "无法撤销会话",
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
  title,
  description,
  children,
}: {
  title?: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <SettingsSection
      className="personal-section"
      description={description}
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
}: {
  children: ReactNode;
  onClick?: () => void;
  danger?: boolean;
  primary?: boolean;
  disabled?: boolean;
  label?: string;
}) {
  return (
    <button
      aria-label={label}
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

function CodeReviews({ values, setValue, p }: PersonalProps) {
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
        <PersonalRow title={p("No signing key added")}>
          <Action disabled>{p("Add key")}</Action>
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

function Security({ data, onNavigate, onReload, p }: PersonalProps) {
  const { formatDate } = useI18n();
  const [sessions, setSessions] = useState<
    Awaited<ReturnType<typeof fetchAccountSessions>>
  >([]);
  const [confirmOpen, setConfirmOpen] = useState(false);
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
  const other = sessions.filter((item) => !item.current);
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
  return (
    <>
      <PersonalPageTitle>{p("Security & access")}</PersonalPageTitle>
      <PersonalSection
        title={p("Sessions")}
        description={p("Devices logged into your account")}
      >
        {sessions
          .filter((item) => item.current)
          .map((item) => (
            <PersonalRow
              key={item.id}
              icon={<Globe />}
              title={p("Current session")}
              description={`${p("Last active")} ${formatDate(item.lastSeenAt, { dateStyle: "medium", timeStyle: "short" })} · ${p("expires")} ${formatDate(item.expiresAt, { dateStyle: "medium", timeStyle: "short" })}`}
            />
          ))}
      </PersonalSection>
      {other.length > 0 && (
        <PersonalSection
          title={`${other.length} ${p(other.length === 1 ? "other session" : "other sessions")}`}
        >
          <div className="personal-section-action">
            <Action danger onClick={() => setConfirmOpen(true)}>
              {p("Revoke all")}
            </Action>
          </div>
          {other.map((item) => (
            <PersonalRow
              key={item.id}
              icon={<Laptop />}
              title={p("Signed-in session")}
              description={`${p("Last active")} ${formatDate(item.lastSeenAt, { dateStyle: "medium", timeStyle: "short" })}`}
            >
              <Action
                danger
                onClick={() =>
                  void revokeAccountSession(item.id).then(() =>
                    fetchAccountSessions().then(setSessions),
                  )
                }
              >
                {p("Revoke")}
              </Action>
            </PersonalRow>
          ))}
        </PersonalSection>
      )}
      <PersonalSection
        title={p("Passkeys")}
        description={p(
          "Passkeys are a secure way to sign in to your Flow account",
        )}
      >
        <div className="personal-empty">
          <KeyRound />
          <h3>{p("No passkeys registered")}</h3>
          <span>
            {p("Passkey enrollment is not available on this server.")}
          </span>
        </div>
      </PersonalSection>
      <PersonalSection
        title={p("Personal API keys")}
        description={p("Use Flow’s API to build your own integrations")}
      >
        <div className="personal-empty">
          <Code2 />
          <h3>
            {data.apiKeys.filter(
              (k) => k.creatorId === data.viewer.id && !k.revokedAt,
            ).length
              ? `${data.apiKeys.filter((k) => k.creatorId === data.viewer.id && !k.revokedAt).length} ${p("active API key")}`
              : p("No API keys created")}
          </h3>
          <Action onClick={() => onNavigate("api")}>
            {p("Manage API keys")}
          </Action>
        </div>
      </PersonalSection>
      <PersonalSection
        title={p("Commit signing key")}
        description={p("Coding sessions use this key to sign your commits")}
      >
        <PersonalRow title={p("No signing key added")}>
          <Action disabled>{p("Add key")}</Action>
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={p("Authorized applications")}>
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
                onClick={() =>
                  void revokeOAuthAuthorization(item.id).then(onReload)
                }
              >
                {p("Revoke")}
              </Action>
            </PersonalRow>
          ))}
        {!data.oauthAuthorizations.some(
          (item) => item.userId === data.viewer.id && !item.revokedAt,
        ) && (
          <div className="personal-empty">
            <ShieldCheck />
            <h3>{p("No authorized applications")}</h3>
          </div>
        )}
      </PersonalSection>
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
    </>
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
