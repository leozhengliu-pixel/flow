import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { CheckIcon, PlusIcon } from './projects-page-icons'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'
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
  targetDate?: string
  labelIds: string[]
  dependencyIds: string[]
  milestones: string[]
}

export type NewProjectChoice = { id: string, label: string, color?: string, initials?: string }
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
  labelIds?: string[]
}

export type NewProjectDialogProps = {
  open: boolean
  teamLabel?: string
  defaultStatus?: string
  teams?: NewProjectChoice[]
  leads?: NewProjectChoice[]
  members?: NewProjectChoice[]
  labels?: NewProjectChoice[]
  dependencies?: NewProjectChoice[]
  templates?: NewProjectTemplateChoice[]
  onClose: () => void
  onCreate: (draft: NewProjectDraft) => Promise<void> | void
}

const STATUS = ['Backlog', 'Planned', 'In Progress', 'Completed', 'Canceled']
const PRIORITY = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
const COLORS = ['#5e6ad2', '#d15f5f', '#d09b42', '#4c9a67', '#4f8cc9', '#a36bc5', '#808087']
const DEFAULT_TEAMS: NewProjectChoice[] = [{ id: 'cle', label: 'Cleantrack' }]
const EMPTY_CHOICES: NewProjectChoice[] = []

export function NewProjectDialog({
  open,
  teamLabel = 'CLE',
  defaultStatus = 'Backlog',
  teams = DEFAULT_TEAMS,
  leads = EMPTY_CHOICES,
  members = EMPTY_CHOICES,
  labels = EMPTY_CHOICES,
  dependencies = EMPTY_CHOICES,
  templates = EMPTY_CHOICES,
  onClose,
  onCreate,
}: NewProjectDialogProps) {
  const panelRef = useRef<HTMLFormElement>(null)
  const nameRef = useRef<HTMLInputElement>(null)
  const defaultTeamId = teams[0]?.id
  const [draft, setDraft] = useState<NewProjectDraft>(() => emptyDraft(defaultStatus, defaultTeamId))
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [nameError, setNameError] = useState(false)

  useEffect(() => {
    if (!open) return
    setDraft(emptyDraft(defaultStatus, defaultTeamId))
    setError(null)
    setNameError(false)
    requestAnimationFrame(() => nameRef.current?.focus())
  }, [defaultStatus, defaultTeamId, open])

  useEffect(() => {
    if (!open) return
    const keydown = (event: globalThis.KeyboardEvent) => {
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
    const template = templates.find(item => item.id === templateId)
    if (!template) {
      set('templateId', undefined)
      return
    }
    setDraft(current => ({
      ...current,
      templateId,
      name: template.name ?? template.label,
      summary: template.summary ?? '',
      description: template.description ?? '',
      icon: template.icon,
      color: template.color,
      status: template.status ?? current.status,
      priority: template.priority ?? current.priority,
      teamIds: template.teamIds?.length ? [...template.teamIds] : current.teamIds,
      labelIds: template.labelIds ? [...template.labelIds] : current.labelIds,
    }))
  }

  return <div aria-label="Create project" aria-modal="true" className="lp-new-project" onKeyDown={trapFocus} role="dialog">
    <div className="lp-new-project__backdrop" />
    <form className="lp-new-project__panel" onSubmit={submit} ref={panelRef}>
      <header className="lp-new-project__header">
        <button aria-label="Change project teams" className="lp-new-project__team" disabled={teams.length < 2} type="button">{teamLabel}</button>
        <span>›</span><span>New project</span>
        <button aria-label="Discard project" className="lp-new-project__discard" disabled={submitting} onClick={onClose} type="button">×</button>
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
          {templates.length > 0 && <DraftPicker label="Apply project template" options={[{ id: '', label: 'No template' }, ...templates]} placeholder="Template" value={draft.templateId ?? ''} onChange={applyTemplate} />}
          <DraftPicker label="Change project status" options={STATUS.map(value => ({ id: value, label: value }))} value={draft.status} onChange={value => set('status', value)} />
          <DraftPicker label="Change project priority" options={PRIORITY.map(value => ({ id: value, label: value }))} value={draft.priority} onChange={value => set('priority', value)} />
          <DraftPicker label="Set project lead" options={[{ id: '', label: 'Lead' }, ...leads]} value={draft.leadId ?? ''} onChange={value => set('leadId', value)} />
          <DraftPicker label="Change project members" multiple options={members} placeholder="Members" value={draft.memberIds} onChange={value => set('memberIds', value)} />
          <DateChip label="Change project start date" placeholder="Start" value={draft.startDate} onChange={value => set('startDate', value)} />
          <DateChip label="Change project target date" placeholder="Target" value={draft.targetDate} onChange={value => set('targetDate', value)} />
          <DraftPicker label="Change labels" multiple options={labels} placeholder="Labels" value={draft.labelIds} onChange={value => set('labelIds', value)} />
          <DraftPicker label="Add dependencies" multiple options={dependencies} placeholder="Dependencies" value={draft.dependencyIds} onChange={value => set('dependencyIds', value)} />
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

function DraftPicker(props: {
  label: string
  multiple?: false
  onChange: (value: string) => void
  options: NewProjectChoice[]
  placeholder?: string
  value: string
} | {
  label: string
  multiple: true
  onChange: (value: string[]) => void
  options: NewProjectChoice[]
  placeholder: string
  value: string[]
}) {
  const { label, options, placeholder, value } = props
  const multiple = props.multiple === true
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLSpanElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const selected = Array.isArray(value) ? options.filter(option => value.includes(option.id)) : options.find(option => option.id === value)
  const command = usePropertyCommand({
    closeOnSelect: !multiple,
    open,
    options,
    selectedIds: Array.isArray(value) ? value : [value],
    onOpenChange: setOpen,
    onSelect: option => {
      if (Array.isArray(value)) {
        if (props.multiple) props.onChange(value.includes(option.id) ? value.filter(id => id !== option.id) : [...value, option.id])
      } else if (!props.multiple) props.onChange(option.id)
    },
  })
  useDismissibleLayer({ open, refs: [ref], onDismiss: () => setOpen(false), restoreFocusRef: triggerRef })
  return <span className="lp-new-project-picker" ref={ref}>
    <button ref={triggerRef} aria-expanded={open} aria-haspopup="listbox" aria-label={label} onClick={() => setOpen(current => !current)} type="button"><span className="lp-new-project-picker__dot" style={{ background: Array.isArray(selected) ? selected[0]?.color : selected?.color }} />{Array.isArray(selected) ? selected.length ? `${placeholder} · ${selected.length}` : placeholder : selected?.label ?? placeholder}</button>
    {open && <div className="lp-new-project-picker__surface" role="dialog">
      <input ref={command.inputRef} aria-label={`${label}…`} onChange={event => command.onQueryChange(event.target.value)} onKeyDown={command.onKeyDown} placeholder={`${label.replace(/^(Change|Set|Add) /, '')}…`} value={command.query} />
      <div role="listbox" aria-multiselectable={multiple || undefined} onKeyDown={command.onKeyDown}>{command.filteredOptions.map(option => {
        const checked = command.isSelected(option.id)
        return <button aria-checked={checked} aria-selected={option.id === command.activeId} key={option.id} onFocus={() => command.setActiveId(option.id)} onMouseEnter={() => command.setActiveId(option.id)} onClick={() => command.choose(option)} role="option" type="button"><span className="lp-new-project-picker__check">{checked && <CheckIcon />}</span><span className="lp-new-project-picker__dot" style={{ background: option.color }} />{option.label}</button>
      })}</div>
    </div>}
  </span>
}

function DateChip({ label, onChange, placeholder, value }: { label: string, onChange: (value: string) => void, placeholder: string, value?: string }) {
  return <label className="lp-new-project-date"><span>{value || placeholder}</span><input aria-label={label} onChange={event => onChange(event.target.value)} type="date" value={value ?? ''} /></label>
}

function ProjectIconPicker({ color = COLORS[0], icon = '◇', onChange }: { color?: string, icon?: string, onChange: (icon: string, color: string) => void }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLSpanElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  useDismissibleLayer({ open, refs: [ref], onDismiss: () => setOpen(false), restoreFocusRef: triggerRef })
  return <span className="lp-new-project-icon" ref={ref}>
    <button ref={triggerRef} aria-label="Choose icon" onClick={() => setOpen(current => !current)} style={{ color }} type="button">{icon}</button>
    {open && <div className="lp-new-project-icon__menu" role="dialog"><div>{['◇', '□', '○', '△', '✦', '⌁'].map(value => <button className={value === icon ? 'is-active' : ''} key={value} onClick={() => onChange(value, color)} type="button">{value}</button>)}</div><div>{COLORS.map(value => <button aria-label={`Use color ${value}`} className={value === color ? 'is-active' : ''} key={value} onClick={() => onChange(icon, value)} style={{ background: value }} type="button" />)}</div></div>}
  </span>
}

function MilestonesEditor({ milestones, onChange }: { milestones: string[], onChange: (value: string[]) => void }) {
  const [adding, setAdding] = useState(false)
  const [value, setValue] = useState('')
  const add = () => {
    if (value.trim()) onChange([...milestones, value.trim()])
    setValue('')
    setAdding(false)
  }
  return <section className="lp-new-project__milestones"><header><span>◇</span><strong>Milestones</strong><button aria-label="Add" onClick={() => setAdding(true)} type="button"><PlusIcon /></button></header>{milestones.map((item, index) => <div key={`${item}-${index}`}><span>◇</span><span>{item}</span><button aria-label={`Remove ${item}`} onClick={() => onChange(milestones.filter((_, itemIndex) => itemIndex !== index))} type="button">×</button></div>)}{adding && <div><span>◇</span><input autoFocus onChange={event => setValue(event.target.value)} onKeyDown={event => {
    if (event.key === 'Enter') add()
    if (event.key === 'Escape') setAdding(false)
  }} placeholder="Milestone name" value={value} /><button onClick={add} type="button">Add</button></div>}</section>
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
  return { name: '', summary: '', description: '', status, priority: 'No priority', memberIds: [], teamIds: teamId ? [teamId] : [], labelIds: [], dependencyIds: [], milestones: [] }
}
