import { useEffect, useId, useRef, useState, type KeyboardEvent, type RefObject } from 'react'
import { ArrowDownUp, ChevronDown, GitBranch, LayoutGrid, List } from 'lucide-react'
import { usePropertyCommand, type PropertyCommandOption } from '@/components/property/use-property-command'
import { CheckIcon } from './projects-page-icons'
import type { ProjectsDisplaySettings } from './projects-display-model'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'

const PROJECT_DISPLAY_PROPERTIES = [
  'Milestones', 'Summary', 'Priority', 'Status', 'Health', 'Teams', 'Lead', 'Members', 'Dependencies', 'Start date', 'Target date', 'Issues', 'Created', 'Updated', 'Completed', 'Customers', 'Customer revenue', 'Labels',
]

type DisplayField = 'grouping' | 'subGrouping' | 'ordering' | 'showClosed'
type DisplayOption = PropertyCommandOption & { id: string }

const GROUPING_OPTIONS: DisplayOption[] = [
  { id: 'No grouping', label: 'No grouping' },
  { id: 'Initiative', label: 'Initiative' },
  { id: 'Lead', label: 'Lead' },
  { id: 'Member', label: 'Member' },
  { id: 'Status', label: 'Status' },
  { id: 'Priority', label: 'Priority' },
  { id: 'Label', label: 'Label' },
  { id: 'Team', label: 'Team' },
  { id: 'Health', label: 'Health' },
  { id: 'Start date', label: 'Start date' },
  { id: 'Target date', label: 'Target date' },
]

const SELECT_OPTIONS: Record<DisplayField, DisplayOption[]> = {
  grouping: GROUPING_OPTIONS,
  subGrouping: GROUPING_OPTIONS,
  ordering: [
    { id: 'Manual', label: 'Manual' },
    { id: 'Name', label: 'Name' },
    { id: 'Status', label: 'Status' },
    { id: 'Priority', label: 'Priority' },
    { id: 'Updated', label: 'Updated' },
    { id: 'Created', label: 'Created' },
    { id: 'Health updated', label: 'Health updated' },
    { id: 'Start date', label: 'Start date' },
    { id: 'Target date', label: 'Target date' },
    { id: 'Customer count', label: 'Customer count' },
    { id: 'Customer revenue', label: 'Customer revenue' },
    { id: 'Important count', label: 'Important count' },
  ],
  showClosed: [
    { id: 'None', label: 'None' },
    { id: 'Past week', label: 'Past week' },
    { id: 'Past month', label: 'Past month' },
    { id: 'Past 3 months', label: 'Past 3 months' },
    { id: 'Past 6 months', label: 'Past 6 months' },
    { id: 'All', label: 'All' },
  ],
}

const LAYOUTS = [
  { id: 'list' as const, label: 'List', icon: List },
  { id: 'board' as const, label: 'Board', icon: LayoutGrid },
  { id: 'timeline' as const, label: 'Timeline', icon: GitBranch },
]

export function ProjectsDisplayMenu({ onChange, onReset, onSetDefault, rootRef, settings }: {
  onChange: (settings: ProjectsDisplaySettings) => void
  onReset?: () => void
  onSetDefault?: () => void
  rootRef?: RefObject<HTMLDivElement | null>
  settings: ProjectsDisplaySettings
}) {
  const [openField, setOpenField] = useState<DisplayField | null>(null)
  const set = <K extends keyof ProjectsDisplaySettings>(key: K, value: ProjectsDisplaySettings[K]) => onChange({ ...settings, [key]: value })
  const toggleProperty = (property: string) => set('properties', settings.properties.includes(property)
    ? settings.properties.filter(item => item !== property)
    : [...settings.properties, property])

  return <div aria-label="Display options" className="lp-projects-display" ref={rootRef} role="dialog">
    <div aria-label="Project layout" className="lp-projects-display__modes" role="tablist">
      {LAYOUTS.map(({ icon: Icon, id, label }) => <button
        aria-selected={settings.layout === id}
        className={settings.layout === id ? 'is-active' : ''}
        key={id}
        onClick={() => set('layout', id)}
        role="tab"
        type="button"
      ><Icon aria-hidden="true" size={13} />{label}</button>)}
    </div>

    <div className="lp-projects-display__selectors">
      <ProjectDisplaySelect direction={settings.groupOrder} field="grouping" label="Grouping" onChange={value => set('grouping', value)} onOpenChange={open => setOpenField(open ? 'grouping' : null)} onToggleDirection={() => set('groupOrder', settings.groupOrder === 'asc' ? 'desc' : 'asc')} open={openField === 'grouping'} value={settings.grouping} />
      <ProjectDisplaySelect field="subGrouping" label="Sub-grouping" onChange={value => set('subGrouping', value)} onOpenChange={open => setOpenField(open ? 'subGrouping' : null)} open={openField === 'subGrouping'} value={settings.subGrouping} />
      <ProjectDisplaySelect direction={settings.orderingDirection} field="ordering" label="Ordering" onChange={value => set('ordering', value)} onOpenChange={open => setOpenField(open ? 'ordering' : null)} onToggleDirection={() => set('orderingDirection', settings.orderingDirection === 'asc' ? 'desc' : 'asc')} open={openField === 'ordering'} value={settings.ordering} />
      <ProjectDisplaySelect field="showClosed" label="Show closed projects" onChange={value => set('showClosed', value)} onOpenChange={open => setOpenField(open ? 'showClosed' : null)} open={openField === 'showClosed'} value={settings.showClosed} />
    </div>

    {(settings.layout === 'list' || settings.layout === 'board') && <section className="lp-projects-display__section">
      <h2>{settings.layout === 'board' ? 'Board options' : 'List options'}</h2>
      <div className="lp-projects-display__check">
        <span>Show empty groups</span>
        <button
          aria-checked={settings.showEmptyGroups}
          aria-label="Show empty groups"
          className="lp-projects-display__check-control"
          onClick={() => set('showEmptyGroups', !settings.showEmptyGroups)}
          role="checkbox"
          type="button"
        ><i aria-hidden="true" /></button>
      </div>
    </section>}

    <section className="lp-projects-display__section lp-projects-display__properties">
      <h2>Display properties</h2>
      <div>{PROJECT_DISPLAY_PROPERTIES.map(property => {
        const selected = settings.properties.includes(property)
        return <button
          aria-pressed={selected}
          className={selected ? 'is-selected' : ''}
          key={property}
          onClick={() => toggleProperty(property)}
          type="button"
        >{property}</button>
      })}</div>
    </section>

    <footer className="lp-projects-display__footer">
      <button aria-label="Reset to view default" onClick={onReset} type="button">Reset</button>
      <button aria-label="Save as default for view" onClick={onSetDefault} type="button">Set default for everyone</button>
    </footer>
  </div>
}

function ProjectDisplaySelect({ direction, field, label, onChange, onOpenChange, onToggleDirection, open, value }: {
  direction?: 'asc' | 'desc'
  field: DisplayField
  label: string
  onChange: (value: string) => void
  onOpenChange: (open: boolean) => void
  onToggleDirection?: () => void
  open: boolean
  value: string
}) {
  const listboxId = useId()
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const optionsRef = useRef<HTMLDivElement>(null)
  const command = usePropertyCommand<DisplayOption>({
    onOpenChange,
    onSelect: option => onChange(option.id),
    open,
    options: SELECT_OPTIONS[field],
    selectedIds: [value],
  })

  useEffect(() => {
    if (!open) return
    requestAnimationFrame(() => optionsRef.current?.querySelector<HTMLButtonElement>('[aria-selected="true"]')?.focus())
  }, [open])
  useDismissibleLayer({ open, refs: [rootRef], onDismiss: () => onOpenChange(false), restoreFocusRef: triggerRef })

  const triggerKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp' || event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onOpenChange(!open)
    }
  }

  return <div className="lp-projects-display-select" ref={rootRef}>
    <span>{label}</span>
    <div className="lp-projects-display-select__actions">
      {direction && <button
        aria-label={field === 'grouping' ? 'Group ordering' : 'Direction'}
        className="lp-projects-display-select__direction"
        data-direction={direction}
        onClick={onToggleDirection}
        type="button"
      ><ArrowDownUp aria-hidden="true" size={13} /></button>}
      <button
        aria-controls={open ? listboxId : undefined}
        aria-expanded={open}
        aria-haspopup="listbox"
        className="lp-projects-display-select__trigger"
        onClick={() => onOpenChange(!open)}
        onKeyDown={triggerKeyDown}
        role="combobox"
        ref={triggerRef}
        type="button"
      ><span>{value}</span><ChevronDown aria-hidden="true" size={12} /></button>
    </div>
    {open && <div aria-label={`Choose ${label}`} className="lp-projects-display-select__menu" onKeyDown={command.onKeyDown} ref={optionsRef} role="listbox">
      {command.filteredOptions.map(option => <button
        aria-selected={command.isSelected(option.id)}
        className={command.activeId === option.id ? 'is-active' : ''}
        id={`${listboxId}-${option.id}`}
        key={option.id}
        onClick={() => command.choose(option)}
        onMouseMove={() => command.setActiveId(option.id)}
        role="option"
        type="button"
      ><span>{option.label}</span>{command.isSelected(option.id) && <CheckIcon />}</button>)}
    </div>}
  </div>
}
