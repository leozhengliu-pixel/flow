import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { LayoutTemplate, Link2, X } from 'lucide-react'
import { PlusIcon } from './projects-page-icons'
import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'
import { ViewIconPicker } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { CalendarIcon, LabelIcon, MembersIcon, NoAssigneeIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { ProjectStatusGlyph } from './project-property-picker'
import { ProjectDatePicker } from './project-target-date-picker'
import { formatProjectPropertyDate } from '@/components/project-detail/project-detail-helpers'
import { useI18n } from '@/i18n/i18n'
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
  milestones: string[]
}

export type NewProjectChoice = { id: string, label: string, color?: string, groupId?: string, groupLabel?: string, icon?: ReactNode, initials?: string }
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
  statuses = DEFAULT_STATUSES,
  templates = EMPTY_TEMPLATES,
  onClose,
  onCreate,
}: NewProjectDialogProps) {
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

  useEffect(() => {
    if (!open) return
    setDraft(applyProjectTemplateDraft(emptyDraft(defaultStatus, defaultTeamId), initialTemplateId, templatesRef.current, labelsRef.current))
    setError(null)
    setNameError(false)
    requestAnimationFrame(() => nameRef.current?.focus())
  }, [defaultStatus, defaultTeamId, initialTemplateId, labelSignature, open, templateSignature])

  useEffect(() => {
    if (!open) return
    const keydown = (event: globalThis.KeyboardEvent) => {
      if (event.defaultPrevented) return
      if (event.key === 'Escape' && document.querySelector('[data-radix-popper-content-wrapper]')) return
      if (event.key === 'Escape' && !submitting) onClose()
    }
    document.addEventListener('keydown', keydown)
    return () => document.removeEventListener('keydown', keydown)
  }, [onClose, open, submitting])

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

  return <div aria-label="Create project" aria-modal="true" className="lp-new-project" onKeyDown={trapFocus} role="dialog">
    <div className="lp-new-project__backdrop" />
    <form className="lp-new-project__panel" onSubmit={submit} ref={panelRef}>
      <header className="lp-new-project__header">
        {teams.length<2?<button aria-label="Change project teams" className="lp-new-project__team" disabled type="button"><ViewIconPickerGlyph color={teams[0]?.color} icon="Team"/><span>{teamLabel}</span></button>:<PropertyMenu compact multiple label="Teams" value={draft.teamIds.length===1?teams.find(team=>team.id===draft.teamIds[0])?.label??teamLabel:`${draft.teamIds.length} teams`} selectedIds={draft.teamIds} options={teams.map(team=>({id:team.id,label:team.label,color:team.color,i18nIgnore:true}))} trigger={<><ViewIconPickerGlyph color={teams.find(team=>draft.teamIds.includes(team.id))?.color??teams[0]?.color} icon="Team"/><span data-i18n-ignore>{draft.teamIds.length===1?teams.find(team=>team.id===draft.teamIds[0])?.label??teamLabel:`${draft.teamIds.length} teams`}</span></>} triggerClassName="lp-new-project__team" ariaLabel="Change project teams" onChange={id=>{if(draft.teamIds.includes(id)&&draft.teamIds.length===1)return;set('teamIds',draft.teamIds.includes(id)?draft.teamIds.filter(value=>value!==id):[...draft.teamIds,id])}}/>}
        <span>›</span><span>New project</span>
        <button aria-label="Discard project" className="lp-new-project__discard" disabled={submitting} onClick={onClose} type="button"><X size={16}/></button>
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
          <ProjectDraftProperty icon={<NoAssigneeIcon size={14}/>} label="Set project lead" options={[{ id: '', icon: <NoAssigneeIcon size={14}/>, label: 'Lead' }, ...leads]} value={draft.leadId ?? ''} onChange={value => set('leadId', value)} />
          <ProjectDraftProperty icon={<MembersIcon size={14}/>} label="Change project members" multiple options={members} placeholder="Members" value={draft.memberIds} onChange={value => set('memberIds', value)} />
          <DateChip kind="start" max={draft.targetDate} placeholder="Start" resolution={draft.startDateResolution} value={draft.startDate} onChange={(value, resolution) => setDraft(current => ({ ...current, startDate: value || undefined, startDateResolution: resolution }))} />
          <DateChip kind="target" min={draft.startDate} placeholder="Target" resolution={draft.targetDateResolution} value={draft.targetDate} onChange={(value, resolution) => setDraft(current => ({ ...current, targetDate: value || undefined, targetDateResolution: resolution }))} />
          <ProjectDraftProperty icon={<LayoutTemplate size={14}/>} label="Change project initiatives" multiple options={initiatives} placeholder="Initiatives" value={draft.initiativeIds} onChange={value => set('initiativeIds', value)} />
          <ProjectDraftProperty icon={<LabelIcon size={14}/>} label="Change labels" multiple options={labels} placeholder="Labels" value={draft.labelIds} onChange={value => set('labelIds', value)} />
          <ProjectDraftProperty icon={<Link2 size={14}/>} label="Add dependencies" multiple options={dependencies} placeholder="Dependencies" value={draft.dependencyIds} onChange={value => set('dependencyIds', value)} />
        </div>
        <textarea aria-label="Project description" className="lp-new-project__description" onChange={event => set('description', event.target.value)} placeholder="Write a description, a project brief, or collect ideas…" value={draft.description} />
        <MilestonesEditor milestones={draft.milestones} onChange={value => set('milestones', value)} />
      </div>
      <footer className="lp-new-project__footer">
        <button disabled={submitting} onClick={onClose} type="button">Cancel</button>
        <button className="is-primary" disabled={submitting} type="submit">{submitting ? <span className="lp-new-project__spinner" /> : null}{submitting ? 'Creating…' : 'Create project'}</button>
      </footer>
    </form>
    {error && <div className="lp-new-project__toast" role="alert"><span>!</span><div><strong>{nameError ? 'Project name required' : 'Could not create project'}</strong><p>{error}</p></div></div>}
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

function MilestonesEditor({ milestones, onChange }: { milestones: string[], onChange: (value: string[]) => void }) {
  const [adding, setAdding] = useState(false)
  const [open, setOpen] = useState(true)
  const [value, setValue] = useState('')
  const add = () => {
    if (value.trim()) onChange([...milestones, value.trim()])
    setValue('')
    setAdding(false)
  }
  return <section className="lp-new-project__milestones"><header><button aria-expanded={open} className="lp-new-project__milestone-toggle" onClick={() => setOpen(value => !value)} type="button"><ViewIconPickerGlyph icon="MilestoneNone"/><strong>Milestones</strong></button><button aria-label="Add" onClick={() => { setOpen(true); setAdding(true) }} type="button"><PlusIcon /></button></header>{open&&<>{milestones.map((item, index) => <div key={`${item}-${index}`}><ViewIconPickerGlyph icon="MilestoneNone"/><span>{item}</span><button aria-label={`Remove ${item}`} onClick={() => onChange(milestones.filter((_, itemIndex) => itemIndex !== index))} type="button"><X size={13}/></button></div>)}{adding && <div><ViewIconPickerGlyph icon="MilestoneNone"/><input autoFocus onChange={event => setValue(event.target.value)} onKeyDown={event => {
    if (event.key === 'Enter') add()
    if (event.key === 'Escape') setAdding(false)
  }} placeholder="Milestone name" value={value} /><button onClick={add} type="button">Add</button></div>}</>}</section>
}

function trapFocus(event: KeyboardEvent<HTMLDivElement>) {
  if (event.key !== 'Tab') return
  const focusables = [...event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), [tabindex="0"]')].filter(element => element.offsetParent !== null)
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
  return { name: '', icon: 'Project', color: '#eb5757', summary: '', description: '', status, priority: 'No priority', memberIds: [], teamIds: teamId ? [teamId] : [], initiativeIds: [], labelIds: [], dependencyIds: [], milestones: [] }
}

function applyProjectTemplateDraft(current:NewProjectDraft,templateId:string|undefined,templates:NewProjectTemplateChoice[],labels:NewProjectChoice[]):NewProjectDraft{
  if(!templateId)return current
  const template=templates.find(item=>item.id===templateId)
  if(!template)return current
  const labelOptionIds=new Set(labels.map(label=>label.id))
  return {...current,templateId,name:template.name??template.label,summary:template.summary??'',description:template.description??'',icon:normalizeProjectIcon(template.icon),color:template.color??current.color,status:template.status??current.status,priority:template.priority??current.priority,teamIds:template.teamIds?.length?[...template.teamIds]:current.teamIds,initiativeIds:template.initiativeIds?[...template.initiativeIds]:current.initiativeIds,labelIds:template.labelIds?template.labelIds.filter(id=>labelOptionIds.has(id)):current.labelIds}
}

function projectStatusType(status: string) { return status === 'Backlog' ? 'backlog' : status === 'In Progress' ? 'started' : status === 'Completed' ? 'completed' : status === 'Canceled' ? 'canceled' : 'planned' }
