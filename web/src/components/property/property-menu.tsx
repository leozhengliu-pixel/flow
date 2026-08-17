import * as Popover from '@radix-ui/react-popover'
import { Check, Layers3, Plus } from 'lucide-react'
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
  keywords?: string
  shortcut?: string
  icon?: ReactNode
}

export type PropertyMenuKind = 'standard' | 'labels' | 'project'

export function PropertyMenu({ label, value, icon, options, onChange, multiple = false, selectedId, selectedIds = [], compact = false, searchPlaceholder, searchShortcut, kind: explicitKind, teamName = 'Cleantrack', trigger, triggerClassName, ariaLabel }: {
  label: string
  value?: string
  icon?: ReactNode
  options: PropertyOption[]
  onChange?: (id: string) => void | Promise<void>
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
  ariaLabel?: string
}) {
  const [open, setOpen] = useState(false)
  const listboxId = useId()
  const selected = multiple ? selectedIds : [selectedId ?? options.find(option => option.label === value)?.id ?? '']
  const selectedSet = new Set(selected)
  const kind = explicitKind ?? (multiple && label === 'Labels' ? 'labels' : label === 'Project' ? 'project' : 'standard')
  const orderedOptions = multiple ? [...options].sort((left, right) => Number(selectedSet.has(right.id)) - Number(selectedSet.has(left.id))) : options
  const command = usePropertyCommand({
    closeOnSelect: !multiple,
    open,
    options: orderedOptions,
    selectedIds: selected,
    onOpenChange: setOpen,
    onSelect: option => onChange?.(option.id),
  })
  const selectedOptions = command.filteredOptions.filter(option => selectedSet.has(option.id))
  const remainingOptions = command.filteredOptions.filter(option => !selectedSet.has(option.id))
  const noProject = command.filteredOptions.filter(option => !option.id)
  const projects = command.filteredOptions.filter(option => option.id)
  const placeholder = kind === 'labels' ? 'Change or add labels…' : kind === 'project' ? 'Add to project…' : searchPlaceholder ?? `Change ${label.toLowerCase()}…`
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild>
      <button type="button" role="combobox" className={triggerClassName ?? (compact ? 'mini-property-trigger' : 'property-row')} aria-label={ariaLabel ?? `Change ${label}. Current value is ${value || 'none'}`} aria-haspopup="dialog" aria-expanded={open}>
        {trigger ?? (compact ? <>{icon ?? iconFor(label)}<span>{value || label}</span></> : <><span>{icon ?? iconFor(label)}</span><span className="property-label">{label}</span><span className="property-value">{value || `Add ${label.toLowerCase()}`}</span></>)}
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content className={`property-command-surface property-command-${kind}`} role="dialog" aria-label={`Change ${label}`} align="start" sideOffset={4} collisionPadding={10} onClick={event => event.stopPropagation()} onOpenAutoFocus={event => event.preventDefault()}>
        <div onKeyDown={command.onKeyDown}>
          <div className="property-command-search">
            <input ref={command.inputRef} value={command.query} onChange={event => command.onQueryChange(event.target.value)} aria-label={placeholder} aria-controls={listboxId} aria-activedescendant={command.activeId ? `${listboxId}-${command.activeId || 'none'}` : undefined} placeholder={placeholder} autoComplete="off" spellCheck={false}/>
            {(searchShortcut ?? (kind === 'labels' ? 'L' : kind === 'project' ? 'Shift P' : undefined)) && <kbd>{searchShortcut ?? (kind === 'labels' ? 'L' : 'Shift P')}</kbd>}
          </div>
          <div id={listboxId} className="property-command-options" role="listbox" aria-label={label} aria-multiselectable={multiple || undefined}>
            {kind === 'labels' && <>{selectedOptions.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} multi onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{selectedOptions.length > 0 && remainingOptions.length > 0 && <div className="property-command-separator" role="separator"/>}{remainingOptions.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} multi onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }</>}
            {kind === 'project' && <>{noProject.map(option => <CommandOption key="none" option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{projects.length > 0 && <div className="property-command-group">Projects in {teamName} team</div>}{projects.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }<div className="property-command-group">New project</div><button type="button" className="property-command-create" role="option" aria-label="Create new project" onClick={() => setOpen(false)}><Plus size={15}/><span>Create new project…</span></button></>}
            {kind === 'standard' && command.filteredOptions.map(option => <CommandOption key={option.id || 'none'} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }
            {!command.filteredOptions.length && <div className="core-property-empty">No results</div>}
          </div>
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function CommandOption({ option, active, checked, icon, listboxId, multi = false, onChoose, onActive }: { option: PropertyOption; active: boolean; checked: boolean; icon: ReactNode; listboxId: string; multi?: boolean; onChoose: () => void; onActive: () => void }) {
  const row = <button type="button" id={`${listboxId}-${option.id || 'none'}`} role="option" aria-selected={active} aria-checked={checked} onPointerMove={onActive} onFocus={onActive} onClick={onChoose}>
    <span className="property-command-option-background"/>{multi && <span className="property-command-checkbox">{checked && <Check size={12}/>}</span>}<span className="property-command-icon">{option.icon ?? (option.color ? <i className="option-dot" style={{ background: option.color }}/> : icon)}</span><span className="property-command-label">{option.label}</span>{!multi && checked && <span className="property-command-check"><Check size={14}/></span>}{option.shortcut && <kbd>{option.shortcut}</kbd>}
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
