import type { Comment, Issue, IssueLabel, IssueUpdateInput, Project, ProjectMilestone, ProjectResource, ProjectStatus, ProjectUpdate, SavedView, SavedViewMutationInput, Team, User } from '@/types/flow'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'

export type ProjectDetailTab = 'overview' | 'activity' | 'issues' | 'new'

export type ProjectDetailProps = {
  project: Project
  projects: Project[]
  projectStatuses: ProjectStatus[]
  projectUpdates: ProjectUpdate[]
  issues: Issue[]
  users: User[]
  teams: Team[]
  labels: IssueLabel[]
  viewer: User
  tab: ProjectDetailTab
  onTabChange: (tab: ProjectDetailTab) => void
  onUpdate: (projectId: string, input: ProjectMutationInput) => Promise<Project>
  onCreateUpdate: (projectId: string, input: { body: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  onUpdateProjectUpdate: (projectId: string, updateId: string, input: { body?: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  onDeleteUpdate: (projectId: string, updateId: string) => Promise<void>
  onCommentProjectUpdate: (projectId: string, updateId: string, body: string) => Promise<ProjectUpdate>
  onReactProjectUpdate: (projectId: string, updateId: string, emoji: string) => Promise<ProjectUpdate>
  onCommentProject: (projectId: string, body: string) => Promise<Comment>
  onCreateResource: (projectId: string, input: { type?: 'link'|'document'; title?: string; url: string }) => Promise<ProjectResource>
  onUpdateResource: (projectId: string, resourceId: string, input: { type?: 'link'|'document'; title?: string; url?: string }) => Promise<ProjectResource>
  onDeleteResource: (projectId: string, resourceId: string) => Promise<void>
  onCreateMilestone: (projectId: string, input: { name: string; targetDate?: string }) => Promise<ProjectMilestone>
  onUpdateMilestone: (projectId: string, milestoneId: string, input: { name?: string; targetDate?: string }) => Promise<ProjectMilestone>
  onDeleteMilestone: (projectId: string, milestoneId: string) => Promise<void>
  onMoveMilestone: (projectId: string, milestoneId: string, targetProjectId: string) => Promise<void>
  onConvertMilestone: (projectId: string, milestoneId: string) => Promise<Project>
  onReorderMilestones: (projectId: string, ids: string[]) => Promise<ProjectMilestone[]>
  onDelete: (projectId: string) => Promise<void>
  onCreateSavedView: (input: SavedViewMutationInput) => Promise<SavedView>
  savedViews: SavedView[]
  onUpdateSavedView: (viewId: string, input: SavedViewMutationInput) => Promise<SavedView>
  onDeleteSavedView: (view: SavedView) => Promise<void>
  onOpenIssue: (issue: Issue) => void
  onUpdateIssue: (issueId: string, input: IssueUpdateInput) => Promise<Issue>
  onDeleteIssues: (issueIds: string[]) => Promise<void>
  onCreateIssue: (projectId: string) => void
  onOpenSidebar?: () => void
}

export const PROJECT_HEALTHS: { id: Project['health']; label: string }[] = [
  { id: 'onTrack', label: 'On track' },
  { id: 'atRisk', label: 'At risk' },
  { id: 'offTrack', label: 'Off track' },
  { id: 'noUpdate', label: 'No update' },
]

export const PRIORITY_LABELS = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
