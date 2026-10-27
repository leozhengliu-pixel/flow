import * as Popover from '@radix-ui/react-popover'
import * as Tooltip from '@radix-ui/react-tooltip'
import { ChevronRight, CircleUserRound, Link2, Milestone } from 'lucide-react'
import { useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactElement, type ReactNode } from 'react'

import { CalendarIcon, PriorityIcon, ProjectIcon, TeamIcon } from '@/components/issue/issue-icons'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ProjectStatusGlyph } from './project-property-picker'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { markdownPlainText } from '@/lib/markdown-plain-text'
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

  return <Tooltip.Provider delayDuration={450} skipDelayDuration={400}><Tooltip.Root>
    <Popover.Root onOpenChange={setOpen} open={open}>
    {menuTrigger}
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating"
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
        <Tooltip.Content data-flow-motion="tooltip" className="project-dependency-picker__summary" collisionPadding={8} side="top" sideOffset={6}>
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
  const { inputRef, ...command } = usePropertyCommand({
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
        <DependencyDirectionGlyph direction={direction}/>
        <span>{t(label)}</span>
        <ChevronRight size={13}/>
      </button>
    </Popover.Anchor>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating"
        align="start"
        alignOffset={-30}
        className="project-dependency-picker__projects"
        collisionPadding={10}
        onClick={event => event.stopPropagation()}
        onCloseAutoFocus={event => event.preventDefault()}
        onOpenAutoFocus={event => {
          event.preventDefault()
          requestAnimationFrame(() => inputRef.current?.focus())
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
            ref={inputRef}
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
  return <Tooltip.Provider delayDuration={450} skipDelayDuration={400}><Tooltip.Root><Tooltip.Trigger asChild>{row}</Tooltip.Trigger><Tooltip.Portal><Tooltip.Content data-flow-motion="tooltip" className="project-dependency-picker__preview" collisionPadding={8} side="left" sideOffset={6}>{preview}</Tooltip.Content></Tooltip.Portal></Tooltip.Root></Tooltip.Provider>
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
    {details?.summary && <p data-i18n-ignore>{markdownPlainText(details.summary)}</p>}
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

/** The reference app's "Blocked by…" / "Blocking…" glyphs (16×16, drawn in the dependency colour). */
function DependencyDirectionGlyph({ direction }: { direction: ProjectDependencyType }) {
  return <svg aria-hidden="true" className="project-dependency-picker__direction-glyph" width={16} height={16} viewBox="0 0 16 16" fill="currentColor">
    {direction === 'blocked_by'
      ? <path fillRule="evenodd" clipRule="evenodd" d="M5.82843 1H10.1716C10.702 1 11.2107 1.21071 11.5858 1.58579L14.4142 4.41421C14.7893 4.78929 15 5.29799 15 5.82843V10.1716C15 10.702 14.7893 11.2107 14.4142 11.5858L11.5858 14.4142C11.2107 14.7893 10.702 15 10.1716 15H5.82843C5.29799 15 4.78929 14.7893 4.41421 14.4142L1.58579 11.5858C1.21071 11.2107 1 10.702 1 10.1716V5.82843C1 5.29799 1.21071 4.78929 1.58579 4.41421L4.41421 1.58579C4.78929 1.21071 5.29799 1 5.82843 1ZM4.5 6.75C4.22386 6.75 4 6.97386 4 7.25V8.75C4 9.02614 4.22386 9.25 4.5 9.25H11.5C11.7761 9.25 12 9.02614 12 8.75V7.25C12 6.97386 11.7761 6.75 11.5 6.75H4.5Z"/>
      : <>
        <path d="M10.7461 1C12.8172 1 14.4961 2.67893 14.4961 4.75V5.24561C14.4961 5.65982 14.1603 5.99561 13.7461 5.99561C13.3319 5.99561 12.9961 5.65982 12.9961 5.24561V4.75C12.9961 3.50736 11.9887 2.5 10.7461 2.5H4.74609C3.50345 2.5 2.49609 3.50736 2.49609 4.75V10.9705C2.49609 12.2131 3.50345 13.2205 4.74609 13.2205H5.25009C5.66431 13.2205 6.00009 13.5562 6.00009 13.9705C6.00009 14.3847 5.66431 14.7205 5.25009 14.7205H4.74609C2.67503 14.7205 0.996094 13.0415 0.996094 10.9705V4.75C0.996094 2.67893 2.67503 1 4.74609 1H10.7461Z"/>
        <path fillRule="evenodd" clipRule="evenodd" d="M12.2409 7H9.7591C9.456 7 9.16531 7.12041 8.95098 7.33473L7.33473 8.95098C7.12041 9.16531 7 9.456 7 9.7591V12.2409C7 12.544 7.12041 12.8347 7.33473 13.049L8.95098 14.6653C9.16531 14.8796 9.456 15 9.7591 15H12.2409C12.544 15 12.8347 14.8796 13.049 14.6653L14.6653 13.049C14.8796 12.8347 15 12.544 15 12.2409V9.7591C15 9.456 14.8796 9.16531 14.6653 8.95098L13.049 7.33473C12.8347 7.12041 12.544 7 12.2409 7ZM9.22222 10.1111C8.97676 10.1111 8.77778 10.3101 8.77778 10.5556V11.4444C8.77778 11.6899 8.97676 11.8889 9.22222 11.8889H12.7778C13.0232 11.8889 13.2222 11.6899 13.2222 11.4444V10.5556C13.2222 10.3101 13.0232 10.1111 12.7778 10.1111H9.22222Z"/>
      </>}
  </svg>
}
