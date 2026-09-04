import type {
  ActivityEvent,
  Comment,
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
  User,
} from "@/types/flow";
import type { MyIssuesCreateContext } from "@/components/my-issues/my-issues-list";
import type { ProjectMutationInput } from "@/components/projects-page/projects-page";

export type ProjectDetailTab = "overview" | "activity" | "issues" | "new";

export type ProjectDetailProps = {
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
  users: User[];
  teams: Team[];
  labels: IssueLabel[];
  labelGroups: LabelGroup[];
  viewer: User;
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
  onCommentProject: (projectId: string, body: string) => Promise<Comment>;
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
