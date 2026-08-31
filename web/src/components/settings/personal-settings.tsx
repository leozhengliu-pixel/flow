import { useEffect, useMemo, useState, type ReactNode } from "react";
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

const EN = {
  preferences: "Preferences",
  profile: "Profile",
  notifications: "Notifications",
  codeReviews: "Code & reviews",
  security: "Security & access",
  connections: "Connected accounts",
  agents: "Agent personalization",
  general: "General",
  interfaceTheme: "Interface and theme",
  desktopApp: "Desktop application",
  workflows: "Automations and workflows",
  language: "Language",
  languageDescription: "Choose the language used throughout the application",
};
const ZH = {
  preferences: "偏好设置",
  profile: "个人资料",
  notifications: "通知",
  codeReviews: "代码与评审",
  security: "安全与访问",
  connections: "已连接账户",
  agents: "Agent 个性化",
  general: "通用",
  interfaceTheme: "界面与主题",
  desktopApp: "桌面应用",
  workflows: "自动化与工作流",
  language: "语言",
  languageDescription: "选择整个应用使用的语言",
};

export function PersonalSettings(props: Props) {
  const { locale } = useI18n();
  const text = locale === "zh-CN" ? ZH : EN;
  let content: ReactNode;
  if (props.page === "preferences")
    content = <Preferences {...props} text={text} />;
  else if (props.page === "profile")
    content = <Profile {...props} text={text} />;
  else if (props.page === "notifications")
    content = <Notifications {...props} text={text} />;
  else if (props.page === "code-and-reviews")
    content = <CodeReviews {...props} text={text} />;
  else if (props.page === "account-security")
    content = <Security {...props} text={text} />;
  else if (props.page === "connections")
    content = <Connections {...props} text={text} />;
  else content = <Agents {...props} text={text} />;
  return <div className="personal-settings">{content}</div>;
}

type PersonalProps = Props & { text: typeof EN };

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
  options: string[];
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
  text,
  onCustomizeSidebar,
}: PersonalProps) {
  const { locale, setLocale } = useI18n();
  return (
    <>
      <PersonalPageTitle>{text.preferences}</PersonalPageTitle>
      <PersonalSection title={text.general}>
        <PersonalRow
          title={text.language}
          description={text.languageDescription}
        >
          <PersonalSelect
            label={text.language}
            value={locale === "zh-CN" ? "简体中文" : "English"}
            options={["English", "简体中文"]}
            onChange={(value) => {
              const next = value === "简体中文" ? "zh-CN" : "en-US";
              setLocale(next);
              setValue("language", next);
            }}
          />
        </PersonalRow>
        <PersonalRow
          title="Default home view"
          description="Select which view to display when launching Flow"
        >
          <PersonalSelect
            label="Default home view"
            value={String(values.homeView)}
            options={["Flow Agent (default)", "Inbox", "My issues"]}
            onChange={(v) => setValue("homeView", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Display names"
          description="Select how names are displayed in the Flow interface"
        >
          <PersonalSelect
            label="Display names"
            value={String(values.displayNames)}
            options={["Full name", "First name", "Username"]}
            onChange={(v) => setValue("displayNames", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="First day of the week"
          description="Used for date pickers"
        >
          <PersonalSelect
            label="First day of the week"
            value={String(values.firstDay)}
            options={["Monday", "Saturday", "Sunday"]}
            onChange={(v) => setValue("firstDay", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Convert text emoticons into emojis"
          description="Strings like :) will be converted to 🙂"
        >
          <SettingsToggle
            label="Convert text emoticons into emojis"
            checked={Boolean(values.emoticons)}
            onChange={(v) => setValue("emoticons", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Send comments on…"
          description="Choose which key press is used to submit comments"
        >
          <PersonalSelect
            label="Send comments on…"
            value={String(values.sendComments)}
            options={["Enter", "⌘ Enter"]}
            onChange={(v) => setValue("sendComments", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={text.interfaceTheme}>
        <PersonalRow
          title="App sidebar"
          description="Customize sidebar item visibility, ordering, and badge style"
        >
          <Action onClick={onCustomizeSidebar}>Customize</Action>
        </PersonalRow>
        <PersonalRow
          title="Font size"
          description="Adjust the size of text across the app"
        >
          <PersonalSelect
            label="Font size"
            value={String(values.fontSize)}
            options={["Small", "Default", "Large"]}
            onChange={(v) => setValue("fontSize", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Use pointer cursors"
          description="Change the cursor to a pointer when hovering over interactive elements"
        >
          <SettingsToggle
            label="Use pointer cursors"
            checked={Boolean(values.pointerCursor)}
            onChange={(v) => setValue("pointerCursor", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Underline links"
          description="Always underline links in text content"
        >
          <SettingsToggle
            label="Underline links"
            checked={Boolean(values.underlineLinks)}
            onChange={(v) => setValue("underlineLinks", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Disable animated images & emoji"
          description="When enabled, GIFs and animated emojis remain static until hovered"
        >
          <SettingsToggle
            label="Disable animated images & emoji"
            checked={Boolean(values.disableAnimatedImages)}
            onChange={(v) => setValue("disableAnimatedImages", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection>
        <PersonalRow
          title="Interface theme"
          description="Select or customize your interface color scheme"
        >
          <PersonalSelect
            label="Interface theme"
            value={String(values.interfaceTheme)}
            options={["System preference", "Light", "Dark"]}
            onChange={(v) => setValue("interfaceTheme", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={text.desktopApp}>
        <PersonalRow
          title="Open in desktop app"
          description="Automatically open links in desktop app when possible"
        >
          <SettingsToggle
            label="Open in desktop app"
            checked={Boolean(values.desktopLinks)}
            onChange={(v) => setValue("desktopLinks", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title={text.workflows}>
        <PersonalRow
          title="Auto-assign to self"
          description="When creating new issues, always assign them to yourself by default"
        >
          <SettingsToggle
            label="Auto-assign to self"
            checked={Boolean(values.autoAssign)}
            onChange={(v) => setValue("autoAssign", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="On move to started status, assign to yourself"
          description="When you move an unassigned issue to started, it will be automatically assigned to you"
        >
          <SettingsToggle
            label="On move to started status, assign to yourself"
            checked={Boolean(values.assignStarted)}
            onChange={(v) => setValue("assignStarted", v)}
          />
        </PersonalRow>
      </PersonalSection>
    </>
  );
}

function Profile({ data, onReload, onBack, text }: PersonalProps) {
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
      toast.success("Profile updated");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not update profile",
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
        error instanceof Error ? error.message : "Could not leave workspace",
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <PersonalPageTitle>{text.profile}</PersonalPageTitle>
      <PersonalSection>
        <PersonalRow title="Profile picture">
          <span className="personal-avatar">{initials(displayName)}</span>
        </PersonalRow>
        <PersonalRow title="Email">
          <span className="personal-static">{data.viewer.email}</span>
        </PersonalRow>
        <PersonalRow title="Full name">
          <input
            className="personal-input"
            aria-label="Full name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </PersonalRow>
        <PersonalRow title="Title" description="Your job title or role">
          <input
            className="personal-input"
            aria-label="Title"
            placeholder="Software engineer"
            value={jobTitle}
            onChange={(e) => setJobTitle(e.target.value)}
          />
        </PersonalRow>
        <PersonalRow
          title="Username"
          description="One word, like a nickname or first name"
        >
          <input
            className="personal-input"
            aria-label="Username"
            placeholder="username"
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
            Cancel
          </Action>
          <Action
            primary
            disabled={busy || !displayName.trim() || !username.trim()}
            onClick={() => void save()}
          >
            {busy ? "Saving…" : "Update"}
          </Action>
        </div>
      )}
      <PersonalSection title="Workspace access">
        <PersonalRow title="Remove yourself from workspace" danger>
          <Action danger onClick={() => setLeaveOpen(true)}>
            Leave workspace
          </Action>
        </PersonalRow>
      </PersonalSection>
      <Dialog open={leaveOpen} onOpenChange={setLeaveOpen}>
        <DialogContent className="personal-dialog">
          <DialogTitle>Leave {data.workspace.name}?</DialogTitle>
          <p>
            You will lose access to this workspace. An administrator must invite
            you again to restore access.
          </p>
          <footer>
            <Action onClick={() => setLeaveOpen(false)}>Cancel</Action>
            <Action danger disabled={busy} onClick={() => void leave()}>
              Leave workspace
            </Action>
          </footer>
        </DialogContent>
      </Dialog>
    </>
  );
}

function Notifications({
  data,
  values,
  setValue,
  onReload,
  text,
}: PersonalProps) {
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
        error instanceof Error ? error.message : "Could not save notifications",
      );
    }
  };
  return (
    <>
      <PersonalPageTitle>{text.notifications}</PersonalPageTitle>
      <PersonalSection
        title="Push notifications"
        description="Choose which notifications are pushed to your devices. All notifications will still appear in your inbox."
      >
        <NotificationChannel
          icon={<Monitor />}
          title="Desktop"
          status={preferences.desktop.enabled ? "Enabled" : "Disabled"}
          onClick={() => setChannel("desktop")}
        />
        <NotificationChannel
          icon={<Smartphone />}
          title="Mobile"
          status="Not available in Flow web"
          disabled
        />
        <NotificationChannel
          icon={<Mail />}
          title="Email"
          status={
            preferences.email.enabled
              ? "Enabled for all notifications"
              : "Disabled"
          }
          onClick={() => setChannel("email")}
        />
        <NotificationChannel
          icon={<MessageCircle />}
          title="Slack"
          status="Not connected"
          disabled
        />
      </PersonalSection>
      <PersonalSection
        title="Updates from Flow"
        description="Subscribe to product announcements and important changes from the Flow team"
      >
        <PersonalRow
          title="Show updates in sidebar"
          description="Highlight new features and improvements in the app sidebar"
        >
          <SettingsToggle
            label="Show updates in sidebar"
            checked={Boolean(values.changelogUpdates)}
            onChange={(v) => setValue("changelogUpdates", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Changelog newsletter"
          description="Receive an email twice a month highlighting new features and improvements"
        >
          <SettingsToggle
            label="Changelog newsletter"
            checked={Boolean(values.changelogNewsletter)}
            onChange={(v) => setValue("changelogNewsletter", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title="Marketing">
        <PersonalRow
          title="Marketing and onboarding"
          description="Occasional updates to help you get the most out of Flow"
        >
          <SettingsToggle
            label="Marketing and onboarding"
            checked={Boolean(values.marketingUpdates)}
            onChange={(v) => setValue("marketingUpdates", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title="Other updates">
        <PersonalRow
          title="Invite accepted"
          description="Email when invitees accept an invite"
        >
          <SettingsToggle
            label="Invite accepted"
            checked={Boolean(values.inviteAcceptedUpdates)}
            onChange={(v) => setValue("inviteAcceptedUpdates", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Privacy and legal updates"
          description="Email when privacy policies or terms of service change"
        >
          <SettingsToggle
            label="Privacy and legal updates"
            checked={Boolean(values.privacyUpdates)}
            onChange={(v) => setValue("privacyUpdates", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Data processing agreement (DPA)"
          description="Email when our DPA changes"
        >
          <SettingsToggle
            label="Data processing agreement"
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
              ? "Email notifications"
              : "Desktop notifications"}
          </DialogTitle>
          {channel && channel !== "mobile" && channel !== "slack" && (
            <>
              <PersonalRow title={`Enable ${channel} notifications`}>
                <SettingsToggle
                  label={`Enable ${channel} notifications`}
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
                  <PersonalRow title="Notification sounds">
                    <SettingsToggle
                      label="Notification sounds"
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
                      title={item.userAgent || "Web browser"}
                      description={`Enabled ${new Date(item.createdAt).toLocaleDateString()}`}
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
                                  : "Could not remove device",
                              ),
                            )
                        }
                      >
                        Remove
                      </Action>
                    </PersonalRow>
                  ))}
                  {!pushSubscriptions.length && (
                    <PersonalRow
                      title="No browser push devices"
                      description="Browser devices appear here after notification permission is granted."
                    />
                  )}
                </>
              )}
              <footer>
                <Action onClick={() => setChannel(null)}>Done</Action>
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
}: {
  icon: ReactNode;
  title: string;
  status: string;
  onClick?: () => void;
  disabled?: boolean;
}) {
  return (
    <button className="personal-channel" disabled={disabled} onClick={onClick}>
      <span className="personal-channel-icon">{icon}</span>
      <span>
        <strong>{title}</strong>
        <small>{status}</small>
      </span>
      <ChevronDown size={14} />
    </button>
  );
}

function CodeReviews({ values, setValue, text }: PersonalProps) {
  return (
    <>
      <PersonalPageTitle description="Review GitHub pull requests and agent code diffs in Flow">
        {text.codeReviews}
      </PersonalPageTitle>
      <PersonalSection>
        <PersonalRow
          title="Enable code reviews"
          description="Review GitHub pull requests, accessible from the sidebar"
        >
          <SettingsToggle
            label="Enable code reviews"
            checked={Boolean(values.codeReviewsEnabled)}
            onChange={(v) => setValue("codeReviewsEnabled", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Auto-convert draft pull requests"
          description="Automatically mark your drafts as ready upon approval or requesting a review"
        >
          <SettingsToggle
            label="Auto-convert draft pull requests"
            checked={Boolean(values.autoConvertDrafts)}
            onChange={(v) => setValue("autoConvertDrafts", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Merge strategy"
          description="Choose the default merge strategy for pull requests"
        >
          <PersonalSelect
            label="Merge strategy"
            value={String(values.mergeStrategy)}
            options={["Squash and merge", "Merge commit", "Rebase and merge"]}
            onChange={(v) => setValue("mergeStrategy", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection>
        <PersonalRow
          title="Code theme"
          description="Select the syntax highlighting theme used in code diffs and viewers"
        >
          <PersonalSelect
            label="Code theme"
            value={String(values.codeTheme)}
            options={["Flow Light", "Flow Dark", "GitHub Light", "GitHub Dark"]}
            onChange={(v) => setValue("codeTheme", v)}
          />
        </PersonalRow>
        <PersonalRow title="Font">
          <PersonalSelect
            label="Code font"
            value={String(values.codeFont)}
            options={[
              "12px, Regular, Default",
              "13px, Regular, Default",
              "14px, Regular, Default",
            ]}
            onChange={(v) => setValue("codeFont", v)}
          />
        </PersonalRow>
        <pre className="personal-code-preview">
          <code>
            <span>const</span> config = {"{"}\n apiUrl:{" "}
            <i>"https://api.example.com"</i>,\n timeout: <b>5000</b>,\n debug:{" "}
            <em>true</em>\n{"}"};
          </code>
        </pre>
      </PersonalSection>
      <PersonalSection
        title="Notifications"
        description="Choose which review activity appears in your Flow inbox and push notifications"
      >
        <PersonalRow
          title="Comments & reviews"
          description="Comments, mentions, and submitted reviews"
        >
          <PersonalSelect
            label="Comments & reviews"
            value={String(values.reviewCommentsFilter)}
            options={["All", "Exclude Bots", "Mentions only"]}
            onChange={(v) => setValue("reviewCommentsFilter", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Review requests"
          description="Requests for your personal review"
        >
          <SettingsToggle
            label="Review requests"
            checked={Boolean(values.reviewRequests)}
            onChange={(v) => setValue("reviewRequests", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="GitHub team review requests"
          description="Requests for review from your GitHub teams with 10 or fewer members"
        >
          <SettingsToggle
            label="GitHub team review requests"
            checked={Boolean(values.githubTeamReviewRequests)}
            onChange={(v) => setValue("githubTeamReviewRequests", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="Checks & merge queue"
          description="Check failures and merge queue updates"
        >
          <SettingsToggle
            label="Checks & merge queue"
            checked={Boolean(values.checksMergeQueue)}
            onChange={(v) => setValue("checksMergeQueue", v)}
          />
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title="Signed commits">
        <PersonalRow
          title="Require signed commits"
          description="Users must upload a signing key before starting a coding session"
        >
          <SettingsToggle
            label="Require signed commits"
            checked={Boolean(values.requireSignedCommits)}
            onChange={(v) => setValue("requireSignedCommits", v)}
            disabled
          />
        </PersonalRow>
        <PersonalRow title="No signing key added">
          <Action disabled>Add key</Action>
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title="External tools">
        <PersonalRow
          title="Configure coding tools"
          description="Configure the external coding tools you can open issues in"
        >
          <ExternalLink size={16} />
        </PersonalRow>
        <PersonalRow
          title="Git attachment format"
          description="The format of GitHub/GitLab attachments on issues"
        >
          <PersonalSelect
            label="Git attachment format"
            value={String(values.gitAttachmentFormat)}
            options={["Title", "URL", "Title and URL"]}
            onChange={(v) => setValue("gitAttachmentFormat", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="On git branch copy, move issue to started status"
          description="After copying the git branch name, issue status is moved to the team’s first started workflow status."
        >
          <SettingsToggle
            label="On git branch copy, move issue to started status"
            checked={Boolean(values.gitBranchMoveStarted)}
            onChange={(v) => setValue("gitBranchMoveStarted", v)}
          />
        </PersonalRow>
        <PersonalRow
          title="On open in coding tool, move issue to started status"
          description="After opening an issue in a coding tool or copying as prompt, issue status is moved to the team’s first started workflow status."
        >
          <SettingsToggle
            label="On open in coding tool, move issue to started status"
            checked={Boolean(values.codingToolMoveStarted)}
            onChange={(v) => setValue("codingToolMoveStarted", v)}
          />
        </PersonalRow>
      </PersonalSection>
    </>
  );
}

function Security({ data, onNavigate, onReload, text }: PersonalProps) {
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
            : "Could not load security information",
        ),
      );
  }, []);
  const other = sessions.filter((item) => !item.current);
  const revoke = async () => {
    try {
      await revokeOtherAccountSessions();
      setSessions(await fetchAccountSessions());
      setConfirmOpen(false);
      toast.success("Other sessions revoked");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not revoke sessions",
      );
    }
  };
  return (
    <>
      <PersonalPageTitle>{text.security}</PersonalPageTitle>
      <PersonalSection
        title="Sessions"
        description="Devices logged into your account"
      >
        {sessions
          .filter((item) => item.current)
          .map((item) => (
            <PersonalRow
              key={item.id}
              icon={<Globe />}
              title="Current session"
              description={`Last active ${formatDate(item.lastSeenAt, { dateStyle: "medium", timeStyle: "short" })} · expires ${formatDate(item.expiresAt, { dateStyle: "medium", timeStyle: "short" })}`}
            />
          ))}
      </PersonalSection>
      {other.length > 0 && (
        <PersonalSection
          title={`${other.length} other session${other.length === 1 ? "" : "s"}`}
        >
          <div className="personal-section-action">
            <Action danger onClick={() => setConfirmOpen(true)}>
              Revoke all
            </Action>
          </div>
          {other.map((item) => (
            <PersonalRow
              key={item.id}
              icon={<Laptop />}
              title="Signed-in session"
              description={`Last active ${formatDate(item.lastSeenAt, { dateStyle: "medium", timeStyle: "short" })}`}
            >
              <Action
                danger
                onClick={() =>
                  void revokeAccountSession(item.id).then(() =>
                    fetchAccountSessions().then(setSessions),
                  )
                }
              >
                Revoke
              </Action>
            </PersonalRow>
          ))}
        </PersonalSection>
      )}
      <PersonalSection
        title="Passkeys"
        description="Passkeys are a secure way to sign in to your Flow account"
      >
        <div className="personal-empty">
          <KeyRound />
          <h3>No passkeys registered</h3>
          <span>Passkey enrollment is not available on this server.</span>
        </div>
      </PersonalSection>
      <PersonalSection
        title="Personal API keys"
        description="Use Flow’s API to build your own integrations"
      >
        <div className="personal-empty">
          <Code2 />
          <h3>
            {data.apiKeys.filter(
              (k) => k.creatorId === data.viewer.id && !k.revokedAt,
            ).length
              ? `${data.apiKeys.filter((k) => k.creatorId === data.viewer.id && !k.revokedAt).length} active API key`
              : "No API keys created"}
          </h3>
          <Action onClick={() => onNavigate("api")}>Manage API keys</Action>
        </div>
      </PersonalSection>
      <PersonalSection
        title="Commit signing key"
        description="Coding sessions use this key to sign your commits"
      >
        <PersonalRow title="No signing key added">
          <Action disabled>Add key</Action>
        </PersonalRow>
      </PersonalSection>
      <PersonalSection title="Authorized applications">
        {data.oauthAuthorizations
          .filter((item) => item.userId === data.viewer.id && !item.revokedAt)
          .map((item) => (
            <PersonalRow
              key={item.id}
              icon={<ShieldCheck />}
              title={item.clientName}
              description={`MCP · ${item.scopes.join(", ")}`}
            >
              <Action
                danger
                onClick={() =>
                  void revokeOAuthAuthorization(item.id).then(onReload)
                }
              >
                Revoke
              </Action>
            </PersonalRow>
          ))}
        {!data.oauthAuthorizations.some(
          (item) => item.userId === data.viewer.id && !item.revokedAt,
        ) && (
          <div className="personal-empty">
            <ShieldCheck />
            <h3>No authorized applications</h3>
          </div>
        )}
      </PersonalSection>
      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent className="personal-dialog">
          <DialogTitle>Revoke all other sessions?</DialogTitle>
          <p>Every other device will need to sign in again.</p>
          <footer>
            <Action onClick={() => setConfirmOpen(false)}>Cancel</Action>
            <Action danger onClick={() => void revoke()}>
              Revoke all
            </Action>
          </footer>
        </DialogContent>
      </Dialog>
    </>
  );
}

function Connections({ data, onNavigate, text }: PersonalProps) {
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
            : "Could not load connected accounts",
        ),
      );
  }, []);
  const entries = [
    {
      provider: "slack",
      name: "Slack",
      description:
        "Sync your message attribution, and receive notifications in Slack",
      icon: <MessageCircle />,
    },
    {
      provider: "google-calendar",
      name: "Google Calendar",
      description: "Sync your calendar out-of-office status to Flow",
      icon: <CalendarDays />,
    },
    {
      provider: "notion",
      name: "Notion",
      description: "Preview issues, projects, and views within Notion",
      icon: <MessageSquare />,
    },
    {
      provider: "github",
      name: "GitHub",
      description:
        "Review code in Flow and sync attribution of your git-related actions",
      icon: <GitFork />,
    },
  ];
  return (
    <>
      <PersonalPageTitle description="Connect your user accounts to sync attribution of your actions between apps">
        {text.connections}
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
                  {connection ? "Connected" : "Connect"}
                </Action>
              </PersonalRow>
            </div>
          );
        })}
      </div>
      <PersonalSection
        title="Sign-in identities"
        description="Identity provider accounts connected to your Flow profile"
      >
        {identities.map((item) => (
          <PersonalRow
            key={item.id}
            icon={<ShieldCheck />}
            title={item.username || item.subject}
            description={`${item.provider.toUpperCase()} · ${item.issuer} · last used ${formatDate(item.lastLoginAt, { dateStyle: "medium" })}`}
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
                        : "Could not unlink identity",
                    ),
                  )
              }
            >
              Unlink
            </Action>
          </PersonalRow>
        ))}
        {!identities.length && (
          <div className="personal-empty">
            <ShieldCheck />
            <h3>No sign-in identities connected</h3>
          </div>
        )}
      </PersonalSection>
    </>
  );
}

function Agents({ data, values, setValue, onNavigate, text }: PersonalProps) {
  const { t } = useI18n();
  const [draft, setDraft] = useState(String(values.agentInstructions || ""));
  const dirty = draft !== String(values.agentInstructions || "");
  return (
    <>
      <PersonalPageTitle
        description={t("Your personal settings for Flow Agent")}
      >
        {text.agents}
      </PersonalPageTitle>
      <PersonalSection
        title={t("Guidance")}
        description={t(
          "Provide personal instructions and context for Flow Agent when responding to conversations",
        )}
      >
        <textarea
          className="personal-agent-guidance"
          aria-label={t("AI prompt rules")}
          placeholder={t("Enter personal guidance for Flow Agent (optional)…")}
          maxLength={4000}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => {
            if (dirty) setValue("agentInstructions", draft);
          }}
        />
      </PersonalSection>
      <PersonalSection
        title={t("Skills")}
        description={t(
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
              {t(
                data.agentSkills.length ? "Create skill" : "No skills created",
              )}
            </span>
            <Plus />
          </NavLink>
        </div>
      </PersonalSection>
      <PersonalSection
        title={t("MCP connectors")}
        description={t(
          "Add MCP connectors for use with Flow Agent. Workspace admins can manage available connectors in security settings.",
        )}
      >
        <PersonalRow title={t("Agent MCP access disabled in this workspace")}>
          <Action onClick={() => onNavigate("security")}>
            {t("Configure")}
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
function isBusinessName(value: string) {
  return /Flow|GitHub|GitLab|Slack|Notion|Google Calendar|MCP/.test(value);
}
