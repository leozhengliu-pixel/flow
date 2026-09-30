import { Fragment, useState, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { toast } from 'sonner'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import type { Project, ProjectMilestone } from '@/types/flow'
import { ProjectMenuSearch } from './project-menu-primitives'
import { projectMilestoneLink } from './project-detail-helpers'

type MilestoneMenuIconName = 'calendar'|'chevron-right'|'close'|'copy'|'edit'|'issues'|'link'|'link-name'|'milestone'|'more-horizontal'|'project'|'trash'

export function MilestoneMenuIcon({ name }: { name: MilestoneMenuIconName }) {
  return <svg aria-hidden="true" height="16" viewBox="0 0 16 16" width="16"><use href={`#${name}`}/></svg>
}


type Action = { key: string; label: string; node: ReactNode }

/**
 * Linear's milestone "⋯" menu. The sidebar variant offers Set target date…
 * (the row has no inline date control); the overview variant offers Show
 * description history instead, matching the two menus in Linear.
 */
export function ProjectMilestoneMenu({ count, milestone, onConvert, onDelete, onEdit, onMove, onOpenIssues, onSetTargetDate, onShowHistory, projects, trigger, variant }: {
  count: number
  milestone: ProjectMilestone
  onConvert: () => Promise<void>
  onDelete: () => Promise<void>
  onEdit: () => void
  onMove: (targetProjectId: string) => Promise<void>
  onOpenIssues?: () => void
  onSetTargetDate?: () => void
  onShowHistory?: () => void
  projects: Project[]
  trigger: ReactNode
  variant: 'sidebar'|'overview'
}) {
  const [query, setQuery] = useState('')
  const shows = (label: string) => !query || label.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())
  const link = projectMilestoneLink(milestone.id)
  const copy = (value: string, message: string) => void navigator.clipboard.writeText(value).then(() => toast.success(message))
  const remove = () => { void confirmAction(`Delete “${milestone.name}”?`, { confirmLabel: 'Delete milestone' }).then(confirmed => { if (confirmed) return onDelete() }) }
  const groups: Action[][] = [[
    ...(variant === 'sidebar' && count > 0 && onOpenIssues ? [{ key: 'open', label: 'Open milestone issues', node: <MilestoneMenuItem icon={<MilestoneMenuIcon name="issues"/>} label="Open milestone issues" onSelect={onOpenIssues}/> }] : []),
    { key: 'edit', label: 'Edit…', node: <MilestoneMenuItem icon={<MilestoneMenuIcon name="edit"/>} label="Edit…" onSelect={onEdit}/> },
    ...(variant === 'sidebar' && onSetTargetDate ? [{ key: 'date', label: 'Set target date…', node: <MilestoneMenuItem icon={<MilestoneMenuIcon name="calendar"/>} label="Set target date…" onSelect={onSetTargetDate}/> }] : []),
    { key: 'copy', label: 'Copy', node: <DropdownMenu.Sub><MilestoneSubTrigger icon="copy" label="Copy"/><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" alignOffset={-7} className="project-milestone-menu project-milestone-copy-menu" collisionPadding={8} sideOffset={-2}>
      <MilestoneMenuItem icon={<MilestoneMenuIcon name="link"/>} label="Copy link" onSelect={() => copy(link, 'Milestone link copied')}/>
      <MilestoneMenuItem end="⌘ C" icon={<MilestoneMenuIcon name="link-name"/>} label="Copy name as link" onSelect={() => copy(`[${milestone.name}](${link})`, 'Milestone name and link copied')}/>
      <MilestoneMenuItem icon={<MilestoneMenuIcon name="issues"/>} label="Copy link to issues" onSelect={() => copy(link, 'Issues link copied')}/>
    </DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub> },
    ...(variant === 'overview' && onShowHistory ? [{ key: 'history', label: 'Show description history', node: <MilestoneMenuItem icon={<IssueActionGlyph label="Show description history" fallback={<MilestoneMenuIcon name="edit"/>}/>} label="Show description history" onSelect={onShowHistory}/> }] : []),
  ], [
    { key: 'move', label: 'Move milestone to', node: <DropdownMenu.Sub><MilestoneSubTrigger icon="milestone" label="Move milestone to"/><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" alignOffset={-7} className="project-milestone-menu project-milestone-move-menu" collisionPadding={8} sideOffset={-2}>
      {projects.map(project => <MilestoneMenuItem icon={<ViewGlyph color={project.color} icon={normalizeProjectIcon(project.icon)}/>} key={project.id} label={project.name} i18nIgnore onSelect={() => void onMove(project.id)}/>)}
      {!projects.length && <DropdownMenu.Label>No other projects</DropdownMenu.Label>}
    </DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub> },
    { key: 'convert', label: 'Convert to project', node: <MilestoneMenuItem icon={<ViewGlyph color="var(--text-label)" icon="Project"/>} label="Convert to project" onSelect={() => void onConvert()}/> },
  ], [
    { key: 'delete', label: 'Delete', node: <MilestoneMenuItem end="⌘ ⌫" icon={<MilestoneMenuIcon name="trash"/>} label="Delete" onSelect={remove}/> },
  ]]
  const visibleGroups = groups.map(group => group.filter(action => shows(action.label))).filter(group => group.length)
  return <DropdownMenu.Root onOpenChange={open => { if (!open) setQuery('') }}>
    <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
    <DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="end" alignOffset={variant === 'sidebar' ? -25 : 0} className="project-milestone-menu" collisionPadding={8} onClick={event => event.stopPropagation()} onCloseAutoFocus={event => event.preventDefault()} sideOffset={4}>
      <ProjectMenuSearch label="Filter milestone actions" query={query} onChange={setQuery}/>
      {visibleGroups.map((group, index) => <Fragment key={group[0].key}>{index > 0 && !query && <DropdownMenu.Separator/>}{group.map(action => <Fragment key={action.key}>{action.node}</Fragment>)}</Fragment>)}
      {query && !visibleGroups.length && <div className="project-action-menu__empty">No results</div>}
    </DropdownMenu.Content></DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function MilestoneMenuItem({ end, i18nIgnore, icon, label, onSelect }: { end?: string; i18nIgnore?: boolean; icon: ReactNode; label: string; onSelect: () => void }) {
  return <DropdownMenu.Item onSelect={onSelect}><span className="project-milestone-menu__item-background"/>{icon}<span data-i18n-ignore={i18nIgnore || undefined}>{label}</span>{end && <kbd>{end}</kbd>}</DropdownMenu.Item>
}

function MilestoneSubTrigger({ icon, label }: { icon: MilestoneMenuIconName; label: string }) {
  return <DropdownMenu.SubTrigger><span className="project-milestone-menu__item-background"/><MilestoneMenuIcon name={icon}/><span>{label}</span><span aria-hidden="true" className="project-milestone-menu__submenu-arrow">▶</span></DropdownMenu.SubTrigger>
}
