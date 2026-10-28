import { useLatestRef } from '@/hooks/use-latest-ref'
import { useResetOnChange } from "@/hooks/use-reset-on-change";
import { WelcomeMessageSettingsPage } from "./welcome-message-settings-page";
import { CodingToolsSettingsPage } from "./coding-tools-settings-page";
import {
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { persistUserSettings } from '@/lib/settings-persistence';
import { settingsSidebarTeams } from './settings-sidebar-teams';
import { UploadPolicyDialog } from './upload-policy-dialog';
import { ApplicationPolicySettings, DataPrivacyDialog } from './application-policy-settings';
import type { IntegrationProvider, UsageSettingsView } from '@/lib/app-routes';
import { adminHintsApply, hiddenSettingsMessage, isPersonalSettingsPage, isWorkspaceAdmin, roleAllowed, teamReadOnlyNotice, teamSettingsAccess, workspaceReadOnlyNotice, workspaceSettingsAccess } from '@/lib/settings-access';
import { AdminOnlyHint } from './admin-only-hint';
import { EnabledIntegrationNav } from './enabled-integration-nav';
import { useSecuritySetting } from '@/hooks/use-security-setting';
import { securityPermissionLabel, type SecuritySettingKey } from '@/lib/security-setting';
import {
  ArrowLeft,
  ChevronDown,
  ChevronRight,
  Upload,
  KeyboardOff,
  MonitorX,
  ListFilter,
  MoreHorizontal,
  Plus,
  Search,
  ShieldCheck,
  Trash2,
  UserRound,
  X,
  Lock,
} from "lucide-react";
import { toast } from "@/lib/toast";
import { useI18n } from "@/i18n/i18n";
import { useChatPlatform } from "@/lib/chat-platform";

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
  createIdentityProvider,
  deleteIdentityProvider,
  disconnectIntegration,
  revokeAPIKey,
  inviteMembers,
  removeMember,
  resendInvitation,
  revokeInvitation,
  setTeamMembership,
  suspendMember,
  updateAgentSkill,
  updateIdentityProvider,
  updateMemberRole,
  updateWebhook,
  updateWorkspacePreferences,
  uploadWorkspaceLogo,
  deleteWorkspaceLogo,
  verifyIdentityProvider,
  disableWorkspaceInviteLink,
  fetchWorkspaceInviteLink,
  rotateWorkspaceInviteLink,
} from "@/lib/api";
import type { NotificationSettingsView, SettingsPageId, TeamSettingsSection } from "@/lib/app-routes";
import { AgentCursorGlyph, AgentHistoryGlyph } from '@/components/ui/agent-glyph'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { AppLink } from "@/components/ui/app-link";
import { SettingsNavGlyph, type SettingsNavGlyphName } from './settings-nav-glyphs'
import {
  apiKeysSettingsPath,
  identityProviderSettingsPath,
  integrationSettingsPath,
  settingsPath,
  webhookSettingsPath,
} from "@/lib/app-routes";
import { agentSkillsSettingsBackLink } from "@/lib/agent-skills-settings-back-link";
import { useNavigate } from "react-router-dom";
import type {
  APIKey,
  BootstrapData,
  IdentityProvider,
  IssueTemplate,
  ProjectTemplate,
  ReleasePipeline,
  UserSettings,
  WorkspaceMutationInput,
  WorkspaceSettings,
} from "@/types/flow";
import { lazyPage } from "@/lib/lazy-page";
import { TeamCreatePage } from '@/lib/route-pages';
import {
  SettingsPageTitle as PageTitle,
  SettingsRow as Row,
  SettingsCrumb,
  SettingsGroup as Group,
  SettingsSection as Section,
  SettingsSelect as Select,
  SettingsToggle as Toggle,
} from "./settings-primitives";
import { SettingsReadOnlyCrumbSlot } from "./settings-read-only-context";
import { SettingsSearchResults } from "./settings-search-results";
import { chatPlatformSearchPages, createSettingsSearchIndex, searchTeams, SETTINGS_SEARCH_PAGES } from "./settings-search";

import "./settings.css";
import "./workflow-settings.css";
import "./advanced-settings.css";
import "./settings-parity.css";
import { WebhookEditPage } from "./webhook-edit-page";
import {
  webhookResourceLabel,
  webhookStateLabel,
} from "@/lib/webhook-resources";
import { WorkspaceTeamsSettings } from "./workspace-teams-settings";
import { applyTheme } from "@/lib/theme";
import { workspaceRegionLabel } from "@/components/workspace/workspace-regions";
import { SidebarCustomization } from "@/components/layout/sidebar";
import { useSidebarCustomizationState } from "@/components/layout/sidebar-customization-state";
import { agentWebAccessEnabled } from "@/lib/agent-web-access";
import { mcpConnectorsEnabled } from "@/lib/mcp-connectors";
import { InstalledApplicationPage, InstalledApplicationsPage } from "./installed-applications-section";
import { OAuthApplicationsSection } from "./oauth-applications-section";
import { WorkspaceMembersPage } from "./workspace-members-page";

const TeamWorkflowSettings = lazyPage(
  () => import("./team-workflow-settings"),
  "TeamWorkflowSettings",
);
const BillingSettings = lazyPage(
  () => import("./billing-settings"),
  "BillingSettings",
);
const UsageSettings = lazyPage(
  () => import("./usage-settings"),
  "UsageSettings",
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
const EnabledIntegrationsSettingsPage = lazyPage(
  () => import("./enabled-integrations-page"),
  "EnabledIntegrationsSettingsPage",
);
const IntegrationSettingsPage = lazyPage(
  () => import("./integration-settings-page"),
  "IntegrationSettingsPage",
);
const JiraSettingsPage = lazyPage(
  () => import("@/components/settings/jira-settings"),
  "JiraSettingsPage",
);
const JiraSyncPage = lazyPage(
  () => import("@/components/settings/jira-sync-page"),
  "JiraSyncPage",
);
const AsksSlackSettingsPage = lazyPage(
  () => import("./asks-settings"),
  "AsksSlackSettingsPage",
);
const AsksEmailIntakeDetailPage = lazyPage(
  () => import("./asks-settings"),
  "AsksEmailIntakeDetailPage",
);
const NewAsksEmailIntakePage = lazyPage(
  () => import("./asks-settings"),
  "NewAsksEmailIntakePage",
);
const IdentityProviderSettingsPage = lazyPage(
  () => import("./identity-provider-settings-page"),
  "IdentityProviderSettingsPage",
);
const ApplicationDetailsPage = lazyPage(
  () => import("./application-pages"),
  "ApplicationDetailsPage",
);
const ApplicationCreatePage = lazyPage(
  () => import("./application-pages"),
  "ApplicationCreatePage",
);
const ApplicationEditPage = lazyPage(
  () => import("./application-pages"),
  "ApplicationEditPage",
);
const AuditLogSettings = lazyPage(
  () => import("./audit-log-settings"),
  "AuditLogSettings",
);
const WorkflowAutomationSettings = lazyPage(
  () => import("./workflow-automation-settings"),
  "WorkflowAutomationSettings",
);

// oxlint-disable-next-line react/only-export-components -- Preloading must share the lazy instances used by SettingsBody.
export async function preloadSettingsPage(page: SettingsPageProps['page'], options: Pick<SettingsPageProps, 'releasePipelineMode' | 'integrationProvider' | 'integrationSlug' | 'jiraSyncMode' | 'agentSkillMode' | 'asksIntegrationId' | 'asksEmailIntakeMode'> = {}) {
  if (options.agentSkillMode) return
  if (['preferences', 'profile', 'notifications', 'code-and-reviews', 'account-security', 'connections', 'agents'].includes(page)) return PersonalSettings.preload()
  if (page === 'issue-labels' || page === 'project-labels' || page === 'initiative-labels') return DomainLabelsSettings.preload()
  if (page === 'project-statuses') return ProjectStatusesSettings.preload()
  if (page === 'issue-templates' || page === 'project-templates') return TemplateSettings.preload()
  if (page === 'sla') return SLASettings.preload()
  if (page === 'project-updates') return ProjectUpdateSettings.preload()
  if (page === 'team') return TeamWorkflowSettings.preload()
  if (page === 'audit-log') return AuditLogSettings.preload()
  if (page === 'import-export') return ImportExportSettings.preload()
  if (page === 'billing') return BillingSettings.preload()
  if (page === 'usage') return UsageSettings.preload()
  if (page === 'workflows') return WorkflowAutomationSettings.preload()
  if (page === 'releases' && options.releasePipelineMode) return PipelineEditorPage.preload()
  if (page === 'integrations' && options.integrationSlug === 'enabled') return EnabledIntegrationsSettingsPage.preload()
  if (page === 'integrations' && options.integrationProvider === 'jira') {
    if (options.jiraSyncMode) return JiraSyncPage.preload()
    return JiraSettingsPage.preload()
  }
  if (page === 'integrations' && options.integrationProvider) return CodeIntegrationSettings.preload()
  if (page === 'integrations' && options.integrationSlug) return IntegrationSettingsPage.preload()
  if (page === 'asks' && options.asksEmailIntakeMode) return NewAsksEmailIntakePage.preload()
  if (page === 'asks' && options.asksIntegrationId) return AsksSlackSettingsPage.preload()
  if (['ai', 'ai-agent', 'code-intelligence', 'triage-intelligence', 'ai-summaries', 'ai-updates', 'installed-agents', 'coding-sessions', 'coding-environments', 'initiatives', 'documents', 'customer-requests', 'releases', 'pulse', 'asks', 'emojis', 'integrations'].includes(page)) return FeatureSettingsPage.preload()
}

/** The sidebar entry a settings page highlights: AI & agents covers its sub-pages and the workspace Agents page. */
function settingsNavPage(page: SettingsPageId): SettingsPageId {
  return ["ai-agent", "code-intelligence", "triage-intelligence", "ai-summaries", "ai-updates", "installed-agents", "coding-sessions", "coding-environments"].includes(page) ? "ai" : page;
}

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
  notificationChannel?: NotificationSettingsView;
  onNavigateNotification?: (channel?: NotificationSettingsView) => void;
  apiKeyMode?: "new" | "detail" | "edit";
  apiKeyId?: string;
  signingKeyMode?: "new";
  teamKey?: string;
  teamSection?: TeamSettingsSection;
  releasePipelineMode?: "new" | "edit";
  workspaceView?: "welcome-message";
  accountView?: "coding-tools";
  apiView?: "keys";
  usageView?: UsageSettingsView;
  onOpenUsageView?: (view?: UsageSettingsView) => void;
  webhookId?: string;
  releasePipelineSlug?: string;
  integrationProvider?: IntegrationProvider;
  integrationSlug?: string;
  jiraSyncMode?: "new" | "edit";
  jiraProjectId?: string;
  onOpenJiraSyncNew?: () => void;
  onOpenJiraSyncEdit?: (jiraProjectId: string) => void;
  asksIntegrationId?: string;
  asksEmailIntakeMode?: "new" | "edit";
  asksEmailIntakeId?: string;
  onOpenAsksSlack?: (integrationId: string) => void;
  onOpenAsksEmailIntake?: (addressId?: string) => void;
  onOpenWelcomeMessage?: () => void;
  onOpenCodingTools?: () => void;
  identityProviderId?: string;
  applicationId?: string;
  applicationMode?: "detail" | "edit";
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
    teamSubPath?: string,
  ) => void;
  teamSubPath?: string;
  onCreateAPIKey?: () => void;
  onCreateSigningKey?: () => void;
  onOpenAPIKey?: (key: APIKey) => void;
  onEditAPIKey?: (key: APIKey) => void;
  onLogout?: () => Promise<void>;
  onNavigateAgent?: () => void;
  onOpenAgentHistory?: () => void;
  onCreateReleasePipeline: () => void;
  onOpenReleasePipeline: (pipeline: ReleasePipeline) => void;
  onOpenIntegration: (provider: IntegrationProvider | string) => void;
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

type NavItem = { id: SettingsPageId; label: string; glyph: SettingsNavGlyphName };
const NAV: { title: string; items: NavItem[] }[] = [
  {
    title: "Personal",
    items: [
      { id: "preferences", label: "Preferences", glyph: "preferences" },
      { id: "profile", label: "Profile", glyph: "profile" },
      { id: "notifications", label: "Notifications", glyph: "notifications" },
      { id: "code-and-reviews", label: "Code & reviews", glyph: "code" },
      { id: "account-security", label: "Security & access", glyph: "accountSecurity" },
      { id: "connections", label: "Connected accounts", glyph: "connections" },
      { id: "agents", label: "Agent personalization", glyph: "agents" },
    ],
  },
  {
    title: "Issues",
    items: [
      { id: "issue-labels", label: "Labels", glyph: "labels" },
      { id: "issue-templates", label: "Templates", glyph: "templates" },
      { id: "sla", label: "SLAs", glyph: "sla" },
    ],
  },
  {
    title: "Projects",
    items: [
      { id: "project-labels", label: "Labels", glyph: "labels" },
      { id: "project-templates", label: "Templates", glyph: "templates" },
      { id: "project-statuses", label: "Statuses", glyph: "projectStatuses" },
      { id: "project-updates", label: "Updates", glyph: "projectUpdates" },
    ],
  },
  {
    title: "Features",
    items: [
      { id: "ai", label: "AI & agents", glyph: "ai" },
      { id: "loops", label: "Loops", glyph: "loops" },
      { id: "initiatives", label: "Initiatives", glyph: "initiatives" },
      { id: "documents", label: "Documents", glyph: "documents" },
      { id: "customer-requests", label: "Customer requests", glyph: "customerRequests" },
      { id: "releases", label: "Releases", glyph: "releases" },
      { id: "pulse", label: "Pulse", glyph: "pulse" },
      { id: "asks", label: "Asks", glyph: "asks" },
      { id: "emojis", label: "Emojis", glyph: "emojis" },
    ],
  },
  {
    // The reference app: its own section. Members see "Integrations"; admins "All
    // integrations" followed by each enabled integration (EnabledIntegrationNav).
    title: "Integrations",
    items: [{ id: "integrations", label: "Integrations", glyph: "integrations" }],
  },
  {
    title: "Administration",
    items: [
      { id: "workspace", label: "Workspace", glyph: "workspace" },
      { id: "teams", label: "Teams", glyph: "teams" },
      { id: "members", label: "Members", glyph: "members" },
      { id: "security", label: "Security", glyph: "security" },
      { id: "api", label: "API", glyph: "code" },
      { id: "applications", label: "Applications", glyph: "applications" },
      { id: "billing", label: "Billing", glyph: "billing" },
      { id: "usage", label: "Usage & limits", glyph: "usage" },
      { id: "import-export", label: "Import & export", glyph: "importExport" },
    ],
  },
];

const DEFAULT_VALUES: StoredSettings["values"] = {
  homeView: "Flow Agent (default)",
  displayNames: "Full name",
  firstDay: "",
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
  codeTheme: "Match interface theme",
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
  const { locale, t } = useI18n();
  const [query, setQuery] = useState("");
  const [mobileNav, setMobileNav] = useState(false);
  const [sidebarCustomizationOpen, setSidebarCustomizationOpen] =
    useState(false);
  const mainRef = useRef<HTMLElement | null>(null);
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const searchResultsRef = useRef<HTMLDivElement | null>(null);
  const sidebarRef = useRef<HTMLElement | null>(null);
  // Opening a page deep in the nav (direct link, search result) reveals its
  // item (centered), like the reference sidebar; visible items stay put.
  useEffect(() => {
    const nav = sidebarRef.current?.querySelector<HTMLElement>("nav");
    const active = nav?.querySelector<HTMLElement>("button.active");
    if (!nav || !active) return;
    const navBox = nav.getBoundingClientRect();
    const box = active.getBoundingClientRect();
    if (box.top < navBox.top + 29 || box.bottom > navBox.bottom - 4) {
      nav.scrollTop += box.top - navBox.top - (navBox.height - box.height) / 2;
    }
  }, [props.page, props.teamKey, props.teamSection]);
  const searchTargetRequestRef = useRef(0);
  const sidebarCustomization = useSidebarCustomizationState(props.data.viewer.id);
  const [settings, setSettings] = useUserStoredSettings(props.data);
  // The reference app: "Team creation" (Settings → Security) defaults to all members.
  const canCreateTeam = roleAllowed(props.data.viewerRole, props.data.workspaceSettings?.teamCreatePermission);
  const allSidebarTeams = useMemo(() => settingsSidebarTeams(props.data, ""), [props.data]);
  const sidebarTeams = allSidebarTeams;
  const visible = useMemo(
    () =>
      NAV.map((section) => ({
        ...section,
        items: section.items
          .filter((item) => workspaceSettingsAccess(props.data, item.id) !== "hidden")
          .map((item) => item.id === "integrations" && isWorkspaceAdmin(props.data) ? { ...item, label: "All integrations" } : item),
      })).filter((section) => section.items.length),
    [props.data],
  );
  const accessiblePageIds = useMemo(
    () => new Set(visible.flatMap(section => section.items.map(item => item.id))),
    [visible],
  );
  // Entries about the chat platform name Lark or Feishu when it is the deployment's platform.
  const searchChat = useChatPlatform(props.data);
  const searchChatName = searchChat.name;
  const searchTranslate = useCallback((value: string) => t(value).replaceAll("{name}", searchChatName), [t, searchChatName]);
  const searchablePages = useMemo(
    () => chatPlatformSearchPages(SETTINGS_SEARCH_PAGES, searchChat.platform).filter(page => accessiblePageIds.has(page.id)),
    [accessiblePageIds, searchChat.platform],
  );
  const settingsSearchIndex = useMemo(
    () => createSettingsSearchIndex(searchablePages, searchTranslate),
    [searchablePages, searchTranslate],
  );
  const searchResults = useMemo(
    () => settingsSearchIndex.search(query),
    [query, settingsSearchIndex],
  );
  const searchableTeams = useMemo(
    () => allSidebarTeams.map(({ team }) => ({ key: team.key, name: team.name })),
    [allSidebarTeams],
  );
  const teamSearchResults = useMemo(
    () => searchTeams(query, searchableTeams, t, locale),
    [locale, query, searchableTeams, t],
  );
  const searchPageMeta = useMemo(
    () => Object.fromEntries(visible.flatMap(section => section.items.map(item => [
      item.id,
      {
        id: item.id,
        title: item.label,
        section: section.title,
        icon: <SettingsNavGlyph name={item.glyph} size={14}/>,
      },
    ]))),
    [visible],
  );
  const trimmedQuery = query.trim();
  const scrollToSearchTarget = useCallback((targetTitle: string) => {
    const request = ++searchTargetRequestRef.current;
    const scrollToTarget = (attempt = 0) => {
      window.setTimeout(() => {
        if (request !== searchTargetRequestRef.current) return;
        const target = findSettingsSearchTarget(searchTranslate(targetTitle));
        if (target) {
          target.scrollIntoView({ behavior: "smooth", block: "center" });
          target.classList.add("settings-search-target");
          window.setTimeout(() => target.classList.remove("settings-search-target"), 1600);
        } else if (attempt < 12) {
          scrollToTarget(attempt + 1);
        }
      }, attempt ? 80 : 0);
    };
    scrollToTarget();
  }, [searchTranslate]);
  const selectSearchResult = useCallback((result: (typeof searchResults)[number]) => {
    props.onNavigate(result.page);
    if (result.targetTitle) {
      scrollToSearchTarget(result.targetTitle);
    }
  }, [props, scrollToSearchTarget]);
  const selectSearchTeam = useCallback((teamKey: string, section?: TeamSettingsSection, targetTitle?: string) => {
    props.onNavigate("team", teamKey, section);
    if (targetTitle) scrollToSearchTarget(targetTitle);
  }, [props, scrollToSearchTarget]);
  const moveSearchFocus = useCallback((delta: number) => {
    const items = Array.from(searchResultsRef.current?.querySelectorAll<HTMLButtonElement>("[data-settings-search-result]") ?? []);
    if (!items.length) return;
    const active = document.activeElement;
    const index = items.indexOf(active as HTMLButtonElement);
    const next = index < 0
      ? delta > 0 ? 0 : items.length - 1
      : (index + delta + items.length) % items.length;
    items[next]?.focus();
  }, []);
  const setValue = (key: string, value: string | boolean) => {
    if (Object.is(settings.values[key], value)) return;
    setSettings((current) => ({
      ...current,
      values: { ...current.values, [key]: value },
    }));
  };
  // Nested settings routes should always start at the top of their content.
  useEffect(() => {
    const main = mainRef.current;
    if (!main) return;
    if (typeof main.scrollTo === "function") {
      main.scrollTo({ top: 0, left: 0 });
    } else {
      main.scrollTop = 0;
      main.scrollLeft = 0;
    }
  }, [
    props.page,
    props.apiKeyMode,
    props.signingKeyMode,
    props.teamKey,
    props.teamSection,
    props.issueTemplateMode,
    props.issueTemplateId,
    props.projectTemplateMode,
    props.projectTemplateId,
    props.agentSkillMode,
    props.agentSkillId,
    props.releasePipelineMode,
    props.releasePipelineSlug,
    props.integrationProvider,
    props.integrationSlug,
  ]);
  useEffect(() => {
    const focusSearch = (event: globalThis.KeyboardEvent) => {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
      if (isTextEditingTarget(event.target)) return;
      event.preventDefault();
      searchInputRef.current?.focus();
    };
    window.addEventListener("keydown", focusSearch);
    return () => window.removeEventListener("keydown", focusSearch);
  }, []);
  return (
    <>
      <div className="settings-app">
        <aside ref={sidebarRef} className={`settings-sidebar${mobileNav ? " open" : ""}`}>
          <button className="settings-back" onClick={props.onBack}>
            <ArrowLeft size={16} />
            {t("Back to app")}
          </button>
          <label className="settings-search">
            <Search size={15} />
            <input
              aria-label="Search settings"
              aria-controls="settings-search-results"
              aria-expanded={Boolean(trimmedQuery)}
              placeholder={t("Search…")}
              ref={searchInputRef}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                  event.preventDefault();
                  moveSearchFocus(event.key === "ArrowDown" ? 1 : -1);
                } else if (event.key === "Enter" && document.activeElement !== searchInputRef.current) {
                  event.preventDefault();
                  (document.activeElement as HTMLElement | null)?.click();
                } else if (event.key === "Escape") {
                  event.preventDefault();
                  event.stopPropagation();
                  if (query.length > 0) setQuery("");
                  else searchInputRef.current?.blur();
                }
              }}
            />
            {query && (
              <button aria-label="Clear search" onClick={() => { setQuery(""); searchInputRef.current?.focus(); }}>
                <X size={13} />
              </button>
            )}
          </label>
          <nav
            aria-label="Settings navigation"
            onKeyDown={(event) => {
              if (!trimmedQuery) return;
              if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault();
                moveSearchFocus(event.key === "ArrowDown" ? 1 : -1);
              } else if (event.key === "Escape") {
                event.preventDefault();
                event.stopPropagation();
                setQuery("");
                searchInputRef.current?.focus();
              }
            }}
            ref={trimmedQuery ? searchResultsRef : undefined}
          >
            {trimmedQuery ? (
              <div id="settings-search-results">
                <SettingsSearchResults
                  onSelectPage={page => props.onNavigate(page)}
                  onSelectResult={selectSearchResult}
                  onSelectTeam={selectSearchTeam}
                  pageMeta={searchPageMeta}
                  query={trimmedQuery}
                  results={searchResults}
                  teamResults={teamSearchResults}
                  format={text => text.replaceAll("{name}", searchChatName)}
                />
              </div>
            ) : visible.map((section) => (
              <section key={section.title}>
                <h2>{t(section.title)}</h2>
                {section.title === "Integrations" && isWorkspaceAdmin(props.data) ? (
                  <EnabledIntegrationNav
                    data={props.data}
                    onIntegrationsPage={props.page === "integrations"}
                    provider={props.integrationProvider}
                    slug={props.integrationSlug}
                    onOpenAll={() => { props.onNavigate("integrations"); setMobileNav(false); }}
                    onOpen={(target) => { props.onOpenIntegration(target); setMobileNav(false); }}
                  />
                ) : section.items.map((item) => (
                  <SettingsNavButton
                    key={item.id}
                    item={item}
                    active={settingsNavPage(props.page) === item.id}
                    onClick={() => {
                      props.onNavigate(item.id);
                      setMobileNav(false);
                    }}
                  />
                ))}
              </section>
            ))}
            {!trimmedQuery && props.data.viewerRole !== "guest" && (
              <section>
                <h2>{t("Your teams")}</h2>
                {sidebarTeams.map(({ team, depth }) => (
                    <button
                      key={team.id}
                      style={depth ? { paddingInlineStart: 10 + Math.min(depth, 6) * 18 } : undefined}
                      aria-current={props.page === 'team' && props.teamKey?.toLowerCase() === team.key.toLowerCase() ? 'page' : undefined}
                      className={
                        props.page === "team" &&
                        props.teamKey?.toLowerCase() === team.key.toLowerCase()
                          ? "active"
                          : ""
                      }
                      onClick={() => { props.onNavigate("team", team.key); setMobileNav(false); }}
                      onPointerEnter={() => { void TeamWorkflowSettings.preload().catch(() => undefined); }}
                      onFocus={() => { void TeamWorkflowSettings.preload().catch(() => undefined); }}
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
                      <span data-i18n-ignore>{team.name}</span>
                    </button>
                  ))}
                {canCreateTeam && !query.trim() && (
                  <button onPointerEnter={() => { void TeamCreatePage.preload().catch(() => undefined); }} onFocus={() => { void TeamCreatePage.preload().catch(() => undefined); }} onClick={props.onCreateTeam}>
                    <FlowGlyph name="plus" size={14} className="settings-nav-create-glyph" />
                      <span>{t("Create a team")}</span>
                  </button>
                )}
              </section>
            )}
          </nav>
        </aside>
        <button
          className={`settings-sidebar-scrim${mobileNav ? " open" : ""}`}
          aria-label={t("Close settings navigation")}
          onClick={() => setMobileNav(false)}
        />
        <main ref={mainRef} className="settings-main" data-settings-page={props.page} data-team-section={props.teamSection}>
          <button
            className="settings-mobile-menu"
            aria-label={t("Settings navigation")}
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
          {props.page !== "team" && adminHintsApply(props.data, props.page) && (
            <AdminOnlyHint
              containerRef={mainRef}
              page={props.page}
              readOnly={settingsBodyAccess(props).kind === "read"}
              workspaceSlug={props.data.workspace.urlKey}
            />
          )}
        </main>
      </div>
      {(props.onNavigateAgent || props.onOpenAgentHistory) && (
        <div className="bottom-agent settings-bottom-agent">
          {props.onNavigateAgent && (
            <button type="button" aria-label="Agent" onClick={props.onNavigateAgent}>
              <AgentCursorGlyph size={14} />
              <span>Agent</span>
            </button>
          )}
          {props.onOpenAgentHistory && (
            <button type="button" aria-label="Chat history" onClick={props.onOpenAgentHistory}>
              <AgentHistoryGlyph size={14} />
            </button>
          )}
        </div>
      )}
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

function findSettingsSearchTarget(title: string) {
  const expected = normalizeSettingsSearchText(title);
  const candidates = Array.from(document.querySelectorAll<HTMLElement>(".settings-row, .settings-section, .feature-row, .feature-section"));
  const labelFor = (candidate: HTMLElement) => {
    const selector = candidate.matches(".feature-row") ? ":scope > div > strong"
      : candidate.matches(".feature-section") ? ":scope > header > h2"
      : candidate.matches(".settings-section") ? ":scope > h3" : ".settings-row-copy > strong";
    return normalizeSettingsSearchText(candidate.querySelector(selector)?.textContent ?? "");
  };
  const exact = candidates.find(candidate => labelFor(candidate) === expected);
  if (exact) return exact;
  return candidates.find(candidate => labelFor(candidate).startsWith(expected)) ?? null;
}

function normalizeSettingsSearchText(value: string) {
  return value
    .replaceAll("…", "")
    .replace(/\s+/g, " ")
    .trim()
    .toLocaleLowerCase();
}

function isTextEditingTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable ||
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLSelectElement;
}

function SettingsPageFallback() {
  const { t } = useI18n();
  return (
    <div className="settings-loading" aria-label={t("Loading settings")}>
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
  const { t } = useI18n();
  return (
    <button aria-current={active ? "page" : undefined} className={active ? "active" : ""} onPointerEnter={() => { void preloadSettingsPage(item.id).catch(() => undefined); }} onFocus={() => { void preloadSettingsPage(item.id).catch(() => undefined); }} onClick={onClick}>
      <SettingsNavGlyph name={item.glyph} />
      <span>{t(item.label)}</span>
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
  const chat = useChatPlatform(props.data);
  const access = settingsBodyAccess(props);
  if (access.kind === "hidden" && access.team && props.teamSection === "security" && props.teamKey)
    return <TeamOverviewRedirect teamKey={props.teamKey} onNavigate={props.onNavigate} />;
  if (access.kind === "hidden")
    return access.team ? (
      <div className="settings-empty" role="status">
        <ShieldCheck size={28} />
        <h3>{t("You need permission to view this team’s settings.")}</h3>
      </div>
    ) : (
      // The reference app answers an administration URL a member cannot open with "Not found".
      <div className="settings-shortcuts-not-found" role="status">
        <MonitorX size={68} strokeWidth={1} />
        <strong>{t("Not found")}</strong>
        <span>{t(hiddenSettingsMessage(props.page))}</span>
      </div>
    );
  const body = <SettingsBodyContent {...props} />;
  return access.kind === "read" ? <ReadOnlySettings notice={access.notice} name={chat.name} banner={props.page === "team"}>{body}</ReadOnlySettings> : body;
}

/** The reference app sends members without access to a team section back to the team overview. */
function TeamOverviewRedirect({ teamKey, onNavigate }: { teamKey: string; onNavigate: SettingsPageProps["onNavigate"] }) {
  useEffect(() => {
    onNavigate("team", teamKey);
  }, [onNavigate, teamKey]);
  return null;
}

type SettingsBodyAccess = { kind: "hidden"; team: boolean } | { kind: "read"; notice: string } | { kind: "edit" };

function settingsBodyAccess(props: SettingsPageProps): SettingsBodyAccess {
  const { page, data } = props;
  if (page === "team") {
    const team = data.teams.find((item) => item.key.toLowerCase() === props.teamKey?.toLowerCase());
    // Unknown teams fall through to the "Team not found" state.
    if (!team) return { kind: "edit" };
    const section = props.teamSection ?? "overview";
    const access = teamSettingsAccess(data, team.id, section);
    if (access === "hidden") return { kind: "hidden", team: true };
    // The overview is a navigation hub; it disables owner-only actions itself.
    if (access === "read" && section !== "overview") return { kind: "read", notice: teamReadOnlyNotice(section) };
    return { kind: "edit" };
  }
  const access = workspaceSettingsAccess(data, page);
  if (access === "hidden") return { kind: "hidden", team: false };
  if (access === "read") return { kind: "read", notice: workspaceReadOnlyNotice(page) };
  if (isWorkspaceAdmin(data)) return { kind: "edit" };
  // Members may browse the integrations directory and Asks, but configuring an
  // integration or an Asks intake is an admin operation (the reference app: integrationManagement).
  if (page === "integrations" && (props.integrationProvider || props.integrationSlug) && props.integrationSlug !== "enabled")
    return { kind: "read", notice: "Only workspace admins can configure this" };
  if (page === "asks" && (props.asksIntegrationId || props.asksEmailIntakeMode || props.asksEmailIntakeId))
    return { kind: "read", notice: "You do not have permission to change Asks settings" };
  return { kind: "edit" };
}

/**
 * Read-only settings: every control is disabled. Workspace pages explain it on
 * hover like the reference app (AdminOnlyHint); team pages keep the notice above the fields.
 */
function ReadOnlySettings({ notice, name, banner, children }: { notice: string; name: string; banner: boolean; children: ReactNode }) {
  const { t } = useI18n();
  // Notices name the deployment's chat platform as {name}.
  const text = t(notice).replaceAll("{name}", name);
  // A stable element (not state) so Suspense re-attaching refs cannot loop.
  const [crumbSlot] = useState(() => document.createElement("div"));
  return (
    <div className="settings-read-only">
      <div
        className="settings-read-only-crumb"
        ref={(element) => {
          if (element && crumbSlot.parentNode !== element) element.appendChild(crumbSlot);
        }}
      />
      {banner && (
        <p className="settings-read-only-notice" role="note">
          <Lock size={14} aria-hidden />
          <span>{text}</span>
        </p>
      )}
      <fieldset disabled className="settings-read-only-fields" aria-label={text} data-read-only-notice={notice}>
        <SettingsReadOnlyCrumbSlot.Provider value={crumbSlot}>
          {children}
        </SettingsReadOnlyCrumbSlot.Provider>
      </fieldset>
    </div>
  );
}

function SettingsBodyContent(
  props: SettingsPageProps & {
    settings: StoredSettings;
    setSettings: React.Dispatch<React.SetStateAction<StoredSettings>>;
    setValue: (key: string, value: string | boolean) => void;
    onCustomizeSidebar: () => void;
  },
) {
  const { t } = useI18n();
  const { page } = props;
  if (page === 'shortcuts') return <div className="settings-shortcuts-not-found" role="status"><KeyboardOff size={68} strokeWidth={1}/><strong>{t('Not found')}</strong><span>{t('We could not find the page you were looking for')}</span></div>;
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
  if (page === "code-and-reviews" && props.accountView === "coding-tools")
    return (
      <CodingToolsSettingsPage
        data={props.data}
        onBack={() => props.onNavigate("code-and-reviews")}
        onReload={props.onReload}
      />
    );
  if (isPersonalSettingsPage(page))
    return (
      <PersonalSettings
        onOpenCodingTools={props.onOpenCodingTools}
        page={page}
        notificationChannel={props.notificationChannel}
        onNavigateNotification={props.onNavigateNotification}
        apiKeyMode={props.apiKeyMode}
        apiKeyId={props.apiKeyId}
        signingKeyMode={props.signingKeyMode}
        data={props.data}
        values={props.settings.values}
        setValue={props.setValue}
        onNavigate={props.onNavigate}
        onCreateAPIKey={props.onCreateAPIKey}
        onCreateSigningKey={props.onCreateSigningKey}
        onOpenAPIKey={props.onOpenAPIKey}
        onEditAPIKey={props.onEditAPIKey}
        onLogout={props.onLogout}
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
  if (page === "initiative-labels")
    return (
      <DomainLabelsSettings
        data={props.data}
        resourceType="initiative"
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
  if (page === "workspace" && props.workspaceView === "welcome-message")
    return (
      <WelcomeMessageSettingsPage
        data={props.data}
        onBack={() => props.onNavigate("workspace")}
        onReload={props.onReload}
      />
    );
  if (page === "workspace") return <WorkspacePage {...props} />;
  if (page === "teams")
    return (
      <WorkspaceTeamsSettings
        data={props.data}
        onCreate={props.onCreateTeam}
        onOpen={(team) => props.onNavigate("team", team.key)}
        onReload={props.onReload}
      />
    );
  if (page === "members")
    return <WorkspaceMembersPage data={props.data} onReload={props.onReload} />;
  if (page === "audit-log") return <AuditLogSettings data={props.data} />;
  // OAuth application developer pages (Settings › API › New / <app> / <app>/edit, as the reference app routes them). The older
  // /settings/applications/<id> links of a developer application still open its page.
  const developerApplication =
    props.applicationId && (page === "api" || (page === "applications" && (props.applicationId === "new" || props.applicationMode === "edit" || props.data.oauthApplications.some((item) => item.id === props.applicationId))))
      ? props.applicationId
      : "";
  if (developerApplication === "new")
    return (
      <ApplicationCreatePage
        data={props.data}
        onReload={props.onReload}
        onBack={() => props.onNavigate("api")}
      />
    );
  if (developerApplication && props.applicationMode === "edit")
    return (
      <ApplicationEditPage
        data={props.data}
        applicationId={developerApplication}
        onReload={props.onReload}
        onBack={() => props.onNavigate("api")}
      />
    );
  if (developerApplication)
    return (
      <ApplicationDetailsPage
        data={props.data}
        applicationId={developerApplication}
        onReload={props.onReload}
        onBack={() => props.onNavigate("api")}
      />
    );
  if (page === "api")
    return (
      <ApiPage
        data={props.data}
        apiView={props.apiView}
        webhookId={props.webhookId}
        onNavigate={props.onNavigate}
        onReload={props.onReload}
      />
    );
  if (page === "applications" && props.applicationId)
    return <InstalledApplicationPage data={props.data} clientId={props.applicationId} onReload={props.onReload} />;
  if (page === "applications")
    return <InstalledApplicationsPage data={props.data} />;
  if (page === "billing")
    return <BillingSettings data={props.data} onOpenUsage={() => props.onOpenUsageView?.()} />;
  if (page === "usage")
    return <UsageSettings data={props.data} view={props.usageView} onReload={props.onReload} onOpenView={(view) => props.onOpenUsageView?.(view)} />;
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
        onCreated={async (created) => {
          await props.onReload();
          props.onOpenReleasePipeline(created);
        }}
        onChanged={props.onReload}
      />
    );
  }
  if (page === "integrations" && props.integrationSlug === "enabled")
    return (
      <EnabledIntegrationsSettingsPage
        data={props.data}
        onBack={() => props.onNavigate("integrations")}
        onOpenSlug={(slug) => props.onOpenIntegration(slug)}
        onReload={props.onReload}
      />
    );
  if (page === "integrations" && props.integrationProvider === "jira" && props.jiraSyncMode)
    return (
      <JiraSyncPage
        data={props.data}
        mode={props.jiraSyncMode}
        jiraProjectId={props.jiraProjectId}
        onBack={() => props.onOpenIntegration("jira")}
        onReload={props.onReload}
      />
    );
  if (page === "integrations" && props.integrationProvider === "jira")
    return (
      <JiraSettingsPage
        data={props.data}
        onBack={() => props.onNavigate("integrations")}
        onReload={props.onReload}
        onOpenSyncNew={() => props.onOpenJiraSyncNew?.()}
        onOpenSyncEdit={(jiraProjectId) => props.onOpenJiraSyncEdit?.(jiraProjectId)}
        onOpenConnectedAccounts={() => props.onNavigate("connections")}
      />
    );
  if (page === "integrations" && props.integrationProvider)
    return (
      <CodeIntegrationSettings
        provider={props.integrationProvider as "github" | "gitlab"}
        data={props.data}
        onBack={() => props.onNavigate("integrations")}
        onReload={props.onReload}
        onOpenConnectedAccounts={() => props.onNavigate("connections")}
      />
    );
  if (page === "integrations" && props.integrationSlug)
    return (
      <IntegrationSettingsPage
        slug={props.integrationSlug}
        data={props.data}
        onBack={() => props.onNavigate("integrations")}
        onReload={props.onReload}
      />
    );
  if (page === "asks" && (props.asksEmailIntakeMode === "new" || (props.asksEmailIntakeMode === "edit" && props.asksEmailIntakeId)))
    return (
      <NewAsksEmailIntakePage
        data={props.data}
        addressId={props.asksEmailIntakeMode === "edit" ? props.asksEmailIntakeId : undefined}
        onBack={() => props.onNavigate("asks")}
        onReload={props.onReload}
        onOpenAddress={props.onOpenAsksEmailIntake}
      />
    );
  if (page === "asks" && props.asksEmailIntakeId)
    return (
      <AsksEmailIntakeDetailPage
        data={props.data}
        addressId={props.asksEmailIntakeId}
        onBack={() => props.onNavigate("asks")}
        onReload={props.onReload}
      />
    );
  if (page === "asks" && props.asksIntegrationId)
    return (
      <AsksSlackSettingsPage
        data={props.data}
        integrationId={props.asksIntegrationId}
        onBack={() => props.onNavigate("asks")}
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
        onNavigate={(section, subPath) => props.onNavigate("team", team.key, section, subPath)}
        subPath={props.teamSubPath}
        onOpenTeams={() => props.onNavigate("teams")}
        onReload={props.onReload}
      />
    ) : (
      <div className="settings-empty">
        <h3>{t("Team not found")}</h3>
      </div>
    );
  }
  if (page === "security" && props.identityProviderId)
    return (
      <IdentityProviderSettingsPage
        data={props.data}
        providerId={props.identityProviderId}
        onReload={props.onReload}
        onBack={() => props.onNavigate("security")}
      />
    );
  if (page === "security")
    return (
      <>
        <SecurityPage data={props.data} onReload={props.onReload} onNavigate={props.onNavigate} />
      </>
    );
  if (page === "authentication")
    return (
      <AuthenticationPage data={props.data} onReload={props.onReload} />
    );
  if (
    [
      "ai",
      "ai-agent",
      "code-intelligence",
      "triage-intelligence",
      "ai-summaries",
      "ai-updates",
      "installed-agents",
      "loops",
      "coding-sessions",
      "coding-environments",
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
            | "ai-agent"
            | "code-intelligence"
            | "triage-intelligence"
            | "ai-summaries"
            | "ai-updates"
            | "installed-agents"
            | "loops"
            | "coding-sessions"
            | "coding-environments"
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
        onNavigateSettings={props.onNavigate}
        onOpenAsksSlack={props.onOpenAsksSlack}
        onOpenAsksEmailIntake={props.onOpenAsksEmailIntake}
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
        <button onClick={onCancel}>{agentSkillsSettingsBackLink(data.workspace.urlKey).label}</button>
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
    await updateWorkspacePreferences(changed);
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
            <span data-i18n-ignore>flow.app/</span>
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
        <Row
          title="Welcome message"
          description="Configure a message that new users will receive when they join the workspace"
          className="personal-row-link"
          role="button"
          tabIndex={0}
          onClick={() => props.onOpenWelcomeMessage?.()}
          onKeyDown={(event) => {
            if (event.key === "Enter") props.onOpenWelcomeMessage?.();
          }}
        >
          <span className="personal-row-link-meta">
            {props.data.workspaceSettings.welcomeMessageEnabled ? "Enabled" : "Disabled"}
            <ChevronRight size={14} />
          </span>
        </Row>
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
        {props.data.workspace.deletionRequestedAt ? (
          <Row
            danger
            title="Workspace scheduled for deletion"
            description={`Deletion requested ${new Date(props.data.workspace.deletionRequestedAt).toLocaleString()}. Cancel to keep this workspace.`}
          >
            <ActionButton
              onClick={() => {
                void (async () => {
                  const { cancelOrganizationDeletion } = await import(
                    "@/lib/organization-settings-helper"
                  );
                  const updated = await cancelOrganizationDeletion(
                    props.data.workspace.urlKey,
                  );
                  if (updated) await props.onReload();
                })();
              }}
            >
              Cancel deletion
            </ActionButton>
          </Row>
        ) : (
          <Row
            danger
            title="Delete workspace"
            description="Schedule workspace to be permanently deleted"
          >
            <ActionButton danger onClick={() => setConfirm(true)}>
              Delete workspace
            </ActionButton>
          </Row>
        )}
      </Section>
      <ConfirmDialog
        open={confirm}
        title="Schedule workspace deletion?"
        description={`This schedules ${props.data.workspace.name} for permanent deletion. You can cancel from the Danger zone until deletion completes.`}
        confirm="Schedule deletion"
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
          <span data-i18n-ignore>{initials(data.workspace.name)}</span>
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

export function MembersPage({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
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
                                data-i18n-ignore
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
                                <ViewGlyph color={team.color} icon={team.icon || "Team"} />
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
          <p>{t("Invite teammates to")} <span data-i18n-ignore>{data.workspace.name}</span>.</p>
          <label>
            Email addresses
            <textarea
              autoFocus
              data-i18n-ignore
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
                      data-i18n-ignore
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
                      <ViewGlyph color={team.color} icon={team.icon || "Team"} />
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

function SecurityPage({
  data,
  onReload,
  onNavigate,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
  onNavigate: (page: SettingsPageId, teamKey?: string, teamSection?: TeamSettingsSection) => void;
}) {
  const [settings, setSettings] = useState(data.workspaceSettings);
  const [uploadPolicyOpen, setUploadPolicyOpen] = useState(false);
  const [privacyDialog, setPrivacyDialog] = useState<"support" | "health" | null>(null);
  useResetOnChange([data.workspaceSettings], () => setSettings(data.workspaceSettings));
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
    description?: string,
  ) => (
    <Row title={title} description={description}>
      <Toggle
        label={title}
        checked={Boolean(settings[key])}
        onChange={(value) => void save({ ...settings, [key]: value })}
      />
    </Row>
  );
  const services = settings.allowedAuthServices?.length
    ? settings.allowedAuthServices
    : [
        ...(settings.googleAuthEnabled !== false ? (["google"] as const) : []),
        ...(settings.emailAuthEnabled !== false ? (["email"] as const) : []),
      ];
  const toggleService = (service: "google" | "email", enabled: boolean) => {
    const next = enabled
      ? Array.from(new Set([...services, service]))
      : services.filter((item) => item !== service && !(service === "email" && item === "passkey"));
    void save({
      ...settings,
      allowedAuthServices: next as WorkspaceSettings["allowedAuthServices"],
      googleAuthEnabled: next.includes("google"),
      emailAuthEnabled: next.includes("email") || next.includes("passkey"),
    });
  };
  const permission = (title: string, settingKey: SecuritySettingKey, description: string) => (
    <PermissionRow title={title} description={description} settingKey={settingKey} settings={settings} save={save} />
  );
  const identityProviders = data.identityProviders?.length ?? 0;
  return (
    <>
      <PageTitle>Security</PageTitle>
      <Section grouped title="Workspace access">
        <Group title="Invite links" description="A uniquely generated invite link allows anyone with the link to join your workspace">
          {toggle("Enable invite links", "inviteLinksEnabled")}
          <WorkspaceInviteLinkRow data={data} enabled={Boolean(settings.inviteLinksEnabled)} />
        </Group>
        <Group title="Workspace login and restrictions" description="Anyone with an email address at these domains is allowed to sign up for this workspace.">
          <ApprovedDomainRows
            domains={settings.allowedDomains ?? []}
            onChange={(allowedDomains) => void save({ ...settings, allowedDomains })}
          />
        </Group>
        <Group title="Authentication methods" description="Admins and guests can always authenticate via Google and email/passkeys—even when disabled for members">
          <Row title="Google authentication" description="When enabled, this is available to all workspace members and guests">
            <Toggle label="Google authentication" checked={services.includes("google")} onChange={(value) => toggleService("google", value)} />
          </Row>
          <Row title="Email & passkey authentication" description="When enabled, this is available to all workspace members and guests">
            <Toggle label="Email & passkey authentication" checked={services.includes("email") || services.includes("passkey")} onChange={(value) => toggleService("email", value)} />
          </Row>
          <Row title="SAML & SCIM" description={identityProviders ? `${identityProviders} identity provider${identityProviders === 1 ? "" : "s"} configured` : "Manage logins via an identity provider’s SSO"}>
            <ActionButton onClick={() => onNavigate("authentication")}>Configure</ActionButton>
          </Row>
          {toggle("Disable authentication bypass for admins", "disableAdminBypass", "When enabled, admins are restricted to the enabled authentication methods")}
          {toggle("Require two-factor authentication", "requireTwoFactor", "Require a second factor for all members")}
          <Row title="Session duration" description="How long members stay signed in before re-authenticating">
            <Select
              label="Session duration"
              value={`${settings.sessionDurationDays} days`}
              options={["7 days", "30 days", "90 days"]}
              onChange={(value) => void save({ ...settings, sessionDurationDays: Number(value.split(" ")[0]) })}
            />
          </Row>
          {toggle("Allow guest accounts", "guestsAllowed", "Guest invitations are rejected when this is disabled")}
        </Group>
      </Section>
      <Section grouped title="Workspace management">
        <Group title="Permissions" description="Choose who can perform various actions across the workspace">
          {permission("New user invitations", "invitePermission", "Who can invite new members to the workspace")}
          {permission("Team creation", "teamCreatePermission", "Who can create new teams")}
          {permission("Workspace initiatives", "initiativePermission", "Who can create workspace level initiatives")}
          {permission("Manage workspace labels", "labelPermission", "Who can create, update, and delete workspace labels")}
          {permission("Manage workspace templates", "templatePermission", "Who can manage workspace templates")}
          {permission("Manage pinned views", "pinnedViewPermission", "Who can pin views to team pages and edit, reorder, or remove them")}
          {permission("Manage loops", "loopPermission", "Who can create, update, and delete workspace loops")}
          {permission("API key creation", "apiKeyPermission", "Who can create API keys to interact with the Flow API on their behalf")}
          {permission("Import data", "importPermission", "Who can import data from other services")}
          {permission("Modify agent guidance", "agentGuidancePermission", "Who can modify workspace-level agent guidance prompts")}
        </Group>
        <Group title="File uploads" description="Restrict the file types users are allowed to upload. Images and videos remain allowed.">
          <Row title="Restrict file uploads" description={settings.restrictFileUploads ? `${settings.allowedFileExtensions?.length ?? 0} allowed file types` : undefined}>
            <ActionButton onClick={() => setUploadPolicyOpen(true)}>Configure</ActionButton>
          </Row>
        </Group>
        <Group title="Audit log" description="Review security-relevant events across the workspace">
          <Row title="Audit log">
            <ActionButton onClick={() => onNavigate("audit-log")}>Open</ActionButton>
          </Row>
        </Group>
      </Section>
      <Section title="Integrations & applications">
        {toggle("Review third-party applications", "reviewThirdPartyApplications", "Control which applications can be installed to your workspace")}
        <Row title="Reduce personal information from support integrations" description="Personal information from support integrations won’t be stored">
          <ActionButton onClick={() => setPrivacyDialog("support")}>Configure</ActionButton>
        </Row>
        {toggle("Prevent guests from interacting with agents in the workspace", "preventGuestAgents", "Restrict agent invocation to full workspace members only")}
      </Section>
      <ApplicationPolicySettings admin={data.viewerRole === "admin" || data.viewerRole === "owner"} />
      <Section grouped title="AI & agents">
        <Group title="Web search" description="Let Flow Agent use current public information">
          {/* Web access is on unless an admin turns it off (like the reference app's "Enable web access"). */}
          <Row title="Enable Flow Agent web search" description="Allow Flow Agent to search the public web for current information and cite sources">
            <Toggle
              label="Enable Flow Agent web search"
              checked={agentWebAccessEnabled(settings)}
              onChange={(value) => void save({ ...settings, agentWebAccess: value, agentWebSearch: value, agentWebSearchDisabled: !value })}
            />
          </Row>
        </Group>
        <Group title="Trusted external sources">
          <Row title="External sources" description="Manage allowed sources in Loops settings">
            <ActionButton onClick={() => onNavigate("loops")}>Open Loops settings</ActionButton>
          </Row>
        </Group>
        <Group title="MCP connectors">
          <Row title="Enable Flow Agent MCP connectors" description="Allow Flow Agent to use MCP connectors">
            <Toggle
              label="Enable Flow Agent MCP connectors"
              checked={mcpConnectorsEnabled(settings)}
              disabled={Boolean(settings.hipaaCompliance)}
              onChange={(value) => void save({ ...settings, mcpConnectors: value })}
            />
          </Row>
          <Row title="Active MCP connections" description="Review MCP connections from all workspace members">
            <ActionButton onClick={() => onNavigate("applications")}>Review</ActionButton>
          </Row>
          <Row title="Allowed MCP connectors" description="Choose which MCP connectors Flow Agent can use">
            <Select
              label="Allowed MCP connectors"
              value={(settings.allowedMcpConnectors ?? "all") === "all" ? "All connectors" : "Approved connectors"}
              options={["All connectors", "Approved connectors"]}
              onChange={(value) => void save({ ...settings, allowedMcpConnectors: value === "All connectors" ? "all" : "approved" })}
            />
          </Row>
        </Group>
      </Section>
      <Section title="Compliance">
        <Row title="HIPAA compliance" description="Enable privacy and security measures to ensure that Protected Health Information is handled appropriately">
          <ActionButton onClick={() => setPrivacyDialog("health")}>Configure</ActionButton>
        </Row>
      </Section>
      {uploadPolicyOpen && <UploadPolicyDialog settings={settings} onSave={async (patch) => { await updateWorkspacePreferences(patch); await onReload(); }} onClose={() => setUploadPolicyOpen(false)} />}
      {privacyDialog && <DataPrivacyDialog kind={privacyDialog} settings={settings} onSave={async (patch) => { await updateWorkspacePreferences(patch); await onReload(); }} onClose={() => setPrivacyDialog(null)} />}
    </>
  );
}
function ApprovedDomainRows({
  domains,
  onChange,
}: {
  domains: string[];
  onChange: (domains: string[]) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState("");
  const commit = () => {
    const domain = draft.trim().toLowerCase().replace(/^@/, "");
    setAdding(false);
    setDraft("");
    if (domain && !domains.includes(domain)) onChange([...domains, domain]);
  };
  return (
    <>
      {domains.length === 0 && !adding && (
        <Row title="No approved email domains" className="settings-row--empty">
          <ActionButton onClick={() => setAdding(true)}><Plus size={14} />Add domain</ActionButton>
        </Row>
      )}
      {domains.map((domain) => (
        <Row key={domain} title={domain}>
          <ActionButton onClick={() => onChange(domains.filter((item) => item !== domain))}>Remove</ActionButton>
        </Row>
      ))}
      {adding ? (
        <Row title="New domain">
          <input
            autoFocus
            className="settings-input"
            aria-label="Domain"
            placeholder="example.com"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") commit();
              if (event.key === "Escape") { setAdding(false); setDraft(""); }
            }}
            onBlur={commit}
          />
        </Row>
      ) : domains.length > 0 && (
        <Row title="Add another domain">
          <ActionButton onClick={() => setAdding(true)}><Plus size={14} />Add domain</ActionButton>
        </Row>
      )}
    </>
  );
}
function PermissionRow({
  title,
  description,
  settingKey,
  settings,
  save,
}: {
  title: string;
  description?: string;
  settingKey: SecuritySettingKey;
  settings: WorkspaceSettings;
  save: (next: WorkspaceSettings) => Promise<void>;
}) {
  const security = useSecuritySetting(settingKey, settings, save);
  return (
    <>
      <Row title={title} description={description}>
        <Select
          label={title}
          value={security.value}
          options={security.options.map((option) => ({
            value: option.value,
            label: option.label,
          }))}
          onChange={security.onValueChange}
        />
      </Row>
      {settingKey === "apiKeyPermission" && (
        <ConfirmDialog
          open={security.confirmOpen}
          title={security.confirmTitle}
          description={security.confirmDescription}
          confirm="Confirm"
          onCancel={security.cancelRestrict}
          onConfirm={() => void security.confirmRestrict()}
        />
      )}
    </>
  );
}
function ApiPage({
  data,
  apiView,
  webhookId,
  onNavigate,
  onReload,
}: {
  data: BootstrapData;
  apiView?: "keys";
  webhookId?: string;
  onNavigate: (page: SettingsPageId) => void;
  onReload: () => Promise<void>;
}) {
  const navigate = useNavigate();
  const [settings, setSettings] = useState(data.workspaceSettings);
  useResetOnChange([data.workspaceSettings], () => setSettings(data.workspaceSettings))
  const admin = data.viewerRole === "admin" || data.viewerRole === "owner";
  const apiPath = settingsPath(data.workspace.urlKey, "api");
  const issuedKeys = (data.apiKeys ?? []).filter((item) => !item.revokedAt && !item.oauthClientId);
  if (webhookId) {
    const webhook = webhookId === "new" ? null : data.webhooks.find((item) => item.id === webhookId);
    if (webhook === undefined)
      return (
        <div className="settings-empty">
          <h3>Webhook not found</h3>
          <ActionButton onClick={() => navigate(apiPath)}>Back to API</ActionButton>
        </div>
      );
    return (
      <WebhookEditPage
        data={data}
        webhook={webhook}
        onClose={() => navigate(apiPath)}
        onSaved={onReload}
      />
    );
  }
  if (apiView === "keys")
    return <IssuedApiKeysPage data={data} onBack={() => navigate(apiPath)} onReload={onReload} />;
  const save = async (next: WorkspaceSettings) => {
    setSettings(next);
    try {
      await updateWorkspacePreferences(next);
      await onReload();
    } catch (error) {
      setSettings(data.workspaceSettings);
      toast.error(error instanceof Error ? error.message : "Could not save policy");
    }
  };
  return (
    <>
      <PageTitle description="Flow’s API provides a programmable interface to your data. Use it to build public or private apps, workflows, and integrations for Flow.">
        API
      </PageTitle>
      <OAuthApplicationsSection data={data} />
      <Section title="Webhooks" description="Webhooks allow you to receive HTTP requests when an entity is created, updated, or deleted.">
        {data.webhooks.map((item) => (
          <Row
            key={item.id}
            title={item.name}
            description={[
              webhookStateLabel(item),
              item.url,
              item.resourceTypes.map(webhookResourceLabel).join(", ") ||
                "all resources",
            ]
              .filter(Boolean)
              .join(" · ")}
          >
            <div className="settings-inline-actions">
              <Toggle
                label={`${item.name} enabled`}
                checked={item.enabled}
                onChange={(value) =>
                  void updateWebhook(item.id, { enabled: value }).then(onReload)
                }
              />
              <ActionButton onClick={() => navigate(webhookSettingsPath(data.workspace.urlKey, item.id))}>
                Configure
              </ActionButton>
            </div>
          </Row>
        ))}
        <Row title={data.webhooks.length ? "" : "No webhooks"} className="settings-row--action">
          <ActionButton onClick={() => navigate(webhookSettingsPath(data.workspace.urlKey, "new"))}>
            <Plus size={14} />
            New webhook
          </ActionButton>
        </Row>
      </Section>
      <Section
        title="API keys"
        description={
          <>
            Control whether workspace members can create personal API keys. View your own keys in{" "}
            <button type="button" className="settings-inline-link" onClick={() => onNavigate("account-security")}>
              Security &amp; access
            </button>{" "}
            settings.
          </>
        }
      >
        {admin ? (
          <PermissionRow
            title="API key creation"
            description="Choose which members can create personal API keys"
            settingKey="apiKeyPermission"
            settings={settings}
            save={save}
          />
        ) : (
          <Row title="API key creation" description="Choose which members can create personal API keys">
            <span className="settings-static">{securityPermissionLabel(settings.apiKeyPermission)}</span>
          </Row>
        )}
        <Row
          title="Issued keys"
          description={admin ? "View and manage API keys created by workspace members" : "API keys you created"}
          className="personal-row-link"
          role="button"
          tabIndex={0}
          onClick={() => navigate(apiKeysSettingsPath(data.workspace.urlKey))}
          onKeyDown={(event) => {
            if (event.key === "Enter") navigate(apiKeysSettingsPath(data.workspace.urlKey));
          }}
        >
          <span className="personal-row-link-meta">
            {issuedKeys.length ? `${issuedKeys.length} ${issuedKeys.length === 1 ? "key" : "keys"}` : "No API keys"}
          </span>
        </Row>
      </Section>
    </>
  );
}
function IssuedApiKeysPage({
  data,
  onBack,
  onReload,
}: {
  data: BootstrapData;
  onBack: () => void;
  onReload: () => Promise<void>;
}) {
  const admin = data.viewerRole === "admin" || data.viewerRole === "owner";
  const keys = (data.apiKeys ?? []).filter((item) => !item.revokedAt && !item.oauthClientId);
  const creator = (id: string) => data.users.find((user) => user.id === id)?.displayName ?? "Unknown";
  return (
    <>
      <SettingsCrumb onClick={onBack}>API</SettingsCrumb>
      <PageTitle description={admin ? "API keys created by members of this workspace." : "API keys you created in this workspace."}>
        Issued keys
      </PageTitle>
      <Section>
        {keys.map((item) => (
          <Row
            key={item.id}
            title={<span data-i18n-ignore>{item.name}</span>}
            description={`${creator(item.creatorId)} · ${item.prefix}… · ${item.scopes == null ? "Full access" : item.scopes.length ? item.scopes.join(", ") : "No permissions"} · ${item.lastUsedAt ? `last used ${formatDate(item.lastUsedAt)}` : "never used"}`}
          >
            {(admin || item.creatorId === data.viewer.id) && (
              <ActionButton
                danger
                onClick={() =>
                  void revokeAPIKey(item.id)
                    .then(onReload)
                    .catch((error) => toast.error(error instanceof Error ? error.message : "Could not revoke key"))
                }
              >
                Revoke
              </ActionButton>
            )}
          </Row>
        ))}
        {!keys.length && <Row title="No API keys" />}
      </Section>
    </>
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
    title: "AI & agents",
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
    description: "Pulse centralizes all your project and initiative updates into a single feed. Members can choose to receive summary notifications daily or weekly.",
    rows: [
      ["Enable Pulse", "Workspace-wide feed of updates with optional summary notifications"],
      ["Default workspace schedule", "Applies to all members who haven’t set their own preference"],
      ["Your personal schedule", "Only applies to you, overriding the workspace default"],
    ],
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
  const { t } = useI18n();
  const chat = useChatPlatform(data);
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
        {chat.provider && <Row
          key={chat.provider}
          title={chat.name}
          description={chat.format(t("Create and update issues from {name} groups"))}
        >
          {/* Lark and Feishu are configured on their own integration page. */}
          <AppLink className="settings-action" href={integrationSettingsPath(data.workspace.urlKey, chat.provider)}>{t("Open settings")}</AppLink>
        </Row>}
        {[
          ["github", "Link pull requests and commits to issues"],
          ...(chat.provider ? [] : [["slack", "Create and update issues from Slack"]]),
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
    </>
  );
}
function AuthenticationPage({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const [settings, setSettings] = useState(data.workspaceSettings);
  useResetOnChange([data.workspaceSettings], () => setSettings(data.workspaceSettings))
  const save = async (next: WorkspaceSettings) => {
    setSettings(next);
    try {
      await updateWorkspacePreferences(next);
      await onReload();
      toast.success("Authentication settings saved");
    } catch (error) {
      setSettings(data.workspaceSettings);
      toast.error(error instanceof Error ? error.message : "Could not save authentication settings");
    }
  };
  const services = settings.allowedAuthServices?.length
    ? settings.allowedAuthServices
    : [
        ...(settings.googleAuthEnabled !== false ? (["google"] as const) : []),
        ...(settings.emailAuthEnabled !== false ? (["email"] as const) : []),
      ];
  const toggleService = (service: "google" | "email" | "passkey" | "saml" | "appUser", enabled: boolean) => {
    const next = enabled
      ? Array.from(new Set([...services, service]))
      : services.filter((item) => item !== service);
    void save({
      ...settings,
      allowedAuthServices: next as WorkspaceSettings["allowedAuthServices"],
      googleAuthEnabled: next.includes("google"),
      emailAuthEnabled: next.includes("email") || next.includes("passkey"),
    });
  };
  return (
    <>
      <PageTitle description="Configure enterprise SSO and which login methods members can use.">
        Authentication
      </PageTitle>
      <EnterpriseIdentityProviders data={data} onReload={onReload} />
      <Section title="Other domains">
        <p className="settings-section-note">
          Control how all other email domains (those not approved by an identity provider above) can authenticate.
        </p>
        <Row title="Google authentication" description="Currently available to all members and guests">
          <Toggle
            label="Google authentication"
            checked={services.includes("google")}
            onChange={(value) => toggleService("google", value)}
          />
        </Row>
        <Row title="Email & passkey authentication" description="Currently available to all members and guests">
          <Toggle
            label="Email & passkey authentication"
            checked={services.includes("email") || services.includes("passkey")}
            onChange={(value) => toggleService("email", value)}
          />
        </Row>
        <Row title="SAML authentication" description="Allow SAML/SSO for domains without a dedicated identity provider row">
          <Toggle
            label="SAML authentication"
            checked={services.includes("saml")}
            onChange={(value) => toggleService("saml", value)}
          />
        </Row>
        <Row title="App users" description="Allow application / agent accounts to authenticate">
          <Toggle
            label="App users"
            checked={services.includes("appUser")}
            onChange={(value) => toggleService("appUser", value)}
          />
        </Row>
      </Section>
    </>
  );
}
function WorkspaceInviteLinkRow({
  data,
  enabled,
}: {
  data: BootstrapData;
  enabled: boolean;
}) {
  const [link, setLink] = useState<string>("");
  const [busy, setBusy] = useState(false);
  useResetOnChange([enabled], () => { if (!enabled) setLink(""); });
  useEffect(() => {
    if (!enabled) return;
    void fetchWorkspaceInviteLink(data.workspace.urlKey)
      .then(async (result) => {
        if ("token" in result && result.enabled) {
          setLink(`${window.location.origin}/join/${result.token}`);
          return;
        }
        const rotated = await rotateWorkspaceInviteLink(data.workspace.urlKey);
        setLink(`${window.location.origin}/join/${rotated.token}`);
      })
      .catch(() => setLink(""));
  }, [data.workspace.urlKey, enabled]);
  if (!enabled) return null;
  return (
    <Row
      title="Workspace invite link"
      description={
        link || "Generate a shareable join link for this workspace."
      }
    >
      <div className="settings-inline-actions">
        <ActionButton
          disabled={!link || busy}
          onClick={() => {
            if (!link) return;
            void navigator.clipboard.writeText(link);
            toast.success("Invite link copied");
          }}
        >
          Copy link
        </ActionButton>
        <ActionButton
          disabled={busy}
          onClick={() => {
            setBusy(true);
            void rotateWorkspaceInviteLink(data.workspace.urlKey)
              .then((rotated) => {
                setLink(`${window.location.origin}/join/${rotated.token}`);
                toast.success("Invite link rotated");
              })
              .catch((error) =>
                toast.error(
                  error instanceof Error
                    ? error.message
                    : "Could not rotate invite link",
                ),
              )
              .finally(() => setBusy(false));
          }}
        >
          Rotate
        </ActionButton>
        <ActionButton
          danger
          disabled={busy}
          onClick={() => {
            setBusy(true);
            void disableWorkspaceInviteLink(data.workspace.urlKey)
              .then(() => {
                setLink("");
                toast.success("Invite link disabled");
              })
              .catch((error) =>
                toast.error(
                  error instanceof Error
                    ? error.message
                    : "Could not disable invite link",
                ),
              )
              .finally(() => setBusy(false));
          }}
        >
          Disable
        </ActionButton>
      </div>
    </Row>
  );
}

function EnterpriseIdentityProviders({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const navigate = useNavigate();
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
            <ActionButton
              onClick={() =>
                navigate(
                  identityProviderSettingsPath(
                    data.workspace.urlKey,
                    item.id,
                  ),
                )
              }
            >
              Open
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
            data-i18n-ignore
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
  const pending = useRef(0);
  const [state, setStateInternal] = useState<StoredSettings>(() => ({
    values: storedUserValues(source),
    lists: {},
  }));
  const stateRef = useLatestRef(state);
  const setState = useCallback<
    React.Dispatch<React.SetStateAction<StoredSettings>>
  >((update) => {
    const before = stateRef.current;
    const next = typeof update === 'function' ? update(before) : update;
    const patch = Object.fromEntries(Object.entries(next.values).filter(([key,value]) => !Object.is(value,before.values[key])));
    stateRef.current = next;
    setStateInternal(next);
    if (!Object.keys(patch).length) return;
    pending.current++;
    void persistUserSettings(data.workspace.urlKey,data.viewer.id,patch as Partial<UserSettings>).catch(() => {
      setStateInternal(current => ({...current,values:{...current.values,...Object.fromEntries(Object.entries(patch).filter(([key,value]) => Object.is(current.values[key],value)).map(([key]) => [key,before.values[key]]))}}));
      toast.error('Could not save settings');
    }).finally(() => { pending.current--; });
  }, [data.workspace.urlKey,data.viewer.id,stateRef]);
  useEffect(() => {
    const next = data.userSettings[data.viewer.id];
    if (next)
      setStateInternal((current) => {
        if (pending.current) return current;
        const values = storedUserValues(next);
        return sameStoredValues(current.values, values)
          ? current
          : { ...current, values };
      });
  }, [data.userSettings, data.viewer.id]);
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
  return new Intl.DateTimeFormat(document.documentElement.lang || "en-US", {
    month: "short",
    day: "numeric",
  }).format(new Date(value));
}
function slug(value: string) {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 32);
}
