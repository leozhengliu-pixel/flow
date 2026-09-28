/**
 * Workspace entity kinds addressable via useWorkspaceStore().getById / hydrateModel.
 * Aligned with BootstrapData collections (Wave 2 store spine — LS-0770 / LS-0718).
 */
export type WorkspaceEntityType =
  | 'issue'
  | 'project'
  | 'team'
  | 'user'
  | 'state'
  | 'label'
  | 'cycle'
  | 'initiative'
  | 'document'
  | 'notification'
  | 'favorite'
  | 'agentSession'
  | 'ask'
  | 'savedView'

/** Normalize Linear-style model class names / aliases to Flow entity keys. */
export function normalizeEntityType(type: string): WorkspaceEntityType | string {
  const key = type.trim()
  const aliases: Record<string, WorkspaceEntityType> = {
    Issue: 'issue',
    issue: 'issue',
    Project: 'project',
    project: 'project',
    Team: 'team',
    team: 'team',
    User: 'user',
    user: 'user',
    WorkflowState: 'state',
    workflowState: 'state',
    state: 'state',
    IssueLabel: 'label',
    label: 'label',
    Cycle: 'cycle',
    cycle: 'cycle',
    Initiative: 'initiative',
    initiative: 'initiative',
    Document: 'document',
    document: 'document',
    Notification: 'notification',
    notification: 'notification',
    Favorite: 'favorite',
    favorite: 'favorite',
    AgentSession: 'agentSession',
    agentSession: 'agentSession',
    Ask: 'ask',
    ask: 'ask',
    SavedView: 'savedView',
    savedView: 'savedView',
  }
  return aliases[key] ?? key
}
