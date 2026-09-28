import { useMemo, useState, type ReactNode } from 'react'

import { IssueExplorerPage } from '@/components/issue-explorer/issue-explorer-page'
import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'
import { memberProfilePath } from '@/lib/app-routes'
import type { BootstrapData, Issue, IssueUpdateInput, User } from '@/types/flow'

import { MemberProfileAside } from './member-profile-aside'
import { MemberProfileEmpty } from './member-profile-empty'
import { memberAppKind, useOpenUserShortcut } from './member-profile-model'
import { MemberUserSwitcher } from './member-user-switcher'
import './member-profile-page.css'

/**
 * Linear `userProfile` / `userProfileCreatedByUser`: the shared issue view scoped to one member,
 * with Assigned / Created tabs. Filters, display options, bulk actions and paging come from the explorer;
 * the header opens a user switcher and "Open details" shows the member's profile aside.
 */
export function MemberProfilePage({ data, user, view, onNavigate, onOpenMember, onOpenIssue, onUpdateIssue, onUpdateIssues, onDeleteIssues, onCreateIssue, renderIssuePreview, onOpenSidebar }: {
  data: BootstrapData
  user: User
  view: 'assigned' | 'created'
  onNavigate: (view: 'assigned' | 'created') => void
  /** Opens another member's profile (user switcher). */
  onOpenMember?: (user: User) => void
  onOpenIssue: (issue: Issue, sequence?: string[]) => void
  onUpdateIssue: (id: string, input: IssueUpdateInput) => Promise<Issue>
  onUpdateIssues?: (ids: string[], input: IssueUpdateInput) => Promise<Issue[]>
  onDeleteIssues?: (ids: string[]) => Promise<void>
  onCreateIssue?: (context?: MyIssuesCreateContext) => void
  renderIssuePreview?: (issue: Issue, onClose: () => void) => ReactNode
  onOpenSidebar?: () => void
}) {
  const [switcherOpen, setSwitcherOpen] = useState(false)
  useOpenUserShortcut(() => setSwitcherOpen(true))
  const scopeFilter = useMemo(() => view === 'created'
    ? (issue: Issue) => issue.creator.id === user.id
    : (issue: Issue) => user.app ? issue.delegate?.id === user.id : issue.assignee?.id === user.id, [user.app, user.id, view])
  const scopeConditions = useMemo(() => [{ field: view === 'created' ? 'creator' : user.app ? 'delegateId' : 'assignee', values: [user.id] }], [user.app, user.id, view])
  const kind = memberAppKind(user)
  const openMember = (next: User) => {
    if (onOpenMember) onOpenMember(next)
    else window.location.assign(memberProfilePath(data.workspace.urlKey, next.name))
  }
  return <IssueExplorerPage
    key={`${user.id}-${view}`}
    className="member-profile-explorer"
    data={data}
    scope={{ kind: 'workspace' }}
    view="all"
    preferenceScope={`member:${user.id}:${view}`}
    defaultDisplayOverrides={{ showTriageIssues: true, grouping: 'none' }}
    scopeFilter={scopeFilter}
    scopeConditions={scopeConditions}
    insightsLabel="insights"
    detailsStorageKey={`${data.workspace.urlKey}:member-profile:details`}
    detailsPanel={<MemberProfileAside data={data} user={user}/>}
    emptyState={<MemberProfileEmpty/>}
    resourceHeader={{
      title: <span className="member-profile-title">
        <MemberUserSwitcher user={user} users={data.users} open={switcherOpen} onOpenChange={setSwitcherOpen} onSelect={openMember}/>
        {kind ? <small className="member-profile-title__kind">{kind}</small> : null}
      </span>,
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
