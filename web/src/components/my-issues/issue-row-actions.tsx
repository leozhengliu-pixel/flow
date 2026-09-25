import { createContext, useContext, useMemo, useState, type ReactNode } from 'react'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { createIssue, createIssueLink, createIssueReminder, createRelation, deleteRelation } from '@/lib/api'
import { settingsPath } from '@/lib/app-routes'
import { enabledCodingTools, renderCodingPrompt } from '@/lib/coding-tools'
import { findFavorite, toggleFavoriteFor } from '@/lib/favorites'
import { configuredIssueBranch } from '@/lib/issue-work-actions'
import { estimateLabel, estimateValues } from '@/lib/estimates'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { StatusIcon } from '@/components/issue/issue-icons'
import { useIssueCandidates } from '@/components/issue/use-issue-candidates'
import type { BootstrapData, Issue, IssueRelation, IssueRelationType, IssueUpdateInput } from '@/types/flow'
import type { MyIssuesCreateContext, MyIssuesRowData } from './my-issues-list'
import styles from './my-issues-list.module.css'
import { ContextMenuIcon } from './context-menu-icon'

/**
 * Host callbacks for the full row context menu. Pages that render issue lists provide this once;
 * every row menu below them gets the same actions.
 */
export interface IssueRowActionContextValue {
  data: BootstrapData
  onUpdateIssue: (issueId: string, input: IssueUpdateInput) => Promise<unknown>
  onDeleteIssues?: (issueIds: string[]) => Promise<void>
  onOpenIssue?: (issue: Issue) => void
  onCreateIssue?: (context: MyIssuesCreateContext) => void
}

const IssueRowActionContext = createContext<IssueRowActionContextValue | undefined>(undefined)

export function IssueRowActionsProvider({ value, children }: { value: IssueRowActionContextValue; children: ReactNode }) {
  return <IssueRowActionContext.Provider value={value}>{children}</IssueRowActionContext.Provider>
}

export function useIssueRowActions() { return useContext(IssueRowActionContext) }

type MarkAs = { id: string; label: string; shortcut?: string; relation?: IssueRelationType }
const MARK_AS: MarkAs[] = [
  { id: 'parentOf', label: 'Parent of…' },
  { id: 'subIssueOf', label: 'Sub-issue of…', shortcut: '⌘ ⇧ P' },
  { id: 'related', label: 'Related to…', shortcut: 'M R', relation: 'related' },
  { id: 'blockedBy', label: 'Blocked by…', shortcut: 'M B', relation: 'blocked_by' },
  { id: 'blocking', label: 'Blocking…', shortcut: 'M X', relation: 'blocks' },
  { id: 'duplicate', label: 'Duplicate of…', shortcut: 'M M', relation: 'duplicate' },
]

const RELATION_LABEL: Record<IssueRelationType, string> = { blocked_by: 'Blocked by', blocks: 'Blocking', related: 'Related to', duplicate: 'Duplicate of', parent_of: 'Parent of', sub_issue_of: 'Sub-issue of' } as Record<IssueRelationType, string>
const INVERSE_LABEL: Partial<Record<IssueRelationType, string>> = { blocked_by: 'Blocking', blocks: 'Blocked by', related: 'Related to', duplicate: 'Duplicated by' }

function useRowHelpers(row: MyIssuesRowData) {
  const context = useIssueRowActions()!
  const { data } = context
  const issue = data.issues.find(item => item.id === row.id)
  const update = (input: IssueUpdateInput, message?: string, issueId = row.id) => context.onUpdateIssue(issueId, input).then(() => { if (message) toast.success(message) }).catch(error => toast.error(error instanceof Error ? error.message : 'Could not update issue'))
  return { context, data, issue, update }
}

/** Estimate item, shown between Project and Cycle when the row's team uses estimates. */
export function IssueRowEstimateItem({ row }: { row: MyIssuesRowData }) {
  const context = useIssueRowActions()
  if (!context) return null
  const settings = context.data.teamSettings?.[row.teamId ?? '']
  const values = estimateValues(settings)
  if (!values.length) return null
  const type = settings?.estimateType ?? 'notUsed'
  const current = context.data.issues.find(item => item.id === row.id)?.estimate
  const update = (estimate: number) => void context.onUpdateIssue(row.id, { estimate }).catch(() => toast.error('Could not update issue'))
  return <Sub label="Estimate" shortcut="⇧ E">
    <Item label="No estimate" checked={current === undefined || current === null} onSelect={() => update(-1)}/>
    {values.map(value => <Item key={value} label={estimateLabel(value, type)} checked={current === value} onSelect={() => update(value)}/>)}
  </Sub>
}

/** "More properties" submenu: team, milestone and links. */
export function IssueRowMoreProperties({ row }: { row: MyIssuesRowData }) {
  const context = useIssueRowActions()
  if (!context) return null
  return <MoreProperties row={row}/>
}

function MoreProperties({ row }: { row: MyIssuesRowData }) {
  const { data, update } = useRowHelpers(row)
  const milestones = row.project ? data.projects.find(project => project.id === row.project!.id)?.milestones ?? [] : []
  const teams = data.teams.filter(team => team.id !== row.teamId)
  return <Sub label="More properties">
    {teams.length > 0 && <Sub label="Team…">{teams.map(team => <Item key={team.id} label={team.name} onSelect={() => void update({ teamId: team.id }, `Moved to ${team.name}`)}/>)}</Sub>}
    {milestones.length > 0 && <Sub label="Milestone">{[{ id: '', name: 'No milestone' }, ...milestones].map(milestone => <Item key={milestone.id || 'none'} label={milestone.name} checked={(row.projectMilestoneId ?? '') === milestone.id} onSelect={() => void update({ projectMilestoneId: milestone.id })}/>)}</Sub>}
    <Sub label="Add link…" shortcut="Ctrl L" issueSearch><AddLink issueId={row.id}/></Sub>
  </Sub>
}

/** Everything after the property items: create related, mark as, copy, open in, subscribe, remind and delete. */
export function IssueRowActionGroups({ row, onDelete }: { row: MyIssuesRowData; onDelete?: () => void }) {
  const context = useIssueRowActions()
  if (!context) return null
  return <ActionGroups row={row} onDelete={onDelete}/>
}

function ActionGroups({ row, onDelete }: { row: MyIssuesRowData; onDelete?: () => void }) {
  const { context, data, issue, update } = useRowHelpers(row)
  const viewerId = data.viewer.id
  const subscribed = Boolean(issue?.subscriberIds.includes(viewerId))
  const favorite = Boolean(findFavorite(data.favorites, viewerId, 'issue', row.id))
  const url = `${location.origin}${row.href ?? `/${data.workspace.urlKey}/issue/${row.identifier}`}`
  const settings = data.userSettings?.[viewerId]
  const tools = enabledCodingTools(settings)
  const branch = issue ? configuredIssueBranch(issue, data) : `${row.identifier.toLowerCase()}-${row.title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 50)}`
  const prompt = () => renderCodingPrompt(settings?.codingPromptTemplate, { identifier: row.identifier, title: row.title, description: issue?.description ?? row.description, branchName: branch, url })
  const copy = (text: string, message: string) => void navigator.clipboard.writeText(text).then(() => toast.success(message)).catch(() => toast.error('Could not copy'))
  const copyLink = () => {
    const html = `<a href="${url}">${row.identifier} ${row.title.replace(/</g, '&lt;')}</a>`
    const item = typeof ClipboardItem === 'undefined' ? undefined : new ClipboardItem({ 'text/html': new Blob([html], { type: 'text/html' }), 'text/plain': new Blob([`${row.identifier} ${row.title}`], { type: 'text/plain' }) })
    void (item ? navigator.clipboard.write([item]) : navigator.clipboard.writeText(url)).then(() => toast.success('Link copied')).catch(() => toast.error('Could not copy'))
  }
  const remind = (at: Date) => void createIssueReminder(row.id, at.toISOString()).then(() => toast.success('Reminder set')).catch(() => toast.error('Could not set reminder'))
  const at = (days: number, hour = 9) => { const date = new Date(); date.setDate(date.getDate() + days); date.setHours(hour, 0, 0, 0); return date }
  const nextMonday = () => { const date = at(((8 - new Date().getDay()) % 7) || 7); return date }
  const makeCopy = () => issue && void createIssue({ title: `${issue.title} (copy)`, description: issue.description, teamId: issue.team.id, stateId: issue.state.id, priority: issue.priority, estimate: issue.estimate, assigneeId: issue.assignee?.id, projectId: issue.project?.id, projectMilestoneId: issue.projectMilestoneId, cycleId: issue.cycleId, dueDate: issue.dueDate, labelIds: issue.labels.map(label => label.id), parentId: issue.parentId })
    .then(created => { toast.success(`Created ${created.identifier}`); context.onOpenIssue?.(created) }).catch(() => toast.error('Could not copy issue'))
  const createRelated = (extra: MyIssuesCreateContext) => context.onCreateIssue?.({ teamId: row.teamId, ...extra })
  const markAs = (mark: MarkAs, target: Issue) => {
    if (mark.id === 'parentOf') return void update({ parentId: row.id }, `${target.identifier} is now a sub-issue`, target.id)
    if (mark.id === 'subIssueOf') return void update({ parentId: target.id }, `Sub-issue of ${target.identifier}`)
    void createRelation(row.id, mark.relation!, target.id).then(() => toast.success(`${mark.label.replace('…', '')} ${target.identifier}`)).catch(() => toast.error('Could not add relation'))
  }
  const parent = row.parentId ? data.issues.find(item => item.id === row.parentId) : undefined
  const relations = (issue?.relations ?? []).filter(relation => relation.issueId === row.id || relation.relatedIssueId === row.id)
  const removeRelation = (relation: IssueRelation) => void deleteRelation(relation.issueId, relation.id).then(() => toast.success('Relation removed')).catch(() => toast.error('Could not remove relation'))
  const relationName = (relation: IssueRelation) => {
    const outgoing = relation.issueId === row.id
    const other = data.issues.find(item => item.id === (outgoing ? relation.relatedIssueId : relation.issueId))
    return `${(outgoing ? RELATION_LABEL[relation.type] : INVERSE_LABEL[relation.type]) ?? 'Related to'} ${other?.identifier ?? ''}`.trim()
  }
  const remove = onDelete ?? (context.onDeleteIssues ? () => void context.onDeleteIssues!([row.id]).catch(() => toast.error('Could not delete issue')) : undefined)

  return <>
    <Separator/>
    {context.onCreateIssue && <Sub label="Create related">
      <Item label="Issue…" onSelect={() => createRelated({ related: { issueId: row.id, kind: 'related' } })}/>
      <Item label="Sub-issue…" shortcut="⌘ ⇧ O" onSelect={() => createRelated({ parentId: row.id })}/>
      <Item label="Parent issue…" onSelect={() => createRelated({ related: { issueId: row.id, kind: 'parent' } })}/>
      <Item label="Blocked issue…" onSelect={() => createRelated({ related: { issueId: row.id, kind: 'blocked' } })}/>
      <Item label="Blocking issue…" onSelect={() => createRelated({ related: { issueId: row.id, kind: 'blocking' } })}/>
    </Sub>}
    <Sub label="Mark as">{MARK_AS.map(mark => <Sub key={mark.id} label={mark.label} shortcut={mark.shortcut} issueSearch><IssueSearch data={data} exclude={row.id} onPick={target => markAs(mark, target)}/></Sub>)}</Sub>
    {(parent || row.parentId || relations.length > 0) && <Sub label="Remove…">
      {row.parentId && <Item label={`Parent issue${parent ? ` ${parent.identifier}` : ''}`} onSelect={() => void update({ parentId: '' }, 'Parent removed')}/>}
      {relations.map(relation => <Item key={relation.id} label={relationName(relation)} onSelect={() => removeRelation(relation)}/>)}
    </Sub>}
    <Separator/>
    <Sub label="Copy">
      <Item label="Copy ID" shortcut="⌘ ." onSelect={() => copy(row.identifier, 'ID copied')}/>
      <Item label="Copy URL" shortcut="⌘ ⇧ ," onSelect={() => copy(url, 'URL copied')}/>
      <Item label="Copy title" shortcut="⌘ ⇧ '" onSelect={() => copy(row.title, 'Title copied')}/>
      <Item label="Copy title as link" shortcut="⌘ C" onSelect={copyLink}/>
      <Item label="Copy issue as Markdown" onSelect={() => copy(`# [${row.identifier} ${row.title}](${url})\n\n${issue?.description ?? row.description ?? ''}`.trim(), 'Copied as Markdown')}/>
      <Item label="Copy git branch name" shortcut="⌘ ⇧ ." onSelect={() => copy(branch, 'Branch name copied')}/>
      <Item label="Copy as prompt" shortcut="⌘ ⌥ P" onSelect={() => copy(prompt(), 'Prompt copied')}/>
    </Sub>
    <Item top label="Make a copy…" onSelect={() => makeCopy()}/>
    <Sub label="Open in">
      {tools.map(tool => <Item key={tool.id} label={tool.name} onSelect={() => { const link = tool.link(prompt(), settings ?? {}); if (link) window.open(link, '_blank', 'noopener'); else toast.error(`${tool.name} is not configured`) }}/>)}
      <ConfigureCodingToolsItem path={`${settingsPath(data.workspace.urlKey, 'code-and-reviews')}/coding-tools`}/>
    </Sub>
    <Separator/>
    <Item top label={favorite ? 'Unfavorite' : 'Favorite'} shortcut="⌥ F" onSelect={() => void toggleFavoriteFor(data, 'issue', row.id, !favorite, favorite)}/>
    <Item top label={subscribed ? 'Unsubscribe' : 'Subscribe'} shortcut="⇧ S" onSelect={() => issue && void update({ subscriberIds: subscribed ? issue.subscriberIds.filter(id => id !== viewerId) : [...issue.subscriberIds, viewerId] }, subscribed ? 'Unsubscribed' : 'Subscribed')}/>
    <Sub label="Remind me" shortcut="⇧ H">
      <Item label="An hour from now" onSelect={() => remind(new Date(Date.now() + 3600_000))}/>
      <Item label="Tomorrow" onSelect={() => remind(at(1))}/>
      <Item label="Next week" onSelect={() => remind(nextMonday())}/>
      <Item label="A month from now" onSelect={() => { const date = at(0); date.setMonth(date.getMonth() + 1); remind(date) }}/>
    </Sub>
    {remove && <><Separator/><Item top label="Delete" shortcut="⌘ ⌫" onSelect={remove}/></>}
  </>
}

/** Mounted only while the "Open in" submenu is open, so the menu itself does not need a router. */
function ConfigureCodingToolsItem({ path }: { path: string }) {
  const navigate = useNavigate()
  return <Item label="Configure coding tools…" onSelect={() => navigate(path)}/>
}

function AddLink({ issueId }: { issueId: string }) {
  const [url, setUrl] = useState('')
  const valid = /^https?:\/\/\S+$/i.test(url.trim())
  const save = () => {
    if (!valid) { toast.error('Enter a link starting with http:// or https://'); return }
    void createIssueLink(issueId, { url: url.trim() }).then(() => { toast.success('Link added'); setUrl('') }).catch(() => toast.error('Could not add link'))
  }
  return <div onKeyDown={event => { event.stopPropagation(); if (event.key === 'Enter') { event.preventDefault(); save() } }}>
    <div className={styles.issueSearch}><input autoFocus aria-label="Link URL" placeholder="Paste a link…" value={url} onChange={event => setUrl(event.target.value)}/></div>
  </div>
}

function Separator() {
  return <ContextMenu.Separator className={styles.menuSeparator}/>
}

function Item({ label, shortcut, checked, top = false, onSelect }: { label: string; shortcut?: string; checked?: boolean; top?: boolean; onSelect: () => void }) {
  return <ContextMenu.Item className={top ? styles.menuItem : styles.submenuItem} onSelect={onSelect}>{top && <ContextMenuIcon label={label}/>}<span>{label}</span>{checked ? <span aria-label="Selected">✓</span> : shortcut ? <kbd>{shortcut}</kbd> : null}</ContextMenu.Item>
}

function Sub({ label, shortcut, issueSearch = false, children }: { label: string; shortcut?: string; issueSearch?: boolean; children: ReactNode }) {
  return <ContextMenu.Sub><ContextMenu.SubTrigger className={styles.menuItem}><ContextMenuIcon label={label}/><span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}<span className={styles.menuChevron} aria-hidden="true">▶</span></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent data-flow-motion="floating" className={issueSearch ? `${styles.contextSubmenu} ${styles.issueSubmenu}` : styles.contextSubmenu} sideOffset={3} alignOffset={-5}>{children}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>
}

function IssueSearch({ data, exclude, onPick }: { data: BootstrapData; exclude: string; onPick: (issue: Issue) => void }) {
  const [open, setOpen] = useState(true)
  const candidates = useIssueCandidates(data, true)
  const options = useMemo(() => candidates.filter(item => item.id !== exclude).map(item => ({ id: item.id, label: `${item.identifier} ${item.title}` })), [candidates, exclude])
  const command = usePropertyCommand({ open, options, onOpenChange: setOpen, closeOnSelect: false, onSelect: option => { const target = candidates.find(item => item.id === option.id); if (target) onPick(target) } })
  return <div onKeyDown={event => { event.stopPropagation(); command.onKeyDown(event) }}>
    <div className={styles.issueSearch}><input ref={command.inputRef} aria-label="Search issues" placeholder="Search issues…" value={command.query} onChange={event => command.onQueryChange(event.target.value)}/></div>
    <div className={styles.issueResults}>
      {command.filteredOptions.slice(0, 8).map(option => { const target = candidates.find(item => item.id === option.id); return <ContextMenu.Item key={option.id} className={styles.submenuItem} onSelect={() => command.choose(option)}>{target && <StatusIcon state={target.state} size={14}/>}<span className={styles.issueIdentifier} data-i18n-ignore>{target?.identifier}</span><span data-i18n-ignore>{target?.title}</span></ContextMenu.Item> })}
      {!command.filteredOptions.length && <div className={styles.issueEmpty}>No matching issues</div>}
    </div>
  </div>
}
