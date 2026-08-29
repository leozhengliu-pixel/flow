import * as Popover from '@radix-ui/react-popover'
import * as Tooltip from '@radix-ui/react-tooltip'
import { Check, ChevronRight, Layers3, Plus } from 'lucide-react'
import { useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { LabelGroupIcon, LabelIcon, NoAssigneeIcon, ProjectIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { LabelHoverPreview } from './label-hover-preview'
import { usePropertyCommand } from './use-property-command'
import { groupOptionSections } from '@/lib/group-options'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import type { LabelResourceType } from '@/types/flow'

export interface PropertyOption {
  id: string
  label: string
  color?: string
  description?: string
  issueCount?: number
  scope?: string
  resourceType?: LabelResourceType
  groupId?: string
  groupLabel?: string
  groupColor?: string
  keywords?: string
  shortcut?: string
  icon?: ReactNode
  i18nIgnore?: boolean
}

export type PropertyMenuKind = 'standard' | 'labels' | 'project'

export function PropertyMenu({ label, value, icon, options, onChange, onCreate, multiple = false, selectedId, selectedIds = [], compact = false, searchPlaceholder, searchShortcut, kind: explicitKind, teamName = 'Cleantrack', trigger, customTrigger, triggerClassName, surfaceClassName, side = 'bottom', align = 'start', alignOffset = 0, ariaLabel, hoverContent, hoverClassName, valueIsEntityName = false }: {
  label: string
  value?: string
  icon?: ReactNode
  options: PropertyOption[]
  onChange?: (id: string) => void | Promise<void>
  onCreate?: (name: string) => void | Promise<void>
  multiple?: boolean
  selectedId?: string
  selectedIds?: string[]
  compact?: boolean
  searchPlaceholder?: string
  searchShortcut?: string
  kind?: PropertyMenuKind
  teamName?: string
  trigger?: ReactNode
  customTrigger?: (controls: { open: boolean; activeTrigger?: string; openMenu: (triggerId?: string) => void }) => ReactNode
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
  const [activeTrigger, setActiveTrigger] = useState<string>()
  const [hoverOpen, setHoverOpen] = useState(false)
  const [openLabelGroupId, setOpenLabelGroupId] = useState<string>()
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
  const labelMenu = groupLabelOptions(command.filteredOptions, selectedSet)
  const standardSections = groupOptionSections(command.filteredOptions)
  const noProject = command.filteredOptions.filter(option => !option.id)
  const projects = command.filteredOptions.filter(option => option.id)
  const createName = command.query.trim()
  const canCreateLabel = kind === 'labels' && Boolean(onCreate && createName && !options.some(option => option.label.toLocaleLowerCase() === createName.toLocaleLowerCase()))
  const createLabel = () => { if (!canCreateLabel || !onCreate) return; setOpen(false); void onCreate(createName) }
  const onCommandKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' && canCreateLabel && !command.filteredOptions.length) { event.preventDefault(); createLabel(); return }
    command.onKeyDown(event)
  }
  const placeholder = searchPlaceholder ?? (kind === 'labels' ? 'Change or add labels…' : kind === 'project' ? 'Add to project…' : `Change ${label.toLowerCase()}…`)
  const triggerButton = <button type="button" role="combobox" className={triggerClassName ?? (compact ? 'mini-property-trigger' : 'property-row')} aria-label={ariaLabel ?? `Change ${label}. Current value is ${value || 'none'}`} aria-haspopup="dialog" aria-expanded={open}>
        {trigger ?? (compact ? <>{icon ?? iconFor(label)}<span data-i18n-ignore={valueIsEntityName || undefined}>{value || label}</span></> : <><span>{icon ?? iconFor(label)}</span><span className="property-label">{label}</span><span className="property-value" data-i18n-ignore={valueIsEntityName || undefined}>{value || `Add ${label.toLowerCase()}`}</span></>)}
      </button>
  const popoverTrigger = <Popover.Trigger asChild>{triggerButton}</Popover.Trigger>
  const menuTrigger = customTrigger
    ? <Popover.Anchor asChild>{customTrigger({ open, activeTrigger, openMenu: triggerId => { setActiveTrigger(triggerId); setHoverOpen(false); setOpen(true) } })}</Popover.Anchor>
    : hoverContent ? <Tooltip.Trigger asChild>{popoverTrigger}</Tooltip.Trigger> : popoverTrigger
  return <Tooltip.Provider delayDuration={500} skipDelayDuration={0}><Tooltip.Root open={Boolean(hoverContent) && !open && hoverOpen} onOpenChange={setHoverOpen}><Popover.Root open={open} onOpenChange={next => { setOpen(next); if (next) setHoverOpen(false); else { setActiveTrigger(undefined); setOpenLabelGroupId(undefined) } }}>
    {menuTrigger}
    <Popover.Portal>
      <Popover.Content className={`property-command-surface property-command-${kind}${surfaceClassName ? ` ${surfaceClassName}` : ''}`} role="dialog" aria-label={`Change ${label}`} align={align} alignOffset={alignOffset} side={side} sideOffset={4} collisionPadding={10} onClick={event => event.stopPropagation()} onOpenAutoFocus={event => event.preventDefault()}>
        <div onKeyDown={onCommandKeyDown}>
          <div className="property-command-search">
            <input ref={command.inputRef} value={command.query} onFocus={() => setOpenLabelGroupId(undefined)} onChange={event => command.onQueryChange(event.target.value)} aria-label={placeholder} aria-controls={listboxId} aria-activedescendant={command.activeId ? `${listboxId}-${command.activeId || 'none'}` : undefined} placeholder={placeholder} autoComplete="off" spellCheck={false}/>
            {(searchShortcut ?? (kind === 'labels' ? 'L' : kind === 'project' ? 'Shift P' : undefined)) && <kbd>{searchShortcut ?? (kind === 'labels' ? 'L' : 'Shift P')}</kbd>}
          </div>
          <div id={listboxId} className="property-command-options" role="listbox" aria-label={label} aria-multiselectable={multiple || undefined} onWheel={event => event.stopPropagation()}>
            {kind === 'labels' && <>{labelMenu.selectedOptions.length > 0 && <><div className="property-command-group">Frequently used</div>{labelMenu.selectedOptions.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked listboxId={listboxId} icon={iconFor(label)} multi showGroupLabel onChoose={() => command.choose(option)} onActive={() => { setOpenLabelGroupId(undefined); command.setActiveId(option.id) }}/>)}</>}{(labelMenu.options.length > 0 || labelMenu.groups.length > 0) && <div className="property-command-group">Labels</div>}{labelMenu.groups.map(group => <LabelGroupOption key={group.id} group={group} open={openLabelGroupId === group.id} selectedIds={selectedSet} listboxId={listboxId} onOpenChange={next => setOpenLabelGroupId(next ? group.id : undefined)} onChoose={option => command.choose(option)}/>)}{labelMenu.options.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={false} listboxId={listboxId} icon={iconFor(label)} multi onChoose={() => command.choose(option)} onActive={() => { setOpenLabelGroupId(undefined); command.setActiveId(option.id) }}/>) }{canCreateLabel && <button type="button" className="property-command-create" role="option" aria-label={`Create new label: ${createName}`} onClick={createLabel}><Plus size={15}/><span>Create new label: <strong data-i18n-ignore>"{createName}"</strong></span></button>}</>}
            {kind === 'project' && <>{noProject.map(option => <CommandOption key="none" option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{projects.length > 0 && <div className="property-command-group">Projects in {teamName} team</div>}{projects.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{onCreate&&<><div className="property-command-group">New project</div><button type="button" className="property-command-create" role="option" aria-label="Create new project" onClick={()=>{setOpen(false);void onCreate('')}}><Plus size={15}/><span>Create new project…</span></button></>}</>}
            {kind === 'standard' && standardSections.map(section => <div className="property-command-section" key={section.id}>{section.label && <div className="property-command-group" data-i18n-ignore>{section.label}</div>}{section.options.map(option => <CommandOption key={option.id || 'none'} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} multi={multiple} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>)}</div>) }
            {!command.filteredOptions.length && !canCreateLabel && <div className="core-property-empty">No results</div>}
          </div>
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>{hoverContent&&<Tooltip.Portal><Tooltip.Content className={hoverClassName ?? 'property-hover-tooltip'} side="left" align="center" sideOffset={6} collisionPadding={8}>{hoverContent}</Tooltip.Content></Tooltip.Portal>}</Tooltip.Root></Tooltip.Provider>
}

function groupLabelOptions(options: PropertyOption[], selectedIds: Set<string>) {
  const direct: PropertyOption[] = []
  const groups = new Map<string, { id: string; label: string; color?: string; options: PropertyOption[] }>()
  for (const option of options) {
    if (!option.groupId || !option.groupLabel) { direct.push(option); continue }
    const group = groups.get(option.groupId) ?? { id: option.groupId, label: option.groupLabel, color: option.groupColor, options: [] }
    group.options.push(option)
    groups.set(option.groupId, group)
  }
  return {
    selectedOptions: [...direct.filter(option => selectedIds.has(option.id)), ...[...groups.values()].flatMap(group => group.options.filter(option => selectedIds.has(option.id)))],
    options: direct.filter(option => !selectedIds.has(option.id)),
    groups: [...groups.values()],
  }
}

function LabelGroupOption({ group, open, selectedIds, listboxId, onOpenChange, onChoose }: { group: { id: string; label: string; color?: string; options: PropertyOption[] }; open: boolean; selectedIds: Set<string>; listboxId: string; onOpenChange: (open: boolean) => void; onChoose: (option: PropertyOption) => void }) {
  const [query, setQuery] = useState('')
  const [activeId, setActiveId] = useState<string>()
  const inputRef = useRef<HTMLInputElement>(null)
  const filtered = query.trim() ? group.options.filter(option => option.label.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())) : group.options
  const changeOpen = (next: boolean) => {
    onOpenChange(next)
    if (!next) return
    setQuery('')
    setActiveId(group.options.find(option => selectedIds.has(option.id))?.id ?? group.options[0]?.id)
    requestAnimationFrame(() => inputRef.current?.focus())
  }
  const choose = (option: PropertyOption) => { onChoose(option); onOpenChange(false) }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onOpenChange(false); return }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const current = filtered.findIndex(option => option.id === activeId)
      const offset = event.key === 'ArrowDown' ? 1 : -1
      const next = current < 0 ? (offset > 0 ? 0 : filtered.length - 1) : (current + offset + filtered.length) % filtered.length
      if (filtered[next]) setActiveId(filtered[next].id)
      return
    }
    if (event.key === 'Enter') {
      const option = filtered.find(item => item.id === activeId) ?? filtered[0]
      if (option) { event.preventDefault(); choose(option) }
    }
  }
  return <Popover.Root open={open} onOpenChange={changeOpen}><Popover.Trigger asChild><button type="button" className="property-command-label-group-option" role="option" aria-selected={open} aria-haspopup="listbox" aria-expanded={open} onPointerEnter={() => changeOpen(true)} onFocus={() => changeOpen(true)} onClick={() => changeOpen(true)}>
    <span className="property-command-option-background"/><span className="property-command-checkbox"/><span className="property-command-icon"><LabelGroupIcon color={group.color ?? group.options[0]?.color ?? 'currentColor'} size={16}/></span><span className="property-command-group-copy"><span className="property-command-label">{group.label}</span></span><ChevronRight className="property-command-group-chevron" size={13}/>
  </button></Popover.Trigger><Popover.Portal><Popover.Content className="property-command-label-submenu" side="right" align="start" alignOffset={-6} sideOffset={-2} collisionPadding={10} onClick={event => event.stopPropagation()} onOpenAutoFocus={event => event.preventDefault()}><div onKeyDown={onKeyDown}><div className="property-command-search"><input ref={inputRef} value={query} onChange={event=>{setQuery(event.target.value);const normalized=event.target.value.trim().toLocaleLowerCase();setActiveId(group.options.find(option=>option.label.toLocaleLowerCase().includes(normalized))?.id)}} aria-label={group.label} placeholder={group.label} autoComplete="off" spellCheck={false}/></div><div className="property-command-options" role="listbox" aria-label={group.label}>{filtered.map(option => <CommandOption key={option.id} option={option} active={option.id === activeId} checked={selectedIds.has(option.id)} listboxId={`${listboxId}-${group.id}`} icon={<LabelIcon size={14}/>} onChoose={() => choose(option)} onActive={() => setActiveId(option.id)}/>)}{!filtered.length&&<div className="core-property-empty">No results</div>}</div></div></Popover.Content></Popover.Portal></Popover.Root>
}

function CommandOption({ option, active, checked, icon, listboxId, multi = false, showGroupLabel = false, onChoose, onActive }: { option: PropertyOption; active: boolean; checked: boolean; icon: ReactNode; listboxId: string; multi?: boolean; showGroupLabel?: boolean; onChoose: () => void; onActive: () => void }) {
  const row = <button type="button" className={showGroupLabel && option.groupLabel ? 'is-grouped-label' : undefined} id={`${listboxId}-${option.id || 'none'}`} role="option" aria-selected={active} aria-checked={checked} onPointerMove={onActive} onFocus={onActive} onClick={onChoose}>
    <span className="property-command-option-background"/>{multi && <span className="property-command-checkbox">{checked && <CheckboxMark/>}</span>}<span className="property-command-icon">{option.icon ?? (option.color ? <i className="option-dot" style={{ background: option.color }}/> : icon)}</span><span className="property-command-label" data-i18n-ignore={option.i18nIgnore || undefined}>{option.label}</span>{showGroupLabel && option.groupLabel && <span className="property-command-option-group" data-i18n-ignore>{option.groupLabel}</span>}{!multi && checked && <span className="property-command-check"><Check size={14}/></span>}{option.shortcut && <kbd>{option.shortcut}</kbd>}
  </button>
  return multi && option.color ? <LabelHoverPreview label={{ name: option.label, color: option.color, description: option.description, issueCount: option.issueCount, scope: option.scope, resourceType: option.resourceType }}>{row}</LabelHoverPreview> : row
}

function iconFor(label: string) {
  if (label === 'Priority') return <PriorityIcon priority={0} size={14}/>
  if (label === 'Assignee') return <NoAssigneeIcon size={14}/>
  if (label === 'Labels') return <LabelIcon size={14}/>
  if (label === 'Project') return <ProjectIcon size={14}/>
  return <Layers3 size={14}/>
}
