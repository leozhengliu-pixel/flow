import { useMemo, type ReactNode } from 'react'

import { IssueExplorerPage } from '@/components/issue-explorer/issue-explorer-page'
import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'
import { UserAvatar } from '@/components/ui/user-avatar'
import { memberProfilePath } from '@/lib/app-routes'
import type { BootstrapData, Issue, IssueUpdateInput, User } from '@/types/flow'

import './workspace-directory.css'

/**
 * Linear `userProfile` / `userProfileCreatedByUser`: the shared issue view scoped to one member,
 * with Assigned / Created tabs. Filters, display options, bulk actions and paging come from the explorer.
 */
export function MemberProfilePage({ data, user, view, onNavigate, onOpenIssue, onUpdateIssue, onUpdateIssues, onDeleteIssues, onCreateIssue, renderIssuePreview, onOpenSidebar }: {
  data: BootstrapData
  user: User
  view: 'assigned' | 'created'
  onNavigate: (view: 'assigned' | 'created') => void
  onOpenIssue: (issue: Issue, sequence?: string[]) => void
  onUpdateIssue: (id: string, input: IssueUpdateInput) => Promise<Issue>
  onUpdateIssues?: (ids: string[], input: IssueUpdateInput) => Promise<Issue[]>
  onDeleteIssues?: (ids: string[]) => Promise<void>
  onCreateIssue?: (context?: MyIssuesCreateContext) => void
  renderIssuePreview?: (issue: Issue, onClose: () => void) => ReactNode
  onOpenSidebar?: () => void
}) {
  const scopeFilter = useMemo(() => view === 'created'
    ? (issue: Issue) => issue.creator.id === user.id
    : (issue: Issue) => user.app ? issue.delegate?.id === user.id : issue.assignee?.id === user.id, [user.app, user.id, view])
  const scopeConditions = useMemo(() => [{ field: view === 'created' ? 'creator' : user.app ? 'delegateId' : 'assignee', values: [user.id] }], [user.app, user.id, view])
  const kind = user.app ? (user.appScopes?.some(scope => scope === 'app:mentionable' || scope === 'app:assignable') ? 'Agent' : 'Application') : undefined
  return <IssueExplorerPage
    key={`${user.id}-${view}`}
    data={data}
    scope={{ kind: 'workspace' }}
    view="all"
    preferenceScope={`member:${user.id}:${view}`}
    scopeFilter={scopeFilter}
    scopeConditions={scopeConditions}
    resourceHeader={{
      icon: <UserAvatar avatarUrl={user.avatarUrl} color="#5e6ad2" name={user.displayName} />,
      title: <span data-i18n-ignore>{user.displayName}{kind ? <small className="member-profile-kind">{kind}</small> : null}</span>,
      tabs: (['assigned', 'created'] as const).map(id => ({ id, label: id === 'assigned' ? 'Assigned' : 'Created', href: memberProfilePath(data.workspace.urlKey, user.name, id), active: view === id, onSelect: () => onNavigate(id) })),
    }}
    viewHref={() => memberProfilePath(data.workspace.urlKey, user.name, view)}
    onNavigateView={() => onNavigate(view)}
    onOpenIssue={onOpenIssue}
    renderIssuePreview={renderIssuePreview}
    onOpenSidebar={onOpenSidebar}
    onCreateIssue={context => onCreateIssue?.(view === 'assigned' && !user.app ? { ...context, assigneeId: user.id } : context)}
    onUpdateIssue={onUpdateIssue}
    onUpdateIssues={onUpdateIssues ?? (async (ids, input) => Promise.all(ids.map(id => onUpdateIssue(id, input))))}
    onDeleteIssues={onDeleteIssues ?? (async () => undefined)}
  />
}
