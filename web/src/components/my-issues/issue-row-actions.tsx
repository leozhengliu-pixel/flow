import { createContext, useContext, useMemo, useState, type ReactNode } from 'react'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { ChevronRight } from 'lucide-react'
import { toast } from 'sonner'
import { createIssue, createIssueReminder, createRelation } from '@/lib/api'
import { findFavorite, toggleFavoriteFor } from '@/lib/favorites'
import { usePropertyCommand } from '@/components/property/use-property-command'
import type { BootstrapData, Issue, IssueRelationType, IssueUpdateInput } from '@/types/flow'
import type { MyIssuesRowData } from './my-issues-list'
import styles from './my-issues-list.module.css'
import { ContextMenuIcon } from './context-menu-icon'

/**
 * Host callbacks for the full Linear row context menu (`ContextualMenuActions`). Pages that render
 * issue lists provide this once; every row menu below them gets the same actions.
 */
export interface IssueRowActionContextValue {
  data: BootstrapData
  onUpdateIssue: (issueId: string, input: IssueUpdateInput) => Promise<unknown>
  onDeleteIssues?: (issueIds: string[]) => Promise<void>
  onOpenIssue?: (issue: Issue) => void
}

const IssueRowActionContext = createContext<IssueRowActionContextValue | undefined>(undefined)

export function IssueRowActionsProvider({ value, children }: { value: IssueRowActionContextValue; children: ReactNode }) {
  return <IssueRowActionContext.Provider value={value}>{children}</IssueRowActionContext.Provider>
}

export function useIssueRowActions() { return useContext(IssueRowActionContext) }

const RELATIONS: { type: IssueRelationType; label: string }[] = [
  { type: 'blocked_by', label: 'Blocked by…' },
  { type: 'blocks', label: 'Blocking…' },
  { type: 'related', label: 'Related to…' },
  { type: 'duplicate', label: 'Duplicate of…' },
]

/** Extra row menu items: assign to me, estimate, milestone, parent, relations, team, subscribe, favorite, remind, copy, open, copy issue, archive. */
export function IssueRowExtendedMenuItems({ row }: { row: MyIssuesRowData }) {
  const context = useIssueRowActions()
  if (!context) return null
  const { data } = context
  const issue = data.issues.find(item => item.id === row.id)
  const viewerId = data.viewer.id
  const update = (input: IssueUpdateInput, message?: string) => context.onUpdateIssue(row.id, input).then(() => { if (message) toast.success(message) }).catch(error => toast.error(error instanceof Error ? error.message : 'Could not update issue'))
  const subscribed = Boolean(issue?.subscriberIds.includes(viewerId))
  const favorite = Boolean(findFavorite(data.favorites, viewerId, 'issue', row.id))
  const estimateType = data.teamSettings?.[row.teamId ?? '']?.estimateType ?? 'notUsed'
  const estimates = estimateType === 'fibonacci' ? [0, 1, 2, 3, 5, 8, 13, 21] : estimateType === 'exponential' ? [0, 1, 2, 4, 8, 16] : [0, 1, 2, 3, 5, 8]
  const milestones = row.project ? data.projects.find(project => project.id === row.project!.id)?.milestones ?? [] : []
  const url = `${location.origin}${row.href ?? `/${data.workspace.urlKey}/issue/${row.identifier}`}`
  const copy = (text: string, message: string) => void navigator.clipboard.writeText(text).then(() => toast.success(message))
  const remind = (days: number) => { const at = new Date(); at.setDate(at.getDate() + days); at.setHours(9, 0, 0, 0); void createIssueReminder(row.id, at.toISOString()).then(() => toast.success('Reminder set')).catch(() => toast.error('Could not set reminder')) }
  const makeCopy = () => issue && void createIssue({ title: `${issue.title} (copy)`, description: issue.description, teamId: issue.team.id, stateId: issue.state.id, priority: issue.priority, estimate: issue.estimate, assigneeId: issue.assignee?.id, projectId: issue.project?.id, projectMilestoneId: issue.projectMilestoneId, cycleId: issue.cycleId, dueDate: issue.dueDate, labelIds: issue.labels.map(label => label.id), parentId: issue.parentId })
    .then(created => { toast.success(`Created ${created.identifier}`); context.onOpenIssue?.(created) }).catch(() => toast.error('Could not copy issue'))

  return <>
    <ContextMenu.Separator className={styles.menuSeparator}/>
    {row.assignee?.id !== viewerId && <Item top label="Assign to me" shortcut="I" onSelect={() => void update({ assigneeId: viewerId }, 'Assigned to you')}/>}
    {estimateType !== 'notUsed' && <Sub label="Estimate" shortcut="⇧ E">{estimates.map(value => <Item key={value} label={value ? `${value} point${value === 1 ? '' : 's'}` : 'No estimate'} checked={(row.estimate ?? 0) === value} onSelect={() => void update({ estimate: value })}/>)}</Sub>}
    {milestones.length > 0 && <Sub label="Milestone">{[{ id: '', name: 'No milestone' }, ...milestones].map(milestone => <Item key={milestone.id || 'none'} label={milestone.name} checked={(row.projectMilestoneId ?? '') === milestone.id} onSelect={() => void update({ projectMilestoneId: milestone.id })}/>)}</Sub>}
    <Sub label="Set parent issue…"><IssueSearch data={data} exclude={row.id} onPick={target => void update({ parentId: target.id }, `Sub-issue of ${target.identifier}`)}/>{row.parentId && <Item label="Remove parent" onSelect={() => void update({ parentId: '' })}/>}</Sub>
    <Sub label="Relations">{RELATIONS.map(relation => <Sub key={relation.type} label={relation.label}><IssueSearch data={data} exclude={row.id} onPick={target => void createRelation(row.id, relation.type, target.id).then(() => toast.success(`${relation.label.replace('…', '')} ${target.identifier}`)).catch(() => toast.error('Could not add relation'))}/></Sub>)}</Sub>
    {data.teams.length > 1 && <Sub label="Move to team…" shortcut="⌘ ⇧ M">{data.teams.filter(team => team.id !== row.teamId).map(team => <Item key={team.id} label={team.name} onSelect={() => void update({ teamId: team.id }, `Moved to ${team.name}`)}/>)}</Sub>}
    <ContextMenu.Separator className={styles.menuSeparator}/>
    <Item top label={subscribed ? 'Unsubscribe' : 'Subscribe'} shortcut="⇧ S" onSelect={() => issue && void update({ subscriberIds: subscribed ? issue.subscriberIds.filter(id => id !== viewerId) : [...issue.subscriberIds, viewerId] }, subscribed ? 'Unsubscribed' : 'Subscribed')}/>
    <Item top label={favorite ? 'Remove from favorites' : 'Favorite'} shortcut="⌥ F" onSelect={() => void toggleFavoriteFor(data, 'issue', row.id, !favorite, favorite)}/>
    <Sub label="Remind me" shortcut="H"><Item label="Tomorrow" onSelect={() => remind(1)}/><Item label="In 3 days" onSelect={() => remind(3)}/><Item label="Next week" onSelect={() => remind(7)}/></Sub>
    <ContextMenu.Separator className={styles.menuSeparator}/>
    <Item top label="Copy branch name" shortcut="⌘ ⇧ ." onSelect={() => copy(`${row.identifier.toLowerCase()}-${row.title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 50)}`, 'Branch name copied')}/>
    <Item top label="Copy as Markdown link" onSelect={() => copy(`[${row.identifier} ${row.title}](${url})`, 'Link copied')}/>
    <Item top label="Open in new tab" onSelect={() => { window.open(url, '_blank', 'noopener') }}/>
    <Item top label="Make a copy…" onSelect={() => makeCopy()}/>
    <Item top label="Archive" onSelect={() => void update({ archived: true }, `${row.identifier} archived`)}/>
  </>
}

function Item({ label, shortcut, checked, top = false, onSelect }: { label: string; shortcut?: string; checked?: boolean; top?: boolean; onSelect: () => void }) {
  return <ContextMenu.Item className={top ? styles.menuItem : styles.submenuItem} onSelect={onSelect}>{top && <ContextMenuIcon label={label}/>}<span>{label}</span>{checked ? <span aria-label="Selected">✓</span> : shortcut ? <kbd>{shortcut}</kbd> : null}</ContextMenu.Item>
}

function Sub({ label, shortcut, children }: { label: string; shortcut?: string; children: ReactNode }) {
  return <ContextMenu.Sub><ContextMenu.SubTrigger className={styles.menuItem}><ContextMenuIcon label={label}/><span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}<ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent data-flow-motion="floating" className={styles.contextSubmenu} sideOffset={3} alignOffset={-5}>{children}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>
}

function IssueSearch({ data, exclude, onPick }: { data: BootstrapData; exclude: string; onPick: (issue: Issue) => void }) {
  const [open, setOpen] = useState(true)
  const options = useMemo(() => data.issues.filter(item => item.id !== exclude && !item.archivedAt).map(item => ({ id: item.id, label: `${item.identifier} ${item.title}` })), [data.issues, exclude])
  const command = usePropertyCommand({ open, options, onOpenChange: setOpen, closeOnSelect: false, onSelect: option => { const target = data.issues.find(item => item.id === option.id); if (target) onPick(target) } })
  return <div className="property-command-search" onKeyDown={event => { event.stopPropagation(); command.onKeyDown(event) }}>
    <input ref={command.inputRef} aria-label="Search issues" placeholder="Search issues…" value={command.query} onChange={event => command.onQueryChange(event.target.value)}/>
    {command.filteredOptions.slice(0, 8).map(option => <ContextMenu.Item key={option.id} className={styles.submenuItem} onSelect={() => command.choose(option)}><span data-i18n-ignore>{option.label}</span></ContextMenu.Item>)}
  </div>
}
