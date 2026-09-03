import * as Popover from '@radix-ui/react-popover'
import * as Tooltip from '@radix-ui/react-tooltip'
import { Blocks, ChevronRight, CircleUserRound, Link2, Milestone, OctagonMinus } from 'lucide-react'
import { useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactElement, type ReactNode } from 'react'

import { CalendarIcon, PriorityIcon, ProjectIcon, TeamIcon } from '@/components/issue/issue-icons'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ProjectStatusGlyph } from './project-property-picker'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import type { ProjectDependencyRelationInput } from '@/types/flow'

import './project-dependency-picker.css'

export type ProjectDependencyType = ProjectDependencyRelationInput['type']
export type ProjectDependencyValue = ProjectDependencyRelationInput

export type ProjectDependencyOption = {
  id: string
  label: string
  icon?: string
  color?: string
  group?: 'your' | 'other'
  keywords?: string
  disabled?: boolean
  previewData?: ProjectDependencyPreviewData
}

export type ProjectDependencyPreviewData = {
  summary?: string
  status?: string
  milestone?: string
  team?: string
  lead?: string
  member?: string
  memberAvatarUrl?: string
  memberColor?: string
  priority?: string
  targetDate?: string
  progress?: number
  issueCount?: number
}

export type ProjectDependencyPickerProps = {
  projects: ProjectDependencyOption[]
  value: ProjectDependencyValue[]
  onChange: (value: ProjectDependencyValue[]) => void
  ariaLabel?: string
  closeOnSelect?: boolean
  disabled?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
  renderPreview?: (project: ProjectDependencyOption) => ReactNode
  trigger?: ReactElement
  triggerClassName?: string
}

const DIRECTIONS: Array<{ type: ProjectDependencyType; label: string }> = [
  { type: 'blocked_by', label: 'Blocked by…' },
  { type: 'blocks', label: 'Blocking…' },
]

export function ProjectDependencyPicker({
  projects,
  value: rawValue,
  onChange,
  ariaLabel = 'Add dependencies',
  closeOnSelect = true,
  disabled = false,
  open: controlledOpen,
  onOpenChange,
  renderPreview,
  trigger,
  triggerClassName,
}: ProjectDependencyPickerProps) {
  const { t } = useI18n()
  const value = rawValue ?? []
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false)
  const [direction, setDirection] = useState<ProjectDependencyType>()
  const [rootQuery, setRootQuery] = useState('')
  const rootInputRef = useRef<HTMLInputElement>(null)
  const open = controlledOpen ?? uncontrolledOpen
  const projectById = useMemo(() => new Map(projects.map(project => [project.id, project])), [projects])

  const setOpen = (next: boolean) => {
    if (controlledOpen === undefined) setUncontrolledOpen(next)
    onOpenChange?.(next)
    if (!next) {
      setDirection(undefined)
      setRootQuery('')
    }
  }
  const close = () => {
    setDirection(undefined)
    setOpen(false)
  }
  const toggle = (projectId: string, type: ProjectDependencyType) => {
    const exists = value.some(item => item.projectId === projectId && item.type === type)
    const next = exists
      ? value.filter(item => item.projectId !== projectId)
      : [...value.filter(item => item.projectId !== projectId), { projectId, type }]
    onChange(next)
    if (closeOnSelect) close()
  }
  const selectedFor = (type: ProjectDependencyType) => value
    .filter(item => item.type === type)
    .map(item => projectById.get(item.projectId))
    .filter((project): project is ProjectDependencyOption => Boolean(project))
  const normalizedRootQuery = normalize(rootQuery)
  const visibleDirections = DIRECTIONS.filter(item => fuzzyMatch(item.label, normalizedRootQuery) || fuzzyMatch(t(item.label), normalizedRootQuery))
  const triggerContents = dependencyTriggerContents(value, projectById, t)
  const triggerButton = trigger ?? <button aria-expanded={open} aria-haspopup="dialog" aria-label={t(ariaLabel)} className={triggerClassName ?? 'project-dependency-picker__trigger'} data-state={open ? 'open' : 'closed'} disabled={disabled} role="combobox" type="button">{triggerContents}</button>
  const hasSelection = value.length > 0

  const popoverTrigger = <Popover.Trigger asChild>{triggerButton}</Popover.Trigger>
  const menuTrigger = value.length ? <Tooltip.Trigger asChild>{popoverTrigger}</Tooltip.Trigger> : popoverTrigger

  return <Tooltip.Provider delayDuration={500} skipDelayDuration={0}><Tooltip.Root>
    <Popover.Root onOpenChange={setOpen} open={open}>
    {menuTrigger}
    <Popover.Portal>
      <Popover.Content
        align="start"
        className={`project-dependency-picker__surface${hasSelection ? ' has-selection' : ''}`}
        collisionPadding={10}
        onClick={event => event.stopPropagation()}
        onCloseAutoFocus={event => event.preventDefault()}
        onOpenAutoFocus={event => {
          event.preventDefault()
          requestAnimationFrame(() => rootInputRef.current?.focus())
        }}
        role="dialog"
        side="bottom"
        sideOffset={4}
      >
        <div className={`project-dependency-picker__hidden-search${hasSelection ? ' is-visible' : ''}`}>
          <input
            aria-label={t('Dependencies…')}
            autoComplete="off"
            onChange={event => setRootQuery(event.target.value)}
            onKeyDown={event => rootKeyDown(event, visibleDirections, direction, setDirection)}
            placeholder={t('Dependencies…')}
            ref={rootInputRef}
            spellCheck={false}
            value={rootQuery}
          />
        </div>
        <div aria-label={t('Dependencies')} className="project-dependency-picker__root-list" role="listbox">
          {selectedFor('blocked_by').length > 0 && <SelectedSection label="Blocked by" onToggle={project => toggle(project.id, 'blocked_by')} projects={selectedFor('blocked_by')} renderPreview={renderPreview}/>} 
          {selectedFor('blocks').length > 0 && <SelectedSection label="Blocking" onToggle={project => toggle(project.id, 'blocks')} projects={selectedFor('blocks')} renderPreview={renderPreview}/>} 
          <div className="project-dependency-picker__group-label" role="group">{t('Add dependency')}</div>
          {visibleDirections.map(item => <DirectionPicker
            active={direction === item.type}
            closeOnSelect={closeOnSelect}
            direction={item.type}
            key={item.type}
            label={item.label}
            onChange={next => {
              onChange(next)
              if (closeOnSelect) close()
            }}
            onOpenChange={next => setDirection(next ? item.type : currentDirection(direction, item.type))}
            projects={projects}
            renderPreview={renderPreview}
            value={value}
          />)}
          {!visibleDirections.length && <div className="project-dependency-picker__empty">{t('No results')}</div>}
        </div>
      </Popover.Content>
    </Popover.Portal>
    </Popover.Root>
    {value.length > 0 && <Tooltip.Portal>
        <Tooltip.Content className="project-dependency-picker__summary" collisionPadding={8} side="top" sideOffset={6}>
          {(['blocked_by', 'blocks'] as const).map(type => {
            const selected = selectedFor(type)
            if (!selected.length) return null
            const heading = type === 'blocked_by' ? 'Blocked by' : 'Blocking'
            return <section key={type}>
              <strong>{t(heading)} {selected.length} {t(selected.length === 1 ? 'project' : 'projects')}</strong>
              {selected.map(project => <span key={project.id}><ProjectDependencyIcon project={project} size={14}/><span data-i18n-ignore>{project.label}</span></span>)}
            </section>
          })}
        </Tooltip.Content>
      </Tooltip.Portal>}
  </Tooltip.Root></Tooltip.Provider>
}

function DirectionPicker({ active, closeOnSelect, direction, label, onChange, onOpenChange, projects, renderPreview, value }: {
  active: boolean
  closeOnSelect: boolean
  direction: ProjectDependencyType
  label: string
  onChange: (value: ProjectDependencyValue[]) => void
  onOpenChange: (open: boolean) => void
  projects: ProjectDependencyOption[]
  renderPreview?: (project: ProjectDependencyOption) => ReactNode
  value: ProjectDependencyValue[]
}) {
  const { t } = useI18n()
  const command = usePropertyCommand({
    closeOnSelect,
    keepSelectedVisible: true,
    onOpenChange,
    onSelect: project => toggle(project),
    open: active,
    options: projects,
    resetKey: direction,
    selectedIds: value.filter(item => item.type === direction).map(item => item.projectId),
  })
  const filtered = command.filteredOptions
  const groups = [
    { id: 'your', label: 'Your projects', projects: orderSelected(filtered.filter(project => project.group !== 'other'), value, direction) },
    { id: 'other', label: 'Other projects', projects: orderSelected(filtered.filter(project => project.group === 'other'), value, direction) },
  ].filter(group => group.projects.length > 0)
  const placeholder = direction === 'blocked_by' ? 'Mark as blocked by…' : 'Mark as blocking…'
  const toggle = (project: ProjectDependencyOption) => {
    if (project.disabled) return
    const selected = value.some(item => item.projectId === project.id && item.type === direction)
    const next = selected
      ? value.filter(item => item.projectId !== project.id)
      : [...value.filter(item => item.projectId !== project.id), { projectId: project.id, type: direction }]
    onChange(next)
  }

  return <Popover.Root onOpenChange={onOpenChange} open={active}>
    <Popover.Anchor asChild>
      <button aria-expanded={active} aria-haspopup="listbox" aria-selected={active} className="project-dependency-picker__direction" onClick={() => onOpenChange(true)} onPointerEnter={() => onOpenChange(true)} role="option" type="button">
        <span className="project-dependency-picker__row-background"/>
        {direction === 'blocked_by' ? <OctagonMinus size={16}/> : <Blocks size={16}/>} 
        <span>{t(label)}</span>
        <ChevronRight size={13}/>
      </button>
    </Popover.Anchor>
    <Popover.Portal>
      <Popover.Content
        align="start"
        alignOffset={-30}
        className="project-dependency-picker__projects"
        collisionPadding={10}
        onClick={event => event.stopPropagation()}
        onCloseAutoFocus={event => event.preventDefault()}
        onOpenAutoFocus={event => {
          event.preventDefault()
          requestAnimationFrame(() => command.inputRef.current?.focus())
        }}
        role="dialog"
        side="right"
        sideOffset={3}
      >
        <div className="project-dependency-picker__project-search">
          <input
            aria-activedescendant={command.activeId ? `project-dependency-${direction}-${command.activeId}` : undefined}
            aria-label={t(placeholder)}
            autoComplete="off"
            onChange={event => command.onQueryChange(event.target.value)}
            onKeyDown={command.onKeyDown}
            placeholder={t(placeholder)}
            ref={command.inputRef}
            spellCheck={false}
            value={command.query}
          />
        </div>
        <div aria-label={t(placeholder)} className="project-dependency-picker__project-list" role="listbox">
          {groups.map(group => <div key={group.id} role="group">
            <div className="project-dependency-picker__group-label">{t(group.label)}</div>
            {group.projects.map(project => <ProjectOptionRow
              active={command.activeId === project.id}
              checked={value.some(item => item.projectId === project.id && item.type === direction)}
              disabled={project.disabled}
              id={`project-dependency-${direction}-${project.id}`}
              key={project.id}
              onActive={() => command.setActiveId(project.id)}
              onChoose={() => command.choose(project)}
              project={project}
              renderPreview={renderPreview}
            />)}
          </div>)}
          {!filtered.length && <div className="project-dependency-picker__empty">{t('No projects found')}</div>}
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function SelectedSection({ label, onToggle, projects, renderPreview }: { label: string; onToggle: (project: ProjectDependencyOption) => void; projects: ProjectDependencyOption[]; renderPreview?: (project: ProjectDependencyOption) => ReactNode }) {
  const { t } = useI18n()
  return <div role="group">
    <div className="project-dependency-picker__group-label">{t(label)}</div>
    {projects.map(project => <ProjectOptionRow checked key={project.id} onChoose={() => onToggle(project)} project={project} renderPreview={renderPreview}/>)}
  </div>
}

function ProjectOptionRow({ active = false, checked, disabled = false, id, onActive, onChoose, project, renderPreview }: {
  active?: boolean
  checked: boolean
  disabled?: boolean
  id?: string
  onActive?: () => void
  onChoose: () => void
  project: ProjectDependencyOption
  renderPreview?: (project: ProjectDependencyOption) => ReactNode
}) {
  const row = <button
    aria-checked={checked}
    aria-disabled={disabled}
    aria-selected={active}
    className="project-dependency-picker__project"
    disabled={disabled}
    id={id}
    onClick={onChoose}
    onFocus={onActive}
    onPointerMove={onActive}
    role="option"
    type="button"
  >
    <span className="project-dependency-picker__row-background"/>
    <span className="project-dependency-picker__checkbox">{checked && <CheckboxMark/>}</span>
    <span className="project-dependency-picker__project-label"><ProjectDependencyIcon project={project} size={16}/><span data-i18n-ignore>{project.label}</span></span>
  </button>
  const preview = renderPreview?.(project) ?? (project.previewData ? <ProjectDependencyPreview project={project}/> : undefined)
  if (!preview) return row
  return <Tooltip.Provider delayDuration={500} skipDelayDuration={0}><Tooltip.Root><Tooltip.Trigger asChild>{row}</Tooltip.Trigger><Tooltip.Portal><Tooltip.Content className="project-dependency-picker__preview" collisionPadding={8} side="left" sideOffset={6}>{preview}</Tooltip.Content></Tooltip.Portal></Tooltip.Root></Tooltip.Provider>
}

function ProjectDependencyIcon({ project, size }: { project?: ProjectDependencyOption; size: number }) {
  if (project?.icon) return <ViewGlyph className="project-dependency-picker__icon" color={project.color} icon={project.icon}/>
  return <ProjectIcon size={size} style={{ color: project?.color }}/>
}

function ProjectDependencyPreview({ project }: { project: ProjectDependencyOption }) {
  const { formatDate, locale, t } = useI18n()
  const details = project.previewData
  return <div className="project-dependency-picker__preview-card">
    <header><ProjectDependencyIcon project={project} size={16}/><strong data-i18n-ignore>{project.label}</strong></header>
    {details?.summary && <p data-i18n-ignore>{details.summary}</p>}
    <div className="project-dependency-picker__preview-meta">
      {details?.status && <span><PreviewMetaIcon kind="status" value={details.status}/>{t(details.status)}</span>}
      {details?.milestone && <span><PreviewMetaIcon kind="milestone"/><span data-i18n-ignore>{details.milestone}</span></span>}
      {details?.team && <span><PreviewMetaIcon kind="team"/><span data-i18n-ignore>{details.team}</span></span>}
      {details?.lead && <span><PreviewMetaIcon kind="lead"/><span data-i18n-ignore>{details.lead}</span></span>}
      {details?.member && <span><UserAvatar avatarUrl={details.memberAvatarUrl} className="avatar project-dependency-picker__preview-member" color={details.memberColor ?? previewAvatarColor(details.member)} name={details.member}/><span data-i18n-ignore>{details.member}</span></span>}
      {details?.priority && <span><PreviewMetaIcon kind="priority" value={details.priority}/>{t(details.priority)}</span>}
      {details?.targetDate && <span><PreviewMetaIcon kind="date"/><span data-i18n-ignore>{formatPreviewDate(details.targetDate, locale, formatDate)}</span></span>}
      {details?.progress !== undefined && <span><PreviewMetaIcon kind="progress" value={String(details.progress)}/>{details.progress}%{details.issueCount !== undefined ? ` ${t('of')} ${details.issueCount}` : ''}</span>}
    </div>
  </div>
}

function PreviewMetaIcon({ kind, value }: { kind: 'status' | 'milestone' | 'team' | 'lead' | 'priority' | 'date' | 'progress'; value?: string }) {
  const props = { 'aria-hidden': true, size: 14, strokeWidth: 1.8 }
  if (kind === 'status') return <ProjectStatusGlyph name={value ?? 'Planned'} type={value}/>
  if (kind === 'milestone') return <Milestone {...props}/>
  if (kind === 'team') return <TeamIcon size={14}/>
  if (kind === 'lead') return <CircleUserRound {...props}/>
  if (kind === 'priority') return <PriorityIcon priority={priorityNumber(value)} size={14}/>
  if (kind === 'date') return <CalendarIcon size={14}/>
  if (kind === 'progress') return <span aria-hidden="true" className="project-dependency-picker__preview-progress-icon" style={{ '--progress': `${Math.max(0, Math.min(100, Number(value) || 0))}%` } as CSSProperties}/>
  return null
}

function priorityNumber(value?: string) {
  return ({ Urgent: 1, High: 2, Medium: 3, Low: 4 } as Record<string, number>)[value ?? ''] ?? 0
}

function previewAvatarColor(value: string) {
  const colors = ['#d15f5f', '#5e6ad2', '#4c9a67', '#d09b42']
  return colors[[...value].reduce((sum, character) => sum + character.charCodeAt(0), 0) % colors.length]
}

function formatPreviewDate(value: string, locale: string, formatDate: (value: Date | string | number, options?: Intl.DateTimeFormatOptions) => string) {
  const normalized = /^\d{4}-\d{2}$/.test(value) ? `${value}-01` : value
  const date = new Date(`${normalized}T00:00:00`)
  if (Number.isNaN(date.getTime())) return value
  return locale === 'en-US' ? new Intl.DateTimeFormat('en-US', { month: 'short', year: '2-digit' }).format(date) : formatDate(`${normalized}T00:00:00`, { month: 'short', year: '2-digit' })
}

function dependencyTriggerContents(value: ProjectDependencyValue[], projects: Map<string, ProjectDependencyOption>, t: (value: string) => string) {
  if (!value.length) return <><Link2 size={14}/><span>{t('Dependencies')}</span></>
  if (value.length === 1) {
    const project = projects.get(value[0].projectId)
    return <><ProjectDependencyIcon project={project} size={14}/><span data-i18n-ignore>{project?.label ?? t('Dependencies')}</span></>
  }
  return <><Link2 size={14}/><span>{t(`${value.length} dependencies`)}</span></>
}

function rootKeyDown(event: KeyboardEvent<HTMLInputElement>, directions: typeof DIRECTIONS, active: ProjectDependencyType | undefined, setActive: (value: ProjectDependencyType | undefined) => void) {
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp' && event.key !== 'Home' && event.key !== 'End' && event.key !== 'Enter' && event.key !== 'ArrowRight') return
  const index = directions.findIndex(item => item.type === active)
  if (event.key === 'Enter' || event.key === 'ArrowRight') {
    const target = directions[index >= 0 ? index : 0]
    if (target) {
      event.preventDefault()
      setActive(target.type)
    }
    return
  }
  event.preventDefault()
  if (event.key === 'Home') setActive(directions[0]?.type)
  else if (event.key === 'End') setActive(directions[directions.length - 1]?.type)
  else {
    const offset = event.key === 'ArrowDown' ? 1 : -1
    const next = index < 0 ? (offset > 0 ? 0 : directions.length - 1) : (index + offset + directions.length) % directions.length
    setActive(directions[next]?.type)
  }
}

function currentDirection(current: ProjectDependencyType | undefined, closing: ProjectDependencyType) {
  return current === closing ? undefined : current
}

function orderSelected(projects: ProjectDependencyOption[], value: ProjectDependencyValue[], direction: ProjectDependencyType) {
  const selected = new Set(value.filter(item => item.type === direction).map(item => item.projectId))
  return [...projects].sort((left, right) => Number(selected.has(right.id)) - Number(selected.has(left.id)))
}

function normalize(value: string) {
  return value.trim().toLocaleLowerCase()
}

function fuzzyMatch(value: string, query: string) {
  if (!query) return true
  const normalized = normalize(value)
  if (normalized.includes(query)) return true
  let cursor = 0
  for (const character of query) {
    cursor = normalized.indexOf(character, cursor)
    if (cursor < 0) return false
    cursor += 1
  }
  return true
}
