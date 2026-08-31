import {
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  Activity,
  AppWindow,
  ArrowLeft,
  Bell,
  Bot,
  Braces,
  Building2,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleDot,
  Code2,
  CreditCard,
  FileText,
  Flame,
  Gauge,
  FileClock,
  Import,
  Upload,
  KeyRound,
  LayoutTemplate,
  Link2,
  ListFilter,
  MessageCircleQuestion,
  MoreHorizontal,
  PanelTop,
  Plug,
  Plus,
  Radio,
  Rocket,
  Search,
  ShieldCheck,
  SlidersHorizontal,
  Smile,
  Sparkles,
  Tag,
  Trash2,
  UserRound,
  UsersRound,
  X,
  Zap,
  type LucideIcon,
} from "lucide-react";
import { toast } from "sonner";
import { useI18n } from "@/i18n/i18n";

import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import {
  connectIntegration,
  createAgentSkill,
  createAPIKey,
  createIdentityProvider,
  createOAuthApplication,
  createWebhook,
  deleteIdentityProvider,
  deleteOAuthApplication,
  deleteWebhook,
  disconnectIntegration,
  fetchWorkspaceUsage,
  getPaidSubscription,
  listUsageAlerts,
  revokeAPIKey,
  revokeOAuthAuthorization,
  inviteMembers,
  removeMember,
  resumeMember,
  resendInvitation,
  revokeInvitation,
  setTeamMembership,
  suspendMember,
  updateAgentSkill,
  updateIdentityProvider,
  updateMemberRole,
  updateMemberIdentity,
  updateOAuthApplication,
  updateUserSettings,
  updateWebhook,
  updateWorkspacePreferences,
  uploadWorkspaceLogo,
  deleteWorkspaceLogo,
  verifyIdentityProvider,
} from "@/lib/api";
import type { SettingsPageId, TeamSettingsSection } from "@/lib/app-routes";
import type {
  BootstrapData,
  IdentityProvider,
  IssueTemplate,
  OAuthApplication,
  PaidSubscription,
  ProjectTemplate,
  ReleasePipeline,
  Team,
  UsageAlert,
  UserSettings,
  Webhook,
  WorkspaceMutationInput,
  WorkspaceMember,
  WorkspaceSettings,
} from "@/types/flow";
import { lazyPage } from "@/lib/lazy-page";
import {
  SettingsPageTitle as PageTitle,
  SettingsRow as Row,
  SettingsSection as Section,
  SettingsSelect as Select,
  SettingsToggle as Toggle,
} from "./settings-primitives";

import "./settings.css";
import "./workflow-settings.css";
import "./advanced-settings.css";
import { applyTheme } from "@/lib/theme";
import { workspaceRegionLabel } from "@/components/workspace/workspace-regions";
import { SidebarCustomization } from "@/components/layout/sidebar";
import { useSidebarCustomizationState } from "@/components/layout/sidebar-customization-state";

const TeamWorkflowSettings = lazyPage(
  () => import("./team-workflow-settings"),
  "TeamWorkflowSettings",
);
const ImportExportSettings = lazyPage(
  () => import("./advanced-settings"),
  "ImportExportSettings",
);
const ProjectUpdateSettings = lazyPage(
  () => import("./issues-projects-settings"),
  "ProjectUpdateSettings",
);
const SLASettings = lazyPage(
  () => import("./issues-projects-settings"),
  "SLASettings",
);
const TemplateSettings = lazyPage(
  () => import("./issues-projects-settings"),
  "TemplateSettings",
);
const DomainLabelsSettings = lazyPage(
  () => import("./domain-settings"),
  "DomainLabelsSettings",
);
const ProjectStatusesSettings = lazyPage(
  () => import("./domain-settings"),
  "ProjectStatusesSettings",
);
const FeatureSettingsPage = lazyPage(
  () => import("./feature-settings"),
  "FeatureSettingsPage",
);
const PersonalSettings = lazyPage(
  () => import("./personal-settings"),
  "PersonalSettings",
);
const PipelineEditorPage = lazyPage(
  () => import("@/components/releases/pipeline-editor-page"),
  "PipelineEditorPage",
);
const CodeIntegrationSettings = lazyPage(
  () => import("./code-integration-settings"),
  "CodeIntegrationSettings",
);
const AuditLogSettings = lazyPage(
  () => import("./audit-log-settings"),
  "AuditLogSettings",
);
const WorkflowAutomationSettings = lazyPage(
  () => import("./workflow-automation-settings"),
  "WorkflowAutomationSettings",
);

type StoredSettings = {
  values: Record<string, string | boolean>;
  lists: Record<string, SettingListItem[]>;
};
type SettingListItem = {
  id: string;
  name: string;
  description?: string;
  color?: string;
};

type SettingsPageProps = {
  data: BootstrapData;
  page: SettingsPageId;
  teamKey?: string;
  teamSection?: TeamSettingsSection;
  releasePipelineMode?: "new" | "edit";
  releasePipelineSlug?: string;
  integrationProvider?: "github" | "gitlab";
  issueTemplateMode?: "new" | "new-form" | "edit";
  issueTemplateId?: string;
  projectTemplateMode?: "new" | "edit";
  projectTemplateId?: string;
  agentSkillMode?: "new" | "edit";
  agentSkillId?: string;
  onBack: () => void;
  onNavigate: (
    page: SettingsPageId,
    teamKey?: string,
    teamSection?: TeamSettingsSection,
  ) => void;
  onCreateReleasePipeline: () => void;
  onOpenReleasePipeline: (pipeline: ReleasePipeline) => void;
  onOpenIntegration: (provider: "github" | "gitlab") => void;
  onCreateIssueTemplate: (form: boolean) => void;
  onOpenIssueTemplate: (template: IssueTemplate) => void;
  onDuplicateIssueTemplate: (template: IssueTemplate) => void;
  onCreateProjectTemplate: () => void;
  onOpenProjectTemplate: (template: ProjectTemplate) => void;
  onDuplicateProjectTemplate: (template: ProjectTemplate) => void;
  onCreateTeam: () => void;
  onWorkspaceUpdate: (input: WorkspaceMutationInput) => Promise<void>;
  onWorkspaceDelete: () => Promise<void>;
  onSettingsUpdate: (
    settings: Record<string, unknown>,
  ) => Promise<Record<string, unknown>>;
  onReload: () => Promise<void>;
};

type NavItem = { id: SettingsPageId; label: string; icon: LucideIcon };
const NAV: { title: string; items: NavItem[] }[] = [
  {
    title: "Personal",
    items: [
      { id: "preferences", label: "Preferences", icon: SlidersHorizontal },
      { id: "profile", label: "Profile", icon: UserRound },
      { id: "notifications", label: "Notifications", icon: Bell },
      { id: "code-and-reviews", label: "Code & reviews", icon: Code2 },
      { id: "account-security", label: "Security & access", icon: KeyRound },
      { id: "connections", label: "Connected accounts", icon: Link2 },
      { id: "agents", label: "Agent personalization", icon: Bot },
    ],
  },
  {
    title: "Issues",
    items: [
      { id: "issue-labels", label: "Labels", icon: Tag },
      { id: "issue-templates", label: "Templates", icon: LayoutTemplate },
      { id: "sla", label: "SLAs", icon: Flame },
    ],
  },
  {
    title: "Projects",
    items: [
      { id: "project-labels", label: "Labels", icon: Tag },
      { id: "project-templates", label: "Templates", icon: PanelTop },
      { id: "project-statuses", label: "Statuses", icon: CircleDot },
      { id: "project-updates", label: "Updates", icon: Activity },
    ],
  },
  {
    title: "Features",
    items: [
      { id: "ai", label: "AI & Agents", icon: Sparkles },
      { id: "initiatives", label: "Initiatives", icon: Zap },
      { id: "documents", label: "Documents", icon: FileText },
      { id: "customer-requests", label: "Customer requests", icon: UsersRound },
      { id: "releases", label: "Releases", icon: Rocket },
      { id: "pulse", label: "Pulse", icon: Radio },
      { id: "asks", label: "Asks", icon: MessageCircleQuestion },
      { id: "emojis", label: "Emojis", icon: Smile },
      { id: "integrations", label: "Integrations", icon: Plug },
    ],
  },
  {
    title: "Administration",
    items: [
      { id: "workspace", label: "Workspace", icon: Building2 },
      { id: "teams", label: "Teams", icon: UsersRound },
      { id: "members", label: "Members", icon: UserRound },
      { id: "security", label: "Security", icon: ShieldCheck },
      { id: "audit-log", label: "Audit log", icon: FileClock },
      { id: "api", label: "API", icon: Braces },
      { id: "applications", label: "Applications", icon: AppWindow },
      { id: "billing", label: "Billing", icon: CreditCard },
      { id: "usage", label: "Usage & limits", icon: Gauge },
      { id: "import-export", label: "Import & export", icon: Import },
    ],
  },
];

const DEFAULT_VALUES: StoredSettings["values"] = {
  homeView: "Flow Agent (default)",
  displayNames: "Full name",
  firstDay: "Monday",
  emoticons: true,
  sendComments: "Enter",
  fontSize: "Default",
  pointerCursor: false,
  underlineLinks: false,
  disableAnimatedImages: false,
  interfaceTheme: "System preference",
  lightTheme: "Light",
  darkTheme: "Dark",
  desktopLinks: false,
  autoAssign: false,
  assignStarted: false,
  notificationEmail: true,
  notificationDesktop: true,
  notificationSound: true,
  notificationDigest: "Daily",
  reviewAutoAssign: true,
  branchFormat: "{identifier}-{title}",
  codeReviewsEnabled: true,
  autoConvertDrafts: false,
  mergeStrategy: "Squash and merge",
  codeTheme: "Flow Light",
  codeFont: "12px, Regular, Default",
  reviewCommentsFilter: "Exclude Bots",
  reviewRequests: true,
  githubTeamReviewRequests: true,
  checksMergeQueue: true,
  requireSignedCommits: false,
  gitAttachmentFormat: "Title",
  gitBranchMoveStarted: true,
  codingToolMoveStarted: true,
  changelogUpdates: true,
  changelogNewsletter: false,
  marketingUpdates: false,
  inviteAcceptedUpdates: true,
  privacyUpdates: true,
  dpaUpdates: false,
  passkeys: false,
  agentEnabled: true,
  agentInstructions: "",
};

export function SettingsPage(props: SettingsPageProps) {
  const [query, setQuery] = useState("");
  const [mobileNav, setMobileNav] = useState(false);
  const [sidebarCustomizationOpen, setSidebarCustomizationOpen] =
    useState(false);
  const sidebarCustomization = useSidebarCustomizationState();
  const [settings, setSettings] = useUserStoredSettings(props.data);
  const isAdmin = props.data.viewerRole === "admin";
  const visible = useMemo(
    () =>
      NAV.map((section) => ({
        ...section,
        items: section.items.filter(
          (item) =>
            (item.id !== "audit-log" ||
              (isAdmin &&
                props.data.workspaceSettings.plan === "enterprise")) &&
            (isAdmin ||
              section.title === "Personal" ||
              memberCanManage(item.id, props.data.workspaceSettings)) &&
            item.label.toLowerCase().includes(query.toLowerCase()),
        ),
      })).filter((section) => section.items.length),
    [isAdmin, props.data.workspaceSettings, query],
  );
  const setValue = (key: string, value: string | boolean) => {
    if (Object.is(settings.values[key], value)) return;
    setSettings((current) => ({
      ...current,
      values: { ...current.values, [key]: value },
    }));
  };
  return (
    <>
      <div className="settings-app">
        <aside className={`settings-sidebar${mobileNav ? " open" : ""}`}>
          <button className="settings-back" onClick={props.onBack}>
            <ArrowLeft size={16} />
            Back to app
          </button>
          <label className="settings-search">
            <Search size={15} />
            <input
              aria-label="Search settings"
              placeholder="Search…"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            {query && (
              <button aria-label="Clear search" onClick={() => setQuery("")}>
                <X size={13} />
              </button>
            )}
          </label>
          <nav aria-label="Settings navigation">
            {visible.map((section) => (
              <section key={section.title}>
                <h2>{section.title}</h2>
                {section.items.map((item) => (
                  <SettingsNavButton
                    key={item.id}
                    item={item}
                    active={props.page === item.id}
                    onClick={() => {
                      props.onNavigate(item.id);
                      setMobileNav(false);
                    }}
                  />
                ))}
              </section>
            ))}
            {!query && props.data.viewerRole !== "guest" && (
              <section>
                <h2>Your teams</h2>
                {props.data.teams
                  .filter(
                    (team) =>
                      isAdmin ||
                      props.data.teamMembers.some(
                        (member) =>
                          member.teamId === team.id &&
                          member.userId === props.data.viewer.id &&
                          member.role === "owner",
                      ),
                  )
                  .map((team) => (
                    <button
                      key={team.id}
                      className={
                        props.page === "team" &&
                        props.teamKey?.toLowerCase() === team.key.toLowerCase()
                          ? "active"
                          : ""
                      }
                      onClick={() => props.onNavigate("team", team.key)}
                    >
                      <span
                        className="settings-team-icon"
                        style={{ color: team.color }}
                      >
                        <ViewGlyph
                          color={team.color}
                          icon={team.icon || "Team"}
                        />
                      </span>
                      <span>{team.name}</span>
                    </button>
                  ))}
                {isAdmin && (
                  <button onClick={props.onCreateTeam}>
                    <Plus size={16} />
                    <span>Create a team</span>
                  </button>
                )}
              </section>
            )}
          </nav>
        </aside>
        <button
          className={`settings-sidebar-scrim${mobileNav ? " open" : ""}`}
          aria-label="Close settings navigation"
          onClick={() => setMobileNav(false)}
        />
        <main className="settings-main">
          <button
            className="settings-mobile-menu"
            onClick={() => setMobileNav(true)}
          >
            <ListFilter size={15} />
          </button>
          <div className="settings-content">
            <Suspense fallback={<SettingsPageFallback />}>
              <SettingsBody
                {...props}
                settings={settings}
                setSettings={setSettings}
                setValue={setValue}
                onCustomizeSidebar={() => setSidebarCustomizationOpen(true)}
              />
            </Suspense>
          </div>
        </main>
      </div>
      <SidebarCustomization
        open={sidebarCustomizationOpen}
        onOpenChange={setSidebarCustomizationOpen}
        preferences={sidebarCustomization.preferences}
        order={sidebarCustomization.order}
        badgeStyle={sidebarCustomization.badgeStyle}
        onBadgeStyleChange={sidebarCustomization.setBadgeStyle}
        onChange={sidebarCustomization.setPreferences}
        onReorder={sidebarCustomization.reorder}
      />
    </>
  );
}

function SettingsPageFallback() {
  return (
    <div className="settings-loading" aria-label="Loading settings">
      <span />
      <span />
      <span />
    </div>
  );
}

function SettingsNavButton({
  item,
  active,
  onClick,
}: {
  item: NavItem;
  active: boolean;
  onClick: () => void;
}) {
  const Icon = item.icon;
  return (
    <button className={active ? "active" : ""} onClick={onClick}>
      <Icon size={16} />
      <span>{item.label}</span>
    </button>
  );
}

function SettingsBody(
  props: SettingsPageProps & {
    settings: StoredSettings;
    setSettings: React.Dispatch<React.SetStateAction<StoredSettings>>;
    setValue: (key: string, value: string | boolean) => void;
    onCustomizeSidebar: () => void;
  },
) {
  const { t } = useI18n();
  const { page } = props;
  const personal = [
    "preferences",
    "profile",
    "notifications",
    "code-and-reviews",
    "account-security",
    "connections",
    "agents",
  ].includes(page);
  const teamOwner =
    page === "team" &&
    props.data.teams.some(
      (team) =>
        team.key.toLowerCase() === props.teamKey?.toLowerCase() &&
        props.data.teamMembers.some(
          (member) =>
            member.teamId === team.id &&
            member.userId === props.data.viewer.id &&
            member.role === "owner",
        ),
    );
  if (
    !personal &&
    props.data.viewerRole !== "admin" &&
    !teamOwner &&
    !memberCanManage(page, props.data.workspaceSettings)
  )
    return (
      <div className="settings-empty">
        <ShieldCheck size={28} />
        <h3>Admin access required</h3>
        <p>You don't have permission to manage this workspace setting.</p>
      </div>
    );
  if (
    page === "audit-log" &&
    (props.data.viewerRole !== "admin" ||
      props.data.workspaceSettings.plan !== "enterprise")
  )
    return (
      <div className="settings-not-found">
        <strong>{t("Not found")}</strong>
        <span>{t("We could not find the page you were looking for")}</span>
      </div>
    );
  if (props.agentSkillMode)
    return (
      <AgentSkillEditor
        data={props.data}
        id={props.agentSkillId}
        mode={props.agentSkillMode}
        onCancel={() => props.onNavigate("agents")}
        onReload={props.onReload}
      />
    );
  if (personal)
    return (
      <PersonalSettings
        page={page}
        data={props.data}
        values={props.settings.values}
        setValue={props.setValue}
        onNavigate={props.onNavigate}
        onReload={props.onReload}
        onBack={props.onBack}
        onCustomizeSidebar={props.onCustomizeSidebar}
      />
    );
  if (page === "issue-labels")
    return (
      <DomainLabelsSettings
        data={props.data}
        resourceType="issue"
        onReload={props.onReload}
      />
    );
  if (page === "project-labels")
    return (
      <DomainLabelsSettings
        data={props.data}
        resourceType="project"
        onReload={props.onReload}
      />
    );
  if (page === "project-statuses")
    return (
      <ProjectStatusesSettings data={props.data} onReload={props.onReload} />
    );
  if (page === "issue-templates")
    return (
      <TemplateSettings
        data={props.data}
        type="issue"
        mode={props.issueTemplateMode}
        templateId={props.issueTemplateId}
        onCreateIssue={props.onCreateIssueTemplate}
        onOpenIssue={props.onOpenIssueTemplate}
        onDuplicateIssue={props.onDuplicateIssueTemplate}
        onNavigateList={() => props.onNavigate("issue-templates")}
        onReload={props.onReload}
      />
    );
  if (page === "project-templates")
    return (
      <TemplateSettings
        data={props.data}
        type="project"
        mode={props.projectTemplateMode}
        templateId={props.projectTemplateId}
        onCreateProject={props.onCreateProjectTemplate}
        onOpenProject={props.onOpenProjectTemplate}
        onDuplicateProject={props.onDuplicateProjectTemplate}
        onNavigateList={() => props.onNavigate("project-templates")}
        onReload={props.onReload}
      />
    );
  if (page === "sla")
    return <SLASettings data={props.data} onReload={props.onReload} />;
  if (page === "project-updates")
    return (
      <ProjectUpdateSettings data={props.data} onReload={props.onReload} />
    );
  if (page === "workspace") return <WorkspacePage {...props} />;
  if (page === "teams")
    return (
      <TeamsPage
        data={props.data}
        onCreate={props.onCreateTeam}
        onOpen={(team) => props.onNavigate("team", team.key)}
      />
    );
  if (page === "members")
    return <MembersPageV2 data={props.data} onReload={props.onReload} />;
  if (page === "audit-log") return <AuditLogSettings data={props.data} />;
  if (page === "api")
    return <ApiPage data={props.data} onReload={props.onReload} />;
  if (page === "applications")
    return <ApplicationsPage data={props.data} onReload={props.onReload} />;
  if (page === "billing")
    return <BillingPage data={props.data} onReload={props.onReload} />;
  if (page === "usage") return <UsagePage data={props.data} />;
  if (page === "import-export")
    return <ImportExportSettings data={props.data} onReload={props.onReload} />;
  if (page === "workflows")
    return <WorkflowAutomationSettings data={props.data} />;
  if (page === "releases" && props.releasePipelineMode) {
    const pipeline =
      props.releasePipelineMode === "edit"
        ? props.data.releasePipelines.find(
            (item) => item.slugId === props.releasePipelineSlug,
          )
        : undefined;
    if (props.releasePipelineMode === "edit" && !pipeline)
      return (
        <div className="settings-empty">
          <h3>{t("Release pipeline not found")}</h3>
        </div>
      );
    return (
      <PipelineEditorPage
        data={props.data}
        pipeline={pipeline}
        onCancel={() => props.onNavigate("releases")}
        onSaved={async () => {
          await props.onReload();
          props.onNavigate("releases");
        }}
      />
    );
  }
  if (page === "integrations" && props.integrationProvider)
    return (
      <CodeIntegrationSettings
        provider={props.integrationProvider}
        data={props.data}
        onBack={() => props.onNavigate("integrations")}
        onReload={props.onReload}
      />
    );
  if (page === "team") {
    const team = props.data.teams.find(
      (team) => team.key.toLowerCase() === props.teamKey?.toLowerCase(),
    );
    return team ? (
      <TeamWorkflowSettings
        data={props.data}
        team={team}
        section={props.teamSection ?? "overview"}
        onNavigate={(section) => props.onNavigate("team", team.key, section)}
        onReload={props.onReload}
      />
    ) : (
      <div className="settings-empty">
        <h3>Team not found</h3>
      </div>
    );
  }
  if (page === "security")
    return (
      <>
        <SecurityPage data={props.data} onReload={props.onReload} />
        <SecuritySupplement
          data={props.data}
          onNavigate={props.onNavigate}
          onReload={props.onReload}
        />
      </>
    );
  if (
    [
      "ai",
      "initiatives",
      "documents",
      "customer-requests",
      "releases",
      "pulse",
      "asks",
      "emojis",
      "integrations",
    ].includes(page)
  )
    return (
      <FeatureSettingsPage
        page={
          page as
            | "ai"
            | "initiatives"
            | "documents"
            | "customer-requests"
            | "releases"
            | "pulse"
            | "asks"
            | "emojis"
            | "integrations"
        }
        data={props.data}
        onCreateReleasePipeline={props.onCreateReleasePipeline}
        onOpenReleasePipeline={props.onOpenReleasePipeline}
        onOpenIntegration={props.onOpenIntegration}
        onReload={props.onReload}
      />
    );
  return (
    <FeaturePage page={page} data={props.data} onReload={props.onReload} />
  );
}

function AgentSkillEditor({
  data,
  id,
  mode,
  onCancel,
  onReload,
}: {
  data: BootstrapData;
  id?: string;
  mode: "new" | "edit";
  onCancel: () => void;
  onReload: () => Promise<void>;
}) {
  const existing =
    mode === "edit"
      ? data.agentSkills.find((item) => item.id === id)
      : undefined;
  const [name, setName] = useState(existing?.name ?? ""),
    [instructions, setInstructions] = useState(existing?.instructions ?? ""),
    [saving, setSaving] = useState(false);
  const save = async () => {
    if (!name.trim() || !instructions.trim() || saving) return;
    setSaving(true);
    try {
      if (existing)
        await updateAgentSkill(existing.id, {
          name: name.trim(),
          instructions: instructions.trim(),
        });
      else
        await createAgentSkill({
          name: name.trim(),
          instructions: instructions.trim(),
        });
      await onReload();
      onCancel();
    } finally {
      setSaving(false);
    }
  };
  return (
    <div className="agent-skill-editor-page">
      <nav>
        <button onClick={onCancel}>Agent personalization</button>
        <span>›</span>
        <span>{existing ? existing.name : "New skill"}</span>
      </nav>
      <div
        className="agent-skill-name"
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-label="Skill name"
        data-placeholder="Skill name"
        onInput={(event) => setName(event.currentTarget.textContent ?? "")}
      >
        {name}
      </div>
      <div
        className="agent-skill-instructions"
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-label="Skill instructions"
        data-placeholder="Add instructions…"
        onInput={(event) =>
          setInstructions(event.currentTarget.textContent ?? "")
        }
      >
        {instructions}
      </div>
      <footer>
        <button onClick={onCancel}>Cancel</button>
        <button
          className="primary"
          disabled={!name.trim() || !instructions.trim() || saving}
          onClick={() => void save()}
        >
          {saving ? "Creating…" : existing ? "Save" : "Create"}
        </button>
      </footer>
    </div>
  );
}
function ActionButton({
  children,
  onClick,
  danger,
  primary,
  disabled,
}: {
  children: ReactNode;
  onClick?: () => void;
  danger?: boolean;
  primary?: boolean;
  disabled?: boolean;
}) {
  return (
    <button
      disabled={disabled}
      className={`settings-action${danger ? " danger" : ""}${primary ? " primary" : ""}`}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

function FieldRow({
  title,
  description,
  value,
  onCommit,
}: {
  title: string;
  description?: string;
  value: string;
  onCommit: (value: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return (
    <Row title={title} description={description}>
      <input
        className="settings-input"
        aria-label={title}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => {
          if (draft !== value) onCommit(draft);
        }}
      />
    </Row>
  );
}

function WorkspacePage(
  props: SettingsPageProps & {
    settings: StoredSettings;
    setSettings: React.Dispatch<React.SetStateAction<StoredSettings>>;
    setValue: (key: string, value: string | boolean) => void;
  },
) {
  const [name, setName] = useState(props.data.workspace.name);
  const [urlKey, setUrlKey] = useState(props.data.workspace.urlKey);
  const [confirm, setConfirm] = useState(false);
  const save = async (patch: WorkspaceMutationInput) => {
    const changed = Object.fromEntries(
      Object.entries(patch).filter(([key, value]) => {
        const persisted =
          props.data.workspace[key as keyof typeof props.data.workspace];
        return !Object.is(value, persisted);
      }),
    ) as WorkspaceMutationInput;
    if (!Object.keys(changed).length) return;
    await props.onWorkspaceUpdate(changed);
  };
  const savePreferences = async (patch: Partial<WorkspaceSettings>) => {
    const changed = Object.fromEntries(
      Object.entries(patch).filter(
        ([key, value]) =>
          !Object.is(
            value,
            props.data.workspaceSettings[key as keyof WorkspaceSettings],
          ),
      ),
    ) as Partial<WorkspaceSettings>;
    if (!Object.keys(changed).length) return;
    await updateWorkspacePreferences({
      ...props.data.workspaceSettings,
      ...changed,
    });
    await props.onReload();
  };
  return (
    <>
      <PageTitle>Workspace</PageTitle>
      <Section>
        <Row title="Logo" description="Recommended size is 256x256px">
          <WorkspaceLogoControl data={props.data} onReload={props.onReload} />
        </Row>
        <Row title="Name">
          <input
            className="settings-input"
            aria-label="Name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            onBlur={() => void save({ name })}
          />
        </Row>
        <Row title="URL">
          <div className="settings-url">
            <span>flow.app/</span>
            <input
              aria-label="URL"
              value={urlKey}
              onChange={(event) => setUrlKey(slug(event.target.value))}
              onBlur={() => void save({ urlKey })}
            />
          </div>
        </Row>
      </Section>
      <Section title="Time & region">
        <Row
          title="First month of the fiscal year"
          description="Used when grouping projects and issues quarterly, half-yearly, and yearly"
        >
          <Select
            label="First month of the fiscal year"
            value={props.data.workspaceSettings.fiscalMonth}
            options={[
              "January",
              "February",
              "March",
              "April",
              "May",
              "June",
              "July",
              "August",
              "September",
              "October",
              "November",
              "December",
            ]}
            onChange={(value) => void savePreferences({ fiscalMonth: value })}
          />
        </Row>
        <Row
          title="Region"
          description="Set when a workspace is created and cannot be changed."
        >
          <span className="settings-static">
            {workspaceRegionLabel(props.data.workspace.region)}
          </span>
        </Row>
      </Section>
      <Section title="Member onboarding">
        <FieldRow
          title="Welcome message"
          value={props.data.workspaceSettings.welcomeMessage ?? ""}
          onCommit={(value) => void savePreferences({ welcomeMessage: value })}
        />
        <Row title="Default home view">
          <Select
            label="Default home view"
            value={props.data.workspaceSettings.defaultHomeView ?? "agent"}
            options={[
              { value: "agent", label: "Flow Agent" },
              { value: "inbox", label: "Inbox" },
              { value: "my-issues", label: "My issues" },
              { value: "projects", label: "Projects" },
            ]}
            onChange={(value) =>
              void savePreferences({ defaultHomeView: value })
            }
          />
        </Row>
      </Section>
      <Section title="Danger zone">
        <Row
          danger
          title="Delete workspace"
          description="Schedule workspace to be permanently deleted"
        >
          <ActionButton danger onClick={() => setConfirm(true)}>
            Delete workspace
          </ActionButton>
        </Row>
      </Section>
      <ConfirmDialog
        open={confirm}
        title="Delete workspace?"
        description={`This permanently deletes ${props.data.workspace.name} and all of its data.`}
        confirm="Delete workspace"
        onCancel={() => setConfirm(false)}
        onConfirm={async () => {
          setConfirm(false);
          await props.onWorkspaceDelete();
        }}
      />
    </>
  );
}

function WorkspaceLogoControl({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const inputRef = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const upload = async (file?: File) => {
    if (!file || busy) return;
    setBusy(true);
    try {
      await uploadWorkspaceLogo(data.workspace.urlKey, file);
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("Could not upload workspace logo"),
      );
    } finally {
      setBusy(false);
      if (inputRef.current) inputRef.current.value = "";
    }
  };
  const remove = async () => {
    if (!data.workspace.logoUrl || busy) return;
    setBusy(true);
    try {
      await deleteWorkspaceLogo(data.workspace.urlKey);
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("Could not remove workspace logo"),
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="workspace-logo-control">
      <button
        aria-busy={busy}
        aria-label={t("Logo")}
        disabled={busy}
        onClick={() => inputRef.current?.click()}
        type="button"
      >
        {data.workspace.logoUrl ? (
          <img alt={t("Workspace logo")} src={data.workspace.logoUrl} />
        ) : (
          <span>{initials(data.workspace.name)}</span>
        )}
        <Upload />
      </button>
      <input
        ref={inputRef}
        aria-label={t("Logo")}
        hidden
        accept="image/png,.png,image/jpeg,.jpeg,image/webp,.webp,image/svg+xml,.svg"
        type="file"
        onChange={(event) => void upload(event.target.files?.[0])}
      />
      {data.workspace.logoUrl && (
        <button
          className="workspace-logo-remove"
          aria-label={t("Remove logo")}
          disabled={busy}
          onClick={() => void remove()}
          type="button"
        >
          <Trash2 />
        </button>
      )}
    </div>
  );
}
function ConfirmDialog({
  open,
  title,
  description,
  confirm,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  title: string;
  description: string;
  confirm: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={(value) => !value && onCancel()}>
      <DialogContent className="settings-confirm">
        <DialogTitle>{title}</DialogTitle>
        <p>{description}</p>
        <footer>
          <ActionButton onClick={onCancel}>Cancel</ActionButton>
          <ActionButton danger onClick={onConfirm}>
            {confirm}
          </ActionButton>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function download(filename: string, content: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

function TeamsPage({
  data,
  onCreate,
  onOpen,
}: {
  data: BootstrapData;
  onCreate: () => void;
  onOpen: (team: Team) => void;
}) {
  return (
    <>
      <PageTitle
        description="Teams organize issues, projects, cycles, and views."
        action={
          <ActionButton primary onClick={onCreate}>
            <Plus size={14} />
            New team
          </ActionButton>
        }
      >
        Teams
      </PageTitle>
      <Section>
        {data.teams.map((team) => (
          <button
            className="settings-team-row"
            key={team.id}
            onClick={() => onOpen(team)}
          >
            <span className="settings-team-icon" style={{ color: team.color }}>
              <ViewGlyph color={team.color} icon={team.icon || "Team"} />
            </span>
            <div>
              <strong>{team.name}</strong>
              <span>
                {team.key} ·{" "}
                {
                  data.issues.filter((issue) => issue.team.id === team.id)
                    .length
                }{" "}
                issues
              </span>
            </div>
            <ChevronDown size={14} />
          </button>
        ))}
      </Section>
    </>
  );
}
export function MembersPage({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("All");
  const [inviteOpen, setInviteOpen] = useState(false);
  const [emails, setEmails] = useState("");
  const [role, setRole] = useState<"admin" | "member" | "guest">("member");
  const [teamIds, setTeamIds] = useState<string[]>(
    data.teams[0] ? [data.teams[0].id] : [],
  );
  const [busy, setBusy] = useState(false);
  const members = data.members.filter(
    (member) =>
      (status === "All" || member.status === status.toLowerCase()) &&
      `${member.user.displayName} ${member.user.email}`
        .toLowerCase()
        .includes(query.toLowerCase()),
  );
  const pending = data.invitations.filter(
    (invitation) =>
      invitation.status === "pending" &&
      (status === "All" || status === "Invited") &&
      invitation.email.toLowerCase().includes(query.toLowerCase()),
  );
  const change = async (action: () => Promise<unknown>, success: string) => {
    setBusy(true);
    try {
      await action();
      await onReload();
      toast.success(success);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not update member",
      );
    } finally {
      setBusy(false);
    }
  };
  const exportCsv = () =>
    download(
      "members.csv",
      [
        "Name,Email,Status,Role",
        ...data.members.map(
          (member) =>
            `${member.user.displayName},${member.user.email},${member.status},${member.role}`,
        ),
      ].join("\n"),
      "text/csv",
    );
  const send = () =>
    change(async () => {
      const parsed = emails
        .split(/[\s,;]+/)
        .map((value) => value.trim())
        .filter(Boolean);
      if (!parsed.length) throw new Error("Enter at least one email address");
      if (role === "guest" && !teamIds.length)
        throw new Error("Guests must be assigned to a team");
      const invitations = await inviteMembers(data.workspace.urlKey, {
        emails: parsed,
        role,
        teamIds,
      });
      const token = invitations.find((item) => item.token)?.token;
      if (token)
        await navigator.clipboard?.writeText(
          `${location.origin}/invite/${token}`,
        );
      setInviteOpen(false);
      setEmails("");
    }, "Invitation sent");
  return (
    <>
      <PageTitle
        action={
          <div className="settings-header-actions">
            <ActionButton onClick={exportCsv}>Export CSV</ActionButton>
            <ActionButton primary onClick={() => setInviteOpen(true)}>
              <Plus size={14} />
              Invite
            </ActionButton>
          </div>
        }
      >
        Members
      </PageTitle>
      <div className="settings-members-toolbar">
        <label>
          <Search size={14} />
          <input
            aria-label="Search by name or email"
            placeholder="Search by name or email"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <Select
          label="Member status"
          value={status}
          options={["All", "Active", "Invited", "Suspended"]}
          onChange={setStatus}
        />
      </div>
      <div className="settings-member-groups">
        {members.length > 0 && (
          <section>
            <h3>
              {status === "Suspended" ? "Suspended" : "Active"}
              <span>{members.length}</span>
            </h3>
            <div className="settings-members-table">
              <header>
                <span>Name</span>
                <span>Email</span>
                <span>Role</span>
                <span>Teams</span>
                <span>Joined</span>
                <span />
              </header>
              {members.map((member) => {
                const memberships = data.teamMembers.filter(
                  (item) => item.userId === member.user.id,
                );
                return (
                  <div key={member.user.id}>
                    <span>
                      <b className="settings-member-avatar">
                        {initials(member.user.displayName)}
                      </b>
                      <i>
                        <strong>{member.user.displayName}</strong>
                        {member.user.id === data.viewer.id && (
                          <small>You</small>
                        )}
                      </i>
                    </span>
                    <span>{member.user.email}</span>
                    <span>
                      <Select
                        label={`Role for ${member.user.displayName}`}
                        value={title(member.role)}
                        options={["Admin", "Member", "Guest"]}
                        onChange={(value) =>
                          void change(
                            () =>
                              updateMemberRole(
                                data.workspace.urlKey,
                                member.user.id,
                                value.toLowerCase() as
                                  "admin" | "member" | "guest",
                              ),
                            "Role updated",
                          )
                        }
                      />
                    </span>
                    <span>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <button className="settings-member-teams">
                            {memberships.length
                              ? `${memberships.length} team${memberships.length === 1 ? "" : "s"}`
                              : "No teams"}
                            <ChevronDown size={12} />
                          </button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent
                          align="end"
                          className="settings-team-membership-menu"
                        >
                          {data.teams.map((team) => {
                            const membership = memberships.find(
                              (item) => item.teamId === team.id,
                            );
                            return (
                              <DropdownMenuCheckboxItem
                                key={team.id}
                                checked={Boolean(membership)}
                                onCheckedChange={(checked) =>
                                  void change(
                                    () =>
                                      setTeamMembership(
                                        data.workspace.urlKey,
                                        team.id,
                                        member.user.id,
                                        Boolean(checked),
                                        membership?.role ?? "member",
                                      ),
                                    "Team access updated",
                                  )
                                }
                              >
                                {team.name}
                              </DropdownMenuCheckboxItem>
                            );
                          })}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </span>
                    <span>{formatDate(member.joinedAt)}</span>
                    <span>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <button
                            className="settings-member-more"
                            aria-label={`Actions for ${member.user.displayName}`}
                          >
                            <MoreHorizontal size={15} />
                          </button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            disabled={member.user.id === data.viewer.id}
                            onSelect={() =>
                              void change(
                                () =>
                                  suspendMember(
                                    data.workspace.urlKey,
                                    member.user.id,
                                  ),
                                "Member suspended",
                              )
                            }
                          >
                            Suspend member
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            className="danger-item"
                            disabled={member.user.id === data.viewer.id}
                            onSelect={() =>
                              void change(
                                () =>
                                  removeMember(
                                    data.workspace.urlKey,
                                    member.user.id,
                                  ),
                                "Member removed",
                              )
                            }
                          >
                            Remove from workspace
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </span>
                  </div>
                );
              })}
            </div>
          </section>
        )}
        {pending.length > 0 && (
          <section>
            <h3>
              Invited<span>{pending.length}</span>
            </h3>
            <div className="settings-members-table pending">
              <header>
                <span>Email</span>
                <span>Role</span>
                <span>Teams</span>
                <span>Invited</span>
                <span />
              </header>
              {pending.map((invitation) => (
                <div key={invitation.id}>
                  <span>{invitation.email}</span>
                  <span>{title(invitation.role)}</span>
                  <span>
                    {invitation.teamIds.length} team
                    {invitation.teamIds.length === 1 ? "" : "s"}
                  </span>
                  <span>{formatDate(invitation.createdAt)}</span>
                  <span>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <button className="settings-member-more">
                          <MoreHorizontal size={15} />
                        </button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem
                          onSelect={() =>
                            void change(
                              () =>
                                resendInvitation(
                                  data.workspace.urlKey,
                                  invitation.id,
                                ),
                              "Invitation resent",
                            )
                          }
                        >
                          Resend invite
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          className="danger-item"
                          onSelect={() =>
                            void change(
                              () =>
                                revokeInvitation(
                                  data.workspace.urlKey,
                                  invitation.id,
                                ),
                              "Invitation revoked",
                            )
                          }
                        >
                          Revoke invite
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </span>
                </div>
              ))}
            </div>
          </section>
        )}
        {!members.length && !pending.length && (
          <div className="settings-empty compact">
            <UserRound size={24} />
            <h3>No members found</h3>
            <p>Try another name, email, or member status.</p>
          </div>
        )}
      </div>
      <Dialog open={inviteOpen} onOpenChange={setInviteOpen}>
        <DialogContent className="settings-invite-dialog">
          <DialogTitle>Invite to your workspace</DialogTitle>
          <p>Invite teammates to {data.workspace.name}.</p>
          <label>
            Email addresses
            <textarea
              autoFocus
              placeholder="name@company.com"
              value={emails}
              onChange={(event) => setEmails(event.target.value)}
            />
            <small>Separate multiple emails with commas or spaces.</small>
          </label>
          <div className="settings-invite-options">
            <label>
              Role
              <Select
                label="Invitation role"
                value={title(role)}
                options={["Member", "Admin", "Guest"]}
                onChange={(value) =>
                  setRole(value.toLowerCase() as "admin" | "member" | "guest")
                }
              />
            </label>
            <label>
              Teams
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button className="settings-select">
                    {teamIds.length
                      ? `${teamIds.length} selected`
                      : "Select teams"}
                    <ChevronDown size={13} />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  {data.teams.map((team) => (
                    <DropdownMenuCheckboxItem
                      key={team.id}
                      checked={teamIds.includes(team.id)}
                      onCheckedChange={(checked) =>
                        setTeamIds((current) =>
                          checked
                            ? [...new Set([...current, team.id])]
                            : current.filter((id) => id !== team.id),
                        )
                      }
                    >
                      {team.name}
                    </DropdownMenuCheckboxItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            </label>
          </div>
          <footer>
            <ActionButton onClick={() => setInviteOpen(false)}>
              Cancel
            </ActionButton>
            <ActionButton primary disabled={busy} onClick={send}>
              Send invites
            </ActionButton>
          </footer>
        </DialogContent>
      </Dialog>
    </>
  );
}

function SecuritySupplement({
  data,
  onNavigate,
  onReload,
}: {
  data: BootstrapData;
  onNavigate: (page: SettingsPageId) => void;
  onReload: () => Promise<void>;
}) {
  const settings = data.workspaceSettings;
  const save = async (patch: Partial<WorkspaceSettings>) => {
    try {
      await updateWorkspacePreferences({ ...settings, ...patch });
      await onReload();
      toast.success("Security policy saved");
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not save policy",
      );
    }
  };
  const enterprise = settings.plan === "enterprise";
  return (
    <>
      <Section title="File uploads">
        <Row
          title="Restrict file uploads"
          description="Restrict uploaded file types. Images and videos remain allowed."
        >
          <ActionButton disabled={!enterprise}>
            {enterprise ? "Configure" : "Available on Enterprise"}
          </ActionButton>
        </Row>
      </Section>
      <Section title="Application approvals">
        <Row
          title="Review third-party applications"
          description="Control which applications can be installed to your workspace"
        >
          <Toggle
            label="Review third-party applications"
            checked={Boolean(settings.reviewThirdPartyApplications)}
            onChange={(value) =>
              void save({ reviewThirdPartyApplications: value })
            }
          />
        </Row>
        <Row
          title="Reduce personal information from support integrations"
          description="Personal information from support integrations will not be stored"
        >
          <ActionButton disabled={!enterprise}>
            {enterprise ? "Configure" : "Available on Enterprise"}
          </ActionButton>
        </Row>
      </Section>
      <Section title="MCP connections">
        <Row
          title="Active MCP connections"
          description={`${data.oauthAuthorizations.filter((item) => !item.revokedAt).length} active connections across workspace members`}
        >
          <ActionButton onClick={() => onNavigate("applications")}>
            Review
          </ActionButton>
        </Row>
        <Row
          title="Allowed MCP connectors"
          description="Choose which MCP connectors Flow Agent can use"
        >
          <Select
            label="Allowed MCP connectors"
            value={
              (settings.allowedMcpConnectors ?? "all") === "all"
                ? "All connectors"
                : "Approved connectors"
            }
            options={["All connectors", "Approved connectors"]}
            onChange={(value) =>
              void save({
                allowedMcpConnectors:
                  value === "All connectors" ? "all" : "approved",
              })
            }
          />
        </Row>
      </Section>
      <Section title="Compliance">
        <Row
          title="HIPAA compliance"
          description="Enable privacy and security measures for protected health information"
        >
          <ActionButton disabled={!enterprise}>
            {enterprise ? "Configure" : "Available on Enterprise"}
          </ActionButton>
        </Row>
      </Section>
    </>
  );
}

function MembersPageV2({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("All");
  const [sort, setSort] = useState<
    "name" | "email" | "status" | "joined" | "lastSeen"
  >("name");
  const [descending, setDescending] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [emails, setEmails] = useState("");
  const [inviteRole, setInviteRole] = useState<"admin" | "member" | "guest">(
    "member",
  );
  const [inviteTeams, setInviteTeams] = useState<string[]>(
    data.teams.map((team) => team.id),
  );
  const [roleTarget, setRoleTarget] = useState<WorkspaceMember>();
  const [roleDraft, setRoleDraft] = useState<"admin" | "member" | "guest">(
    "member",
  );
  const [identityTarget, setIdentityTarget] = useState<{
    member: WorkspaceMember;
    field: "displayName" | "username" | "email";
    value: string;
  }>();
  const [teamsTarget, setTeamsTarget] = useState<WorkspaceMember>();
  const [teamDraft, setTeamDraft] = useState<string[]>([]);
  const [confirmTarget, setConfirmTarget] = useState<{
    member: WorkspaceMember;
    action: "suspend" | "remove" | "resume";
  }>();
  const [busy, setBusy] = useState(false);
  const normalized = query.trim().toLowerCase();
  const visibleMembers = data.members
    .filter((member) => {
      const matchesQuery =
        !normalized ||
        `${member.user.displayName} ${member.user.name} ${member.user.email}`
          .toLowerCase()
          .includes(normalized);
      const matchesStatus =
        status === "All" ||
        (status === "Admins" &&
          member.role === "admin" &&
          member.status === "active") ||
        (status === "Members" &&
          member.role === "member" &&
          member.status === "active") ||
        (status === "Guests" &&
          member.role === "guest" &&
          member.status === "active") ||
        (status === "Suspended" && member.status === "suspended");
      return matchesQuery && matchesStatus;
    })
    .sort((left, right) => {
      const value = (member: WorkspaceMember) =>
        sort === "name"
          ? member.user.displayName
          : sort === "email"
            ? member.user.email
            : sort === "status"
              ? `${member.status}:${member.role}`
              : sort === "joined"
                ? member.joinedAt
                : (member.lastSeenAt ?? "");
      return value(left).localeCompare(value(right)) * (descending ? -1 : 1);
    });
  const pending = data.invitations.filter(
    (invitation) =>
      invitation.status === "pending" &&
      (status === "All" || status === "Pending invites") &&
      (!normalized || invitation.email.toLowerCase().includes(normalized)),
  );
  const applications =
    status === "All" || status === "Applications"
      ? data.oauthApplications.filter(
          (application) =>
            !normalized || application.name.toLowerCase().includes(normalized),
        )
      : [];
  const active = visibleMembers.filter((member) => member.status === "active"),
    suspended = visibleMembers.filter(
      (member) => member.status === "suspended",
    );
  const change = async (action: () => Promise<unknown>, success: string) => {
    setBusy(true);
    try {
      await action();
      await onReload();
      toast.success(t(success));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("Could not update member"),
      );
    } finally {
      setBusy(false);
    }
  };
  const order = (key: typeof sort) => {
    if (sort === key) setDescending((value) => !value);
    else {
      setSort(key);
      setDescending(false);
    }
  };
  const sendInvites = () =>
    change(async () => {
      const parsed = emails
        .split(/[\s,;]+/)
        .map((value) => value.trim())
        .filter(Boolean);
      if (!parsed.length)
        throw new Error(t("Enter at least one email address"));
      if (inviteRole === "guest" && !inviteTeams.length)
        throw new Error(t("Guests must be assigned to a team"));
      await inviteMembers(data.workspace.urlKey, {
        emails: parsed,
        role: inviteRole,
        teamIds: inviteTeams,
      });
      setInviteOpen(false);
      setEmails("");
    }, "Invitation sent");
  const exportCsv = () => {
    const quote = (value: string) => `"${value.replaceAll('"', '""')}"`;
    download(
      "members.csv",
      [
        [
          "Name",
          "Username",
          "Email",
          "Status",
          "Role",
          "Teams",
          "Joined",
          "Last seen",
        ]
          .map(quote)
          .join(","),
        ...data.members.map((member) =>
          [
            member.user.displayName,
            member.user.name,
            member.user.email,
            member.status,
            member.role,
            String(
              data.teamMembers.filter((team) => team.userId === member.user.id)
                .length,
            ),
            member.joinedAt,
            member.lastSeenAt ?? "",
          ]
            .map(quote)
            .join(","),
        ),
      ].join("\n"),
      "text/csv",
    );
  };
  const header = (
    <div className="settings-members-columns">
      <button onClick={() => order("name")}>{t("Name")}</button>
      <button onClick={() => order("email")}>{t("Email")}</button>
      <button onClick={() => order("status")}>{t("Status")}</button>
      <span>{t("Teams")}</span>
      <button onClick={() => order("joined")}>{t("Joined")}</button>
      <button onClick={() => order("lastSeen")}>{t("Last seen")}</button>
      <span />
    </div>
  );
  const memberRow = (member: WorkspaceMember) => {
    const memberships = data.teamMembers.filter(
      (item) => item.userId === member.user.id,
    );
    return (
      <div className="settings-member-directory-row" key={member.user.id}>
        <span>
          <b className="settings-member-avatar">
            {initials(member.user.displayName)}
          </b>
          <i>
            <strong data-i18n-ignore>{member.user.displayName}</strong>
            <small data-i18n-ignore>{member.user.name}</small>
          </i>
        </span>
        <span data-i18n-ignore>{member.user.email}</span>
        <span>
          <em className={`settings-member-role is-${member.role}`}>
            {t(title(member.role))}
            {member.status === "suspended" ? ` (${t("Suspended")})` : ""}
          </em>
        </span>
        <span>
          <button
            className="settings-member-teams"
            onClick={() => {
              setTeamsTarget(member);
              setTeamDraft(memberships.map((item) => item.teamId));
            }}
          >
            {memberships.length
              ? `${memberships.length} ${t(memberships.length === 1 ? "team" : "teams")}`
              : t("No teams")}
          </button>
        </span>
        <time>{formatDate(member.joinedAt)}</time>
        <span>
          {member.user.id === data.viewer.id ? (
            <>
              <i className="settings-member-online" />
              {t("Online")}
            </>
          ) : member.lastSeenAt ? (
            formatDate(member.lastSeenAt)
          ) : (
            t("Never")
          )}
        </span>
        <span>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                className="settings-member-more"
                aria-label={`${t("Open menu")} ${member.user.displayName}`}
              >
                <MoreHorizontal size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              align="end"
              className="settings-member-action-menu"
              data-i18n-ignore
            >
              <DropdownMenuItem
                onSelect={() => {
                  setRoleTarget(member);
                  setRoleDraft(member.role as typeof roleDraft);
                }}
              >
                {t("Change role…")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onSelect={() =>
                  setIdentityTarget({
                    member,
                    field: "displayName",
                    value: member.user.displayName,
                  })
                }
              >
                {t("Update name…")}
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() =>
                  setIdentityTarget({
                    member,
                    field: "username",
                    value: member.user.name,
                  })
                }
              >
                {t("Update username…")}
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() =>
                  setIdentityTarget({
                    member,
                    field: "email",
                    value: member.user.email,
                  })
                }
              >
                {t("Update email…")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              {member.status === "suspended" ? (
                <DropdownMenuItem
                  onSelect={() =>
                    setConfirmTarget({ member, action: "resume" })
                  }
                >
                  {t("Restore user…")}
                </DropdownMenuItem>
              ) : (
                <DropdownMenuItem
                  disabled={member.user.id === data.viewer.id}
                  onSelect={() =>
                    setConfirmTarget({ member, action: "suspend" })
                  }
                >
                  {t("Suspend user…")}
                </DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onSelect={() => {
                  setTeamsTarget(member);
                  setTeamDraft(memberships.map((item) => item.teamId));
                }}
              >
                {t("Manage teams…")}
              </DropdownMenuItem>
              <DropdownMenuItem
                className="danger-item"
                disabled={member.user.id === data.viewer.id}
                onSelect={() => setConfirmTarget({ member, action: "remove" })}
              >
                {t("Remove from workspace…")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </span>
      </div>
    );
  };
  return (
    <div className="settings-members-page" data-i18n-ignore>
      <PageTitle>{t("Members")}</PageTitle>
      <div className="settings-members-toolbar">
        <label>
          <Search size={14} />
          <input
            aria-label={t("Search by name or email")}
            placeholder={t("Search by name or email")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <Select
          label={t("Member status")}
          value={t(status)}
          options={[
            "All",
            "Admins",
            "Members",
            "Guests",
            "Applications",
            "Pending invites",
            "Suspended",
            "Left workspace",
          ].map(t)}
          onChange={(value) => {
            const values = [
              "All",
              "Admins",
              "Members",
              "Guests",
              "Applications",
              "Pending invites",
              "Suspended",
              "Left workspace",
            ];
            setStatus(values.find((item) => t(item) === value) ?? "All");
          }}
        />
        <span />
        <ActionButton onClick={exportCsv}>{t("Export CSV")}</ActionButton>
        <ActionButton primary onClick={() => setInviteOpen(true)}>
          <Plus size={14} />
          {t("Invite")}
        </ActionButton>
      </div>
      <div className="settings-members-directory">
        {header}
        {active.length > 0 && (
          <>
            <div className="settings-members-group">
              <strong>{t("Active")}</strong>
              <span>{active.length}</span>
            </div>
            {active.map(memberRow)}
          </>
        )}
        {suspended.length > 0 && (
          <>
            <div className="settings-members-group">
              <strong>{t("Suspended")}</strong>
              <span>{suspended.length}</span>
            </div>
            {suspended.map(memberRow)}
          </>
        )}
        {pending.length > 0 && (
          <>
            <div className="settings-members-group">
              <strong>{t("Invited")}</strong>
              <span>{pending.length}</span>
            </div>
            {pending.map((invitation) => (
              <div
                className="settings-member-directory-row is-invited"
                key={invitation.id}
              >
                <span>
                  <b className="settings-member-avatar is-invited">
                    {initials(invitation.email)}
                  </b>
                  <i>
                    <strong data-i18n-ignore>{invitation.email}</strong>
                    <small data-i18n-ignore>{invitation.email}</small>
                  </i>
                </span>
                <span data-i18n-ignore>{invitation.email}</span>
                <span>
                  <em className={`settings-member-role is-${invitation.role}`}>
                    {t(title(invitation.role))} ({t("Invited")})
                  </em>
                </span>
                <span>
                  {invitation.teamIds.length}{" "}
                  {t(invitation.teamIds.length === 1 ? "team" : "teams")}
                </span>
                <time>{formatDate(invitation.createdAt)}</time>
                <span>—</span>
                <span>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <button
                        className="settings-member-more"
                        aria-label={t("Open menu")}
                      >
                        <MoreHorizontal size={15} />
                      </button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem
                        onSelect={() =>
                          void change(
                            () =>
                              resendInvitation(
                                data.workspace.urlKey,
                                invitation.id,
                              ),
                            "Invitation resent",
                          )
                        }
                      >
                        {t("Resend invite")}
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        className="danger-item"
                        onSelect={() =>
                          void change(
                            () =>
                              revokeInvitation(
                                data.workspace.urlKey,
                                invitation.id,
                              ),
                            "Invitation revoked",
                          )
                        }
                      >
                        {t("Revoke invite")}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </span>
              </div>
            ))}
          </>
        )}
        {applications.length > 0 && (
          <>
            <div className="settings-members-group">
              <strong>{t("Application")}</strong>
              <span>{applications.length}</span>
            </div>
            {applications.map((application) => (
              <div
                className="settings-member-directory-row is-application"
                key={application.id}
              >
                <span>
                  <b className="settings-member-avatar">AP</b>
                  <i>
                    <strong data-i18n-ignore>{application.name}</strong>
                    <small>application</small>
                  </i>
                </span>
                <span>—</span>
                <span>{t("Application")}</span>
                <span>—</span>
                <time>—</time>
                <span>—</span>
                <span />
              </div>
            ))}
          </>
        )}
        {!active.length &&
          !suspended.length &&
          !pending.length &&
          !applications.length && (
            <div className="settings-empty compact">
              <UserRound size={24} />
              <h3>{t("No members found")}</h3>
              <p>{t("Try another name, email, or member status.")}</p>
            </div>
          )}
      </div>
      <Dialog open={inviteOpen} onOpenChange={setInviteOpen}>
        <DialogContent className="settings-invite-dialog">
          <DialogTitle>
            <span className="settings-invite-mark">
              {data.workspace.name.slice(0, 2).toUpperCase()}
            </span>
            {t("Invite to your workspace")}
          </DialogTitle>
          <label>
            {t("Email")}
            <textarea
              autoFocus
              aria-label={t("Email")}
              placeholder="email@foxmail.com, email2@foxmail.com…"
              value={emails}
              onChange={(event) => setEmails(event.target.value)}
            />
          </label>
          <label>
            {t("Role")}
            <Select
              label={t("Invitation role")}
              value={t(title(inviteRole))}
              options={["Member", "Admin", "Guest"].map(t)}
              onChange={(value) =>
                setInviteRole(
                  (["member", "admin", "guest"] as const)[
                    ["Member", "Admin", "Guest"].map(t).indexOf(value)
                  ] ?? "member",
                )
              }
            />
          </label>
          {inviteRole === "guest" && (
            <label>
              {t("Teams")}
              <button
                className="settings-select"
                onClick={() =>
                  setInviteTeams((current) =>
                    current.length ? [] : data.teams.map((team) => team.id),
                  )
                }
              >
                {inviteTeams.length
                  ? `${inviteTeams.length} ${t("selected")}`
                  : t("Select teams")}
              </button>
            </label>
          )}
          <footer>
            <ActionButton
              primary
              disabled={busy || !emails.trim()}
              onClick={() => void sendInvites()}
            >
              {busy ? t("Sending…") : t("Send invites")}
            </ActionButton>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(roleTarget)}
        onOpenChange={(open) => !open && setRoleTarget(undefined)}
      >
        <DialogContent className="settings-member-dialog">
          <DialogTitle>{t("Change role")}</DialogTitle>
          {(["admin", "member", "guest"] as const).map((value) => (
            <label className="settings-member-role-option" key={value}>
              <input
                type="radio"
                checked={roleDraft === value}
                onChange={() => setRoleDraft(value)}
              />
              <span>
                <strong>
                  {t(value === "admin" ? "Workspace admin" : title(value))}
                </strong>
                <small>
                  {t(
                    value === "admin"
                      ? "Full control of the workspace including security, billing, and all settings"
                      : value === "member"
                        ? "Standard workspace access with the ability to act within all public teams"
                        : "Access limited to specific teams, with no workspace views or features",
                  )}
                </small>
              </span>
            </label>
          ))}
          <footer>
            <ActionButton onClick={() => setRoleTarget(undefined)}>
              {t("Cancel")}
            </ActionButton>
            <ActionButton
              primary
              disabled={busy}
              onClick={() =>
                roleTarget &&
                void change(
                  () =>
                    updateMemberRole(
                      data.workspace.urlKey,
                      roleTarget.user.id,
                      roleDraft,
                    ),
                  "Role updated",
                ).then(() => setRoleTarget(undefined))
              }
            >
              {t("Save")}
            </ActionButton>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(identityTarget)}
        onOpenChange={(open) => !open && setIdentityTarget(undefined)}
      >
        <DialogContent className="settings-member-dialog">
          <DialogTitle>
            {t(
              identityTarget?.field === "displayName"
                ? "Update name"
                : identityTarget?.field === "username"
                  ? "Update username"
                  : "Update email",
            )}
          </DialogTitle>
          <input
            autoFocus
            className="settings-input"
            value={identityTarget?.value ?? ""}
            onChange={(event) =>
              setIdentityTarget((current) =>
                current ? { ...current, value: event.target.value } : current,
              )
            }
          />
          <footer>
            <ActionButton onClick={() => setIdentityTarget(undefined)}>
              {t("Cancel")}
            </ActionButton>
            <ActionButton
              primary
              disabled={busy || !identityTarget?.value.trim()}
              onClick={() =>
                identityTarget &&
                void change(
                  () =>
                    updateMemberIdentity(
                      data.workspace.urlKey,
                      identityTarget.member.user.id,
                      { [identityTarget.field]: identityTarget.value.trim() },
                    ),
                  "Member updated",
                ).then(() => setIdentityTarget(undefined))
              }
            >
              {t("Save")}
            </ActionButton>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(teamsTarget)}
        onOpenChange={(open) => !open && setTeamsTarget(undefined)}
      >
        <DialogContent className="settings-member-dialog">
          <DialogTitle>{t("Manage teams")}</DialogTitle>
          {data.teams.map((team) => (
            <label className="settings-member-team-option" key={team.id}>
              <input
                type="checkbox"
                checked={teamDraft.includes(team.id)}
                onChange={(event) =>
                  setTeamDraft((current) =>
                    event.target.checked
                      ? [...current, team.id]
                      : current.filter((id) => id !== team.id),
                  )
                }
              />
              <span data-i18n-ignore>{team.name}</span>
            </label>
          ))}
          <footer>
            <ActionButton onClick={() => setTeamsTarget(undefined)}>
              {t("Cancel")}
            </ActionButton>
            <ActionButton
              primary
              disabled={busy}
              onClick={() =>
                teamsTarget &&
                void change(
                  () =>
                    Promise.all(
                      data.teams.map((team) => {
                        const current = data.teamMembers.some(
                            (item) =>
                              item.teamId === team.id &&
                              item.userId === teamsTarget.user.id,
                          ),
                          next = teamDraft.includes(team.id);
                        return current === next
                          ? Promise.resolve()
                          : setTeamMembership(
                              data.workspace.urlKey,
                              team.id,
                              teamsTarget.user.id,
                              next,
                              "member",
                            );
                      }),
                    ),
                  "Team access updated",
                ).then(() => setTeamsTarget(undefined))
              }
            >
              {t("Save")}
            </ActionButton>
          </footer>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={Boolean(confirmTarget)}
        title={
          confirmTarget
            ? `${t(confirmTarget.action === "resume" ? "Restore" : "Suspend")} ${confirmTarget.member.user.displayName}?`
            : ""
        }
        description={
          confirmTarget?.action === "suspend"
            ? t("They won’t be able to access this workspace.")
            : confirmTarget?.action === "remove"
              ? t(
                  "This member will lose access to the workspace and all teams.",
                )
              : t("This member will regain access to the workspace.")
        }
        confirm={t(
          confirmTarget?.action === "remove"
            ? "Remove"
            : confirmTarget?.action === "resume"
              ? "Restore"
              : "Confirm",
        )}
        onCancel={() => setConfirmTarget(undefined)}
        onConfirm={() => {
          if (!confirmTarget) return;
          const action =
            confirmTarget.action === "suspend"
              ? suspendMember(
                  data.workspace.urlKey,
                  confirmTarget.member.user.id,
                )
              : confirmTarget.action === "resume"
                ? resumeMember(
                    data.workspace.urlKey,
                    confirmTarget.member.user.id,
                  )
                : removeMember(
                    data.workspace.urlKey,
                    confirmTarget.member.user.id,
                  );
          void change(
            () => action,
            confirmTarget.action === "resume"
              ? "Member restored"
              : confirmTarget.action === "remove"
                ? "Member removed"
                : "Member suspended",
          ).then(() => setConfirmTarget(undefined));
        }}
      />
    </div>
  );
}

function SecurityPage({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const [settings, setSettings] = useState(data.workspaceSettings);
  useEffect(
    () => setSettings(data.workspaceSettings),
    [data.workspaceSettings],
  );
  const save = async (next: WorkspaceSettings) => {
    setSettings(next);
    try {
      await updateWorkspacePreferences(next);
      await onReload();
      toast.success("Security policy saved");
    } catch (error) {
      setSettings(data.workspaceSettings);
      toast.error(
        error instanceof Error ? error.message : "Could not save policy",
      );
    }
  };
  const toggle = (
    title: string,
    key: keyof WorkspaceSettings,
    description: string,
  ) => (
    <Row title={title} description={description}>
      <Toggle
        label={title}
        checked={Boolean(settings[key])}
        onChange={(value) => void save({ ...settings, [key]: value })}
      />
    </Row>
  );
  return (
    <>
      <PageTitle description="Manage authentication, permissions, and access policies for your workspace.">
        Security
      </PageTitle>
      <Section title="Workspace access">
        {toggle(
          "Invite links",
          "inviteLinksEnabled",
          "Allow members to invite people with a workspace link.",
        )}
        <Row
          title="Allow guest accounts"
          description="Guest invitations are rejected when this is disabled"
        >
          <Toggle
            label="Allow guest accounts"
            checked={settings.guestsAllowed}
            onChange={(value) =>
              void save({ ...settings, guestsAllowed: value })
            }
          />
        </Row>
      </Section>
      <Section title="Authentication methods">
        {toggle(
          "Google authentication",
          "googleAuthEnabled",
          "When enabled, this is available to all workspace members and guests",
        )}
        {toggle(
          "Email & passkey authentication",
          "emailAuthEnabled",
          "When enabled, this is available to all workspace members and guests",
        )}
        <Row
          title="Require two-factor authentication"
          description="Require a second factor for all members."
        >
          <Toggle
            label="Require two-factor authentication"
            checked={settings.requireTwoFactor}
            onChange={(value) =>
              void save({ ...settings, requireTwoFactor: value })
            }
          />
        </Row>
        {toggle(
          "Disable authentication bypass for admins",
          "disableAdminBypass",
          "When enabled, admins are restricted to the enabled authentication methods",
        )}
        <Row title="Session duration">
          <Select
            label="Session duration"
            value={`${settings.sessionDurationDays} days`}
            options={["7 days", "30 days", "90 days"]}
            onChange={(value) =>
              void save({
                ...settings,
                sessionDurationDays: Number(value.split(" ")[0]),
              })
            }
          />
        </Row>
      </Section>
      <EnterpriseIdentityProviders data={data} onReload={onReload} />
      <Section title="Workspace login and restrictions">
        <FieldRow
          title="Approved email domains"
          description="Anyone with an email address at these domains is allowed to sign up for this workspace."
          value={(settings.allowedDomains ?? []).join(", ")}
          onCommit={(value) =>
            void save({
              ...settings,
              allowedDomains: value
                .split(",")
                .map((item) => item.trim())
                .filter(Boolean),
            })
          }
        />
      </Section>
      <Section title="Workspace management">
        <PermissionRow
          title="New user invitations"
          value={settings.invitePermission}
          onChange={(value) =>
            void save({ ...settings, invitePermission: value })
          }
        />
        <PermissionRow
          title="Team creation"
          value={settings.teamCreatePermission}
          onChange={(value) =>
            void save({ ...settings, teamCreatePermission: value })
          }
        />
        <PermissionRow
          title="Manage workspace labels"
          value={settings.labelPermission}
          onChange={(value) =>
            void save({ ...settings, labelPermission: value })
          }
        />
        <PermissionRow
          title="Manage workspace templates"
          value={settings.templatePermission}
          onChange={(value) =>
            void save({ ...settings, templatePermission: value })
          }
        />
        <PermissionRow
          title="Workspace initiatives"
          value={settings.initiativePermission ?? "members"}
          onChange={(value) =>
            void save({ ...settings, initiativePermission: value })
          }
        />
        <PermissionRow
          title="Manage loops"
          value={settings.loopPermission ?? "members"}
          onChange={(value) =>
            void save({ ...settings, loopPermission: value })
          }
        />
        <PermissionRow
          title="Modify agent guidance"
          value={settings.agentGuidancePermission ?? "admins"}
          onChange={(value) =>
            void save({ ...settings, agentGuidancePermission: value })
          }
        />
        <PermissionRow
          title="API key creation"
          value={settings.apiKeyPermission}
          onChange={(value) =>
            void save({ ...settings, apiKeyPermission: value })
          }
        />
      </Section>
      <Section title="Integrations & applications">
        {toggle(
          "Prevent guests from interacting with agents in the workspace",
          "preventGuestAgents",
          "Restrict agent invocation to full workspace members only",
        )}
        {toggle(
          "Improve AI features by sharing usage data",
          "aiUsageSharing",
          "Feedback on AI results is used to enhance functionality",
        )}
        {toggle(
          "Enable Flow Agent web search",
          "agentWebSearch",
          "Allow Flow Agent to search the public web for current information and cite sources",
        )}
        {toggle(
          "Allow external sources to trigger loops",
          "externalLoopTriggers",
          "Select which external sources can trigger loops",
        )}
        {toggle(
          "Enable Flow Agent MCP connectors",
          "mcpConnectorsEnabled",
          "Allow Flow Agent to use MCP connectors",
        )}
      </Section>
    </>
  );
}
function PermissionRow({
  title,
  value,
  onChange,
}: {
  title: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <Row title={title}>
      <Select
        label={title}
        value={value === "admins" ? "Only admins" : "All members"}
        options={["Only admins", "All members"]}
        onChange={(next) =>
          onChange(next === "Only admins" ? "admins" : "members")
        }
      />
    </Row>
  );
}
function ApiPage({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>(["read", "write"]);
  const [teamIds, setTeamIds] = useState<string[]>([]);
  const [secret, setSecret] = useState("");
  const [editingOAuth, setEditingOAuth] = useState<
    OAuthApplication | null | undefined
  >(undefined);
  const [editingWebhook, setEditingWebhook] = useState<
    Webhook | null | undefined
  >(undefined);
  const items = data.apiKeys.filter(
    (item) => item.creatorId === data.viewer.id && !item.revokedAt,
  );
  const submit = async () => {
    try {
      const result = await createAPIKey({ name, scopes, teamIds });
      setSecret(result.secret);
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not create API key",
      );
    }
  };
  const reset = () => {
    setOpen(false);
    setSecret("");
    setName("");
    setTeamIds([]);
    setScopes(["read", "write"]);
  };
  return (
    <>
      <PageTitle description="Developer applications, webhooks, and member API access.">
        API
      </PageTitle>
      <Section title="OAuth applications">
        {data.oauthApplications.map((item) => (
          <Row
            key={item.id}
            title={item.name}
            description={`${item.clientId} · ${item.scopes.join(", ")}`}
          >
            <ActionButton onClick={() => setEditingOAuth(item)}>
              Configure
            </ActionButton>
          </Row>
        ))}
        {!data.oauthApplications.length && (
          <div className="settings-empty compact">
            <AppWindow size={24} />
            <h3>No OAuth applications</h3>
            <p>Create an application for third-party OAuth access.</p>
          </div>
        )}
        <div className="settings-section-action">
          <ActionButton onClick={() => setEditingOAuth(null)}>
            <Plus size={14} />
            New OAuth application
          </ActionButton>
        </div>
      </Section>
      <Section title="Webhooks">
        {data.webhooks.map((item) => (
          <Row
            key={item.id}
            title={item.name}
            description={`${item.url} · ${item.resourceTypes.join(", ") || "all resources"}`}
          >
            <div className="settings-inline-actions">
              <Toggle
                label={`${item.name} enabled`}
                checked={item.enabled}
                onChange={(value) =>
                  void updateWebhook(item.id, { enabled: value }).then(onReload)
                }
              />
              <ActionButton onClick={() => setEditingWebhook(item)}>
                Configure
              </ActionButton>
            </div>
          </Row>
        ))}
        {!data.webhooks.length && (
          <div className="settings-empty compact">
            <Radio size={24} />
            <h3>No webhooks</h3>
            <p>Send workspace events to an HTTPS endpoint.</p>
          </div>
        )}
        <div className="settings-section-action">
          <ActionButton onClick={() => setEditingWebhook(null)}>
            <Plus size={14} />
            New webhook
          </ActionButton>
        </div>
      </Section>
      <Section title="Personal API keys">
        <Row
          title="Member API key creation"
          description="Controlled by the workspace Security policy."
        >
          <span className="settings-static">
            {data.workspaceSettings.apiKeyPermission === "admins"
              ? "Admins only"
              : "All members"}
          </span>
        </Row>
        {items.map((item) => (
          <Row
            key={item.id}
            title={item.name}
            description={`${item.prefix}… · ${item.scopes.join(", ")} · ${item.teamIds.length ? `${item.teamIds.length} teams` : "all teams"} · ${item.lastUsedAt ? `last used ${formatDate(item.lastUsedAt)}` : "never used"}`}
          >
            <ActionButton
              danger
              onClick={() => void revokeAPIKey(item.id).then(onReload)}
            >
              Revoke
            </ActionButton>
          </Row>
        ))}
        {!items.length && (
          <div className="settings-empty compact">
            <Braces size={24} />
            <h3>No personal API keys</h3>
            <p>Create a scoped key to access the Flow API.</p>
          </div>
        )}
        <div className="settings-section-action">
          <ActionButton onClick={() => setOpen(true)}>
            <Plus size={14} />
            New API key
          </ActionButton>
        </div>
      </Section>
      <Dialog
        open={open}
        onOpenChange={(value) => (value ? setOpen(true) : reset())}
      >
        <DialogContent className="settings-confirm">
          <DialogTitle>
            {secret ? "API key created" : "New API key"}
          </DialogTitle>
          {secret ? (
            <>
              <p>This secret is shown once.</p>
              <input
                className="settings-input"
                readOnly
                value={secret}
                onFocus={(event) => event.currentTarget.select()}
              />
              <footer>
                <ActionButton
                  onClick={() => {
                    void navigator.clipboard.writeText(secret);
                    toast.success("Copied");
                  }}
                >
                  Copy
                </ActionButton>
                <ActionButton primary onClick={reset}>
                  Done
                </ActionButton>
              </footer>
            </>
          ) : (
            <>
              <label>
                Name
                <input
                  className="settings-input"
                  autoFocus
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </label>
              <label>
                Scopes
                <div className="settings-segmented">
                  <button
                    className={scopes.includes("read") ? "active" : ""}
                    onClick={() =>
                      setScopes((current) =>
                        current.includes("read")
                          ? current.filter((x) => x !== "read")
                          : [...current, "read"],
                      )
                    }
                  >
                    Read
                  </button>
                  <button
                    className={scopes.includes("write") ? "active" : ""}
                    onClick={() =>
                      setScopes((current) =>
                        current.includes("write")
                          ? current.filter((x) => x !== "write")
                          : [...current, "write"],
                      )
                    }
                  >
                    Write
                  </button>
                </div>
              </label>
              <fieldset className="settings-check-list">
                <legend>Team access</legend>
                <label>
                  <input
                    type="checkbox"
                    checked={!teamIds.length}
                    onChange={() => setTeamIds([])}
                  />
                  All teams
                </label>
                {data.teams.map((team) => (
                  <label key={team.id}>
                    <input
                      type="checkbox"
                      checked={teamIds.includes(team.id)}
                      onChange={(event) =>
                        setTeamIds((current) =>
                          event.target.checked
                            ? [...current, team.id]
                            : current.filter((id) => id !== team.id),
                        )
                      }
                    />
                    {team.name}
                  </label>
                ))}
              </fieldset>
              <footer>
                <ActionButton onClick={reset}>Cancel</ActionButton>
                <ActionButton
                  primary
                  disabled={!name.trim() || !scopes.length}
                  onClick={() => void submit()}
                >
                  Create key
                </ActionButton>
              </footer>
            </>
          )}
        </DialogContent>
      </Dialog>
      {editingOAuth !== undefined && (
        <OAuthEditor
          app={editingOAuth}
          onClose={() => setEditingOAuth(undefined)}
          onSaved={onReload}
        />
      )}{" "}
      {editingWebhook !== undefined && (
        <WebhookEditor
          data={data}
          webhook={editingWebhook}
          onClose={() => setEditingWebhook(undefined)}
          onSaved={onReload}
        />
      )}
    </>
  );
}
function ApplicationsPage({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const authorizations = (data.oauthAuthorizations ?? []).filter(
    (item) => !item.revokedAt,
  );
  const empty = !data.integrationConnections.length && !authorizations.length;
  return (
    <>
      <PageTitle description="Third-party applications authorized for this workspace.">
        Applications
      </PageTitle>
      <Section>
        {authorizations.map((item) => (
          <Row
            key={item.id}
            title={item.clientName}
            description={`MCP · ${item.scopes.join(", ")} · ${item.lastUsedAt ? `last used ${formatDate(item.lastUsedAt)}` : `authorized ${formatDate(item.createdAt)}`}`}
          >
            <ActionButton
              danger
              onClick={() =>
                void revokeOAuthAuthorization(item.id).then(onReload)
              }
            >
              Revoke access
            </ActionButton>
          </Row>
        ))}
        {data.integrationConnections.map((item) => (
          <Row
            key={item.id}
            title={item.name}
            description={`${title(item.provider)} · ${item.status}`}
          >
            <ActionButton
              danger
              onClick={() =>
                void disconnectIntegration(item.provider).then(onReload)
              }
            >
              Revoke access
            </ActionButton>
          </Row>
        ))}
        {empty && (
          <div className="settings-empty">
            <AppWindow size={28} />
            <h3>No authorized applications</h3>
            <p>
              Applications authorized by workspace members will appear here.
            </p>
          </div>
        )}
      </Section>
    </>
  );
}
function WebhookEditor({
  data,
  webhook,
  onClose,
  onSaved,
}: {
  data: BootstrapData;
  webhook: Webhook | null;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [name, setName] = useState(webhook?.name ?? "");
  const [url, setUrl] = useState(webhook?.url ?? "");
  const [resourceTypes, setResourceTypes] = useState(
    webhook?.resourceTypes ?? ["issues"],
  );
  const [teamIds, setTeamIds] = useState(webhook?.teamIds ?? []);
  const resources = [
    "issues",
    "comments",
    "projects",
    "cycles",
    "documents",
    "customers",
  ];
  const save = async () => {
    try {
      const input = {
        name: name.trim(),
        url: url.trim(),
        resourceTypes,
        teamIds,
        enabled: webhook?.enabled ?? true,
      };
      if (webhook) await updateWebhook(webhook.id, input);
      else await createWebhook(input);
      await onSaved();
      onClose();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not save webhook",
      );
    }
  };
  return (
    <Dialog open onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="settings-invite-dialog settings-webhook-dialog">
        <DialogTitle>
          {webhook ? "Configure webhook" : "New webhook"}
        </DialogTitle>
        <label>
          Name
          <input
            autoFocus
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label>
          Endpoint URL
          <input
            value={url}
            onChange={(event) => setUrl(event.target.value)}
            placeholder="https://example.com/webhooks/flow"
          />
        </label>
        <fieldset className="settings-check-list">
          <legend>Resources</legend>
          {resources.map((resource) => (
            <label key={resource}>
              <input
                type="checkbox"
                checked={resourceTypes.includes(resource)}
                onChange={(event) =>
                  setResourceTypes((current) =>
                    event.target.checked
                      ? [...current, resource]
                      : current.filter((item) => item !== resource),
                  )
                }
              />
              {title(resource)}
            </label>
          ))}
        </fieldset>
        <fieldset className="settings-check-list">
          <legend>Teams</legend>
          <label>
            <input
              type="checkbox"
              checked={!teamIds.length}
              onChange={() => setTeamIds([])}
            />
            All teams
          </label>
          {data.teams.map((team) => (
            <label key={team.id}>
              <input
                type="checkbox"
                checked={teamIds.includes(team.id)}
                onChange={(event) =>
                  setTeamIds((current) =>
                    event.target.checked
                      ? [...current, team.id]
                      : current.filter((id) => id !== team.id),
                  )
                }
              />
              {team.name}
            </label>
          ))}
        </fieldset>
        <footer>
          {webhook && (
            <ActionButton
              danger
              onClick={() =>
                void deleteWebhook(webhook.id).then(async () => {
                  await onSaved();
                  onClose();
                })
              }
            >
              Delete
            </ActionButton>
          )}
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            primary
            disabled={
              !name.trim() || !/^https?:\/\//.test(url) || !resourceTypes.length
            }
            onClick={() => void save()}
          >
            Save
          </ActionButton>
        </footer>
      </DialogContent>
    </Dialog>
  );
}
function OAuthEditor({
  app,
  onClose,
  onSaved,
}: {
  app: OAuthApplication | null;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [name, setName] = useState(app?.name ?? "");
  const [description, setDescription] = useState(app?.description ?? "");
  const [redirects, setRedirects] = useState(
    app?.redirectUris.join("\n") ?? "",
  );
  const [scopes, setScopes] = useState(app?.scopes.join(", ") ?? "read");
  const save = async () => {
    try {
      const input = {
        name,
        description,
        redirectUris: redirects.split(/\s+/).filter(Boolean),
        scopes: scopes
          .split(",")
          .map((x) => x.trim())
          .filter(Boolean),
      };
      const result = app
        ? await updateOAuthApplication(app.id, input)
        : await createOAuthApplication(input);
      await onSaved();
      if (result.clientSecret)
        toast.success(`Client secret: ${result.clientSecret}`, {
          duration: 15000,
        });
      onClose();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not save application",
      );
    }
  };
  return (
    <Dialog open onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="settings-invite-dialog">
        <DialogTitle>
          {app ? "Configure application" : "New application"}
        </DialogTitle>
        <label>
          Name
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label>
          Description
          <textarea
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </label>
        <label>
          Redirect URIs
          <textarea
            value={redirects}
            onChange={(event) => setRedirects(event.target.value)}
            placeholder="https://app.example.com/oauth/callback"
          />
        </label>
        <label>
          Scopes
          <input
            value={scopes}
            onChange={(event) => setScopes(event.target.value)}
          />
        </label>
        <footer>
          {app && (
            <ActionButton
              danger
              onClick={() =>
                void deleteOAuthApplication(app.id).then(async () => {
                  await onSaved();
                  onClose();
                })
              }
            >
              Delete
            </ActionButton>
          )}
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            primary
            disabled={!name.trim()}
            onClick={() => void save()}
          >
            Save
          </ActionButton>
        </footer>
      </DialogContent>
    </Dialog>
  );
}
function BillingPage({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const settings = data.workspaceSettings;
  const [email, setEmail] = useState(
    settings.billingEmail || data.viewer.email,
  );
  const save = (patch: Partial<WorkspaceSettings>) =>
    updateWorkspacePreferences({ ...settings, ...patch }).then(onReload);
  return (
    <>
      <PageTitle>Billing</PageTitle>
      <Section title="Current plan">
        <Row
          title={title(settings.plan)}
          description={`${data.users.length} members`}
        >
          <span className="settings-static">Managed locally</span>
        </Row>
        <Row title="Billing email">
          <input
            className="settings-input"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
            onBlur={() =>
              email !== settings.billingEmail &&
              void save({ billingEmail: email })
            }
          />
        </Row>
      </Section>
      <Section title="Payments">
        <div className="settings-empty compact">
          <CreditCard size={24} />
          <h3>Billing provider not configured</h3>
          <p>Plan checkout and invoices require a payment provider.</p>
        </div>
      </Section>
    </>
  );
}
function UsagePage({ data }: { data: BootstrapData }) {
  const { t } = useI18n();
  const [usage, setUsage] = useState<Awaited<
    ReturnType<typeof fetchWorkspaceUsage>
  > | null>(null);
  const [alerts, setAlerts] = useState<UsageAlert[]>([]);
  const [subscription, setSubscription] = useState<PaidSubscription | null>(
    null,
  );
  const [weekOffset, setWeekOffset] = useState(0);
  const load = async () => {
    const [value, alertPage, paid] = await Promise.all([
      fetchWorkspaceUsage(),
      listUsageAlerts(),
      getPaidSubscription(),
    ]);
    setUsage(value);
    setAlerts(alertPage.nodes);
    setSubscription(paid);
  };
  useEffect(() => {
    void load();
  }, [data]);
  if (!usage)
    return (
      <div className="settings-empty compact">
        <Gauge size={24} />
        <p>{t("Loading usage…")}</p>
      </div>
    );
  const save = async (patch: Partial<WorkspaceSettings>) => {
    await updateWorkspacePreferences({ ...data.workspaceSettings, ...patch });
    await load();
  };
  const now = new Date();
  const end = new Date(now);
  end.setDate(now.getDate() + weekOffset * 7);
  const start = new Date(end);
  start.setDate(end.getDate() - 6);
  const periodEvents = usage.events.filter((event) => {
    const date = new Date(event.createdAt);
    return (
      date >= start &&
      date <=
        new Date(end.getFullYear(), end.getMonth(), end.getDate(), 23, 59, 59)
    );
  });
  const coding = periodEvents
    .filter((event) => event.feature === "coding-sessions")
    .reduce((sum, event) => sum + event.amountCents, 0);
  const loops = periodEvents
    .filter((event) => event.feature === "loops")
    .reduce((sum, event) => sum + event.amountCents, 0);
  const maxSpend = Math.max(1, coding, loops);
  return (
    <div className="settings-usage-page">
      <PageTitle
        description={t(
          "Track usage, manage credits, and set spend limits across your workspace",
        )}
      >
        {t("Usage & limits")}
      </PageTitle>
      {alerts
        .filter((alert) => alert.status === "active")
        .map((alert) => (
          <div className="settings-usage-alert" key={alert.id}>
            <Gauge />
            <span>
              <strong>{t("Usage alert")}</strong>
              <small>
                {t(alert.type)}: {alert.current} / {alert.threshold}
              </small>
            </span>
          </div>
        ))}
      <Section title={t("AI credits")}>
        <div className="settings-credit-summary">
          <span>{t("Workspace credits")}</span>
          <strong>
            {currency(usage.aiCredits.balanceCents)}{" "}
            <small>{t("available")}</small>
          </strong>
        </div>
        <Row
          title={t("Automatic reload")}
          description={t(
            "Automatically top up credits when your balance runs low",
          )}
        >
          <Toggle
            label={t("Automatic credit reload")}
            checked={usage.aiCredits.autoReloadEnabled}
            onChange={(value) => void save({ aiCreditAutoReload: value })}
          />
        </Row>
        {usage.aiCredits.autoReloadEnabled && (
          <div className="settings-reload-controls">
            <Row title={t("Reload threshold")}>
              <NumberInput
                value={usage.aiCredits.autoReloadThresholdCents}
                onCommit={(value) =>
                  save({ aiCreditReloadThresholdCents: value })
                }
              />
            </Row>
            <Row title={t("Reload amount")}>
              <NumberInput
                value={usage.aiCredits.autoReloadAmountCents}
                onCommit={(value) => save({ aiCreditReloadAmountCents: value })}
              />
            </Row>
          </div>
        )}
      </Section>
      <Section>
        <Row
          title={t("Spend limits")}
          description={t("Set workspace, user, and loop limits")}
        >
          <NumberInput
            value={usage.aiCredits.workspaceSpendLimitCents}
            onCommit={(value) => save({ aiWorkspaceSpendLimitCents: value })}
          />
        </Row>
      </Section>
      <section className="settings-usage-analytics">
        <header>
          <h3>{t("Analytics")}</h3>
          <div>
            <button type="button">{t("Week")}</button>
            <button
              aria-label={t("Previous period")}
              type="button"
              onClick={() => setWeekOffset((value) => value - 1)}
            >
              <ChevronLeft />
            </button>
            <span>
              {start.toLocaleDateString(undefined, {
                month: "short",
                day: "numeric",
              })}{" "}
              –{" "}
              {end.toLocaleDateString(undefined, {
                month: "short",
                day: "numeric",
              })}
            </span>
            <button
              aria-label={t("Next period")}
              disabled={weekOffset === 0}
              type="button"
              onClick={() => setWeekOffset((value) => Math.min(0, value + 1))}
            >
              <ChevronRight />
            </button>
          </div>
        </header>
        <strong className="settings-usage-total">
          {currency(coding + loops)}
          <small>{t("total spend in this period")}</small>
        </strong>
        <div className="settings-usage-chart">
          <i style={{ height: `${Math.max(2, (coding / maxSpend) * 100)}%` }} />
          <i style={{ height: `${Math.max(2, (loops / maxSpend) * 100)}%` }} />
        </div>
        <div className="settings-usage-series">
          <span>
            <i />
            {t("Coding sessions")}
            <strong>{currency(coding)}</strong>
          </span>
          <span>
            <i />
            {t("Loops")}
            <strong>{currency(loops)}</strong>
          </span>
        </div>
        {!periodEvents.length && (
          <div className="settings-usage-no-data">
            <Activity />
            <span>{t("No usage this week")}</span>
          </div>
        )}
      </section>
      <Section title={t("Plan usage")}>
        {subscription && (
          <Row
            title={title(subscription.plan)}
            description={`${subscription.seats} ${t("seats")} · ${subscription.status}`}
          >
            <span className="settings-static">
              {subscription.cancelAtPeriodEnd
                ? t("Cancels at period end")
                : t("Active")}
            </span>
          </Row>
        )}
        <UsageRow
          title={t("Members")}
          value={usage.members}
          limit={usage.limits.members}
        />
        <UsageRow
          title={t("Issues")}
          value={usage.issues}
          limit={usage.limits.issues}
        />
        <UsageRow
          title={t("File storage (MB)")}
          value={Math.ceil(usage.storageBytes / 1048576)}
          limit={Math.ceil(usage.limits.storageBytes / 1048576)}
        />
      </Section>
    </div>
  );
}
function NumberInput({
  value,
  onCommit,
}: {
  value: number;
  onCommit: (value: number) => void | Promise<void>;
}) {
  const [draft, setDraft] = useState(String(value / 100));
  useEffect(() => setDraft(String(value / 100)), [value]);
  return (
    <div className="settings-money-input">
      <span>$</span>
      <input
        type="number"
        min={0}
        step={1}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => {
          const next = Math.round(Number(draft) * 100);
          if (next !== value) void onCommit(next);
        }}
      />
    </div>
  );
}
function UsageRow({
  title,
  value,
  limit,
}: {
  title: string;
  value: number;
  limit: number;
}) {
  return (
    <div className="settings-usage">
      <div>
        <strong>{title}</strong>
        <span>
          {value} of {limit}
        </span>
      </div>
      <i>
        <b style={{ width: `${Math.min(100, (value / limit) * 100)}%` }} />
      </i>
    </div>
  );
}
const FEATURE_COPY: Partial<
  Record<
    SettingsPageId,
    { title: string; description: string; rows: [string, string][] }
  >
> = {
  sla: {
    title: "SLAs",
    description: "Set response and resolution targets for issues.",
    rows: [
      ["Enable SLAs", "Track service level agreements across issue views"],
      ["Show SLA countdown", "Display remaining time on issue rows"],
    ],
  },
  "project-updates": {
    title: "Project updates",
    description: "Configure reminders and health update cadence.",
    rows: [
      ["Update reminders", "Remind project leads to post updates"],
      [
        "Missed update notifications",
        "Notify members when an update is overdue",
      ],
    ],
  },
  ai: {
    title: "AI & Agents",
    description: "Configure AI features and agents for your workspace.",
    rows: [
      ["Enable AI features", "Allow members to use Flow AI"],
      ["Agent sessions", "Allow agents to work on assigned issues"],
    ],
  },
  initiatives: {
    title: "Initiatives",
    description: "Organize projects into workspace initiatives.",
    rows: [["Enable initiatives", "Show initiatives in the workspace sidebar"]],
  },
  documents: {
    title: "Documents",
    description: "Create collaborative documents inside projects.",
    rows: [["Enable documents", "Allow workspace members to create documents"]],
  },
  "customer-requests": {
    title: "Customer requests",
    description: "Connect customer feedback to product work.",
    rows: [
      [
        "Enable customer requests",
        "Track customer needs on issues and projects",
      ],
    ],
  },
  releases: {
    title: "Releases",
    description: "Coordinate product releases across teams.",
    rows: [["Enable releases", "Show releases in project planning"]],
  },
  pulse: {
    title: "Pulse",
    description: "Share and discover project and initiative updates.",
    rows: [["Enable Pulse", "Show Pulse in the workspace sidebar"]],
  },
  asks: {
    title: "Asks",
    description: "Turn requests from connected tools into Flow issues.",
    rows: [["Enable Asks", "Allow members to create issues through Asks"]],
  },
  emojis: {
    title: "Emojis",
    description: "Manage custom emoji available in your workspace.",
    rows: [["Custom emoji", "Allow members to add workspace emoji"]],
  },
  integrations: {
    title: "Integrations",
    description: "Connect Flow with the tools your team uses.",
    rows: [
      ["GitHub", "Link pull requests and commits to issues"],
      ["Slack", "Create and update issues from Slack"],
      ["Figma", "Preview design links in issues"],
    ],
  },
};
function FeaturePage({
  page,
  data,
  onReload,
}: {
  page: SettingsPageId;
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const copy = FEATURE_COPY[page] ?? {
    title: page,
    description: "Workspace feature settings.",
    rows: [
      ["Enable feature", "Make this feature available to workspace members"],
    ] as [string, string][],
  };
  if (page === "integrations")
    return <IntegrationsSettings data={data} onReload={onReload} />;
  const enabled = data.workspaceSettings.featureFlags[page] ?? false;
  const save = async (value: boolean) => {
    try {
      await updateWorkspacePreferences({
        ...data.workspaceSettings,
        featureFlags: { ...data.workspaceSettings.featureFlags, [page]: value },
      });
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not update feature",
      );
    }
  };
  return (
    <>
      <PageTitle description={copy.description}>{copy.title}</PageTitle>
      <Section>
        <Row title={`Enable ${copy.title}`} description={copy.rows[0]?.[1]}>
          <Toggle
            label={`Enable ${copy.title}`}
            checked={enabled}
            onChange={(value) => void save(value)}
          />
        </Row>
      </Section>
    </>
  );
}
function IntegrationsSettings({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const update = async (provider: string, configured: boolean) => {
    try {
      if (configured) await disconnectIntegration(provider);
      else
        await connectIntegration(provider, {
          name: title(provider),
          config: { mode: "workspace-reference" },
        });
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not update integration",
      );
    }
  };
  return (
    <>
      <PageTitle description="Configure provider records, enterprise sign-in, scopes, channels, and delivery lifecycle.">
        Integrations
      </PageTitle>
      <Section>
        {[
          ["github", "Link pull requests and commits to issues"],
          ["slack", "Create and update issues from Slack"],
          ["figma", "Preview design links in issues"],
        ].map(([provider, description]) => {
          const item = data.integrationConnections.find(
            (connection) => connection.provider === provider,
          );
          return (
            <Row
              key={provider}
              title={title(provider)}
              description={`${description}. ${item ? "Configuration stored" : "Provider credentials not configured"}`}
            >
              <ActionButton
                danger={Boolean(item)}
                onClick={() => void update(provider, Boolean(item))}
              >
                {item ? "Remove configuration" : "Configure reference"}
              </ActionButton>
            </Row>
          );
        })}
      </Section>
      <EnterpriseIdentityProviders data={data} onReload={onReload} />
    </>
  );
}
function EnterpriseIdentityProviders({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const [editing, setEditing] = useState<IdentityProvider | null | undefined>(
      undefined,
    ),
    [busy, setBusy] = useState("");
  const mutate = async (
    id: string,
    action: () => Promise<unknown>,
    message: string,
  ) => {
    setBusy(id);
    try {
      await action();
      await onReload();
      toast.success(message);
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : "Identity provider operation failed",
      );
    } finally {
      setBusy("");
    }
  };
  return (
    <Section title="Identity providers">
      {data.identityProviders.map((item) => (
        <Row
          key={item.id}
          title={item.name}
          description={`${item.type.toUpperCase()} · ${item.domains.join(", ") || "No discovery domains"} · ${item.discoveryStatus}`}
        >
          <div className="settings-inline-actions">
            <Toggle
              label={`${item.name} enabled`}
              checked={item.enabled}
              onChange={(enabled) =>
                void mutate(
                  item.id,
                  () => updateIdentityProvider(item.id, { enabled }),
                  "Identity provider updated",
                )
              }
            />
            <ActionButton
              disabled={busy === item.id}
              onClick={() =>
                void mutate(
                  item.id,
                  () => verifyIdentityProvider(item.id),
                  "Provider verified",
                )
              }
            >
              Verify
            </ActionButton>
            <ActionButton onClick={() => setEditing(item)}>
              Configure
            </ActionButton>
            <ActionButton
              danger
              onClick={() =>
                void mutate(
                  item.id,
                  () => deleteIdentityProvider(item.id),
                  "Provider removed",
                )
              }
            >
              Remove
            </ActionButton>
          </div>
        </Row>
      ))}
      {!data.identityProviders.length && (
        <div className="settings-empty compact">
          <ShieldCheck size={24} />
          <h3>No identity providers</h3>
          <p>Configure OIDC or SAML for domain-based enterprise sign-in.</p>
        </div>
      )}
      <div className="settings-section-action">
        <ActionButton onClick={() => setEditing(null)}>
          <Plus size={14} />
          Add identity provider
        </ActionButton>
      </div>
      {editing !== undefined && (
        <IdentityProviderEditor
          provider={editing}
          onClose={() => setEditing(undefined)}
          onSaved={async () => {
            setEditing(undefined);
            await onReload();
          }}
        />
      )}
    </Section>
  );
}
function IdentityProviderEditor({
  provider,
  onClose,
  onSaved,
}: {
  provider: IdentityProvider | null;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [type, setType] = useState<"oidc" | "saml">(provider?.type ?? "oidc"),
    [name, setName] = useState(provider?.name ?? ""),
    [issuer, setIssuer] = useState(provider?.issuer ?? ""),
    [clientId, setClientId] = useState(provider?.clientId ?? ""),
    [secretEnv, setSecretEnv] = useState(provider?.clientSecretEnv ?? ""),
    [domains, setDomains] = useState((provider?.domains ?? []).join(", ")),
    [enabled, setEnabled] = useState(provider?.enabled ?? true),
    [enforced, setEnforced] = useState(provider?.enforced ?? false),
    [busy, setBusy] = useState(false);
  const submit = async () => {
    setBusy(true);
    const input = {
      type,
      name,
      issuer,
      clientId,
      clientSecretEnv: secretEnv,
      domains: domains
        .split(",")
        .map((v) => v.trim())
        .filter(Boolean),
      scopes: type === "oidc" ? ["openid", "profile", "email"] : [],
      enabled,
      enforced,
    };
    try {
      if (provider) await updateIdentityProvider(provider.id, input);
      else await createIdentityProvider(input);
      await onSaved();
      toast.success("Identity provider saved");
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : "Could not save identity provider",
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="settings-confirm settings-identity-provider-dialog">
        <DialogTitle>
          {provider ? "Configure identity provider" : "Add identity provider"}
        </DialogTitle>
        <label>
          Protocol
          <Select
            label="Identity protocol"
            value={type.toUpperCase()}
            options={["OIDC", "SAML"]}
            onChange={(value) =>
              setType(value.toLowerCase() as "oidc" | "saml")
            }
          />
        </label>
        <label>
          Name
          <input
            className="settings-input"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label>
          {type === "oidc" ? "Issuer URL" : "Metadata URL"}
          <input
            className="settings-input"
            value={issuer}
            onChange={(e) => setIssuer(e.target.value)}
          />
        </label>
        {type === "oidc" && (
          <>
            <label>
              Client ID
              <input
                className="settings-input"
                value={clientId}
                onChange={(e) => setClientId(e.target.value)}
              />
            </label>
            <label>
              Client secret environment variable
              <input
                className="settings-input"
                value={secretEnv}
                onChange={(e) => setSecretEnv(e.target.value)}
                placeholder="FLOW_IDP_CLIENT_SECRET"
              />
            </label>
          </>
        )}
        <label>
          Discovery domains
          <input
            className="settings-input"
            value={domains}
            onChange={(e) => setDomains(e.target.value)}
            placeholder="example.com"
          />
        </label>
        <div className="settings-identity-provider-toggles">
          <Row title="Enabled">
            <Toggle
              label="Identity provider enabled"
              checked={enabled}
              onChange={setEnabled}
            />
          </Row>
          <Row title="Require this provider">
            <Toggle
              label="Require this provider"
              checked={enforced}
              onChange={setEnforced}
            />
          </Row>
        </div>
        <footer>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            primary
            disabled={
              busy ||
              !name.trim() ||
              !issuer.trim() ||
              (type === "oidc" && !clientId.trim())
            }
            onClick={() => void submit()}
          >
            Save
          </ActionButton>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function storedUserValues(source?: UserSettings): StoredSettings["values"] {
  const values = Object.fromEntries(
    Object.entries(source ?? {}).filter(
      ([key]) =>
        !["userId", "updatedAt", "personalSettingsVersion"].includes(key),
    ),
  );
  return { ...DEFAULT_VALUES, ...values } as StoredSettings["values"];
}
function sameStoredValues(
  left: StoredSettings["values"],
  right: StoredSettings["values"],
) {
  const keys = Object.keys(left);
  return (
    keys.length === Object.keys(right).length &&
    keys.every((key) => Object.is(left[key], right[key]))
  );
}
function useUserStoredSettings(data: BootstrapData) {
  const source = data.userSettings[data.viewer.id];
  const sourceRef = useRef(source);
  sourceRef.current = source;
  const dirtyRef = useRef(false);
  const [state, setStateInternal] = useState<StoredSettings>(() => ({
    values: storedUserValues(source),
    lists: {},
  }));
  const setState = useCallback<
    React.Dispatch<React.SetStateAction<StoredSettings>>
  >((update) => {
    dirtyRef.current = true;
    setStateInternal(update);
  }, []);
  useEffect(() => {
    const next = data.userSettings[data.viewer.id];
    if (next)
      setStateInternal((current) => {
        if (dirtyRef.current) return current;
        const values = storedUserValues(next);
        return sameStoredValues(current.values, values)
          ? current
          : { ...current, values };
      });
  }, [data.userSettings, data.viewer.id]);
  useEffect(() => {
    if (!dirtyRef.current) return;
    const timeout = window.setTimeout(() => {
      const current = sourceRef.current;
      if (!current) return;
      dirtyRef.current = false;
      void updateUserSettings({
        ...current,
        ...state.values,
      } as UserSettings).catch(() => {
        dirtyRef.current = true;
        toast.error("Could not save settings");
      });
    }, 250);
    return () => window.clearTimeout(timeout);
  }, [data.viewer.id, state.values]);
  useEffect(() => {
    const root = document.documentElement;
    applyTheme(state.values);
    root.style.fontSize =
      state.values.fontSize === "Small"
        ? "14px"
        : state.values.fontSize === "Large"
          ? "18px"
          : "";
    root.classList.toggle(
      "settings-pointer-cursor",
      Boolean(state.values.pointerCursor),
    );
    root.classList.toggle(
      "settings-underline-links",
      Boolean(state.values.underlineLinks),
    );
    root.classList.toggle(
      "settings-reduce-animated-media",
      Boolean(state.values.disableAnimatedImages),
    );
  }, [state.values]);
  return [state, setState] as const;
}

function memberCanManage(page: SettingsPageId, settings: WorkspaceSettings) {
  if (["issue-labels", "project-labels"].includes(page))
    return settings.labelPermission === "members";
  if (["issue-templates", "project-templates"].includes(page))
    return settings.templatePermission === "members";
  if (page === "api") return settings.apiKeyPermission === "members";
  return false;
}
function initials(value: string) {
  return (
    value
      .split(/\s+/)
      .filter(Boolean)
      .map((part) => part[0])
      .join("")
      .slice(0, 2)
      .toUpperCase() || "W"
  );
}
function title(value: string) {
  return value ? value[0].toUpperCase() + value.slice(1) : value;
}
function formatDate(value: string) {
  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
  }).format(new Date(value));
}
function currency(cents: number) {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
  }).format(cents / 100);
}
function slug(value: string) {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 32);
}
