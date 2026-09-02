import type {
  CodeReview,
  Cycle,
  Initiative,
  Issue,
  Project,
  SavedView,
} from "@/types/flow";

export type MyIssuesRouteView =
  "assigned" | "created" | "subscribed" | "activity";
export type ProjectRouteTab = "overview" | "activity" | "issues" | "new";
export type TeamIssuesRouteView = "active" | "backlog" | "all";
export type ViewsResource = "issues" | "projects";
export type InitiativesRouteView = "active" | "planned" | "all";
export type InitiativeRouteTab =
  "overview" | "activity" | "projects" | "new" | "view";
export type ReleasePipelineTab = "releases" | "changelog" | "archive";
export type ReleaseRouteTab = "issues" | "release-notes";
export type PulseRouteView = "following" | "popular" | "all";
export type ReviewRouteTab = "overview" | "review" | "changes";
export type WorkspaceSecondaryRouteKind =
  | "diary"
  | "meeting"
  | "automations"
  | "automation-new"
  | "automation-detail"
  | "automation-runs"
  | "team-board"
  | "team-triage"
  | "team-updates"
  | "team-update"
  | "team-resources"
  | "team-links"
  | "release-note"
  | "label";
export type TeamArchiveTab =
  | "issues"
  | "projects"
  | "cycles"
  | "recently-deleted"
  | "recently-deleted-projects"
  | "recently-deleted-initiatives"
  | "recently-deleted-documents";
export type SettingsPageId =
  | "preferences"
  | "profile"
  | "notifications"
  | "code-and-reviews"
  | "account-security"
  | "connections"
  | "agents"
  | "issue-labels"
  | "issue-templates"
  | "sla"
  | "project-labels"
  | "project-templates"
  | "project-statuses"
  | "project-updates"
  | "ai"
  | "initiatives"
  | "documents"
  | "customer-requests"
  | "releases"
  | "pulse"
  | "asks"
  | "emojis"
  | "integrations"
  | "workspace"
  | "teams"
  | "members"
  | "security"
  | "audit-log"
  | "api"
  | "applications"
  | "billing"
  | "usage"
  | "import-export"
  | "workflows"
  | "team";
export type TeamSettingsSection =
  | "overview"
  | "general"
  | "security"
  | "members"
  | "notifications"
  | "issue-labels"
  | "templates"
  | "recurring-issues"
  | "statuses"
  | "workflow"
  | "triage"
  | "cycles"
  | "agents"
  | "agent-skills"
  | "ai-updates"
  | "ai-summaries";

export type AppRoute =
  | { kind: "root" }
  | { kind: "workspace-onboarding" }
  | { kind: "workspace-root"; workspaceSlug: string }
  | { kind: "inbox"; workspaceSlug: string }
  | { kind: "search"; workspaceSlug: string }
  | { kind: "diary"; workspaceSlug: string }
  | { kind: "meeting"; workspaceSlug: string; meetingId: string }
  | { kind: "automations"; workspaceSlug: string }
  | { kind: "automation-new"; workspaceSlug: string }
  | {
      kind: "automation-detail";
      workspaceSlug: string;
      automationId: string;
      editing?: boolean;
    }
  | {
      kind: "automation-runs";
      workspaceSlug: string;
      automationId: string;
      runId?: string;
    }
  | {
      kind: "pulse";
      workspaceSlug: string;
      view: PulseRouteView;
      viewId?: string;
    }
  | { kind: "my-issues"; workspaceSlug: string; view: MyIssuesRouteView }
  | { kind: "reviews"; workspaceSlug: string; view: "for-you" | "created" }
  | {
      kind: "review";
      workspaceSlug: string;
      reviewSlug: string;
      tab: ReviewRouteTab;
    }
  | {
      kind: "workspace-issues";
      workspaceSlug: string;
      view: TeamIssuesRouteView;
    }
  | {
      kind: "team-issues";
      workspaceSlug: string;
      teamKey: string;
      view: TeamIssuesRouteView;
    }
  | { kind: "team-overview"; workspaceSlug: string; teamKey: string }
  | { kind: "team-board"; workspaceSlug: string; teamKey: string }
  | { kind: "team-triage"; workspaceSlug: string; teamKey: string }
  | { kind: "team-updates"; workspaceSlug: string; teamKey: string }
  | {
      kind: "team-update";
      workspaceSlug: string;
      teamKey: string;
      updateId: string;
    }
  | { kind: "team-resources"; workspaceSlug: string; teamKey: string }
  | { kind: "team-links"; workspaceSlug: string; teamKey: string }
  | { kind: "team-documents"; workspaceSlug: string; teamKey: string }
  | { kind: "team-loops"; workspaceSlug: string; teamKey: string }
  | { kind: "team-members"; workspaceSlug: string; teamKey: string }
  | { kind: "team-cycles"; workspaceSlug: string; teamKey: string }
  | {
      kind: "team-initiatives";
      workspaceSlug: string;
      teamKey: string;
      view: InitiativesRouteView;
    }
  | { kind: "cycle-upcoming"; workspaceSlug: string; teamKey: string }
  | { kind: "cycle"; workspaceSlug: string; teamKey: string; cycleId: string }
  | {
      kind: "workspace-saved-view";
      workspaceSlug: string;
      viewId: string;
      editing?: boolean;
    }
  | {
      kind: "team-saved-view";
      workspaceSlug: string;
      teamKey: string;
      viewId: string;
      editing?: boolean;
    }
  | { kind: "workspace-views"; workspaceSlug: string; resource: ViewsResource }
  | {
      kind: "workspace-views-new";
      workspaceSlug: string;
      resource: ViewsResource;
    }
  | { kind: "workspace-members"; workspaceSlug: string }
  | {
      kind: "member-profile";
      workspaceSlug: string;
      username: string;
      view: "assigned" | "created";
    }
  | { kind: "workspace-customers"; workspaceSlug: string }
  | { kind: "customer"; workspaceSlug: string; customerSlugId: string }
  | { kind: "documents"; workspaceSlug: string }
  | { kind: "analytics"; workspaceSlug: string }
  | {
      kind: "dashboards";
      workspaceSlug: string;
      dashboardId?: string;
      widgetId?: string;
      creating?: boolean;
      teamKey?: string;
    }
  | { kind: "document"; workspaceSlug: string; documentSlugId: string }
  | { kind: "release-note"; workspaceSlug: string; releaseNoteId: string }
  | {
      kind: "label";
      workspaceSlug: string;
      resourceName: string;
      resourceType: "issue" | "project" | "initiative";
    }
  | { kind: "drafts"; workspaceSlug: string }
  | { kind: "agent"; workspaceSlug: string; chatSlug?: string }
  | { kind: "releases"; workspaceSlug: string }
  | {
      kind: "release-pipeline";
      workspaceSlug: string;
      pipelineSlug: string;
      tab: ReleasePipelineTab;
    }
  | {
      kind: "release";
      workspaceSlug: string;
      pipelineSlug: string;
      releaseSlug: string;
      tab: ReleaseRouteTab;
    }
  | { kind: "asks"; workspaceSlug: string }
  | { kind: "loops"; workspaceSlug: string }
  | {
      kind: "loop-editor";
      workspaceSlug: string;
      loopId?: string;
      draftId?: string;
    }
  | {
      kind: "team-archive";
      workspaceSlug: string;
      teamKey: string;
      tab: TeamArchiveTab;
    }
  | { kind: "workspace-teams"; workspaceSlug: string }
  | { kind: "new-team"; workspaceSlug: string }
  | {
      kind: "settings";
      workspaceSlug: string;
      page: SettingsPageId;
      teamKey?: string;
      teamSection?: TeamSettingsSection;
      issueTemplateMode?: "new" | "new-form" | "edit";
      issueTemplateId?: string;
      projectTemplateMode?: "new" | "edit";
      projectTemplateId?: string;
      agentSkillMode?: "new" | "edit";
      agentSkillId?: string;
      releasePipelineMode?: "new" | "edit";
      releasePipelineSlug?: string;
      integrationProvider?: "github" | "gitlab";
    }
  | {
      kind: "team-views";
      workspaceSlug: string;
      teamKey: string;
      resource: ViewsResource;
    }
  | {
      kind: "team-views-new";
      workspaceSlug: string;
      teamKey: string;
      resource: ViewsResource;
    }
  | { kind: "projects"; workspaceSlug: string }
  | { kind: "initiatives"; workspaceSlug: string; view: InitiativesRouteView }
  | {
      kind: "initiative";
      workspaceSlug: string;
      initiativeSlugId: string;
      tab: InitiativeRouteTab;
      viewId?: string;
    }
  | { kind: "team-projects"; workspaceSlug: string; teamKey: string }
  | { kind: "projects-new-view"; workspaceSlug: string }
  | {
      kind: "projects-saved-view";
      workspaceSlug: string;
      viewId: string;
      editing?: boolean;
    }
  | { kind: "team-projects-new-view"; workspaceSlug: string; teamKey: string }
  | {
      kind: "team-projects-saved-view";
      workspaceSlug: string;
      teamKey: string;
      viewId: string;
      editing?: boolean;
    }
  | {
      kind: "project";
      workspaceSlug: string;
      projectSlugId: string;
      tab: ProjectRouteTab;
    }
  | {
      kind: "project-saved-view";
      workspaceSlug: string;
      projectSlugId: string;
      viewId: string;
      editing?: boolean;
    }
  | {
      kind: "issue";
      workspaceSlug: string;
      identifier: string;
      titleSlug?: string;
    }
  | { kind: "not-found"; workspaceSlug?: string };

const MY_ISSUES_VIEWS = new Set<MyIssuesRouteView>([
  "assigned",
  "created",
  "subscribed",
  "activity",
]);
const PROJECT_TABS = new Set<ProjectRouteTab>([
  "overview",
  "activity",
  "issues",
]);
const TEAM_ISSUES_VIEWS = new Set<TeamIssuesRouteView>([
  "active",
  "backlog",
  "all",
]);
const VIEWS_RESOURCES = new Set<ViewsResource>(["issues", "projects"]);
const INITIATIVE_TABS = new Set<InitiativeRouteTab>([
  "overview",
  "activity",
  "projects",
]);
const TEAM_ARCHIVE_TABS = new Set<TeamArchiveTab>([
  "issues",
  "projects",
  "cycles",
  "recently-deleted",
  "recently-deleted-projects",
  "recently-deleted-initiatives",
  "recently-deleted-documents",
]);

export function parseAppRoute(pathname: string, search = ""): AppRoute {
  const segments = pathname
    .split("/")
    .filter(Boolean)
    .map((segment) => decodeURIComponent(segment));
  if (!segments.length) return { kind: "root" };
  if (segments.length === 1 && segments[0] === "join")
    return { kind: "workspace-onboarding" };
  const [workspaceSlug, section, third, fourth, fifth, sixth] = segments;
  if (!section) return { kind: "workspace-root", workspaceSlug };
  if (section === "inbox" && segments.length === 2)
    return { kind: "inbox", workspaceSlug };
  if (section === "search" && segments.length === 2)
    return { kind: "search", workspaceSlug };
  if (section === "diary" && segments.length === 2)
    return { kind: "diary", workspaceSlug };
  if (section === "meeting" && third && segments.length === 3)
    return { kind: "meeting", workspaceSlug, meetingId: third };
  if (section === "automations" && segments.length === 2)
    return { kind: "automations", workspaceSlug };
  if (section === "automations" && third === "new" && segments.length === 3)
    return { kind: "automation-new", workspaceSlug };
  if (section === "automation" && third && fourth === "edit" && segments.length === 4)
    return { kind: "automation-detail", workspaceSlug, automationId: third, editing: true };
  if (section === "automation" && third && fourth === "runs" && segments.length === 4)
    return { kind: "automation-runs", workspaceSlug, automationId: third };
  if (section === "automation" && third && fourth === "run" && fifth && segments.length === 5)
    return { kind: "automation-runs", workspaceSlug, automationId: third, runId: fifth };
  if (section === "automation" && third && segments.length === 3)
    return { kind: "automation-detail", workspaceSlug, automationId: third };
  if (section === "pulse" && !third)
    return { kind: "pulse", workspaceSlug, view: "following" };
  if (
    section === "pulse" &&
    (third === "following" || third === "popular" || third === "all") &&
    segments.length === 3
  )
    return { kind: "pulse", workspaceSlug, view: third };
  if (
    section === "pulse" &&
    third === "view" &&
    fourth &&
    segments.length === 4
  )
    return { kind: "pulse", workspaceSlug, view: "all", viewId: fourth };
  if (section === "my-issues" && !third)
    return { kind: "my-issues", workspaceSlug, view: "assigned" };
  if (
    section === "my-issues" &&
    MY_ISSUES_VIEWS.has(third as MyIssuesRouteView) &&
    segments.length === 3
  )
    return {
      kind: "my-issues",
      workspaceSlug,
      view: third as MyIssuesRouteView,
    };
  if (section === "reviews" && segments.length === 2)
    return { kind: "reviews", workspaceSlug, view: "for-you" };
  if (section === "reviews" && third === "created" && segments.length === 3)
    return { kind: "reviews", workspaceSlug, view: "created" };
  if (section === "review" && third && segments.length === 3)
    return {
      kind: "review",
      workspaceSlug,
      reviewSlug: third,
      tab: "overview",
    };
  if (
    section === "review" &&
    third &&
    (fourth === "review" || fourth === "changes") &&
    segments.length === 4
  )
    return { kind: "review", workspaceSlug, reviewSlug: third, tab: fourth };
  if (section === "issues" && !third)
    return { kind: "workspace-issues", workspaceSlug, view: "all" };
  if (
    section === "issues" &&
    TEAM_ISSUES_VIEWS.has(third as TeamIssuesRouteView) &&
    segments.length === 3
  )
    return {
      kind: "workspace-issues",
      workspaceSlug,
      view: third as TeamIssuesRouteView,
    };
  if (section === "members" && segments.length === 2)
    return { kind: "workspace-members", workspaceSlug };
  if (
    section === "profiles" &&
    third &&
    (segments.length === 3 || (fourth === "created" && segments.length === 4))
  )
    return {
      kind: "member-profile",
      workspaceSlug,
      username: third,
      view: fourth === "created" ? "created" : "assigned",
    };
  if (section === "customers" && segments.length === 2)
    return { kind: "workspace-customers", workspaceSlug };
  if (section === "customer" && third && segments.length === 3)
    return { kind: "customer", workspaceSlug, customerSlugId: third };
  if (section === "documents" && segments.length === 2)
    return { kind: "documents", workspaceSlug };
  if (section === "analytics" && segments.length === 2)
    return { kind: "analytics", workspaceSlug };
  if (section === "dashboards" && segments.length === 2)
    return { kind: "dashboards", workspaceSlug };
  if (section === "dashboards" && third === "new" && segments.length === 3)
    return { kind: "dashboards", workspaceSlug, creating: true };
  if (
    section === "dashboard" &&
    third &&
    fourth === "widget" &&
    fifth &&
    segments.length === 5
  )
    return {
      kind: "dashboards",
      workspaceSlug,
      dashboardId: third,
      widgetId: fifth,
    };
  if (section === "dashboard" && third && segments.length === 3)
    return { kind: "dashboards", workspaceSlug, dashboardId: third };
  if (
    section === "team" &&
    third &&
    fourth === "dashboards" &&
    fifth === "new" &&
    segments.length === 5
  )
    return {
      kind: "dashboards",
      workspaceSlug,
      teamKey: third,
      creating: true,
    };
  if (
    section === "team" &&
    third &&
    fourth === "dashboards" &&
    segments.length === 4
  )
    return { kind: "dashboards", workspaceSlug, teamKey: third };
  if (section === "document" && third && segments.length === 3)
    return { kind: "document", workspaceSlug, documentSlugId: third };
  if (section === "release-note" && third && segments.length === 3)
    return { kind: "release-note", workspaceSlug, releaseNoteId: third };
  if ((section === "issue-label" || section === "project-label" || section === "initiative-label") && third && segments.length === 3)
    return {
      kind: "label",
      workspaceSlug,
      resourceName: third,
      resourceType: section === "issue-label" ? "issue" : section === "project-label" ? "project" : "initiative",
    };
  if (section === "drafts" && segments.length === 2)
    return { kind: "drafts", workspaceSlug };
  if (section === "agent" && segments.length <= 3)
    return { kind: "agent", workspaceSlug, chatSlug: third };
  if (section === "release-pipelines" && segments.length === 2)
    return { kind: "releases", workspaceSlug };
  if (
    section === "pipeline" &&
    third &&
    (fourth === "releases" || fourth === "changelog") &&
    segments.length === 4
  )
    return {
      kind: "release-pipeline",
      workspaceSlug,
      pipelineSlug: third,
      tab: fourth,
    };
  if (
    section === "pipeline" &&
    third &&
    fourth === "releases" &&
    fifth === "archived" &&
    segments.length === 5
  )
    return {
      kind: "release-pipeline",
      workspaceSlug,
      pipelineSlug: third,
      tab: "archive",
    };
  if (
    section === "pipeline" &&
    third &&
    fourth === "release" &&
    fifth &&
    (sixth === "issues" || sixth === "release-notes") &&
    segments.length === 6
  )
    return {
      kind: "release",
      workspaceSlug,
      pipelineSlug: third,
      releaseSlug: fifth,
      tab: sixth,
    };
  if (section === "asks" && segments.length === 2)
    return { kind: "asks", workspaceSlug };
  if (section === "loops" && !third && segments.length === 2)
    return { kind: "loops", workspaceSlug };
  if (section === "loops" && third === "new" && segments.length === 3) {
    const draftId = new URLSearchParams(search).get("draftId") || undefined;
    return draftId
      ? { kind: "loop-editor", workspaceSlug, draftId }
      : { kind: "loop-editor", workspaceSlug };
  }
  if (section === "loops" && third && segments.length === 3)
    return { kind: "loop-editor", workspaceSlug, loopId: third };
  if (section === "teams" && segments.length === 2)
    return { kind: "workspace-teams", workspaceSlug };
  if (section === "settings" && third === "new-team" && segments.length === 3)
    return { kind: "new-team", workspaceSlug };
  if (
    section === "settings" &&
    third === "account" &&
    fourth === "security" &&
    segments.length === 4
  )
    return { kind: "settings", workspaceSlug, page: "account-security" };
  if (
    section === "settings" &&
    third === "account" &&
    fourth &&
    ACCOUNT_SETTINGS.has(fourth as SettingsPageId) &&
    segments.length === 4
  )
    return { kind: "settings", workspaceSlug, page: fourth as SettingsPageId };
  if (
    section === "settings" &&
    third === "skill" &&
    fourth === "new" &&
    segments.length === 4
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "agents",
      agentSkillMode: "new",
    };
  if (
    section === "settings" &&
    third === "skill" &&
    fourth &&
    segments.length === 4
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "agents",
      agentSkillMode: "edit",
      agentSkillId: fourth,
    };
  if (
    section === "settings" &&
    third === "templates" &&
    fourth === "issue" &&
    fifth === "new" &&
    !sixth &&
    segments.length === 5
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "issue-templates",
      issueTemplateMode: "new",
    };
  if (
    section === "settings" &&
    third === "templates" &&
    fourth === "issue" &&
    fifth === "new" &&
    sixth === "form" &&
    segments.length === 6
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "issue-templates",
      issueTemplateMode: "new-form",
    };
  if (
    section === "settings" &&
    third === "templates" &&
    fourth === "issue" &&
    fifth &&
    sixth === "edit" &&
    segments.length === 6
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "issue-templates",
      issueTemplateMode: "edit",
      issueTemplateId: fifth,
    };
  if (
    section === "settings" &&
    third === "templates" &&
    fourth === "project" &&
    fifth === "new" &&
    !sixth &&
    segments.length === 5
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "project-templates",
      projectTemplateMode: "new",
    };
  if (
    section === "settings" &&
    third === "templates" &&
    fourth === "project" &&
    fifth &&
    sixth === "edit" &&
    segments.length === 6
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "project-templates",
      projectTemplateMode: "edit",
      projectTemplateId: fifth,
    };
  if (
    section === "settings" &&
    third === "releases" &&
    fourth === "pipelines" &&
    fifth === "new" &&
    segments.length === 5
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "releases",
      releasePipelineMode: "new",
    };
  if (
    section === "settings" &&
    third === "releases" &&
    fourth === "pipelines" &&
    fifth &&
    segments.length === 5
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "releases",
      releasePipelineMode: "edit",
      releasePipelineSlug: fifth,
    };
  if (
    section === "settings" &&
    third === "integrations" &&
    (fourth === "github" || fourth === "gitlab") &&
    segments.length === 4
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "integrations",
      integrationProvider: fourth,
    };
  if (
    section === "settings" &&
    third === "teams" &&
    fourth &&
    fifth === "ai" &&
    (sixth === "updates" || sixth === "summaries") &&
    segments.length === 6
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "team",
      teamKey: fourth,
      teamSection: `ai-${sixth}` as TeamSettingsSection,
    };
  if (
    section === "settings" &&
    third === "teams" &&
    fourth &&
    (!fifth || TEAM_SETTINGS_SECTIONS.has(fifth as TeamSettingsSection)) &&
    segments.length <= 5
  )
    return {
      kind: "settings",
      workspaceSlug,
      page: "team",
      teamKey: fourth,
      teamSection: (fifth as TeamSettingsSection) || "overview",
    };
  if (
    section === "settings" &&
    third &&
    SETTINGS_PAGES.has(third as SettingsPageId) &&
    segments.length === 3
  )
    return { kind: "settings", workspaceSlug, page: third as SettingsPageId };
  if (
    section === "views" &&
    VIEWS_RESOURCES.has(third as ViewsResource) &&
    fourth === "new" &&
    segments.length === 4
  )
    return {
      kind: "workspace-views-new",
      workspaceSlug,
      resource: third as ViewsResource,
    };
  if (
    section === "views" &&
    VIEWS_RESOURCES.has(third as ViewsResource) &&
    segments.length === 3
  )
    return {
      kind: "workspace-views",
      workspaceSlug,
      resource: third as ViewsResource,
    };
  if (section === "view" && third && fourth === "edit" && segments.length === 4)
    return {
      kind: "workspace-saved-view",
      workspaceSlug,
      viewId: third,
      editing: true,
    };
  if (section === "view" && third && segments.length === 3)
    return { kind: "workspace-saved-view", workspaceSlug, viewId: third };
  if (
    section === "team" &&
    third &&
    fourth === "board" &&
    segments.length === 4
  )
    return { kind: "team-board", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "triage" &&
    segments.length === 4
  )
    return { kind: "team-triage", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "updates" &&
    segments.length === 4
  )
    return { kind: "team-updates", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "update" &&
    fifth &&
    segments.length === 5
  )
    return { kind: "team-update", workspaceSlug, teamKey: third, updateId: fifth };
  if (
    section === "team" &&
    third &&
    fourth === "resources" &&
    segments.length === 4
  )
    return { kind: "team-resources", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "links" &&
    segments.length === 4
  )
    return { kind: "team-links", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "views" &&
    VIEWS_RESOURCES.has(fifth as ViewsResource) &&
    sixth === "new" &&
    segments.length === 6
  )
    return {
      kind: "team-views-new",
      workspaceSlug,
      teamKey: third,
      resource: fifth as ViewsResource,
    };
  if (
    section === "team" &&
    third &&
    fourth === "views" &&
    VIEWS_RESOURCES.has(fifth as ViewsResource) &&
    segments.length === 5
  )
    return {
      kind: "team-views",
      workspaceSlug,
      teamKey: third,
      resource: fifth as ViewsResource,
    };
  if (
    section === "team" &&
    third &&
    fourth === "view" &&
    fifth &&
    sixth === "edit" &&
    segments.length === 6
  )
    return {
      kind: "team-saved-view",
      workspaceSlug,
      teamKey: third,
      viewId: fifth,
      editing: true,
    };
  if (
    section === "team" &&
    third &&
    fourth === "view" &&
    fifth &&
    segments.length === 5
  )
    return {
      kind: "team-saved-view",
      workspaceSlug,
      teamKey: third,
      viewId: fifth,
    };
  if (
    section === "team" &&
    third &&
    fourth === "cycles" &&
    segments.length === 4
  )
    return { kind: "team-cycles", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "initiatives" &&
    (!fifth || fifth === "active") &&
    segments.length <= 5
  )
    return {
      kind: "team-initiatives",
      workspaceSlug,
      teamKey: third,
      view: "active",
    };
  if (
    section === "team" &&
    third &&
    fourth === "initiatives" &&
    (fifth === "planned" || fifth === "all") &&
    segments.length === 5
  )
    return {
      kind: "team-initiatives",
      workspaceSlug,
      teamKey: third,
      view: fifth,
    };
  if (
    section === "team" &&
    third &&
    fourth === "archive" &&
    segments.length === 4
  )
    return {
      kind: "team-archive",
      workspaceSlug,
      teamKey: third,
      tab: "issues",
    };
  if (
    section === "team" &&
    third &&
    fourth === "archive" &&
    TEAM_ARCHIVE_TABS.has(fifth as TeamArchiveTab) &&
    segments.length === 5
  )
    return {
      kind: "team-archive",
      workspaceSlug,
      teamKey: third,
      tab: fifth as TeamArchiveTab,
    };
  if (
    section === "team" &&
    third &&
    fourth === "cycle" &&
    fifth === "upcoming" &&
    segments.length === 5
  )
    return { kind: "cycle-upcoming", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "cycle" &&
    fifth &&
    segments.length === 5
  )
    return { kind: "cycle", workspaceSlug, teamKey: third, cycleId: fifth };
  if (
    section === "team" &&
    third &&
    fourth === "overview" &&
    segments.length === 4
  )
    return { kind: "team-overview", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "documents" &&
    segments.length === 4
  )
    return { kind: "team-documents", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "loops" &&
    segments.length === 4
  )
    return { kind: "team-loops", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "members" &&
    segments.length === 4
  )
    return { kind: "team-members", workspaceSlug, teamKey: third };
  if (section === "team" && third && !fourth)
    return { kind: "team-overview", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    TEAM_ISSUES_VIEWS.has(fourth as TeamIssuesRouteView) &&
    segments.length === 4
  )
    return {
      kind: "team-issues",
      workspaceSlug,
      teamKey: third,
      view: fourth as TeamIssuesRouteView,
    };
  if (
    section === "team" &&
    third &&
    fourth === "projects" &&
    fifth === "view" &&
    sixth === "new" &&
    segments.length === 6
  )
    return { kind: "team-projects-new-view", workspaceSlug, teamKey: third };
  if (
    section === "team" &&
    third &&
    fourth === "projects" &&
    fifth === "view" &&
    sixth &&
    segments[6] === "edit" &&
    segments.length === 7
  )
    return {
      kind: "team-projects-saved-view",
      workspaceSlug,
      teamKey: third,
      viewId: sixth,
      editing: true,
    };
  if (
    section === "team" &&
    third &&
    fourth === "projects" &&
    fifth === "view" &&
    sixth &&
    segments.length === 6
  )
    return {
      kind: "team-projects-saved-view",
      workspaceSlug,
      teamKey: third,
      viewId: sixth,
    };
  if (
    section === "team" &&
    third &&
    fourth === "projects" &&
    (!fifth || fifth === "all") &&
    segments.length <= 5
  )
    return { kind: "team-projects", workspaceSlug, teamKey: third };
  if (section === "initiatives" && !third)
    return { kind: "initiatives", workspaceSlug, view: "all" };
  if (
    section === "initiatives" &&
    (third === "active" || third === "planned") &&
    segments.length === 3
  )
    return { kind: "initiatives", workspaceSlug, view: third };
  if (section === "initiative" && third && !fourth)
    return {
      kind: "initiative",
      workspaceSlug,
      initiativeSlugId: third,
      tab: "overview",
    };
  if (section === "initiative" && third && fourth === "updates")
    return {
      kind: "initiative",
      workspaceSlug,
      initiativeSlugId: third,
      tab: "activity",
    };
  if (
    section === "initiative" &&
    third &&
    fourth === "view" &&
    fifth === "new" &&
    segments.length === 5
  )
    return {
      kind: "initiative",
      workspaceSlug,
      initiativeSlugId: third,
      tab: "new",
    };
  if (
    section === "initiative" &&
    third &&
    fourth === "view" &&
    fifth &&
    segments.length === 5
  )
    return {
      kind: "initiative",
      workspaceSlug,
      initiativeSlugId: third,
      tab: "view",
      viewId: fifth,
    };
  if (
    section === "initiative" &&
    third &&
    INITIATIVE_TABS.has(fourth as InitiativeRouteTab) &&
    segments.length === 4
  )
    return {
      kind: "initiative",
      workspaceSlug,
      initiativeSlugId: third,
      tab: fourth as InitiativeRouteTab,
    };
  if (
    section === "projects" &&
    third === "view" &&
    fourth === "new" &&
    segments.length === 4
  )
    return { kind: "projects-new-view", workspaceSlug };
  if (
    section === "projects" &&
    third === "view" &&
    fourth &&
    fifth === "edit" &&
    segments.length === 5
  )
    return {
      kind: "projects-saved-view",
      workspaceSlug,
      viewId: fourth,
      editing: true,
    };
  if (
    section === "projects" &&
    third === "view" &&
    fourth &&
    segments.length === 4
  )
    return { kind: "projects-saved-view", workspaceSlug, viewId: fourth };
  if (
    section === "projects" &&
    (!third || third === "all") &&
    segments.length <= 3
  )
    return { kind: "projects", workspaceSlug };
  if (section === "project" && third && !fourth)
    return {
      kind: "project",
      workspaceSlug,
      projectSlugId: third,
      tab: "overview",
    };
  if (
    section === "project" &&
    third &&
    fourth === "updates" &&
    segments.length === 4
  )
    return {
      kind: "project",
      workspaceSlug,
      projectSlugId: third,
      tab: "activity",
    };
  if (
    section === "project" &&
    third &&
    fourth === "view" &&
    fifth === "new" &&
    segments.length === 5
  )
    return { kind: "project", workspaceSlug, projectSlugId: third, tab: "new" };
  if (
    section === "project" &&
    third &&
    fourth === "view" &&
    fifth &&
    sixth === "edit" &&
    segments.length === 6
  )
    return {
      kind: "project-saved-view",
      workspaceSlug,
      projectSlugId: third,
      viewId: fifth,
      editing: true,
    };
  if (
    section === "project" &&
    third &&
    fourth === "view" &&
    fifth &&
    segments.length === 5
  )
    return {
      kind: "project-saved-view",
      workspaceSlug,
      projectSlugId: third,
      viewId: fifth,
    };
  if (
    section === "project" &&
    third &&
    PROJECT_TABS.has(fourth as ProjectRouteTab) &&
    segments.length === 4
  )
    return {
      kind: "project",
      workspaceSlug,
      projectSlugId: third,
      tab: fourth as ProjectRouteTab,
    };
  if (section === "issue" && third && segments.length <= 4)
    return {
      kind: "issue",
      workspaceSlug,
      identifier: third.toUpperCase(),
      titleSlug: fourth,
    };
  return { kind: "not-found", workspaceSlug };
}

export function workspaceRootPath(workspaceSlug: string) {
  return `/${encode(workspaceSlug)}`;
}
export function workspaceOnboardingPath() {
  return "/join";
}
export function inboxPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/inbox`;
}
export function searchPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/search`;
}
export function pulsePath(
  workspaceSlug: string,
  view: PulseRouteView = "following",
) {
  return `${workspaceRootPath(workspaceSlug)}/pulse/${view}`;
}
export function pulseViewPath(workspaceSlug: string, viewId: string) {
  return `${workspaceRootPath(workspaceSlug)}/pulse/view/${encode(viewId)}`;
}
export function myIssuesPath(
  workspaceSlug: string,
  view: MyIssuesRouteView = "assigned",
) {
  return `${workspaceRootPath(workspaceSlug)}/my-issues/${view}`;
}
export function reviewsPath(
  workspaceSlug: string,
  view: "for-you" | "created" = "for-you",
) {
  return `${workspaceRootPath(workspaceSlug)}/reviews${view === "created" ? "/created" : ""}`;
}
export function reviewPath(
  workspaceSlug: string,
  review: Pick<CodeReview, "slugId">,
  tab: ReviewRouteTab = "overview",
) {
  return `${workspaceRootPath(workspaceSlug)}/review/${encode(review.slugId)}${tab === "overview" ? "" : `/${tab}`}`;
}
export function workspaceIssuesPath(
  workspaceSlug: string,
  view: TeamIssuesRouteView = "all",
) {
  return `${workspaceRootPath(workspaceSlug)}/issues/${view}`;
}
export function membersPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/members`;
}
export function memberProfilePath(
  workspaceSlug: string,
  username: string,
  view: "assigned" | "created" = "assigned",
) {
  return `${workspaceRootPath(workspaceSlug)}/profiles/${encodeURIComponent(username)}${view === "created" ? "/created" : ""}`;
}
export function customersPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/customers`;
}
export function documentsPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/documents`;
}
export function analyticsPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/analytics`;
}
export function dashboardsPath(workspaceSlug: string, dashboardId?: string) {
  return `${workspaceRootPath(workspaceSlug)}/${dashboardId ? `dashboard/${encodeURIComponent(dashboardId)}` : "dashboards"}`;
}
export function dashboardWidgetPath(
  workspaceSlug: string,
  dashboardId: string,
  widgetId = "new",
) {
  return `${workspaceRootPath(workspaceSlug)}/dashboard/${encodeURIComponent(dashboardId)}/widget/${encodeURIComponent(widgetId)}`;
}
export function dashboardsNewPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/dashboards/new`;
}
export function teamDashboardsPath(workspaceSlug: string, teamKey: string) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/dashboards`;
}
export function teamDashboardsNewPath(workspaceSlug: string, teamKey: string) {
  return `${teamDashboardsPath(workspaceSlug, teamKey)}/new`;
}
export function customerPath(
  workspaceSlug: string,
  customer: { id: string; name: string },
) {
  return `${workspaceRootPath(workspaceSlug)}/customer/${slug(customer.name)}-${customer.id.slice(-12)}`;
}
export function documentPath(
  workspaceSlug: string,
  document: { slugId: string },
) {
  return `${workspaceRootPath(workspaceSlug)}/document/${encode(document.slugId)}`;
}
export function draftsPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/drafts`;
}
export function agentPath(workspaceSlug: string, chatSlug?: string) {
  return `${workspaceRootPath(workspaceSlug)}/agent${chatSlug ? `/${encodeURIComponent(chatSlug)}` : ""}`;
}
export function newAgentSkillPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/settings/skill/new`;
}
export function agentSkillPath(workspaceSlug: string, skillId: string) {
  return `${workspaceRootPath(workspaceSlug)}/settings/skill/${encodeURIComponent(skillId)}`;
}
export function releasePipelinesPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/release-pipelines`;
}
export function newReleasePipelinePath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/settings/releases/pipelines/new`;
}
export function releasePipelinePath(
  workspaceSlug: string,
  pipelineSlug: string,
  tab: ReleasePipelineTab = "releases",
) {
  return `${workspaceRootPath(workspaceSlug)}/pipeline/${encode(pipelineSlug)}/${tab === "archive" ? "releases/archived" : tab}`;
}
export function releasePath(
  workspaceSlug: string,
  pipelineSlug: string,
  releaseSlug: string,
  tab: ReleaseRouteTab = "issues",
) {
  return `${workspaceRootPath(workspaceSlug)}/pipeline/${encode(pipelineSlug)}/release/${encode(releaseSlug)}/${tab}`;
}
export function releasePipelineSettingsPath(
  workspaceSlug: string,
  pipelineSlug: string,
) {
  return `${workspaceRootPath(workspaceSlug)}/settings/releases/pipelines/${encode(pipelineSlug)}`;
}
export function asksPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/asks`;
}
export function loopsPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/loops`;
}
export function newLoopPath(workspaceSlug: string) {
  return `${loopsPath(workspaceSlug)}/new`;
}
export function loopPath(workspaceSlug: string, loopId: string) {
  return `${loopsPath(workspaceSlug)}/${encode(loopId)}`;
}
export function teamArchivePath(
  workspaceSlug: string,
  teamKey: string,
  tab?: TeamArchiveTab,
) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/archive${tab ? `/${tab}` : ""}`;
}
export function teamsPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/teams`;
}
export function newTeamPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/settings/new-team`;
}
const ACCOUNT_SETTINGS = new Set<SettingsPageId>([
  "preferences",
  "profile",
  "notifications",
  "code-and-reviews",
  "account-security",
  "connections",
  "agents",
]);
const SETTINGS_PAGES = new Set<SettingsPageId>([
  "issue-labels",
  "issue-templates",
  "sla",
  "project-labels",
  "project-templates",
  "project-statuses",
  "project-updates",
  "ai",
  "initiatives",
  "documents",
  "customer-requests",
  "releases",
  "pulse",
  "asks",
  "emojis",
  "integrations",
  "workspace",
  "teams",
  "members",
  "security",
  "audit-log",
  "api",
  "applications",
  "billing",
  "usage",
  "import-export",
  "workflows",
]);
const TEAM_SETTINGS_SECTIONS = new Set<TeamSettingsSection>([
  "overview",
  "general",
  "security",
  "members",
  "notifications",
  "issue-labels",
  "templates",
  "recurring-issues",
  "statuses",
  "workflow",
  "triage",
  "cycles",
  "agents",
  "agent-skills",
]);
export function settingsPath(
  workspaceSlug: string,
  page: SettingsPageId,
  teamKey?: string,
  teamSection?: TeamSettingsSection,
) {
  const root = `${workspaceRootPath(workspaceSlug)}/settings`;
  if (page === "team" && teamKey) {
    if (!teamSection || teamSection === "overview")
      return `${root}/teams/${encode(teamKey)}`;
    if (teamSection === "ai-updates")
      return `${root}/teams/${encode(teamKey)}/ai/updates`;
    if (teamSection === "ai-summaries")
      return `${root}/teams/${encode(teamKey)}/ai/summaries`;
    return `${root}/teams/${encode(teamKey)}/${teamSection}`;
  }
  if (page === "account-security") return `${root}/account/security`;
  return ACCOUNT_SETTINGS.has(page)
    ? `${root}/account/${page}`
    : `${root}/${page}`;
}
export function newIssueTemplatePath(workspaceSlug: string, form = false) {
  return `${workspaceRootPath(workspaceSlug)}/settings/templates/issue/new${form ? "/form" : ""}`;
}
export function issueTemplateEditPath(
  workspaceSlug: string,
  templateId: string,
) {
  return `${workspaceRootPath(workspaceSlug)}/settings/templates/issue/${encode(templateId)}/edit`;
}
export function newProjectTemplatePath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/settings/templates/project/new`;
}
export function projectTemplateEditPath(
  workspaceSlug: string,
  templateId: string,
) {
  return `${workspaceRootPath(workspaceSlug)}/settings/templates/project/${encode(templateId)}/edit`;
}
export function integrationSettingsPath(
  workspaceSlug: string,
  provider: "github" | "gitlab",
) {
  return `${workspaceRootPath(workspaceSlug)}/settings/integrations/${provider}`;
}
export function workspaceSavedViewPath(workspaceSlug: string, viewId: string) {
  return `${workspaceRootPath(workspaceSlug)}/view/${encode(viewId)}`;
}
export function savedViewPathId(view: Pick<SavedView, "id" | "slugId">) {
  return view.slugId || view.id;
}
export function workspaceSavedViewEditPath(
  workspaceSlug: string,
  viewId: string,
) {
  return `${workspaceSavedViewPath(workspaceSlug, viewId)}/edit`;
}
export function teamIssuesPath(
  workspaceSlug: string,
  teamKey: string,
  view: TeamIssuesRouteView = "all",
) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/${view}`;
}
export function teamHomePath(workspaceSlug: string, teamKey: string) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/overview`;
}
export function teamDocumentsPath(workspaceSlug: string, teamKey: string) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/documents`;
}
export function teamLoopsPath(workspaceSlug: string, teamKey: string) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/loops`;
}
export function teamMembersPath(workspaceSlug: string, teamKey: string) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/members`;
}
export function teamCyclesPath(workspaceSlug: string, teamKey: string) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/cycles`;
}
export function currentCyclePath(workspaceSlug: string, teamKey: string) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/cycle/active`;
}
export function upcomingCyclePath(workspaceSlug: string, teamKey: string) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/cycle/upcoming`;
}
export function cyclePath(
  workspaceSlug: string,
  teamKey: string,
  cycle: Pick<Cycle, "number">,
) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/cycle/${cycle.number}`;
}
export function teamSavedViewPath(
  workspaceSlug: string,
  teamKey: string,
  viewId: string,
) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/view/${encode(viewId)}`;
}
export function teamSavedViewEditPath(
  workspaceSlug: string,
  teamKey: string,
  viewId: string,
) {
  return `${teamSavedViewPath(workspaceSlug, teamKey, viewId)}/edit`;
}
export function workspaceViewsPath(
  workspaceSlug: string,
  resource: ViewsResource = "issues",
) {
  return `${workspaceRootPath(workspaceSlug)}/views/${resource}`;
}
export function workspaceViewsNewPath(
  workspaceSlug: string,
  resource: ViewsResource = "issues",
) {
  return `${workspaceViewsPath(workspaceSlug, resource)}/new`;
}
export function teamViewsPath(
  workspaceSlug: string,
  teamKey: string,
  resource: ViewsResource = "issues",
) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/views/${resource}`;
}
export function teamViewsNewPath(
  workspaceSlug: string,
  teamKey: string,
  resource: ViewsResource = "issues",
) {
  return `${teamViewsPath(workspaceSlug, teamKey, resource)}/new`;
}
export function projectsPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/projects/all`;
}
export function initiativesPath(
  workspaceSlug: string,
  view: InitiativesRouteView = "active",
) {
  return `${workspaceRootPath(workspaceSlug)}/initiatives${view === "all" ? "" : `/${view}`}`;
}
export function teamInitiativesPath(
  workspaceSlug: string,
  teamKey: string,
  view: InitiativesRouteView = "active",
) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/initiatives/${view}`;
}
export function initiativePath(
  workspaceSlug: string,
  initiative: Pick<Initiative, "slugId">,
  tab: InitiativeRouteTab = "overview",
  viewId?: string,
) {
  return `${workspaceRootPath(workspaceSlug)}/initiative/${encode(initiative.slugId)}/${tab === "new" ? "view/new" : tab === "view" && viewId ? `view/${encode(viewId)}` : tab}`;
}
export function teamProjectsPath(workspaceSlug: string, teamKey: string) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/projects/all`;
}
export function projectsNewViewPath(workspaceSlug: string) {
  return `${workspaceRootPath(workspaceSlug)}/projects/view/new`;
}
export function projectsSavedViewPath(workspaceSlug: string, viewId: string) {
  return `${workspaceRootPath(workspaceSlug)}/projects/view/${encode(viewId)}`;
}
export function projectsSavedViewEditPath(
  workspaceSlug: string,
  viewId: string,
) {
  return `${projectsSavedViewPath(workspaceSlug, viewId)}/edit`;
}
export function teamProjectsNewViewPath(
  workspaceSlug: string,
  teamKey: string,
) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/projects/view/new`;
}
export function teamProjectsSavedViewPath(
  workspaceSlug: string,
  teamKey: string,
  viewId: string,
) {
  return `${workspaceRootPath(workspaceSlug)}/team/${encode(teamKey)}/projects/view/${encode(viewId)}`;
}
export function teamProjectsSavedViewEditPath(
  workspaceSlug: string,
  teamKey: string,
  viewId: string,
) {
  return `${teamProjectsSavedViewPath(workspaceSlug, teamKey, viewId)}/edit`;
}
export function issuePath(
  workspaceSlug: string,
  issue: Pick<Issue, "identifier" | "title">,
) {
  return `${workspaceRootPath(workspaceSlug)}/issue/${encode(issue.identifier)}/${encode(slug(issue.title))}`;
}
export function projectPath(
  workspaceSlug: string,
  project: Pick<Project, "slugId">,
  tab: ProjectRouteTab = "overview",
) {
  return `${workspaceRootPath(workspaceSlug)}/project/${encode(project.slugId)}/${tab === "new" ? "view/new" : tab}`;
}
export function projectSavedViewPath(
  workspaceSlug: string,
  projectSlugId: string,
  viewId: string,
) {
  return `${workspaceRootPath(workspaceSlug)}/project/${encode(projectSlugId)}/view/${encode(viewId)}`;
}
export function projectSavedViewEditPath(
  workspaceSlug: string,
  projectSlugId: string,
  viewId: string,
) {
  return `${projectSavedViewPath(workspaceSlug, projectSlugId, viewId)}/edit`;
}

export function routeBelongsToWorkspace(
  route: AppRoute,
  workspaceSlug: string,
) {
  return (
    route.kind === "root" ||
    route.kind === "workspace-onboarding" ||
    route.workspaceSlug === workspaceSlug
  );
}

function encode(value: string) {
  return encodeURIComponent(value);
}
function slug(value: string) {
  return (
    value
      .normalize("NFKC")
      .toLowerCase()
      .replace(/[^\p{L}\p{N}]+/gu, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 80) || "issue"
  );
}
