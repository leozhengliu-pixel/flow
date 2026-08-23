import * as Popover from '@radix-ui/react-popover'
import * as Tooltip from '@radix-ui/react-tooltip'
import { Check, ChevronRight, Layers3, Plus } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import { LabelIcon, NoAssigneeIcon, ProjectIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { LabelHoverPreview } from './label-hover-preview'
import { usePropertyCommand } from './use-property-command'

export interface PropertyOption {
  id: string
  label: string
  color?: string
  description?: string
  issueCount?: number
  scope?: string
  groupId?: string
  groupLabel?: string
  keywords?: string
  shortcut?: string
  icon?: ReactNode
  i18nIgnore?: boolean
}

export type PropertyMenuKind = 'standard' | 'labels' | 'project'

export function PropertyMenu({ label, value, icon, options, onChange, onCreate, multiple = false, selectedId, selectedIds = [], compact = false, searchPlaceholder, searchShortcut, kind: explicitKind, teamName = 'Cleantrack', trigger, triggerClassName, surfaceClassName, side = 'bottom', align = 'start', alignOffset = 0, ariaLabel, hoverContent, hoverClassName, valueIsEntityName = false }: {
  label: string
  value?: string
  icon?: ReactNode
  options: PropertyOption[]
  onChange?: (id: string) => void | Promise<void>
  onCreate?: () => void
  multiple?: boolean
  selectedId?: string
  selectedIds?: string[]
  compact?: boolean
  searchPlaceholder?: string
  searchShortcut?: string
  kind?: PropertyMenuKind
  teamName?: string
  trigger?: ReactNode
  triggerClassName?: string
  surfaceClassName?: string
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  alignOffset?: number
  ariaLabel?: string
  hoverContent?: ReactNode
  hoverClassName?: string
  valueIsEntityName?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [hoverOpen, setHoverOpen] = useState(false)
  const listboxId = useId()
  const selected = multiple ? selectedIds : [selectedId ?? options.find(option => option.label === value)?.id ?? '']
  const selectedSet = new Set(selected)
  const kind = explicitKind ?? (multiple && label === 'Labels' ? 'labels' : label === 'Project' ? 'project' : 'standard')
  const orderedOptions = multiple && kind !== 'labels' ? [...options].sort((left, right) => Number(selectedSet.has(right.id)) - Number(selectedSet.has(left.id))) : options
  const command = usePropertyCommand({
    closeOnSelect: !multiple,
    open,
    options: orderedOptions,
    selectedIds: selected,
    onOpenChange: setOpen,
    onSelect: option => onChange?.(option.id),
  })
  const labelMenu = groupLabelOptions(command.filteredOptions)
  const standardSections = groupStandardOptions(command.filteredOptions)
  const noProject = command.filteredOptions.filter(option => !option.id)
  const projects = command.filteredOptions.filter(option => option.id)
  const placeholder = searchPlaceholder ?? (kind === 'labels' ? 'Change or add labels…' : kind === 'project' ? 'Add to project…' : `Change ${label.toLowerCase()}…`)
  const triggerButton = <button type="button" role="combobox" className={triggerClassName ?? (compact ? 'mini-property-trigger' : 'property-row')} aria-label={ariaLabel ?? `Change ${label}. Current value is ${value || 'none'}`} aria-haspopup="dialog" aria-expanded={open}>
        {trigger ?? (compact ? <>{icon ?? iconFor(label)}<span data-i18n-ignore={valueIsEntityName || undefined}>{value || label}</span></> : <><span>{icon ?? iconFor(label)}</span><span className="property-label">{label}</span><span className="property-value" data-i18n-ignore={valueIsEntityName || undefined}>{value || `Add ${label.toLowerCase()}`}</span></>)}
      </button>
  const popoverTrigger = <Popover.Trigger asChild>{triggerButton}</Popover.Trigger>
  return <Tooltip.Provider delayDuration={500} skipDelayDuration={0}><Tooltip.Root open={Boolean(hoverContent) && !open && hoverOpen} onOpenChange={setHoverOpen}><Popover.Root open={open} onOpenChange={next => { setOpen(next); if (next) setHoverOpen(false) }}>
    {hoverContent ? <Tooltip.Trigger asChild>{popoverTrigger}</Tooltip.Trigger> : popoverTrigger}
    <Popover.Portal>
      <Popover.Content className={`property-command-surface property-command-${kind}${surfaceClassName ? ` ${surfaceClassName}` : ''}`} role="dialog" aria-label={`Change ${label}`} align={align} alignOffset={alignOffset} side={side} sideOffset={4} collisionPadding={10} onClick={event => event.stopPropagation()} onOpenAutoFocus={event => event.preventDefault()}>
        <div onKeyDown={command.onKeyDown}>
          <div className="property-command-search">
            <input ref={command.inputRef} value={command.query} onChange={event => command.onQueryChange(event.target.value)} aria-label={placeholder} aria-controls={listboxId} aria-activedescendant={command.activeId ? `${listboxId}-${command.activeId || 'none'}` : undefined} placeholder={placeholder} autoComplete="off" spellCheck={false}/>
            {(searchShortcut ?? (kind === 'labels' ? 'L' : kind === 'project' ? 'Shift P' : undefined)) && <kbd>{searchShortcut ?? (kind === 'labels' ? 'L' : 'Shift P')}</kbd>}
          </div>
          <div id={listboxId} className="property-command-options" role="listbox" aria-label={label} aria-multiselectable={multiple || undefined} onWheel={event => event.stopPropagation()}>
            {kind === 'labels' && <>{(labelMenu.options.length > 0 || labelMenu.groups.length > 0) && <div className="property-command-group">Labels</div>}{labelMenu.options.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} multi onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>)}{labelMenu.groups.map(group => <LabelGroupOption key={group.id} group={group} selectedIds={selectedSet} listboxId={listboxId} activeId={command.activeId} onChoose={option => command.choose(option)} onActive={command.setActiveId}/>)}</>}
            {kind === 'project' && <>{noProject.map(option => <CommandOption key="none" option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{projects.length > 0 && <div className="property-command-group">Projects in {teamName} team</div>}{projects.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{onCreate&&<><div className="property-command-group">New project</div><button type="button" className="property-command-create" role="option" aria-label="Create new project" onClick={()=>{setOpen(false);onCreate()}}><Plus size={15}/><span>Create new project…</span></button></>}</>}
            {kind === 'standard' && standardSections.map(section => <div className="property-command-section" key={section.id}>{section.label && <div className="property-command-group" data-i18n-ignore>{section.label}</div>}{section.options.map(option => <CommandOption key={option.id || 'none'} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} multi={multiple} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>)}</div>) }
            {!command.filteredOptions.length && <div className="core-property-empty">No results</div>}
          </div>
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>{hoverContent&&<Tooltip.Portal><Tooltip.Content className={hoverClassName ?? 'property-hover-tooltip'} side="left" align="center" sideOffset={6} collisionPadding={8}>{hoverContent}</Tooltip.Content></Tooltip.Portal>}</Tooltip.Root></Tooltip.Provider>
}

function groupLabelOptions(options: PropertyOption[]) {
  const direct: PropertyOption[] = []
  const groups = new Map<string, { id: string; label: string; options: PropertyOption[] }>()
  for (const option of options) {
    if (!option.groupId || !option.groupLabel) { direct.push(option); continue }
    const group = groups.get(option.groupId) ?? { id: option.groupId, label: option.groupLabel, options: [] }
    group.options.push(option)
    groups.set(option.groupId, group)
  }
  return { options: direct, groups: [...groups.values()] }
}

function groupStandardOptions(options: PropertyOption[]) {
  const sections: Array<{ id: string; label?: string; options: PropertyOption[] }> = []
  const indexes = new Map<string, number>()
  for (const option of options) {
    const id = option.groupId ?? option.groupLabel ?? 'ungrouped'
    let index = indexes.get(id)
    if (index === undefined) {
      index = sections.length
      indexes.set(id, index)
      sections.push({ id, label: id === 'ungrouped' ? undefined : option.groupLabel, options: [] })
    }
    sections[index].options.push(option)
  }
  return sections
}

function LabelGroupOption({ group, selectedIds, listboxId, activeId, onChoose, onActive }: { group: { id: string; label: string; options: PropertyOption[] }; selectedIds: Set<string>; listboxId: string; activeId?: string; onChoose: (option: PropertyOption) => void; onActive: (id: string) => void }) {
  const [open, setOpen] = useState(false)
  return <Popover.Root open={open} onOpenChange={setOpen}><Popover.Trigger asChild><button type="button" className="property-command-label-group-option" role="option" aria-selected={open} aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen(true)}>
    <span className="property-command-option-background"/><span className="property-command-icon"><Layers3 size={14}/></span><span className="property-command-label">{group.label}</span><ChevronRight className="property-command-group-chevron" size={13}/>
  </button></Popover.Trigger><Popover.Portal><Popover.Content className="property-command-label-submenu" side="right" align="start" sideOffset={4} collisionPadding={10} onClick={event => event.stopPropagation()} onOpenAutoFocus={event => event.preventDefault()}><div className="property-command-options" role="listbox" aria-label={group.label} aria-multiselectable="true">{group.options.map(option => <CommandOption key={option.id} option={option} active={option.id === activeId} checked={selectedIds.has(option.id)} listboxId={`${listboxId}-${group.id}`} icon={<LabelIcon size={14}/>} multi onChoose={() => onChoose(option)} onActive={() => onActive(option.id)}/>)}</div></Popover.Content></Popover.Portal></Popover.Root>
}

function CommandOption({ option, active, checked, icon, listboxId, multi = false, onChoose, onActive }: { option: PropertyOption; active: boolean; checked: boolean; icon: ReactNode; listboxId: string; multi?: boolean; onChoose: () => void; onActive: () => void }) {
  const row = <button type="button" id={`${listboxId}-${option.id || 'none'}`} role="option" aria-selected={active} aria-checked={checked} onPointerMove={onActive} onFocus={onActive} onClick={onChoose}>
    <span className="property-command-option-background"/>{multi && <span className="property-command-checkbox">{checked && <Check size={12}/>}</span>}<span className="property-command-icon">{option.icon ?? (option.color ? <i className="option-dot" style={{ background: option.color }}/> : icon)}</span><span className="property-command-label" data-i18n-ignore={option.i18nIgnore || undefined}>{option.label}</span>{!multi && checked && <span className="property-command-check"><Check size={14}/></span>}{option.shortcut && <kbd>{option.shortcut}</kbd>}
  </button>
  return multi && option.color ? <LabelHoverPreview label={{ name: option.label, color: option.color, description: option.description, issueCount: option.issueCount, scope: option.scope }}>{row}</LabelHoverPreview> : row
}

function iconFor(label: string) {
  if (label === 'Priority') return <PriorityIcon priority={0} size={14}/>
  if (label === 'Assignee') return <NoAssigneeIcon size={14}/>
  if (label === 'Labels') return <LabelIcon size={14}/>
  if (label === 'Project') return <ProjectIcon size={14}/>
  return <Layers3 size={14}/>
}
