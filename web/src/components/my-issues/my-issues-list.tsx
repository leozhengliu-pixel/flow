import { memo, useContext, useEffect, useInsertionEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import { useI18n } from '@/i18n/i18n'
import { Virtuoso, type Components } from 'react-virtuoso'
import * as ContextMenu from '@radix-ui/react-context-menu'
import * as Popover from '@radix-ui/react-popover'
import { Check, ChevronDown, Clock3, Link2 } from 'lucide-react'
import { PlusIcon } from '@/components/ui/view-action-icons'
import { IssueReleaseChip, type IssueReleaseEntry } from '@/components/issue/issue-release-chip'
import type { MyIssuesProperty } from './my-issues-surface'
import { CalendarIcon, CycleIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, StatusIcon, WorkflowStatusGlyph } from '@/components/issue/issue-icons'
import { MilestoneProgressIcon } from '@/components/issue/milestone-progress-icon'
import { isMilestoneDateOverdue } from '@/components/issue/milestone-progress'
import { PropertyMenu, type PropertyMenuKind } from '@/components/property/property-menu'
import { LabelHoverPreview } from '@/components/property/label-hover-preview'
import { DueDatePicker } from '@/components/issue/due-date-picker'
import styles from './my-issues-list.module.css'
import { ContextMenuIcon } from './context-menu-icon'
import { IssueRowActionGroups, IssueRowEstimateItem, IssueRowMoreProperties } from './issue-row-actions'
import { useIssueRowActions, useTeamEstimateSettings } from './use-issue-row-actions'
import { EstimateChip, EstimateIcon } from '@/components/issue/estimate-icon'
import { formatEstimatePoints, sumEstimatePoints, teamUsesEstimates } from '@/lib/estimates'
import { useTeamStatusOptions } from './use-team-status-options'
import { teamWorkflowStates } from '@/lib/team-workflow'
import { AvatarImage, UserAvatar } from '@/components/ui/user-avatar'
import { avatarColor } from '@/components/issue/avatar-color'
import { AgentHover } from '@/components/agent/agent-hover-card'
import { IssueAssigneeSlot } from './issue-assignee-slot'
import { agentHoverProps } from '@/components/agent/agent-hover-props'
import { ASSIGNEE_INVITE_ID, assigneeShortcut } from '@/lib/agent-members'
import { openMemberInvite, viewerCanInvite } from '@/lib/invite-navigation'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { IssueSLAIndicator } from '@/components/issue/issue-sla-indicator'
import { SubIssueProgressRing } from '@/components/issue/sub-issue-progress-ring'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { LoadingContainer, LoadingListFooter } from '@/components/ui/loading-caret'
import type { ActivityEvent, FeatureSettings, IssueSLA } from '@/types/flow'
import { CustomerRevenueRowChip, CustomersRowChip } from '@/components/customer/customer-row-properties'
import { fetchIssueHistory } from '@/lib/api'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import { StatusHoverPreview } from '@/components/property/issue-property-hover'
import { toggleGroupedLabelIds } from '@/lib/labels'
import { PersonHover } from '@/components/property/person-info'
import { isPeopleProperty } from '@/lib/people'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { IssueWidgetAdornments } from '@/components/issues-split-view'
import { nestedLinesByIssue } from '@/components/issue-explorer/issue-grouping'
import { CODING_STATE_LABELS } from '@/components/coding-sessions/coding-session-model'
import { useOptionalTranslate } from '@/i18n/i18n'
import { usePhoneLayout } from '@/hooks/use-phone-layout'

export type MyIssuesStateType = 'backlog' | 'unstarted' | 'started' | 'completed' | 'canceled'
export type MyIssuesContextAction = 'status' | 'priority' | 'assignee' | 'dueDate' | 'labels' | 'project' | 'cycle' | 'moreProperties' | 'createRelated' | 'markAs' | 'copy' | 'copyUrl' | 'copyId' | 'copyTitle' | 'convertTo' | 'move' | 'openIn' | 'runLoop' | 'favorite' | 'remind' | 'delete'
export type MyIssuesEditableProperty = 'status' | 'priority' | 'assignee' | 'dueDate' | 'labels' | 'project' | 'cycle'
export interface MyIssuesContextOption { id: string; teamId?: string; /** An agent member: choosing it delegates instead of assigning (hover card shows the "Agent" pill). */ agent?: boolean; /** The agent's handle (lowercase username), shown in its hover card. */ handle?: string; label: string; color?: string; description?: string; issueCount?: number; scope?: string; groupId?: string; groupLabel?: string; avatarUrl?: string; kind?: MyIssuesEditableProperty; priority?: 0 | 1 | 2 | 3 | 4; shortcut?: string; stateType?: MyIssuesStateType; /** Replaces the default icon (the delegated agent hung under the assignee). */ icon?: ReactNode; /** Replaces the label text. */ labelContent?: ReactNode }
export interface MyIssuesRowPropertyOptions {
  status: MyIssuesContextOption[]
  priority: MyIssuesContextOption[]
  assignee: MyIssuesContextOption[]
  dueDate: MyIssuesContextOption[]
  labels: MyIssuesContextOption[]
  project: MyIssuesContextOption[]
  cycle?: MyIssuesContextOption[]
}

/** Values inherited from the group where an issue is being created. */
export interface MyIssuesCreateContext {
  title?: string
  description?: string
  teamId?: string
  stateId?: string
  priority?: 0 | 1 | 2 | 3 | 4
  assigneeId?: string
  projectId?: string
  projectMilestoneId?: string
  cycleId?: string
  labelIds?: string[]
  parentId?: string
  /** Prefilled-link extras (see lib/prefilled-issue-url). */
  estimate?: number
  dueDate?: string
  templateId?: string
  /** Relate the new issue to an existing one once it is created. */
  related?: { issueId: string; kind: 'related' | 'parent' | 'blocked' | 'blocking' }
}

export interface MyIssuesRowData {
  id: string
  teamId?: string
  teamName?: string
  identifier: string
  title: string
  description?: string
  href?: string
  priority: 0 | 1 | 2 | 3 | 4
  state: { id: string; name: string; type: MyIssuesStateType; color: string }
  labels?: { id: string; name: string; color: string; description?:string; issueCount?:number; scope?:string; groupId?: string }[]
  project?: { id: string; name: string; color: string }
  assignee?: { id: string; name: string; avatarUrl?: string; color?: string }
  delegate?: { id: string; name: string; avatarUrl?: string; color?: string }
  creatorId?: string
  creatorName?: string
  isAssignedToViewer?: boolean
  cycleId?: string
  cycleName?: string
  addedToCycle?: string
  agentSessionId?: string
  agentSessionState?: string
  suggestedLabelIds?: string[]
  suggestedAssigneeIds?: string[]
  suggestedProjectIds?: string[]
  suggestedTeamIds?: string[]
  suggestedDuplicateIds?: string[]
  suggestedRelatedIds?: string[]
  externalSource?: string
  autoClosed?: boolean
  autoClosedAt?: string
  triagedAt?: string
  /** In the team's triage queue (triage enabled, backlog, never accepted). */
  triage?: boolean
  templateId?: string
  initiativeIds?: string[]
  projectStatusId?: string
  projectStatusType?: string
  projectPriority?: number
  projectLabelIds?: string[]
  projectLeadId?: string
  projectMilestoneNames?: string[]
  /** Name of the milestone this issue is in (not the project's first milestone). */
  milestoneName?: string
  projectMilestoneId?: string
  milestoneProgress?: number
  rawMilestoneDate?: string
  customerIds?: string[]
  hasUnknownCustomer?: boolean
  customerOwnerIds?: string[]
  customerStatuses?: string[]
  customerTiers?: string[]
  customerRevenues?: number[]
  customerSizes?: number[]
  customerNames?: string[]
  /** Requesting customers (logo pile on the Customers display property). */
  customers?: Array<{ id: string; name: string; logoUrl?: string; annualRevenue?: number }>
  /** Customers with at least one important request. */
  importantCustomerIds?: string[]
  /** The reference app's customerCount: distinct requesting customers (unknown customers count once). */
  customerCount?: number
  /** Workspace revenue format/currency for the Customer revenue property. */
  customerRevenueSettings?: Partial<FeatureSettings>
  releaseIds?: string[]
  releasePipelineIds?: string[]
  releaseStages?: string[]
  releaseStatuses?: string[]
  hasReleasedRelease?: boolean
  /** When each of the issue's releases was released (Releases ▸ Released date matches any). */
  releasedDates?: string[]
  subscriberIds?: string[]
  relationTypes?: string[]
  hasLinks?: boolean
  linkCount?: number
  pullRequestCount?: number
  /** LS-0359 — dominant linked PR lifecycle for row chrome. */
  pullRequestLifecycle?: 'open' | 'inReview' | 'approved' | 'merged' | 'closed' | 'draft'
  blockedByCount?: number
  blockingCount?: number
  blockedByIds?: readonly string[]
  blockingIds?: readonly string[]
  releaseCount?: number
  releases?: IssueReleaseEntry[]
  hasContent?: boolean
  estimate?: number
  dueDate?: string
  /** The issue owns a recurring schedule (its due date cannot be removed). */
  recurrence?: string
  sla?: IssueSLA & { ruleName?: string }
  createdAt: string
  updatedAt: string
  completedAt?: string
  startedAt?: string
  statusChangedAt?: string
  statusIntervals?: { stateId: string; stateType?: string; enteredAt: string; exitedAt?: string }[]
  timeInStatusMinutes?: number
  myActivityAt?: string
  canceledAt?: string
  archivedAt?: string
  parentId?: string
  parent?: { id: string; identifier: string; title: string }
  ancestors?: { id: string; identifier: string; title: string }[]
  subIssueProgress?: { completed: number; total: number }
  subIssues?: MyIssuesRowData[]
  sortOrder?: number
  viewMatch?: boolean
}

export interface MyIssuesGroupData { id: string; label: string; /** The label is generated UI copy, not a user-defined name. */ translatableLabel?: boolean; stateType?: MyIssuesStateType; state?: MyIssuesRowData['state']; createContext?: MyIssuesCreateContext; issues: MyIssuesRowData[]; totalCount?: number; /** Set on sub-groups (display sub-grouping / board rows). */ parentGroupId?: string; parentLabel?: string }

export interface MyIssuesListProps {
  groups: MyIssuesGroupData[]
  loading?: boolean
  error?: string
  selectedIds?: ReadonlySet<string>
  /** Row shown in the split-layout detail pane. */
  activeIssueId?: string
  collapsedGroupIds?: ReadonlySet<string>
  displayProperties?: ReadonlySet<MyIssuesProperty>
  nestedSubIssues?: boolean
  propertyOptions?: MyIssuesRowPropertyOptions
  mutationErrors?: ReadonlyMap<string, string>
  onClearError?: () => void
  onContextAction?: (issue: MyIssuesRowData, action: MyIssuesContextAction) => void
  onCreateIssue?: (group: MyIssuesGroupData) => void
  onGroupCollapsedChange?: (groupId: string, collapsed: boolean) => void
  onOpenIssue?: (issue: MyIssuesRowData) => void
  onPropertyChange?: (issue: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>
  onRetryMutation?: (issue: MyIssuesRowData) => void
  onSelectIssue?: (issueId: string, selected: boolean, range: boolean) => void
  createIssueLabel?: string
  loadingMore?: boolean
  onEndReached?: () => void
  /** Replaces the default empty state. */
  emptyState?: ReactNode
  /** A flat list without group headers (e.g. an Insights selection). */
  hideGroupHeaders?: boolean
}

type MyIssuesListEntry =
  | { key: string; kind: 'group'; group: MyIssuesGroupData; collapsed: boolean }
  | { key: string; kind: 'parent'; label: string; count: number }
  | { key: string; kind: 'issue'; issue: MyIssuesRowData; nestedLines: readonly boolean[]; groupEnd: boolean }

const VIRTUALIZATION_THRESHOLD = 80
type MyIssuesListContext = { loadingMore: boolean }
const MY_ISSUES_VIRTUAL_COMPONENTS: Components<MyIssuesListEntry, MyIssuesListContext> = { Footer: MyIssuesVirtualFooter }

function MyIssuesVirtualFooter({ context }: { context: MyIssuesListContext }) {
  return context.loadingMore ? <LoadingListFooter label="Loading issues"/> : null
}

export function MyIssuesList({ groups, loading = false, error, selectedIds = EMPTY_SET, activeIssueId, collapsedGroupIds = EMPTY_SET, displayProperties = DEFAULT_PROPERTIES, nestedSubIssues=false, propertyOptions = EMPTY_OPTIONS, mutationErrors = EMPTY_ERRORS, onClearError, onContextAction, onCreateIssue, onGroupCollapsedChange, onOpenIssue, onPropertyChange, onRetryMutation, onSelectIssue, createIssueLabel = 'Create new issue', loadingMore = false, onEndReached, emptyState, hideGroupHeaders = false }: MyIssuesListProps) {
  // The reference phone list keeps the sub-issue progress pill on a parent that
  // also nests its children; desktop keeps hiding it to avoid the duplicate.
  const phone = usePhoneLayout()
  const showSubIssueProgress = phone || !nestedSubIssues
  const entries = useMemo<MyIssuesListEntry[]>(() => groups.flatMap((group, index) => {
    const collapsed = collapsedGroupIds.has(group.id)
    const header: MyIssuesListEntry = { key: `group:${group.id}`, kind: 'group', group, collapsed }
    const parent: MyIssuesListEntry[] = group.parentGroupId && groups[index - 1]?.parentGroupId !== group.parentGroupId ? [{ key: `parent:${group.parentGroupId}`, kind: 'parent', label: group.parentLabel ?? '', count: groups.filter(item => item.parentGroupId === group.parentGroupId).reduce((total, item) => total + item.issues.length, 0) }] : []
    if (collapsed) return [...parent, header]
    if (hideGroupHeaders) return group.issues.map((issue, index) => ({ key: `issue:${group.id}:${issue.id}`, kind: 'issue' as const, issue, nestedLines: EMPTY_LINES, groupEnd: index === group.issues.length - 1 }))
    const nestedLines = nestedSubIssues ? nestedLinesByIssue(group.issues) : EMPTY_LINE_MAP
    return [...parent, header, ...group.issues.map((issue, index) => ({ key: `issue:${group.id}:${issue.id}`, kind: 'issue' as const, issue, nestedLines: nestedLines.get(issue.id) ?? EMPTY_LINES, groupEnd: index === group.issues.length - 1 }))]
  }), [collapsedGroupIds, groups, hideGroupHeaders, nestedSubIssues])
  useSelectHoveredRowWithX(entries, selectedIds, onSelectIssue)
  const estimateSettingsFor = useTeamEstimateSettings()
  const groupEstimate = (group: MyIssuesGroupData) => displayProperties.has('estimate') ? sumEstimatePoints(group.issues, estimateSettingsFor) : undefined
  if (loading) return <LoadingContainer loading label="Loading issues"/>
  if (error) return <MyIssuesListError message={error} onRetry={onClearError}/>
  if (!groups.some(group => group.issues.length)) return emptyState ?? <MyIssuesListEmpty/>
  const renderEntry = (entry: MyIssuesListEntry) => entry.kind === 'parent'
    ? <MyIssuesParentGroupHeader label={entry.label} count={entry.count}/>
    : entry.kind === 'group'
    ? <div style={{ paddingBottom: !entry.collapsed && !entry.group.issues.length ? 4 : 2 }}><MyIssuesGroupHeader collapsed={entry.collapsed} createIssueLabel={createIssueLabel} group={entry.group} estimateSum={groupEstimate(entry.group)} onCreateIssue={onCreateIssue} onGroupCollapsedChange={onGroupCollapsedChange}/></div>
    : <div style={{ paddingBottom: entry.groupEnd ? 2 : 0 }}><MyIssuesRow issue={entry.issue} active={activeIssueId === entry.issue.id} selected={selectedIds.has(entry.issue.id)} displayProperties={displayProperties} nestedLines={entry.nestedLines} showSubIssueProgress={showSubIssueProgress} propertyOptions={propertyOptions} mutationError={mutationErrors.get(entry.issue.id)} onContextAction={onContextAction} onOpen={onOpenIssue} onPropertyChange={onPropertyChange} onRetryMutation={onRetryMutation} onSelect={onSelectIssue}/></div>
  if (entries.length > VIRTUALIZATION_THRESHOLD) return <Virtuoso
    className={styles.virtualList}
    role="list"
    aria-label="Issues"
    data={entries}
    computeItemKey={(_index, entry) => entry.key}
    components={MY_ISSUES_VIRTUAL_COMPONENTS}
    context={{ loadingMore }}
    increaseViewportBy={{ top: 176, bottom: 440 }}
    itemContent={(_index, entry) => renderEntry(entry)}
    endReached={() => onEndReached?.()}
    style={{ '--issue-identifier-width': 'max-content' } as CSSProperties}
  />
  return <div className={styles.list} role="list" aria-label="Issues" style={{ '--issue-identifier-width': 'max-content' } as CSSProperties}>
    {groups.map((group, index) => {
      const collapsed = collapsedGroupIds.has(group.id)
      const nestedLines = nestedSubIssues ? nestedLinesByIssue(group.issues) : EMPTY_LINE_MAP
      const parentHeader = group.parentGroupId && groups[index - 1]?.parentGroupId !== group.parentGroupId
      return <section className={styles.group} key={group.id} aria-labelledby={hideGroupHeaders ? undefined : `my-issues-group-${group.id}`} data-subgroup={group.parentGroupId ? true : undefined}>
        {parentHeader && <MyIssuesParentGroupHeader label={group.parentLabel ?? ''} count={groups.filter(item => item.parentGroupId === group.parentGroupId).reduce((total, item) => total + item.issues.length, 0)}/>}
        {!hideGroupHeaders && <MyIssuesGroupHeader collapsed={collapsed} createIssueLabel={createIssueLabel} group={group} estimateSum={groupEstimate(group)} onCreateIssue={onCreateIssue} onGroupCollapsedChange={onGroupCollapsedChange}/>}
        {!collapsed && <div>{group.issues.map(issue => <MyIssuesRow key={issue.id} issue={issue} active={activeIssueId === issue.id} selected={selectedIds.has(issue.id)} displayProperties={displayProperties} nestedLines={nestedLines.get(issue.id)??EMPTY_LINES} showSubIssueProgress={showSubIssueProgress} propertyOptions={propertyOptions} mutationError={mutationErrors.get(issue.id)} onContextAction={onContextAction} onOpen={onOpenIssue} onPropertyChange={onPropertyChange} onRetryMutation={onRetryMutation} onSelect={onSelectIssue}/>)}</div>}
      </section>
    })}
  </div>
}

/** Top-level header when a sub-grouping is active (the reference app renders the primary group above its sub-groups). */
function MyIssuesParentGroupHeader({ label, count }: { label: string; count: number }) {
  return <header className={styles.parentGroupHeader}><span data-i18n-ignore className={styles.groupName}>{label}</span><span className={styles.groupCount}>{count}</span></header>
}

/** `estimateSum`: the group's estimate total ("Todo 2 △ 8"), shown when the issues' team uses estimates. */
export function MyIssuesGroupHeader({ collapsed, createIssueLabel, group, estimateSum, onCreateIssue, onGroupCollapsedChange }: { collapsed: boolean; createIssueLabel: string; group: MyIssuesGroupData; estimateSum?: number; onCreateIssue?: (group: MyIssuesGroupData) => void; onGroupCollapsedChange?: (groupId: string, collapsed: boolean) => void }) {
  const translate = useOptionalTranslate()
  const label = group.translatableLabel ? translate(group.label) : group.label
  return <header className={styles.groupHeader} data-subgroup={group.parentGroupId ? true : undefined} data-my-issues-group-header>
    <button className={styles.collapseButton} aria-label={collapsed ? 'Expand group' : 'Collapse group'} aria-expanded={!collapsed} onClick={() => onGroupCollapsedChange?.(group.id, !collapsed)}><ChevronDown className={styles.groupChevron} size={12}/><svg className={styles.groupCaret} width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><path d="M6.75194 10.6239C6.41861 10.8183 6 10.5779 6 10.192V5.80802C6 5.42212 6.41861 5.18169 6.75194 5.37613L10.5096 7.56811C10.8404 7.76105 10.8404 8.23895 10.5096 8.43189L6.75194 10.6239Z" fill="currentColor"/></svg></button>
    <GroupStateIcon state={group.state ?? group.issues[0]?.state} type={group.stateType}/><span data-i18n-ignore={group.translatableLabel ? undefined : true} id={`my-issues-group-${group.id}`} className={styles.groupName} data-my-issues-group-title>{label}</span><span className={styles.groupCount}>{group.totalCount ?? group.issues.length}</span>
    {estimateSum !== undefined && <span className={`estimate-chip ${styles.groupEstimate}`} aria-label={`${estimateSum} estimate points`}><EstimateIcon size={14}/><span className="estimate-chip__value">{formatEstimatePoints(estimateSum)}</span></span>}
    {onCreateIssue && (group.createContext || group.stateType) && <button className={styles.createButton} aria-label={createIssueLabel} data-my-issues-group-add onClick={() => onCreateIssue(group)}><PlusIcon width={14} height={14}/></button>}
  </header>
}

/** The reference app's X: toggle selection of the row under the pointer (or the focused row); Shift+X extends a range. */
function useSelectHoveredRowWithX(entries: MyIssuesListEntry[], selectedIds: ReadonlySet<string>, onSelect: MyIssuesListProps['onSelectIssue']) {
  const latest = useRef({ entries, selectedIds, onSelect })
  useInsertionEffect(() => { latest.current = { entries, selectedIds, onSelect } })
  useEffect(() => {
    const keydown = (event: globalThis.KeyboardEvent) => {
      if (event.key.toLowerCase() !== 'x' || event.metaKey || event.ctrlKey || event.altKey || event.defaultPrevented) return
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, select, [contenteditable="true"], [role="dialog"], [role="menu"]')) return
      const { entries, selectedIds, onSelect } = latest.current
      if (!onSelect) return
      const row = (document.activeElement as HTMLElement | null)?.closest<HTMLElement>('[data-issue-row-id]') ?? document.querySelector<HTMLElement>('[data-issue-row-id]:hover')
      const id = row?.dataset.issueRowId
      if (!id || !entries.some(entry => entry.kind === 'issue' && entry.issue.id === id)) return
      event.preventDefault()
      onSelect(id, !selectedIds.has(id), event.shiftKey)
    }
    document.addEventListener('keydown', keydown)
    return () => document.removeEventListener('keydown', keydown)
  }, [])
}

/** The reference phone "No priority" glyph: three short bars (the sprite glyph is used at other widths). */
function PhoneNoPriorityIcon() {
  const { t } = useI18n()
  return <svg className="priority priority-0" width={16} height={16} viewBox="0 0 16 16" fill="currentColor" role="img" aria-label={t('No Priority')}>
    <rect x="1.5" y="7.25" width="3" height="1.5" rx="0.5" opacity="0.9"/>
    <rect x="6.5" y="7.25" width="3" height="1.5" rx="0.5" opacity="0.9"/>
    <rect x="11.5" y="7.25" width="3" height="1.5" rx="0.5" opacity="0.9"/>
  </svg>
}
function MyIssuesRowView({ issue, active = false, selected = false, displayProperties = DEFAULT_PROPERTIES, nestedLines=EMPTY_LINES, showSubIssueProgress=true, propertyOptions = EMPTY_OPTIONS, mutationError, onContextAction, onOpen, onPropertyChange, onRetryMutation, onSelect }: {
  issue: MyIssuesRowData; active?: boolean; selected?: boolean; displayProperties?: ReadonlySet<MyIssuesProperty>; nestedLines?:readonly boolean[]; showSubIssueProgress?:boolean; propertyOptions?: MyIssuesRowPropertyOptions; mutationError?: string
  onContextAction?: (issue: MyIssuesRowData, action: MyIssuesContextAction) => void; onOpen?: (issue: MyIssuesRowData) => void
  onPropertyChange?: (issue: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>; onRetryMutation?: (issue: MyIssuesRowData) => void
  onSelect?: (issueId: string, selected: boolean, range: boolean) => void
}) {
  const nestedDepth=nestedLines.length
  const phone = usePhoneLayout()
  const t = useOptionalTranslate()
  const sessionLabel = phone && issue.delegate && issue.agentSessionState
    ? t(CODING_STATE_LABELS[issue.agentSessionState as keyof typeof CODING_STATE_LABELS] ?? issue.agentSessionState)
    : undefined
  const estimateSettingsFor = useTeamEstimateSettings()
  const hasEstimateContext = Boolean(useIssueRowActions())
  const estimateSettings = estimateSettingsFor(issue.teamId)
  // The chip shows only for teams that use estimates (any estimate outside an issue page provider).
  const showEstimate = displayProperties.has('estimate') && issue.estimate != null && issue.estimate >= 0 && (hasEstimateContext ? teamUsesEstimates(estimateSettings) : true)
  const open = () => onOpen?.(issue)
  const keydown = (event: KeyboardEvent<HTMLAnchorElement>) => { if (event.target === event.currentTarget && event.key === ' ') { event.preventDefault(); open() } }
  const click = (event: MouseEvent<HTMLAnchorElement>) => {
    if ((event.target as Element).closest('button,input,[role="checkbox"]')) { event.preventDefault(); return }
    if (onOpen && !event.metaKey && !event.ctrlKey && !event.shiftKey && event.button === 0) { event.preventDefault(); open() }
  }
  const columns = ['8px', 'var(--row-lead-column)', displayProperties.has('priority') && '16px', displayProperties.has('id') && 'var(--issue-identifier-width, 52px)', displayProperties.has('status') && '22px', 'minmax(80px,1fr)', displayProperties.has('created') && '60px', displayProperties.has('updated') && '60px', displayProperties.has('myActivity') && '60px', displayProperties.has('timeInStatus') && '72px', displayProperties.has('links') && '52px', '18px'].filter(Boolean).join(' ')
  const change = (property: MyIssuesEditableProperty, value: string | string[]) => onPropertyChange?.(issue, property, value)
  const statusOptions = useTeamStatusOptions(propertyOptions.status, issue.teamId)
  return <ContextMenu.Root>
    <ContextMenu.Trigger asChild>
      <div data-issue-row-container>
      <a className={styles.row} style={{'--row-columns':columns,'--nested-depth':nestedDepth} as CSSProperties} data-issue-row-id={issue.id} data-selected={selected} data-active={active || undefined} aria-current={active || undefined} data-nested={nestedDepth>0} data-context={issue.viewMatch === false || undefined} href={issue.href} aria-label={rowAriaLabel(issue)} onClick={click} onKeyDown={keydown}>
        {nestedDepth>0&&<NestedIssueGuide lines={nestedLines}/>}
        <span aria-hidden="true"/><IssueCheckbox checked={selected} onChange={(checked, range) => onSelect?.(issue.id, checked, range)}/>
        {displayProperties.has('priority') && <RowCommandPicker
          propertyLabel="Priority"
          label={`${priorityName(issue.priority)} Priority`}
          searchLabel="Change priority to..."
          selectedIds={[String(issue.priority)]}
          options={propertyOptions.priority}
          onSelect={value => change('priority', value)}
          triggerClassName={`${styles.propertyTrigger} ${styles.priorityTrigger}`}
          trigger={phone && issue.priority === 0 ? <PhoneNoPriorityIcon/> : <PriorityIcon priority={issue.priority} size={16}/>}
        />}
        {displayProperties.has('id') && <span className={styles.identifier}>{issue.identifier}</span>}
        {displayProperties.has('status') && <RowCommandPicker
          propertyLabel="Status"
          label={`Change status. Current status is ${issue.state.name}`}
          searchLabel="Change status..."
          searchShortcut="S"
          selectedIds={[issue.state.id]}
          options={statusOptions}
          hoverContent={<RowStatusHover issue={issue}/>}
          hoverClassName="property-rich-hover"
          hoverPlacement="below"
          triggerClassName={`${styles.propertyTrigger} ${styles.statusTrigger}`}
          onSelect={value => change('status', value)}
          trigger={<StatusIcon state={{ id: issue.state.id, name: issue.state.name, type: issue.state.type, color: issue.state.color }} size={14}/>}
        />}
        <div className={styles.titleProperties}>
          <span className={styles.title} data-row-title data-i18n-ignore>{issue.title}</span>
          {!nestedDepth&&issue.parent&&<IssueParentTrail ancestors={issue.ancestors??[issue.parent]}/>}
          {showSubIssueProgress&&issue.subIssueProgress&&issue.subIssues?.length?<SubIssueProgress
            progress={issue.subIssueProgress}
            subIssues={issue.subIssues}
            onOpenSubIssue={onOpen}
          />:null}
          <span className={styles.badges}>
            {displayProperties.has('labels') && issue.labels?.length ? <RowCommandPicker propertyLabel="Labels" kind="labels" multi label={`Change labels. ${issue.labels.map(label => label.name).join(', ')} selected`} searchLabel="Change or add labels..." selectedIds={issue.labels.map(label => label.id)} options={propertyOptions.labels} onSelect={value => change('labels', toggleGroupedLabelIds(issue.labels?.map(label => label.id) ?? [], value, propertyOptions.labels))} triggerClassName={styles.labelsTrigger} trigger={<span className={styles.badgeGroup}>{issue.labels.map(label => <span key={label.id} className={styles.badgeSlot}><PropertyBadge label={label}/></span>)}</span>}/> : null}
        {displayProperties.has('project') && issue.project ? <RowCommandPicker propertyLabel="Project" kind="project" label={`Change project. Current project is ${issue.project.name}`} searchLabel="Set project..." selectedIds={[issue.project.id]} options={propertyOptions.project} onSelect={value => change('project', value)} triggerClassName={`${styles.propertyTrigger} ${styles.projectTrigger}`} trigger={<PropertyBadge color={issue.project.color}>{issue.project.name}</PropertyBadge>}/> : null}
            {displayProperties.has('cycle') && issue.cycleId ? <RowCommandPicker propertyLabel="Cycle" label={`Change cycle. Current cycle is ${issue.cycleName ?? issue.cycleId}`} searchLabel="Add to cycle..." selectedIds={[issue.cycleId]} options={propertyOptions.cycle ?? []} onSelect={value => change('cycle', value)} trigger={<span className={styles.dueDate}><CycleIcon size={13}/><span data-i18n-ignore>{issue.cycleName ?? issue.cycleId}</span></span>}/> : null}
            {displayProperties.has('release') && issue.releases?.length ? <IssueReleaseChip className={styles.dueDate} releases={issue.releases}/> : null}
            {displayProperties.has('milestone') && issue.milestoneName ? <span className={styles.dueDate} aria-label={`Milestone ${issue.milestoneName}`}><MilestoneProgressIcon overdue={isMilestoneDateOverdue(issue.rawMilestoneDate)} progress={issue.milestoneProgress ?? 0} size={13} /><span data-i18n-ignore>{issue.milestoneName}</span></span> : null}
            {displayProperties.has('customers') && (issue.customerCount ?? issue.customerIds?.length ?? 0) > 0 ? <CustomersRowChip customers={issue.customers ?? []} customerCount={issue.customerCount ?? issue.customerIds?.length ?? 0} importantCustomerIds={issue.importantCustomerIds ?? []} settings={issue.customerRevenueSettings}/> : null}
            {displayProperties.has('customerRevenue') && issue.customers?.length ? <CustomerRevenueRowChip customers={issue.customers} settings={issue.customerRevenueSettings}/> : null}
            {displayProperties.has('dueDate') && issue.dueDate ? <DueDatePicker value={issue.dueDate} allowRemove={!issue.recurrence} onChange={value => change('dueDate', value)} ariaLabel={`Change due date. Current due date is ${formatDueDate(issue.dueDate)}`} triggerClassName={styles.propertyTrigger} trigger={<time className={styles.dueDate} dateTime={issue.dueDate}><CalendarIcon size={13}/>{formatDueDate(issue.dueDate)}</time>}/> : null}
            {displayProperties.has('pullRequests') || issue.blockedByCount || issue.blockingCount ? <IssueWidgetAdornments
              showPullRequests={displayProperties.has('pullRequests')}
              pullRequestLifecycle={issue.pullRequestLifecycle}
              pullRequestCount={issue.pullRequestCount ?? 0}
              blockedByCount={issue.blockedByCount ?? 0}
              blockingCount={issue.blockingCount ?? 0}
              blockedByIds={issue.blockedByIds}
              blockingIds={issue.blockingIds}
            /> : null}
            {displayProperties.has('sla') && issue.sla && <IssueSLAIndicator compact sla={issue.sla} ruleName={issue.sla.ruleName}/>}
            {showEstimate && <EstimateChip className={styles.estimateChip} value={issue.estimate!} settings={estimateSettings} label={`Estimate ${issue.estimate}`}/>}
            {displayProperties.has('assignee') ? <IssueAssigneeSlot issue={issue} options={propertyOptions.assignee} onChange={value => change('assignee', value)} avatarClassName={styles.avatar} emptyClassName={styles.noAssignee} sessionLabel={sessionLabel}/> : null}
            {mutationError && <button type="button" className={styles.rowError} title={mutationError} onClick={() => onRetryMutation?.(issue)}>Retry</button>}
          </span>
        </div>
        {displayProperties.has('created') && <time className={styles.rowDate} aria-label={`Created ${formatFullDate(issue.createdAt)}`} dateTime={issue.createdAt}>{formatRowDate(issue.createdAt)}</time>}
        {displayProperties.has('updated') && <time className={styles.rowDate} aria-label={`Updated ${formatFullDate(issue.updatedAt)}`} dateTime={issue.updatedAt}>{formatRowDate(issue.updatedAt)}</time>}
        {displayProperties.has('myActivity') && <time className={styles.rowDate} aria-label={issue.myActivityAt ? `My activity ${formatFullDate(issue.myActivityAt)}` : 'No activity'} dateTime={issue.myActivityAt}>{issue.myActivityAt ? formatRowDate(issue.myActivityAt) : null}</time>}
        {displayProperties.has('timeInStatus') && <time className={styles.rowDate} aria-label={issue.timeInStatusMinutes == null ? 'Time in status unavailable' : `${formatTimeInStatus(issue.timeInStatusMinutes)} in status`}>{issue.timeInStatusMinutes == null ? null : <><Clock3 size={12}/>{formatTimeInStatus(issue.timeInStatusMinutes)}</>}</time>}
        {displayProperties.has('links') && <span className={styles.rowDate} aria-label={issue.linkCount ? `${issue.linkCount} links` : 'No links'}>{issue.linkCount ? <><Link2 size={12}/>{issue.linkCount}</> : null}</span>}
        <span aria-hidden="true"/>
      </a>
      </div>
    </ContextMenu.Trigger>
    <ContextMenu.Portal><IssueContextMenu editable={Boolean(onPropertyChange)} issue={issue} options={propertyOptions} onPropertyChange={change} onAction={onContextAction?action=>onContextAction(issue,action):undefined}/></ContextMenu.Portal>
  </ContextMenu.Root>
}

/** A row is a pure function of its props: skip re-rendering the mounted rows when the list around them re-renders. */
export const MyIssuesRow = memo(MyIssuesRowView)

const EMPTY_LINES:readonly boolean[]=[]
const EMPTY_LINE_MAP=new Map<string,readonly boolean[]>()
function NestedIssueGuide({lines}:{lines:readonly boolean[]}){return <span aria-hidden="true" className={styles.nestingGuide}>{lines.map((last,level)=>{const current=level===lines.length-1;if(!current&&last)return null;return <span className={`${styles.nestingSegment}${current?` ${styles.nestingCurrent}`:''}${current&&last?` ${styles.nestingLast}`:''}`} key={level} style={{'--nest-level':level} as CSSProperties}>{current&&<svg className={styles.nestingBranch} viewBox="0 0 10 9"><path d="M0 0h1v1c0 2.5 2.212 3.546 2.212 3.546L9.737 8.06c.568.306.094 1.186-.474.88l-6.48-3.488S0 4 0 1V0Z"/></svg>}</span>})}</span>}

export function IssueParentTrail({ancestors,board=false}:{ancestors:NonNullable<MyIssuesRowData['ancestors']>;board?:boolean}){if(!ancestors.length)return null;const shown=board?ancestors:ancestors.slice(0,1);return <span className={`${styles.parentTrail}${board?` ${styles.boardParentTrail}`:''}`} data-i18n-ignore>{shown.map(parent=><span key={parent.id}><b>›</b>{parent.title}</span>)}</span>}

export function SubIssueProgress({progress,subIssues,onOpenSubIssue}:{progress:NonNullable<MyIssuesRowData['subIssueProgress']>;subIssues:MyIssuesRowData[];onOpenSubIssue?:(issue:MyIssuesRowData)=>void}){
  const [open,setOpen]=useState(false),closeTimer=useRef<number|undefined>(undefined)
  const enter=()=>{if(closeTimer.current)window.clearTimeout(closeTimer.current);setOpen(true)},leave=()=>{closeTimer.current=window.setTimeout(()=>setOpen(false),100)}
  return <Popover.Root open={open} onOpenChange={setOpen}><Popover.Trigger asChild><button type="button" className={styles.subIssueProgress} aria-label={`${progress.completed} of ${progress.total} sub-issues completed`} onPointerEnter={enter} onPointerLeave={leave} onFocus={enter} onBlur={leave} onClick={event=>{event.preventDefault();event.stopPropagation();setOpen(value=>!value)}}><SubIssueProgressRing completed={progress.completed} total={progress.total}/><span>{progress.completed}/{progress.total}</span></button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" className={styles.subIssuePopover} side="top" align="start" sideOffset={4} collisionPadding={8} onOpenAutoFocus={event=>event.preventDefault()} onCloseAutoFocus={event=>event.preventDefault()} onPointerEnter={enter} onPointerLeave={leave}>{subIssues.map(item=><button type="button" className={styles.subIssuePopoverRow} key={item.id} onClick={event=>{event.preventDefault();event.stopPropagation();setOpen(false);onOpenSubIssue?.(item)}}><StatusIcon state={{id:item.state.id,name:item.state.name,type:item.state.type,color:item.state.color}} size={16}/><span data-i18n-ignore>{item.title}</span></button>)}</Popover.Content></Popover.Portal></Popover.Root>
}

const statusHistoryCache = new Map<string, { stateId: string; activities: ActivityEvent[] }>()

/** Status hover for list rows: the issue's history is loaded when the card first opens. */
function RowStatusHover({ issue }: { issue: MyIssuesRowData }) {
  const app = useContext(ApplicationStoreContext)
  const cached = statusHistoryCache.get(issue.id)
  const fresh = cached?.stateId === issue.state.id ? cached : undefined
  const [activities, setActivities] = useState<ActivityEvent[] | undefined>(fresh?.activities)
  useEffect(() => {
    if (fresh) return
    const controller = new AbortController()
    void fetchIssueHistory(issue.id, controller.signal)
      .then(page => { statusHistoryCache.set(issue.id, { stateId: issue.state.id, activities: page.activities }); setActivities(page.activities) })
      .catch(() => { if (!controller.signal.aborted) setActivities([]) })
    return () => controller.abort()
  }, [fresh, issue.id, issue.state.id])
  const states = app?.data && issue.teamId ? teamWorkflowStates(app.data, issue.teamId) : app?.data?.states
  return <StatusHoverPreview state={issue.state} activities={activities ?? []} loading={!activities} issueCreatedAt={issue.createdAt} states={states} triagedAt={issue.triagedAt} inTriage={issue.state.id === 'triage'}/>
}

const ASSIGNEE_INVITE_OPTION: MyIssuesContextOption = { id: ASSIGNEE_INVITE_ID, label: 'Invite and assign…', groupLabel: 'New user', kind: 'assignee' }

export function RowCommandPicker({ propertyLabel, label, multi = false, onSelect, options, searchLabel, searchShortcut, selectedIds, trigger, triggerClassName, kind = 'standard', hoverContent, hoverClassName, hoverPlacement, open, onOpenChange }: { propertyLabel: string; label: string; multi?: boolean; onSelect: (id: string) => void | Promise<void>; options: MyIssuesContextOption[]; searchLabel: string; searchShortcut?: string; selectedIds: string[]; trigger: ReactNode; triggerClassName?: string; kind?: PropertyMenuKind; hoverContent?: ReactNode; hoverClassName?: string; hoverPlacement?: 'left' | 'below'; open?: boolean; onOpenChange?: (open: boolean) => void }) {
  const assignee = propertyLabel === 'Assignee'
  // Assignee pickers: number keys on "No assignee" (0) and the rows after it, agents without a pill but with a hover card, and an invite action under "New user".
  const rows = assignee && viewerCanInvite() ? [...options, ASSIGNEE_INVITE_OPTION] : options
  const commandOptions = rows.map((option, index) => ({ ...option, icon: option.icon ?? <MyIssuesOptionIcon option={option}/>, ...(option.id === ASSIGNEE_INVITE_ID ? { action: true } : option.agent ? agentHoverProps({ id: option.id, name: option.handle ?? option.label, displayName: option.label, avatarUrl: option.avatarUrl }) : {}), shortcut: option.kind === 'priority' ? option.id : option.kind === 'status' ? String(index + 1) : assignee && option.id !== ASSIGNEE_INVITE_ID ? assigneeShortcut(index) : option.shortcut }))
  return <PropertyMenu label={propertyLabel} value={options.find(option => selectedIds.includes(option.id))?.label} multiple={multi} selectedId={selectedIds[0]} selectedIds={selectedIds} options={commandOptions} kind={kind} searchPlaceholder={searchLabel} searchShortcut={searchShortcut ?? (assignee ? 'A' : undefined)} ariaLabel={label} triggerClassName={triggerClassName ?? styles.propertyTrigger} trigger={trigger} hoverContent={hoverContent} hoverClassName={hoverClassName} hoverPlacement={hoverPlacement} open={open} onOpenChange={onOpenChange} onChange={id => id === ASSIGNEE_INVITE_ID ? openMemberInvite() : onSelect(id)}/>
}

function IssueCheckbox({ checked, onChange }: { checked: boolean; onChange: (checked: boolean, range: boolean) => void }) {
  const click = (event: MouseEvent<HTMLButtonElement>) => { onChange(!checked, event.shiftKey) }
  return <span className={styles.checkboxCell}><button type="button" className={styles.checkbox} role="checkbox" aria-label="Select issue" aria-checked={checked} data-checked={checked} onClick={click}>{checked && <CheckboxMark/>}</button></span>
}

export function IssueContextMenu({ editable, issue, options, onPropertyChange, onAction }: { editable:boolean;issue:MyIssuesRowData;options: MyIssuesRowPropertyOptions; onPropertyChange: (property: MyIssuesEditableProperty, value: string | string[]) => void | Promise<void>; onAction?: (action: MyIssuesContextAction) => void }) {
  const rowActions = useIssueRowActions()
  const statusOptions = useTeamStatusOptions(options.status, issue.teamId)
  return <ContextMenu.Content data-flow-motion="floating" className={styles.contextMenu} collisionPadding={10}>
    {editable&&<><ContextPropertySub label="Status" shortcut="S" options={statusOptions} selectedIds={[issue.state.id]} onSelect={id => onPropertyChange('status', id)}/>
    <ContextPropertySub label="Priority" shortcut="P" options={options.priority} selectedIds={[String(issue.priority)]} onSelect={id => onPropertyChange('priority', id)}/>
    <ContextPropertySub label="Assignee" shortcut="A" options={options.assignee} selectedIds={[issue.assignee?.id ?? '']} onSelect={id => onPropertyChange('assignee', id)}/>
    <ContextPropertySub label="Due date" shortcut="⇧ D" options={options.dueDate} selectedIds={[issue.dueDate ?? '']} onSelect={id => onPropertyChange('dueDate', id)}/>
    <ContextPropertySub multi label="Labels" shortcut="L" options={options.labels} selectedIds={issue.labels?.map(label => label.id) ?? []} onSelect={id => onPropertyChange('labels', toggleGroupedLabelIds(issue.labels?.map(label => label.id) ?? [], id, options.labels))}/>
    <ContextPropertySub label="Project" shortcut="⇧ P" options={options.project} selectedIds={[issue.project?.id ?? '']} onSelect={id => onPropertyChange('project', id)}/>
    {editable && <IssueRowEstimateItem row={issue}/>}
    {(options.cycle ?? []).some(option => option.id) && <ContextPropertySub label="Cycle" shortcut="⇧ C" options={options.cycle??[]} selectedIds={[issue.cycleId ?? '']} onSelect={id => onPropertyChange('cycle', id)}/>}
    <IssueRowMoreProperties row={issue}/></>}
    {editable && rowActions && <IssueRowActionGroups row={issue} onDelete={onAction ? () => onAction('delete') : undefined}/>}
    {onAction&&!(editable&&rowActions)&&<>{editable&&<ContextMenu.Separator className={styles.menuSeparator}/>}<ContextMenu.Sub><ContextMenu.SubTrigger className={styles.menuItem}><ContextMenuIcon label="Copy"/><span>Copy</span><span className={styles.menuChevron} aria-hidden="true">▶</span></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent data-flow-motion="floating" className={styles.contextSubmenu} sideOffset={3} alignOffset={-5}><MyIssuesMenuItem action="copyUrl" label="Copy issue URL" shortcut="⌘ ⇧ ," onAction={onAction} submenu={false}/><MyIssuesMenuItem action="copyId" label="Copy issue ID" onAction={onAction} submenu={false}/><MyIssuesMenuItem action="copyTitle" label="Copy issue title" onAction={onAction} submenu={false}/></ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub><ContextMenu.Separator className={styles.menuSeparator}/><MyIssuesMenuItem action="delete" label="Delete" shortcut="⌘ ⌫" danger onAction={onAction} submenu={false}/></>}
  </ContextMenu.Content>
}

function ContextPropertySub({ label, multi = false, onSelect, options, selectedIds, shortcut }: { label: string; multi?: boolean; onSelect: (id: string) => void | Promise<void>; options: MyIssuesContextOption[]; selectedIds: string[]; shortcut?: string }) {
  const [open, setOpen] = useState(false)
  const people = isPeopleProperty(label)
  const assignee = label === 'Assignee'
  // Assignee: number keys on "No assignee" (0) and the rows after it, no "Agent" pill, then "New user / Invite and assign…".
  const items = assignee ? options.map((option, index) => ({ ...option, shortcut: assigneeShortcut(index) })) : options
  const choose = (id: string) => id === ASSIGNEE_INVITE_ID ? openMemberInvite() : onSelect(id)
  const { inputRef: commandInputRef, ...command } = usePropertyCommand({ open: open && people, options: items, personOptions: people, selectedIds, onSelect: option => choose(option.id), onOpenChange: setOpen })
  const selected = new Set(selectedIds)
  const sections = multi && label === 'Labels' ? groupContextOptions(items) : [{ id: 'all', label: undefined as string | undefined, options: people ? command.filteredOptions : items }, ...(assignee && !command.query && viewerCanInvite() ? [{ id: 'invite', label: 'New user', options: [ASSIGNEE_INVITE_OPTION] }] : [])]
  const row = (option: MyIssuesContextOption) => {
    const content = multi
      ? <ContextMenu.CheckboxItem className={styles.submenuItem} checked={selected.has(option.id)} onSelect={event => event.preventDefault()} onCheckedChange={() => void choose(option.id)}><span className={styles.optionCheckbox}>{selected.has(option.id) && <CheckboxMark/>}</span><MyIssuesOptionIcon option={option}/><span>{option.label}</span></ContextMenu.CheckboxItem>
      : <ContextMenu.Item className={styles.submenuItem} onSelect={() => void choose(option.id)}><MyIssuesOptionIcon option={option}/><span>{option.label}</span>{selected.has(option.id) && <Check className={styles.optionCheck} size={13}/>}{option.shortcut && <kbd>{option.shortcut}</kbd>}</ContextMenu.Item>
    if (option.agent) return <AgentHover key={option.id} agent={{ id: option.id, name: option.handle ?? option.label, displayName: option.label, avatarUrl: option.avatarUrl }}>{content}</AgentHover>
    return <PersonHover key={option.id || 'none'} userId={people && option.id !== ASSIGNEE_INVITE_ID ? option.id : undefined}>{content}</PersonHover>
  }
  return <ContextMenu.Sub open={open} onOpenChange={setOpen}><ContextMenu.SubTrigger className={styles.menuItem}><ContextMenuIcon label={label}/><span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}<span className={styles.menuChevron} aria-hidden="true">▶</span></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent data-flow-motion="floating" className={styles.contextSubmenu} sideOffset={3} alignOffset={-5}
    onKeyDown={assignee ? event => { if (!event.metaKey && !event.ctrlKey && !event.altKey && /^\d$/.test(event.key)) { const match = items.find(option => option.shortcut === event.key); if (match) { event.preventDefault(); setOpen(false); void choose(match.id) } } } : undefined}>
    {people && <div className="property-command-search"><input ref={commandInputRef} aria-label={assignee ? 'Assign to…' : 'Search people'} placeholder={assignee ? 'Assign to…' : 'Search people…'} value={command.query} onChange={event => command.onQueryChange(event.target.value)} onKeyDown={event => { event.stopPropagation(); command.onKeyDown(event) }}/></div>}
    {sections.map(section => <ContextMenu.Group key={section.id}>{section.label && <ContextMenu.Label className={styles.groupLabel}>{section.label}</ContextMenu.Label>}{section.options.map(row)}</ContextMenu.Group>)}
  </ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>
}

function MyIssuesMenuItem({ action, danger, label, onAction, shortcut, submenu = true }: { action: MyIssuesContextAction; danger?: boolean; label: string; onAction: (action: MyIssuesContextAction) => void; shortcut?: string; submenu?: boolean }) {
  return <ContextMenu.Item className={styles.menuItem} data-danger={danger} onSelect={() => onAction(action)}><ContextMenuIcon label={label}/><span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}{submenu && <span className={styles.menuChevron} aria-hidden="true">▶</span>}</ContextMenu.Item>
}

function MyIssuesOptionIcon({ option }: { option: MyIssuesContextOption }) {
  if (option.kind === 'priority' && option.priority !== undefined) return <PriorityIcon priority={option.priority}/>
  if (option.kind === 'status' && option.stateType && option.color) return <StatusIcon state={{ id: option.id, name: option.label, type: option.stateType, color: option.color }} size={14}/>
  if (option.id === ASSIGNEE_INVITE_ID) return <FlowGlyph className="property-command-invite-icon" name="invitePlane" size={16}/>
  if (option.kind === 'assignee' && option.id) return <UserAvatar avatarUrl={option.avatarUrl} className={`core-person-picker-avatar ${styles.optionAvatar}`} color={avatarColor(option.id)} name={option.label}/>
  if (option.avatarUrl) return <AvatarImage className={styles.optionAvatar} src={option.avatarUrl} alt=""/>
  if (option.kind === 'project' && option.id) return <ProjectIcon className={styles.optionIcon} size={14} style={{ color: option.color }}/>
  if (option.color) return <i className={styles.optionColor} style={{ backgroundColor: option.color }}/>
  if (option.kind === 'assignee') return <NoAssigneeIcon className={styles.optionIcon} size={14}/>
  if (option.kind === 'project') return <NoProjectIcon className={styles.optionIcon} size={14}/>
  if (option.kind === 'dueDate') return <CalendarIcon className={styles.optionIcon} size={14}/>
  if (option.kind === 'cycle') return <CycleIcon noCycle={!option.id} className={styles.optionIcon} size={14}/>
  if (option.kind === 'labels') return <LabelIcon className={styles.optionIcon} size={14}/>
  return <span className={styles.optionSpacer}/>
}
function GroupStateIcon({ state, type }: { state?: MyIssuesRowData['state']; type?: MyIssuesStateType }) { if (state?.id === 'triage') return <span className={styles.groupState}><WorkflowStatusGlyph state={state} size={14}/></span>; return state || type ? <span className={styles.groupState}><StatusIcon state={state ?? { id: type!, name: type!, type: type!, color: 'var(--theme-text-secondary)' }} size={14}/></span> : null }
function PropertyBadge(props:{label:NonNullable<MyIssuesRowData['labels']>[number]}|{children:ReactNode;color:string}){
  if('label'in props){const{label}=props;return <LabelHoverPreview label={label} side="bottom" align="start"><span className={styles.badge}><i style={{backgroundColor:label.color}}/><span data-i18n-ignore>{label.name}</span></span></LabelHoverPreview>}
  return <span className={styles.badge}><i style={{backgroundColor:props.color}}/><span data-i18n-ignore>{props.children}</span></span>
}

export function MyIssuesListEmpty() { return <div className={styles.state} role="status"><span className={styles.emptyIcon}><Check size={15}/></span><strong>No issues</strong><p>No issues match this view.</p></div> }
export function MyIssuesListError({ message, onRetry }: { message: string; onRetry?: () => void }) { return <div className={styles.state} role="alert"><strong>Could not load issues</strong><p>{message}</p>{onRetry && <button type="button" onClick={onRetry}>Try again</button>}</div> }

const EMPTY_SET = new Set<string>()
const EMPTY_ERRORS = new Map<string, string>()
const DEFAULT_PROPERTIES = new Set<MyIssuesProperty>(['id', 'status', 'assignee', 'priority', 'project', 'estimate', 'labels', 'created'])
const EMPTY_OPTIONS: MyIssuesRowPropertyOptions = { status: [], priority: [], assignee: [], dueDate: [], labels: [], project: [], cycle: [] }
function priorityName(priority: number) { return ['No', 'Urgent', 'High', 'Medium', 'Low'][priority] }
function rowAriaLabel(issue: MyIssuesRowData) { return `Select issue ${priorityName(issue.priority)} Priority ${issue.identifier} ${issue.title}` }
// Intl.DateTimeFormat construction is expensive (~0.1ms): build each formatter once, not per row render.
const ROW_DATE_FORMAT = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' })
const FULL_DATE_FORMAT = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' })
function formatRowDate(value: string) { return ROW_DATE_FORMAT.format(new Date(value)) }
function formatFullDate(value: string) { return FULL_DATE_FORMAT.format(new Date(value)) }
function formatDueDate(value: string) { return ROW_DATE_FORMAT.format(new Date(`${value}T00:00:00`)) }
function formatTimeInStatus(minutes: number) {
  if (minutes < 60) return `${Math.max(0, minutes)}m`
  const hours = minutes / 60
  if (hours < 24) return `${Math.round(hours * 10) / 10}h`
  const days = hours / 24
  return `${Math.round(days * 10) / 10}d`
}
function groupContextOptions(options: MyIssuesContextOption[]) {
  const sections: { id: string; label?: string; options: MyIssuesContextOption[] }[] = []
  const indexes = new Map<string, number>()
  for (const option of options) {
    const id = option.groupId || option.groupLabel || 'ungrouped'
    let index = indexes.get(id)
    if (index === undefined) {
      index = sections.length
      indexes.set(id, index)
      sections.push({ id, label: id === 'ungrouped' ? undefined : option.groupLabel, options: [] })
    }
    sections[index].options.push(option)
  }
  return sections
}
