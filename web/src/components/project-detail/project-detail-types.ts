import type {
  BootstrapData,
  ActivityEvent,
  Comment,
  Cycle,
  Draft,
  Favorite,
  FlowDocument,
  Initiative,
  IntegrationConnection,
  Issue,
  IssueLabel,
  IssueUpdateInput,
  LabelGroup,
  Notification,
  Project,
  ProjectRelation,
  ProjectMilestone,
  ProjectResource,
  ProjectStatus,
  ProjectUpdate,
  SavedView,
  SavedViewMutationInput,
  Subscription,
  Team,
  ThreadSubscription,
  ThreadSubscriptionState,
  User,
  WorkflowState,
  WorkspaceRole,
} from "@/types/flow";
import type { MyIssuesCreateContext } from "@/components/my-issues/my-issues-list";
import type { ProjectMutationInput } from "@/components/projects-page/projects-page";
import type { IssueRecordSummary } from '@/lib/api';

export type ProjectDetailTab = "overview" | "activity" | "issues" | "new";

export type ProjectDetailProps = {
  issueData?: BootstrapData;
  issueSummary?: IssueRecordSummary;
  project: Project;
  projectRelations?: ProjectRelation[];
  projects: Project[];
  initiatives: Initiative[];
  documents: FlowDocument[];
  integrationConnections: IntegrationConnection[];
  projectStatuses: ProjectStatus[];
  projectUpdates: ProjectUpdate[];
  drafts?: Draft[];
  issues: Issue[];
  workflowStates?: WorkflowState[];
  cycles?: Cycle[];
  users: User[];
  teams: Team[];
  labels: IssueLabel[];
  labelGroups: LabelGroup[];
  onCreateLabel?: (name: string, groupId?: string) => Promise<IssueLabel>;
  viewer: User;
  /** Workspace role of the viewer; admins and owners may moderate comments. */
  viewerRole?: WorkspaceRole;
  activities: ActivityEvent[];
  favorite?: Favorite;
  subscription?: Subscription;
  tab: ProjectDetailTab;
  savedView?: SavedView;
  editingSavedView?: boolean;
  onTabChange: (tab: ProjectDetailTab) => void;
  onUpdate: (
    projectId: string,
    input: ProjectMutationInput,
  ) => Promise<Project>;
  onCreateUpdate: (
    projectId: string,
    input: { body: string; health?: Project["health"] },
  ) => Promise<ProjectUpdate>;
  onUpdateProjectUpdate: (
    projectId: string,
    updateId: string,
    input: { body?: string; health?: Project["health"] },
  ) => Promise<ProjectUpdate>;
  onDeleteUpdate: (projectId: string, updateId: string) => Promise<void>;
  onCommentProjectUpdate: (
    projectId: string,
    updateId: string,
    body: string,
  ) => Promise<ProjectUpdate>;
  onReactProjectUpdate: (
    projectId: string,
    updateId: string,
    emoji: string,
  ) => Promise<ProjectUpdate>;
  onUploadProjectUpdateAttachment: (
    projectId: string,
    updateId: string,
    file: File,
  ) => Promise<ProjectUpdate>;
  onDeleteProjectUpdateAttachment: (
    projectId: string,
    updateId: string,
    attachmentId: string,
  ) => Promise<ProjectUpdate>;
  onCommentProject: (projectId: string, body: string, bodyData?: Record<string,unknown>, parentId?: string) => Promise<Comment>;
  onUpdateProjectComment: (projectId: string, commentId: string, body: string, bodyData?: Record<string, unknown>) => Promise<Comment>;
  onDeleteProjectComment: (projectId: string, commentId: string) => Promise<void>;
  onReactProjectComment: (projectId: string, commentId: string, emoji: string) => Promise<Comment>;
  /** Resolve or re-open a comment thread (Linear "Resolve thread"). */
  onResolveProjectComment: (projectId: string, commentId: string, resolved: boolean) => Promise<Comment>;
  /** Subscribe to, mute, or (null) clear the viewer's explicit choice for a comment thread. */
  onProjectCommentThreadSubscription: (projectId: string, commentId: string, state: ThreadSubscriptionState | null) => Promise<void>;
  /** The viewer's explicit comment-thread choices; thread participants follow implicitly. */
  threadSubscriptions?: ThreadSubscription[];
  onCreateResource: (
    projectId: string,
    input: { type?: "link" | "document"; title?: string; url?: string },
  ) => Promise<ProjectResource>;
  onUpdateResource: (
    projectId: string,
    resourceId: string,
    input: {
      type?: "link" | "document";
      title?: string;
      url?: string;
      pinnedTeamIds?: string[];
    },
  ) => Promise<ProjectResource>;
  onDeleteResource: (projectId: string, resourceId: string) => Promise<void>;
  /** Open a document resource's page with its history dialog showing. */
  onOpenDocumentHistory?: (document: FlowDocument) => void;
  /** Refetch workspace metadata after document mutations made from a resource menu. */
  onReloadWorkspace?: () => Promise<void>;
  onCreateMilestone: (
    projectId: string,
    input: { name: string; description?: string; targetDate?: string },
  ) => Promise<ProjectMilestone>;
  onUpdateMilestone: (
    projectId: string,
    milestoneId: string,
    input: { name?: string; description?: string; targetDate?: string },
  ) => Promise<ProjectMilestone>;
  onDeleteMilestone: (projectId: string, milestoneId: string) => Promise<void>;
  onMoveMilestone: (
    projectId: string,
    milestoneId: string,
    targetProjectId: string,
  ) => Promise<void>;
  onConvertMilestone: (
    projectId: string,
    milestoneId: string,
  ) => Promise<Project>;
  onReorderMilestones: (
    projectId: string,
    ids: string[],
  ) => Promise<ProjectMilestone[]>;
  onDelete: (projectId: string) => Promise<void>;
  onToggleFavorite: (projectId: string, favorite: boolean) => Promise<void>;
  onSetSubscriptionEvents: (
    projectId: string,
    events: string[],
  ) => Promise<void>;
  onCreateReminder: (
    projectId: string,
    remindAt: string,
  ) => Promise<Notification>;
  onCreateSavedView: (input: SavedViewMutationInput) => Promise<SavedView>;
  onOpenSavedView?: (view: SavedView) => void;
  onEditSavedView?: (view: SavedView) => void;
  savedViews: SavedView[];
  onUpdateSavedView: (
    viewId: string,
    input: SavedViewMutationInput,
  ) => Promise<SavedView>;
  onDeleteSavedView: (view: SavedView) => Promise<void>;
  onOpenIssue: (issue: Issue) => void;
  onUpdateIssue: (issueId: string, input: IssueUpdateInput) => Promise<Issue>;
  onDeleteIssues: (issueIds: string[]) => Promise<void>;
  onCreateIssue: (projectId: string, projectMilestoneId?: string, context?: MyIssuesCreateContext) => void;
  onOpenSidebar?: () => void;
  /** Projects list the project was opened from; the "Projects ›" crumb only renders when set. */
  projectsOriginPath?: string;
  onOpenProjects?: () => void;
};

export const PROJECT_HEALTHS: { id: Project["health"]; label: string }[] = [
  { id: "onTrack", label: "On track" },
  { id: "atRisk", label: "At risk" },
  { id: "offTrack", label: "Off track" },
  { id: "noUpdate", label: "No update" },
];

export const PRIORITY_LABELS = [
  "No priority",
  "Urgent",
  "High",
  "Medium",
  "Low",
];
