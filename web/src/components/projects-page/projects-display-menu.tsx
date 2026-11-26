import { Fragment, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type RefObject } from 'react'
import { chooseLabelGroup, defaultSubLabelGroupId, type LabelGroupOption } from '@/components/issue-explorer/issue-grouping'
import { ArrowDownUp, ChevronDown, GitBranch, LayoutGrid, List, Plus } from 'lucide-react'
import { usePropertyCommand, type PropertyCommandOption } from '@/components/property/use-property-command'
import { CheckIcon } from './projects-page-icons'
import { DEFAULT_PROJECTS_DISPLAY, projectDisplayProperties, projectLabelGroupProperty, projectsDisplayEqual, type ProjectsDisplaySettings } from './projects-display-model'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'
import { Toggle } from '@/components/ui/toggle'

type DisplayField = 'grouping' | 'subGrouping' | 'ordering' | 'showClosed' | 'labelGroup' | 'subLabelGroup'
type DisplayOption = PropertyCommandOption & { id: string; heading?: string; translatableHeading?: boolean }

const GROUPING_OPTIONS: DisplayOption[] = [
  { id: 'No grouping', label: 'No grouping' },
  { id: 'Initiative', label: 'Initiative' },
  { id: 'Lead', label: 'Lead' },
  { id: 'Member', label: 'Member' },
  { id: 'Status', label: 'Status' },
  { id: 'Status type', label: 'Status type' },
  { id: 'Priority', label: 'Priority' },
  { id: 'Label', label: 'Label' },
  { id: 'Label group', label: 'Label group' },
  { id: 'Customer', label: 'Customer' },
  { id: 'Lead team', label: 'Lead team' },
  { id: 'Team', label: 'Team' },
  { id: 'Health', label: 'Health' },
  { id: 'Start date', label: 'Start date' },
  { id: 'Target date', label: 'Target date' },
]

const SELECT_OPTIONS: Record<Exclude<DisplayField, 'labelGroup' | 'subLabelGroup'>, DisplayOption[]> = {
  grouping: GROUPING_OPTIONS,
  subGrouping: GROUPING_OPTIONS,
  ordering: [
    { id: 'Manual', label: 'Manual' },
    { id: 'Relevance', label: 'Relevance' },
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

const ALL_PROJECT_DISPLAY_PROPERTIES = projectDisplayProperties()

export function ProjectsDisplayMenu({ defaultSettings = DEFAULT_PROJECTS_DISPLAY, labelGroups = [], groupingLabelGroups = [], onChange, onReset, onSetDefault, properties = ALL_PROJECT_DISPLAY_PROPERTIES, rootRef, settings, teamScoped = false }: {
  /** The settings Reset returns to; the footer only appears once the current settings differ from it. */
  defaultSettings?: ProjectsDisplaySettings
  labelGroups?: Array<{ id: string; name: string }>
  /** Project label groups the "Label group" grouping offers, under "Workspace" and team headings; the grouping is hidden without any. */
  groupingLabelGroups?: LabelGroupOption[]
  onChange: (settings: ProjectsDisplaySettings) => void
  onReset?: () => void
  onSetDefault?: () => void
  /** Display properties offered, in order (feature-dependent ones already removed). */
  properties?: string[]
  rootRef?: RefObject<HTMLDivElement | null>
  settings: ProjectsDisplaySettings
  /** A team's projects page: offers "Only show lead team projects". */
  teamScoped?: boolean
}) {
  const [openField, setOpenField] = useState<DisplayField | null>(null)
  const [labelGroupsOpen, setLabelGroupsOpen] = useState(false)
  const set = <K extends keyof ProjectsDisplaySettings>(key: K, value: ProjectsDisplaySettings[K]) => onChange({ ...settings, [key]: value })
  const toggleProperty = (property: string) => set('properties', settings.properties.includes(property)
    ? settings.properties.filter(item => item !== property)
    : [...settings.properties, property])
  const grouped = settings.grouping !== 'No grouping'
  const board = settings.layout === 'board'
  // Customer needs the customer requests feature (its display properties are offered only then).
  const groupingOptions = GROUPING_OPTIONS.filter(option => (option.id !== 'Label group' || groupingLabelGroups.length > 0) && (option.id !== 'Customer' || properties.includes('Customers')))
  const labelGroupChoices: DisplayOption[] = groupingLabelGroups.map(group => ({ id: group.id, label: group.name, heading: group.heading, translatableHeading: group.translatableHeading }))
  const labelGroupName = (id?: string) => groupingLabelGroups.find(group => group.id === id)?.name ?? ''
  // Display values use the menu's grouping names; the label-group helpers use the issue grouping ids.
  const asIssueGrouping = <T extends { grouping: string; subGrouping: string }>(value: T) => ({ ...value, grouping: value.grouping === 'Label group' ? 'labelGroup' : value.grouping, subGrouping: value.subGrouping === 'Label group' ? 'labelGroup' : value.subGrouping })
  const primaryLabelGroupId = settings.labelGroupId ?? groupingLabelGroups[0]?.id
  const subLabelGroupId = settings.subLabelGroupId ?? defaultSubLabelGroupId(asIssueGrouping(settings), groupingLabelGroups)
  const chooseGroup = (level: 'grouping' | 'subGrouping', id: string) => { const next = chooseLabelGroup(asIssueGrouping(settings), level, id, groupingLabelGroups); onChange({ ...settings, labelGroupId: next.labelGroupId, subLabelGroupId: next.subLabelGroupId }) }
  const changeGrouping = (grouping: string) => onChange({ ...settings, grouping, ...(grouping === 'Label group' && !settings.labelGroupId ? { labelGroupId: groupingLabelGroups[0]?.id } : {}) })
  const changeSubGrouping = (subGrouping: string) => {
    // Picking the grouping's own value swaps the two levels (label groups instead pick a second group).
    if (subGrouping !== 'No grouping' && subGrouping === settings.grouping && subGrouping !== 'Label group') { onChange({ ...settings, grouping: settings.subGrouping === 'No grouping' ? subGrouping : settings.subGrouping, subGrouping: settings.subGrouping === 'No grouping' ? 'No grouping' : subGrouping }); return }
    onChange({ ...settings, subGrouping, ...(subGrouping === 'Label group' && !settings.subLabelGroupId ? { subLabelGroupId: defaultSubLabelGroupId(asIssueGrouping(settings), groupingLabelGroups) } : {}) })
  }
  const modified = !projectsDisplayEqual(settings, defaultSettings)
  // The design reference sizes every select to the widest one's text (99px to 144px); a label group's stops at 142px.
  // The layout tabs are the menu's first row; the menu is their parent (its own ref belongs to the caller).
  const modesRef = useRef<HTMLDivElement | null>(null)
  useLayoutEffect(() => {
    const menu = modesRef.current?.parentElement
    if (!menu) return
    const size = () => {
      const natural = Math.max(0, ...[...menu.querySelectorAll<HTMLElement>('.lp-projects-display-select__trigger > span')].map(span => Math.ceil(span.scrollWidth) + 26))
      menu.style.setProperty('--display-select-width', `${Math.max(99, Math.min(natural, 144))}px`)
    }
    size()
    const observer = new MutationObserver(size)
    observer.observe(menu, { subtree: true, childList: true, characterData: true })
    return () => observer.disconnect()
  }, [])
  const selectProps = (field: DisplayField) => ({ field, onOpenChange: (open: boolean) => setOpenField(open ? field : null), open: openField === field })

  return <div aria-label="Display options" className="lp-projects-display" ref={rootRef} role="dialog">
    <div aria-label="Project layout" className="lp-projects-display__modes" ref={modesRef} role="tablist">
      {LAYOUTS.map(({ icon: Icon, id, label }) => <button
        aria-selected={settings.layout === id}
        className={settings.layout === id ? 'is-active' : ''}
        key={id}
        onClick={() => onChange({ ...settings, layout: id, ...(id === 'board' ? { showEmptyGroups: true } : {}) })}
        role="tab"
        type="button"
      ><Icon aria-hidden="true" size={14} />{label}</button>)}
    </div>

    <div className="lp-projects-display__rows">
      <ProjectDisplaySelect {...selectProps('grouping')} direction={grouped ? settings.groupOrder : undefined} label={board ? 'Columns' : 'Grouping'} onChange={changeGrouping} onToggleDirection={() => set('groupOrder', settings.groupOrder === 'asc' ? 'desc' : 'asc')} options={groupingOptions} value={settings.grouping} />
      {settings.grouping === 'Label group' && primaryLabelGroupId && <ProjectDisplaySelect {...selectProps('labelGroup')} label="Label group" onChange={id => chooseGroup('grouping', id)} options={labelGroupChoices} value={primaryLabelGroupId} valueLabel={labelGroupName(primaryLabelGroupId)} />}
      {grouped && <ProjectDisplaySelect {...selectProps('subGrouping')} label={board ? 'Rows' : 'Sub-grouping'} onChange={changeSubGrouping} options={groupingOptions} value={settings.subGrouping} />}
      {grouped && settings.subGrouping === 'Label group' && subLabelGroupId && <ProjectDisplaySelect {...selectProps('subLabelGroup')} label="Label group" onChange={id => chooseGroup('subGrouping', id)} options={labelGroupChoices} value={subLabelGroupId} valueLabel={labelGroupName(subLabelGroupId)} />}
      <ProjectDisplaySelect {...selectProps('ordering')} direction={settings.ordering !== 'Manual' ? settings.orderingDirection : undefined} label="Ordering" onChange={value => set('ordering', value)} onToggleDirection={() => set('orderingDirection', settings.orderingDirection === 'asc' ? 'desc' : 'asc')} value={settings.ordering} />
    </div>
    <div className="lp-projects-display__separator" role="separator" />
    <div className="lp-projects-display__rows">
      <ProjectDisplaySelect {...selectProps('showClosed')} label="Show closed projects" onChange={value => set('showClosed', value)} value={settings.showClosed} />
      {teamScoped && <div className="lp-projects-display__row">
        <span>Only show lead team projects</span>
        <Toggle checked={Boolean(settings.onlyLeadTeamProjects)} label="Only show lead team projects" onChange={checked => set('onlyLeadTeamProjects', checked)}/>
      </div>}
    </div>
    <div className="lp-projects-display__separator" role="separator" />

    {(settings.layout === 'list' || settings.layout === 'board') && <section className="lp-projects-display__options">
      <h2>{settings.layout === 'board' ? 'Board options' : 'List options'}</h2>
      {grouped && <div className="lp-projects-display__row">
        <span>{board ? 'Show empty columns' : 'Show empty groups'}</span>
        <Toggle checked={settings.showEmptyGroups} label={board ? 'Show empty columns' : 'Show empty groups'} onChange={showEmptyGroups => set('showEmptyGroups', showEmptyGroups)}/>
      </div>}
      {grouped && board && settings.subGrouping !== 'No grouping' && <div className="lp-projects-display__row">
        <span>Show empty rows</span>
        <Toggle checked={Boolean(settings.showEmptyRows)} label="Show empty rows" onChange={showEmptyRows => set('showEmptyRows', showEmptyRows)}/>
      </div>}
    </section>}

    <section className="lp-projects-display__properties">
      <h3>Display properties</h3>
      <div>{properties.map(property => {
        const selected = settings.properties.includes(property)
        return <button
          aria-pressed={selected}
          className={selected ? 'is-selected' : ''}
          key={property}
          onClick={() => toggleProperty(property)}
          type="button"
        >{property}</button>
      })}{labelGroups.filter(group => settings.properties.includes(projectLabelGroupProperty(group.id))).map(group => <button
        aria-pressed="true"
        className="is-selected"
        key={group.id}
        onClick={() => toggleProperty(projectLabelGroupProperty(group.id))}
        type="button"
      ><span data-i18n-ignore>{group.name}</span></button>)}</div>
      {labelGroups.length > 0 && <div className="lp-projects-display__label-groups">
        <button aria-expanded={labelGroupsOpen} onClick={() => setLabelGroupsOpen(open => !open)} type="button"><Plus size={12}/>Add label group…</button>
        {labelGroupsOpen && <div aria-label="Add label group" role="listbox">{labelGroups.map(group => {
          const property = projectLabelGroupProperty(group.id)
          const selected = settings.properties.includes(property)
          return <button aria-selected={selected} key={group.id} onClick={() => { if (!selected) toggleProperty(property); setLabelGroupsOpen(false) }} role="option" type="button"><span data-i18n-ignore>{group.name}</span>{selected && <CheckIcon/>}</button>
        })}</div>}
      </div>}
    </section>

    {modified && <footer className="lp-projects-display__footer">
      <button aria-label="Reset to view default" onClick={onReset} type="button">Reset</button>
      <button aria-label="Save as default for view" onClick={onSetDefault} type="button">Set default for everyone</button>
    </footer>}
  </div>
}

function ProjectDisplaySelect({ direction, field, label, onChange, onOpenChange, onToggleDirection, open, options, value, valueLabel }: {
  direction?: 'asc' | 'desc'
  field: DisplayField
  label: string
  onChange: (value: string) => void
  onOpenChange: (open: boolean) => void
  onToggleDirection?: () => void
  open: boolean
  /** Choices when they are not the field's fixed list (grouping availability, label groups). */
  options?: DisplayOption[]
  value: string
  /** The trigger's text when the value is an id (a label group). */
  valueLabel?: string
}) {
  const listboxId = useId()
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const optionsRef = useRef<HTMLDivElement>(null)
  const command = usePropertyCommand<DisplayOption>({
    onOpenChange,
    onSelect: option => onChange(option.id),
    open,
    options: options ?? SELECT_OPTIONS[field as keyof typeof SELECT_OPTIONS],
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

  return <div className="lp-projects-display-select" data-label-group-select={field === "labelGroup" || field === "subLabelGroup" ? "" : undefined} ref={rootRef}>
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
      ><span data-i18n-ignore={valueLabel ? true : undefined}>{valueLabel ?? value}</span><ChevronDown aria-hidden="true" size={12} /></button>
    </div>
    {open && <div aria-label={`Choose ${label}`} className="lp-projects-display-select__menu" onKeyDown={command.onKeyDown} ref={optionsRef} role="listbox">
      {command.filteredOptions.map((option, index, list) => <Fragment key={option.id}>{option.heading !== undefined && option.heading !== list[index - 1]?.heading && <div className="lp-projects-display-select__heading" data-i18n-ignore={option.translatableHeading ? undefined : true} role="presentation">{option.heading}</div>}<button
        aria-selected={command.isSelected(option.id)}
        className={command.activeId === option.id ? 'is-active' : ''}
        id={`${listboxId}-${option.id}`}
        key={option.id}
        onClick={() => command.choose(option)}
        onMouseMove={() => command.setActiveId(option.id)}
        role="option"
        type="button"
      ><span data-i18n-ignore={option.heading !== undefined ? true : undefined}>{option.label}</span>{command.isSelected(option.id) && <CheckIcon />}</button></Fragment>)}
    </div>}
  </div>
}
