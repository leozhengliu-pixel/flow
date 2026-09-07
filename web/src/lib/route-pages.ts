import { lazyPage } from './lazy-page'
export const WorkspaceOnboarding = lazyPage(
  () => import("@/components/workspace/workspace-onboarding"),
  "WorkspaceOnboarding",
);
export const WorkspaceDirectoryPage = lazyPage(
  () => import("@/components/workspace-directory/workspace-directory-page"),
  "WorkspaceDirectoryPage",
);
export const MemberProfilePage = lazyPage(
  () => import("@/components/workspace-directory/member-profile-page"),
  "MemberProfilePage",
);
export const TeamCreatePage = lazyPage(
  () => import("@/components/workspace-directory/team-create-page"),
  "TeamCreatePage",
);
export const TeamOverviewPage = lazyPage(
  () => import("@/components/team-overview/team-overview-page"),
  "TeamOverviewPage",
);
export const SettingsPage = lazyPage(
  () => import("@/components/settings/settings-page"),
  "SettingsPage",
);
export const AuthPage = lazyPage(
  () => import("@/components/auth/auth-page"),
  "AuthPage",
);
export const OAuthAuthorizePage = lazyPage(
  () => import("@/components/auth/oauth-authorize-page"),
  "OAuthAuthorizePage",
);
export const WorkspaceSearchPage = lazyPage(
  () => import("@/components/search/workspace-search-page"),
  "WorkspaceSearchPage",
);
export const WorkspaceOperationsPage = lazyPage(
  () => import("@/components/workspace-operations/workspace-operations-page"),
  "WorkspaceOperationsPage",
);
export const DocumentPage = lazyPage(
  () => import("@/components/documents/document-page"),
  "DocumentPage",
);
export const DocumentsIndexPage = lazyPage(
  () => import("@/components/documents/documents-index-page"),
  "DocumentsIndexPage",
);
export const WorkspaceSecondaryPage = lazyPage(
  () => import("@/components/workspace/workspace-secondary-page"),
  "WorkspaceSecondaryPage",
);
export const AnalyticsDashboardPage = lazyPage(
  () => import("@/components/analytics/analytics-dashboard-page"),
  "AnalyticsDashboardPage",
);
export const DashboardsPage = lazyPage(
  () => import("@/components/dashboards/dashboards-page"),
  "DashboardsPage",
);
export const CustomerDetailPage = lazyPage(
  () => import("@/components/customer-detail/customer-detail-page"),
  "CustomerDetailPage",
);
export const InboxAppPage = lazyPage(
  () => import("@/components/inbox/inbox-app-page"),
  "InboxAppPage",
);
export const ProjectsPage = lazyPage(
  () => import("@/components/projects-page/projects-page"),
  "ProjectsPage",
);
export const ProjectDetailPage = lazyPage(
  () => import("@/components/project-detail/project-detail-page"),
  "ProjectDetailPage",
);
export const MyIssuesPage = lazyPage(
  () => import("@/components/my-issues"),
  "MyIssuesPage",
);
export const IssueExplorerPage = lazyPage(
  () => import("@/components/issue-explorer"),
  "IssueExplorerPage",
);
export const ViewsPage = lazyPage(
  () => import("@/components/views-page/views-page"),
  "ViewsPage",
);
export const InitiativesPage = lazyPage(
  () => import("@/components/initiatives/initiatives-page"),
  "InitiativesPage",
);
export const InitiativeDetailPage = lazyPage(
  () => import("@/components/initiatives/initiative-detail-page"),
  "InitiativeDetailPage",
);
export const CyclesPage = lazyPage(
  () => import("@/components/cycles/cycles-page"),
  "CyclesPage",
);
export const CycleDetailPage = lazyPage(
  () => import("@/components/cycles/cycle-detail-page"),
  "CycleDetailPage",
);
export const PulsePage = lazyPage(
  () => import("@/components/pulse/pulse-page"),
  "PulsePage",
);
export const TeamArchivePage = lazyPage(
  () => import("@/components/workspace-operations/team-archive-page"),
  "TeamArchivePage",
);
export const ReviewsPage = lazyPage(
  () => import("@/components/reviews/reviews-page"),
  "ReviewsPage",
);
export const AgentPage = lazyPage(
  () => import("@/components/agent/agent-page"),
  "AgentPage",
);
export const AgentChatPanel = lazyPage(
  () => import("@/components/agent/agent-chat-panel"),
  "AgentChatPanel",
);
export const LoopsPage = lazyPage(
  () => import("@/components/loops/loops-page"),
  "LoopsPage",
);
export const DetailPane = lazyPage(
  () => import("@/components/detail/detail-pane"),
  "DetailPane",
);
export const CommandMenu = lazyPage(
  () => import("@/components/command/command-menu"),
  "CommandMenu",
);
export const BulkActionBar = lazyPage(
  () => import("@/components/issue/bulk-action-bar"),
  "BulkActionBar",
);
export const CreateIssueDialog = lazyPage(
  () => import("@/components/create-issue/create-issue-dialog"),
  "CreateIssueDialog",
);
