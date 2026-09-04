import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { CalendarPlus, LayoutTemplate, X } from 'lucide-react'
import { PlusIcon } from './projects-page-icons'
import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'
import { ViewIconPicker } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { CalendarIcon, LabelIcon, MembersIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { PersonHoverPreview, PersonPicker } from '@/components/issue/core-property-pickers'
import { ProjectDependencyPicker, type ProjectDependencyPreviewData, type ProjectDependencyValue } from './project-dependency-picker'
import { ProjectStatusGlyph } from './project-property-picker'
import { ProjectDatePicker } from './project-target-date-picker'
import { formatProjectPropertyDate } from '@/components/project-detail/project-detail-helpers'
import { useI18n } from '@/i18n/i18n'
import type { PersonalAgentSkill, ProjectDependencyRelationInput } from '@/types/flow'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ProjectCreationAgent } from './project-creation-agent'
import type { ProjectAgentDraft } from './project-agent-draft'
import { AgentPointerIcon } from '@/components/agent/agent-icons'
import './projects-page.css'

export type NewProjectDraft = {
  templateId?: string
  name: string
  icon?: string
  color?: string
  summary: string
  description: string
  status: string
  priority: string
  leadId?: string
  memberIds: string[]
  teamIds: string[]
  startDate?: string
  startDateResolution?: 'halfYear' | 'month' | 'quarter' | 'year'
  targetDate?: string
  targetDateResolution?: 'halfYear' | 'month' | 'quarter' | 'year'
  initiativeIds: string[]
  labelIds: string[]
  dependencyIds: string[]
  dependencyRelations: ProjectDependencyRelationInput[]
  milestones: string[]
  milestoneDetails?: NewProjectMilestoneDraft[]
}

export type NewProjectMilestoneDraft = {
  name: string
  description?: string
  targetDate?: string
}

export type NewProjectChoice = { id: string, label: string, color?: string, email?: string, name?: string, avatarUrl?: string, active?: boolean, invited?: boolean, disabled?: boolean, end?: string, groupId?: string, groupLabel?: string, group?: 'your' | 'other', icon?: ReactNode, initials?: string, hoverContent?: ReactNode, hoverClassName?: string, previewData?: ProjectDependencyPreviewData }
export type NewProjectTemplateChoice = {
  id: string
  label: string
  description?: string
  name?: string
  summary?: string
  icon?: string
  color?: string
  status?: string
  priority?: string
  teamIds?: string[]
  initiativeIds?: string[]
  labelIds?: string[]
  dependencyIds?: string[]
}

export type NewProjectDialogProps = {
  open: boolean
  initialTemplateId?: string
  teamLabel?: string
  defaultStatus?: string
  teams?: NewProjectChoice[]
  leads?: NewProjectChoice[]
  members?: NewProjectChoice[]
  initiatives?: NewProjectChoice[]
  labels?: NewProjectChoice[]
  dependencies?: NewProjectChoice[]
  agentSkills?: PersonalAgentSkill[]
  workspaceName?: string
  statuses?: NewProjectChoice[]
  templates?: NewProjectTemplateChoice[]
  onClose: () => void
  onCreate: (draft: NewProjectDraft) => Promise<void> | void
}

const STATUS = ['Backlog', 'Planned', 'In Progress', 'Completed', 'Canceled']
const PRIORITY = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
const DEFAULT_TEAMS: NewProjectChoice[] = []
const EMPTY_CHOICES: NewProjectChoice[] = []
const EMPTY_TEMPLATES: NewProjectTemplateChoice[] = []
const DEFAULT_STATUSES: NewProjectChoice[] = STATUS.map(value => ({ id: value, icon: <ProjectStatusGlyph name={value} type={projectStatusType(value)}/>, label: value }))

export function NewProjectDialog({
  open,
  initialTemplateId,
  teamLabel = 'Team',
  defaultStatus = 'Backlog',
  teams = DEFAULT_TEAMS,
  leads = EMPTY_CHOICES,
  members = EMPTY_CHOICES,
  initiatives = EMPTY_CHOICES,
  labels = EMPTY_CHOICES,
  dependencies = EMPTY_CHOICES,
  agentSkills = [],
  workspaceName,
  statuses = DEFAULT_STATUSES,
  templates = EMPTY_TEMPLATES,
  onClose,
  onCreate,
}: NewProjectDialogProps) {
  const { t } = useI18n()
  const panelRef = useRef<HTMLFormElement>(null)
  const nameRef = useRef<HTMLInputElement>(null)
  const defaultTeamId = teams[0]?.id
  const templateSignature = templates
    .map(template => [template.id, template.name, template.icon, template.color].join(':'))
    .join('|')
  const labelSignature = labels.map(label => label.id).join('|')
  const templatesRef = useRef(templates)
  const labelsRef = useRef(labels)
  templatesRef.current = templates
  labelsRef.current = labels
  const [draft, setDraft] = useState<NewProjectDraft>(() => emptyDraft(defaultStatus, defaultTeamId))
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [nameError, setNameError] = useState(false)
  const [agentOpen, setAgentOpen] = useState(false)
  const [discardOpen, setDiscardOpen] = useState(false)
  const initialDraftRef = useRef<NewProjectDraft | undefined>(undefined)
  const dirty = initialDraftRef.current ? JSON.stringify(draft) !== JSON.stringify(initialDraftRef.current) : false
  const requestClose = useCallback(() => {
    if (submitting) return
    if (dirty) setDiscardOpen(true)
    else onClose()
  }, [dirty, onClose, submitting])

  useEffect(() => {
    if (!open) return
    const nextDraft = applyProjectTemplateDraft(emptyDraft(defaultStatus, defaultTeamId), initialTemplateId, templatesRef.current, labelsRef.current)
    initialDraftRef.current = nextDraft
    setDraft(nextDraft)
    setError(null)
    setNameError(false)
    setAgentOpen(false)
    setDiscardOpen(false)
    requestAnimationFrame(() => nameRef.current?.focus())
  }, [defaultStatus, defaultTeamId, initialTemplateId, labelSignature, open, templateSignature])

  useEffect(() => {
    if (!open) return
    const keydown = (event: globalThis.KeyboardEvent) => {
      if (event.defaultPrevented) return
      if (event.key === 'Escape' && document.querySelector('[data-radix-popper-content-wrapper]')) return
      if (event.key === 'Escape' && discardOpen) { setDiscardOpen(false); return }
      if (event.key === 'Escape' && !submitting) requestClose()
    }
    document.addEventListener('keydown', keydown)
    return () => document.removeEventListener('keydown', keydown)
  }, [discardOpen, onClose, open, requestClose, submitting])

  if (!open) return null

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!draft.name.trim()) {
      setNameError(true)
      setError('The project name cannot be empty.')
      nameRef.current?.focus()
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      await onCreate({ ...draft, name: draft.name.trim() })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Something went wrong while creating the project.')
    } finally {
      setSubmitting(false)
    }
  }

  const set = <K extends keyof NewProjectDraft>(key: K, value: NewProjectDraft[K]) => setDraft(current => ({ ...current, [key]: value }))
  const applyTemplate = (templateId: string) => {
    setDraft(current => applyProjectTemplateDraft(current, templateId, templates, labels))
  }

  return <div aria-label={t('Create project')} aria-modal="true" className={`lp-new-project${agentOpen ? ' lp-new-project--agent-open' : ''}`} onKeyDown={event => trapFocus(event, discardOpen ? '.lp-new-project__discard-dialog' : undefined)} role="dialog">
    <div className="lp-new-project__backdrop" />
    <div aria-hidden={discardOpen || undefined} className="lp-new-project__panel" inert={discardOpen || undefined}>
    <form className="lp-new-project__form" onSubmit={submit} ref={panelRef}>
      <header className="lp-new-project__header">
        {teams.length<2?<button aria-label="Change project teams" className="lp-new-project__team" disabled type="button"><ViewIconPickerGlyph color={teams[0]?.color} icon="Team"/><span>{teamLabel}</span></button>:<PropertyMenu compact multiple label="Teams" value={draft.teamIds.length===1?teams.find(team=>team.id===draft.teamIds[0])?.label??teamLabel:`${draft.teamIds.length} teams`} selectedIds={draft.teamIds} options={teams.map(team=>({id:team.id,label:team.label,color:team.color,i18nIgnore:true}))} trigger={<><ViewIconPickerGlyph color={teams.find(team=>draft.teamIds.includes(team.id))?.color??teams[0]?.color} icon="Team"/><span data-i18n-ignore>{draft.teamIds.length===1?teams.find(team=>team.id===draft.teamIds[0])?.label??teamLabel:`${draft.teamIds.length} teams`}</span></>} triggerClassName="lp-new-project__team" ariaLabel="Change project teams" onChange={id=>{if(draft.teamIds.includes(id)&&draft.teamIds.length===1)return;set('teamIds',draft.teamIds.includes(id)?draft.teamIds.filter(value=>value!==id):[...draft.teamIds,id])}}/>}
        <span>›</span><span>New project</span>
        {!agentOpen && <button aria-expanded={agentOpen} aria-label={t('Create with Agent')} className="lp-new-project__agent-toggle" onClick={() => setAgentOpen(true)} type="button"><AgentPointerIcon size={14}/><span>{t('Create with Agent')}</span></button>}
        <button aria-label="Discard project" className="lp-new-project__discard" disabled={submitting} onClick={requestClose} type="button"><X size={16}/></button>
      </header>
      <div className="lp-new-project__scroll">
        <div className="lp-new-project__identity">
          <ProjectIconPicker color={draft.color} icon={draft.icon} onChange={(icon, color) => setDraft(current => ({ ...current, icon, color }))} />
          <div>
            <input
              aria-invalid={nameError}
              aria-label="Project name"
              className="lp-new-project__name"
              onChange={event => {
                set('name', event.target.value)
                if (event.target.value.trim()) setNameError(false)
              }}
              placeholder="Project name"
              ref={nameRef}
              value={draft.name}
            />
            <input aria-label="Project summary" className="lp-new-project__summary" onChange={event => set('summary', event.target.value)} placeholder="Add a short summary…" value={draft.summary} />
          </div>
        </div>
        <div className="lp-new-project__properties">
          {templates.length > 0 && <ProjectDraftProperty icon={<LayoutTemplate size={14}/>} label="Apply project template" options={[{ id: '', label: 'No template' }, ...templates]} placeholder="Template" value={draft.templateId ?? ''} onChange={applyTemplate} />}
          <ProjectDraftProperty icon={statuses.find(status=>status.id===draft.status)?.icon??<ProjectStatusGlyph name={draft.status} type={projectStatusType(draft.status)}/>} label="Change project status" options={statuses} value={draft.status} onChange={value => set('status', value)} />
          <ProjectDraftProperty icon={<PriorityIcon priority={Math.max(0, PRIORITY.indexOf(draft.priority))} size={14}/>} label="Change project priority" options={PRIORITY.map((value, priority) => ({ id: value, icon: <PriorityIcon priority={priority} size={14}/>, label: value }))} value={draft.priority} onChange={value => set('priority', value)} />
          <PersonPicker ariaLabel="Set project lead" emptyOptionLabel="No lead" emptyOptionShortcut="0" emptyTriggerLabel="Lead" label="Lead" onChange={value => set('leadId', value || undefined)} optionHoverClassName="lp-new-project-person-hover" optionHoverContent={person => <PersonHoverPreview person={person} workspaceName={teamLabel}/>} people={leads.map(lead => ({ id: lead.id, label: lead.label, name: lead.name, email: lead.email, avatarUrl: lead.avatarUrl, color: lead.color, active: lead.active, disabled: lead.disabled, end: lead.end, groupId: lead.groupId, groupLabel: lead.groupLabel, hoverContent: lead.hoverContent, hoverClassName: lead.hoverClassName }))} searchPlaceholder="Set lead…" searchShortcut="P, then A" selectedId={draft.leadId} showUnselectedGroupWhenEmpty surfaceClassName="lp-new-project-picker__surface lp-new-project-person-picker__surface lp-new-project-person-picker__lead" triggerClassName="lp-new-project-picker__trigger" unselectedGroupLabel="Users from the project team" />
          <PersonPicker ariaLabel="Change project members" closeOnSelect emptyTriggerLabel="Members" icon={<MembersIcon size={14}/>} label="Members" multiple onChange={id => setDraft(current => ({ ...current, memberIds: current.memberIds.includes(id) ? current.memberIds.filter(value => value !== id) : [...current.memberIds, id] }))} optionHoverClassName="lp-new-project-person-hover" optionHoverContent={person => <PersonHoverPreview person={person} workspaceName={teamLabel}/>} people={members.map(member => ({ id: member.id, label: member.label, name: member.name, email: member.email, avatarUrl: member.avatarUrl, color: member.color, active: member.active, disabled: member.disabled, end: member.end, groupId: member.groupId, groupLabel: member.groupLabel, hoverContent: member.hoverContent, hoverClassName: member.hoverClassName }))} searchPlaceholder="Change members…" searchShortcut="P, then M" selectedIds={draft.memberIds} surfaceClassName="lp-new-project-picker__surface lp-new-project-person-picker__surface lp-new-project-person-picker__members" trigger={<>{members.find(member => member.id === draft.memberIds[0]) ? <UserAvatar avatarUrl={members.find(member => member.id === draft.memberIds[0])?.avatarUrl} className="avatar core-person-picker-avatar" color={members.find(member => member.id === draft.memberIds[0])?.color} name={members.find(member => member.id === draft.memberIds[0])?.label ?? ''}/> : <MembersIcon size={14}/>}<span>{draft.memberIds.length ? t(`${draft.memberIds.length} member${draft.memberIds.length === 1 ? '' : 's'}`) : t('Members')}</span></>} triggerClassName="lp-new-project-picker__trigger" unselectedGroupLabel="Users from the project team" />
          <DateChip kind="start" max={draft.targetDate} placeholder="Start" resolution={draft.startDateResolution} value={draft.startDate} onChange={(value, resolution) => setDraft(current => ({ ...current, startDate: value || undefined, startDateResolution: resolution }))} />
          <DateChip kind="target" min={draft.startDate} placeholder="Target" resolution={draft.targetDateResolution} value={draft.targetDate} onChange={(value, resolution) => setDraft(current => ({ ...current, targetDate: value || undefined, targetDateResolution: resolution }))} />
          <ProjectDraftProperty icon={<LayoutTemplate size={14}/>} label="Change project initiatives" multiple options={initiatives} placeholder="Initiatives" value={draft.initiativeIds} onChange={value => set('initiativeIds', value)} />
          <ProjectDraftProperty icon={<LabelIcon size={14}/>} label="Change labels" multiple options={labels} placeholder="Labels" value={draft.labelIds} onChange={value => set('labelIds', value)} />
          <ProjectDependencyPicker ariaLabel="Add dependencies" onChange={value => setDraft(current => ({ ...current, dependencyIds: value.filter(item => item.type === 'blocked_by').map(item => item.projectId), dependencyRelations: value }))} projects={dependencies.filter(project => !project.id.startsWith('__')).map(project => ({ id: project.id, label: project.label, icon: typeof project.icon === 'string' ? project.icon : undefined, color: project.color, group: project.group, keywords: [project.name, project.email].filter(Boolean).join(' '), disabled: project.disabled, previewData: project.previewData }))} triggerClassName="lp-new-project-picker__trigger lp-new-project-dependency-trigger" value={draft.dependencyRelations as ProjectDependencyValue[]} />
        </div>
        <textarea aria-label="Project description" className="lp-new-project__description" onChange={event => set('description', event.target.value)} placeholder="Write a description, a project brief, or collect ideas…" value={draft.description} />
        <MilestonesEditor
          milestones={draft.milestoneDetails ?? draft.milestones.map(name => ({ name }))}
          onChange={milestones => setDraft(current => ({ ...current, milestoneDetails: milestones, milestones: milestones.map(item => item.name) }))}
        />
      </div>
      <footer className="lp-new-project__footer">
        <button disabled={submitting} onClick={requestClose} type="button">Cancel</button>
        <button className="is-primary" disabled={submitting} type="submit">{submitting ? <span className="lp-new-project__spinner" /> : null}{submitting ? 'Creating…' : 'Create project'}</button>
      </footer>
    </form>
    <ProjectCreationAgent agentSkills={agentSkills} draft={draft} hidden={!agentOpen} onApplyDraft={patch => setDraft(current => mergeAgentDraft(current, patch, statuses, { dependencies, initiatives, labels, leads, members, teams }))} onClose={requestClose} onHide={() => setAgentOpen(false)} workspaceName={workspaceName ?? teamLabel} />
    </div>
    {error && <div className="lp-new-project__toast" role="alert"><span>!</span><div><strong>{nameError ? 'Project name required' : 'Could not create project'}</strong><p>{error}</p></div></div>}
    {discardOpen && <DiscardProjectDialog onCancel={() => setDiscardOpen(false)} onDiscard={() => { setDiscardOpen(false); onClose() }} />}
  </div>
}

function ProjectDraftProperty(props: {
  icon?: ReactNode
  label: string
  multiple?: false
  onChange: (value: string) => void
  options: NewProjectChoice[]
  placeholder?: string
  value: string
} | {
  icon?: ReactNode
  label: string
  multiple: true
  onChange: (value: string[]) => void
  options: NewProjectChoice[]
  placeholder: string
  value: string[]
}) {
  const { icon, label, options, placeholder, value } = props
  const multiple = props.multiple === true
  const selected = Array.isArray(value) ? options.filter(option => value.includes(option.id)) : options.find(option => option.id === value)
  const selectedIds = Array.isArray(value) ? value : [value]
  const display = Array.isArray(selected) ? selected.length ? `${placeholder} · ${selected.length}` : placeholder : selected?.label ?? placeholder ?? label
  const propertyOptions: PropertyOption[] = options.map(option => ({ id: option.id, label: option.label, color: option.color, groupId: option.groupId, groupLabel: option.groupLabel, icon: option.icon, i18nIgnore: Boolean(option.id) }))
  const choose = (id: string) => {
    if (Array.isArray(value)) {
      if (props.multiple) { const target = options.find(option => option.id === id); const next = target?.groupId ? value.filter(selectedId => options.find(option => option.id === selectedId)?.groupId !== target.groupId) : value; props.onChange(value.includes(id) ? value.filter(item => item !== id) : [...next, id]) }
    } else if (!props.multiple) props.onChange(id)
  }
  return <PropertyMenu
    ariaLabel={label}
    compact
    label={placeholder ?? label}
    multiple={multiple}
    onChange={choose}
    options={propertyOptions}
    searchPlaceholder={`${label.replace(/^(Change|Set|Add) /, '')}…`}
    selectedId={Array.isArray(value) ? undefined : value}
    selectedIds={selectedIds}
    surfaceClassName="lp-new-project-picker__surface"
    trigger={<>{icon ?? <span className="lp-new-project-picker__dot" style={{ background: Array.isArray(selected) ? selected[0]?.color : selected?.color }}/>}<span data-i18n-ignore={Boolean(Array.isArray(selected) ? selected.length : selected) || undefined}>{display}</span></>}
    triggerClassName="lp-new-project-picker__trigger"
    value={display}
    valueIsEntityName={Boolean(Array.isArray(selected) ? selected.length : selected)}
  />
}

function DateChip({ kind, max, min, onChange, placeholder, resolution, value }: { kind: 'start' | 'target', max?: string, min?: string, onChange: (value: string, resolution?: 'halfYear' | 'month' | 'quarter' | 'year') => void, placeholder: string, resolution?: 'halfYear' | 'month' | 'quarter' | 'year', value?: string }) {
  const label = kind === 'start' ? 'Start date' : 'Target date'
  const { locale, formatDate } = useI18n()
  const displayValue = value ? formatProjectPropertyDate(value, resolution, placeholder, locale, formatDate) : placeholder
  return <ProjectDatePicker align="start" buttonClassName="lp-new-project-date" contentClassName="lp-new-project-date__surface" label={label} max={max} min={min} onChange={onChange} resolution={resolution} value={value}><CalendarIcon size={14} variant={kind}/><span>{displayValue}</span></ProjectDatePicker>
}

function ProjectIconPicker({ color = '#5e6ad2', icon = 'Project', onChange }: { color?: string, icon?: string, onChange: (icon: string, color: string) => void }) {
  return <ViewIconPicker color={color} icon={normalizeProjectIcon(icon)} onChange={visual => onChange(visual.icon, visual.color)} triggerClassName="lp-new-project-icon-trigger"/>
}

function ViewIconPickerGlyph({ color = '#8a8f98', icon }: { color?: string, icon: string }) {
  return <svg aria-hidden="true" className="lp-new-project-team-icon" fill="currentColor" style={{ color }} viewBox="0 0 16 16"><use href={`#${icon}`}/></svg>
}

function MilestonesEditor({ milestones, onChange }: { milestones: NewProjectMilestoneDraft[], onChange: (value: NewProjectMilestoneDraft[]) => void }) {
  const { formatDate, locale, t } = useI18n()
  const sectionRef = useRef<HTMLElement>(null)
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [targetDate, setTargetDate] = useState('')
  const [dateOpen, setDateOpen] = useState(false)
  useEffect(() => {
    if (!adding) return
    requestAnimationFrame(() => sectionRef.current?.scrollIntoView({ block: 'end' }))
  }, [adding])
  const add = () => {
    if (!name.trim()) return
    onChange([...milestones, { name: name.trim(), description: description.trim() || undefined, targetDate: targetDate || undefined }])
    setName('')
    setDescription('')
    setTargetDate('')
    setAdding(false)
  }
  const cancel = () => { setName(''); setDescription(''); setTargetDate(''); setAdding(false) }
  if (adding) return <section className="lp-new-project__milestones lp-new-project__milestones--creating" ref={sectionRef}>
    <header><span>{t('Create milestone')}</span></header>
    <div className="lp-new-project__milestone-editor">
      <div className="lp-new-project__milestone-fields">
        <MilestoneOutline />
        <input autoFocus aria-label={t('Milestone name')} className="lp-new-project__milestone-name" onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') cancel(); if (event.key === 'Enter' && name.trim()) { event.preventDefault(); add() } }} placeholder={t('Milestone name')} value={name} />
        <ProjectDatePicker align="end" ariaLabel="Choose date" buttonClassName={`lp-new-project__milestone-date${dateOpen || targetDate ? ' has-value' : ''}`} compactCalendar contentClassName="lp-new-project__milestone-calendar" label="Target date" onChange={value => setTargetDate(value)} onOpenChange={setDateOpen} side="top" value={targetDate}>
          {targetDate ? <span>{locale === 'en-US' ? new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' }).format(new Date(`${targetDate}T00:00:00`)) : formatDate(`${targetDate}T00:00:00`, { month: 'short', day: 'numeric' })}</span> : dateOpen ? <span>{t('Target date')}</span> : <CalendarPlus size={16}/>}
        </ProjectDatePicker>
        <textarea aria-label={t('Milestone description')} className="lp-new-project__milestone-description" onChange={event => setDescription(event.target.value)} placeholder={t('Add a description…')} value={description}/>
      </div>
      <footer><button aria-label={t('Discard changes')} onClick={cancel} type="button">{t('Cancel')}</button><button aria-label={t('Add milestone')} className="is-primary" onClick={add} type="button">{t('Add milestone')}</button></footer>
    </div>
  </section>
  return <section className="lp-new-project__milestones" ref={sectionRef}>
    <header><button aria-expanded="true" className="lp-new-project__milestone-toggle" type="button"><ViewIconPickerGlyph icon="MilestoneNone"/><strong>{t('Milestones')}</strong></button><button aria-label={t('Add')} onClick={() => setAdding(true)} type="button"><PlusIcon /></button></header>
    {milestones.map((item, index) => <div className="lp-new-project__milestone-row" key={`${item.name}-${index}`}><MilestoneOutline/><span><strong>{item.name}</strong>{item.description && <small>{item.description}</small>}</span>{item.targetDate && <time dateTime={item.targetDate}>{formatDate(`${item.targetDate}T00:00:00`, { month: 'short', day: 'numeric' })}</time>}<button aria-label={`${t('Remove')} ${item.name}`} onClick={() => onChange(milestones.filter((_, itemIndex) => itemIndex !== index))} type="button"><X size={13}/></button></div>)}
  </section>
}

function MilestoneOutline() {
  return <svg aria-hidden="true" className="lp-new-project__milestone-outline" fill="none" viewBox="0 0 16 16"><path d="M7.341 2.32a.85.85 0 0 1 1.318 0l4.131 5.082a.95.95 0 0 1 0 1.196L8.659 13.68a.85.85 0 0 1-1.318 0L3.21 8.598a.95.95 0 0 1 0-1.196L7.341 2.32Z" stroke="currentColor" strokeWidth="2"/></svg>
}

function DiscardProjectDialog({ onCancel, onDiscard }: { onCancel: () => void; onDiscard: () => void }) {
  const { t } = useI18n()
  return <>
    <div aria-hidden="true" className="lp-new-project__discard-overlay" />
    <section aria-describedby="discard-project-description" aria-labelledby="discard-project-title" aria-modal="true" className="lp-new-project__discard-dialog" role="alertdialog">
      <h2 id="discard-project-title">{t('Discard changes?')}</h2>
      <p id="discard-project-description">{t('Are you sure you want to discard the changes you’ve made to this project?')}</p>
      <footer>
        <button onClick={onCancel} type="button">{t('Cancel')}</button>
        <button autoFocus className="is-danger" onClick={onDiscard} type="button">{t('Discard')}</button>
      </footer>
    </section>
  </>
}

function trapFocus(event: KeyboardEvent<HTMLDivElement>, scopeSelector?: string) {
  if (event.key !== 'Tab') return
  const scope = scopeSelector ? event.currentTarget.querySelector<HTMLElement>(scopeSelector) ?? event.currentTarget : event.currentTarget
  const focusables = [...scope.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), [tabindex="0"]')].filter(element => element.offsetParent !== null)
  if (!focusables.length) return
  const index = focusables.indexOf(document.activeElement as HTMLElement)
  if (!event.shiftKey && index === focusables.length - 1) {
    event.preventDefault()
    focusables[0].focus()
  } else if (event.shiftKey && index <= 0) {
    event.preventDefault()
    focusables[focusables.length - 1].focus()
  }
}

function emptyDraft(status: string, teamId?: string): NewProjectDraft {
  return { name: '', icon: 'Project', color: '#eb5757', summary: '', description: '', status, priority: 'No priority', memberIds: [], teamIds: teamId ? [teamId] : [], initiativeIds: [], labelIds: [], dependencyIds: [], dependencyRelations: [], milestones: [] }
}

function applyProjectTemplateDraft(current:NewProjectDraft,templateId:string|undefined,templates:NewProjectTemplateChoice[],labels:NewProjectChoice[]):NewProjectDraft{
  if(!templateId)return current
  const template=templates.find(item=>item.id===templateId)
  if(!template)return current
  const labelOptionIds=new Set(labels.map(label=>label.id))
  const dependencyIds = template.dependencyIds ? [...template.dependencyIds] : current.dependencyIds
  return {...current,templateId,name:template.name??template.label,summary:template.summary??'',description:template.description??'',icon:normalizeProjectIcon(template.icon),color:template.color??current.color,status:template.status??current.status,priority:template.priority??current.priority,teamIds:template.teamIds?.length?[...template.teamIds]:current.teamIds,initiativeIds:template.initiativeIds?[...template.initiativeIds]:current.initiativeIds,labelIds:template.labelIds?template.labelIds.filter(id=>labelOptionIds.has(id)):current.labelIds,dependencyIds,dependencyRelations:dependencyIds.map(projectId => ({ projectId, type: 'blocked_by' as const }))}
}

function projectStatusType(status: string) { return status === 'Backlog' ? 'backlog' : status === 'In Progress' ? 'started' : status === 'Completed' ? 'completed' : status === 'Canceled' ? 'canceled' : 'planned' }

function mergeAgentDraft(current: NewProjectDraft, patch: ProjectAgentDraft, statuses: NewProjectChoice[], choices: Pick<NewProjectDialogProps, 'dependencies' | 'initiatives' | 'labels' | 'leads' | 'members' | 'teams'>) {
  const next = { ...current }
  if (patch.name?.trim()) next.name = patch.name.trim()
  if (patch.summary?.trim()) next.summary = patch.summary.trim()
  if (patch.description?.trim()) next.description = patch.description.trim()
  if (patch.status?.trim()) {
    const match = statuses.find(option => option.id.toLowerCase() === patch.status!.trim().toLowerCase() || option.label.toLowerCase() === patch.status!.trim().toLowerCase())
    if (match) next.status = match.id
  }
  if (patch.priority?.trim()) {
    const priority = patch.priority.trim().toLowerCase()
    const normalizedPriority = priority === 'none' || priority === 'no priority' ? 'No priority' : patch.priority.trim()
    const match = PRIORITY.find(value => value.toLowerCase() === normalizedPriority.toLowerCase())
    if (match) next.priority = match
  }
  if (isIsoDate(patch.startDate)) next.startDate = patch.startDate
  if (isIsoDate(patch.targetDate)) next.targetDate = patch.targetDate
  if (patch.milestones?.length) {
    const milestoneDetails = current.milestoneDetails ?? current.milestones.map(name => ({ name }))
    const existingNames = new Set(milestoneDetails.map(item => item.name.toLocaleLowerCase()))
    const additions: NewProjectMilestoneDraft[] = []
    for (const value of patch.milestones) {
      const name = value.trim()
      const key = name.toLocaleLowerCase()
      if (!name || existingNames.has(key)) continue
      existingNames.add(key)
      additions.push({ name })
    }
    next.milestoneDetails = [...milestoneDetails, ...additions]
    next.milestones = next.milestoneDetails.map(item => item.name)
  }
  if (patch.team?.trim()) {
    const team = findChoice(patch.team, choices.teams ?? [])
    if (team) next.teamIds = [team.id]
  }
  if (patch.lead?.trim()) {
    if (/^(none|no lead|unassigned)$/i.test(patch.lead.trim())) next.leadId = undefined
    else {
      const lead = findChoice(patch.lead, choices.leads ?? [])
      if (lead) next.leadId = lead.id
    }
  }
  const memberIds = resolveChoices(patch.members, choices.members ?? [])
  if (memberIds.length) next.memberIds = memberIds
  const initiativeIds = resolveChoices(patch.initiatives, choices.initiatives ?? [])
  if (initiativeIds.length) next.initiativeIds = initiativeIds
  const labelIds = resolveChoices(patch.labels, choices.labels ?? [])
  if (labelIds.length) next.labelIds = labelIds
  const dependencyIds = resolveChoices(patch.dependencies, choices.dependencies ?? [])
  if (dependencyIds.length) {
    next.dependencyIds = dependencyIds
    next.dependencyRelations = dependencyIds.map(projectId => ({ projectId, type: 'blocked_by' as const }))
  }
  return next
}

function resolveChoices(values: string[] | undefined, options: NewProjectChoice[]) {
  return [...new Set((values ?? []).map(value => findChoice(value, options)?.id).filter((id): id is string => Boolean(id)))]
}

function findChoice(value: string, options: NewProjectChoice[]) {
  const needle = value.trim().toLocaleLowerCase()
  return options.find(option => [option.id, option.label, option.name, option.email].some(candidate => candidate?.trim().toLocaleLowerCase() === needle))
}

function isIsoDate(value: string | undefined): value is string {
  return Boolean(value && /^\d{4}-\d{2}-\d{2}$/.test(value) && !Number.isNaN(Date.parse(`${value}T00:00:00`)))
}
