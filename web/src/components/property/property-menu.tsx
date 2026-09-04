import * as Popover from '@radix-ui/react-popover'
import * as Tooltip from '@radix-ui/react-tooltip'
import { Check, ChevronRight, Layers3, Plus } from 'lucide-react'
import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { LabelGroupIcon, LabelIcon, NoAssigneeIcon, ProjectIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { LabelHoverPreview } from './label-hover-preview'
import { usePropertyCommand } from './use-property-command'
import { groupOptionSections } from '@/lib/group-options'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import type { LabelResourceType } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'

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
  end?: string
  disabled?: boolean
  icon?: ReactNode
  i18nIgnore?: boolean
  hoverContent?: ReactNode
  hoverClassName?: string
}

export type PropertyMenuKind = 'standard' | 'labels' | 'project' | 'milestone'

export function PropertyMenu({ label, value, icon, options, onChange, onCreate, multiple = false, closeOnSelect, keepSelectedVisible = false, selectedId, selectedIds = [], compact = false, emptyLabel, hideSearch = false, searchPlaceholder, searchShortcut, showGroupHeadings = true, kind: explicitKind, teamName, trigger, customTrigger, triggerClassName, triggerRole = 'combobox', surfaceClassName, side = 'bottom', align = 'start', alignOffset = 0, ariaLabel, hoverContent, hoverClassName, valueIsEntityName = false }: {
  label: string
  value?: string
  icon?: ReactNode
  options: PropertyOption[]
  onChange?: (id: string) => void | Promise<unknown>
  onCreate?: (name: string) => void | Promise<unknown>
  multiple?: boolean
  closeOnSelect?: boolean
  keepSelectedVisible?: boolean
  selectedId?: string
  selectedIds?: string[]
  compact?: boolean
  emptyLabel?: string
  hideSearch?: boolean
  searchPlaceholder?: string
  searchShortcut?: string
  showGroupHeadings?: boolean
  kind?: PropertyMenuKind
  teamName?: string
  trigger?: ReactNode
  customTrigger?: (controls: { open: boolean; activeTrigger?: string; openMenu: (triggerId?: string) => void }) => ReactNode
  triggerClassName?: string
  triggerRole?: 'button' | 'combobox'
  surfaceClassName?: string
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  alignOffset?: number
  ariaLabel?: string
  hoverContent?: ReactNode
  hoverClassName?: string
  valueIsEntityName?: boolean
}) {
  const { t } = useI18n()
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
    closeOnSelect: closeOnSelect ?? !multiple,
    keepSelectedVisible,
    open,
    options: orderedOptions,
    selectedIds: selected,
    onOpenChange: setOpen,
    onSelect: option => { void onChange?.(option.id) },
  })
  const labelMenu = groupLabelOptions(command.filteredOptions, selectedSet)
  const standardSections = groupOptionSections(command.filteredOptions)
  const noProject = command.filteredOptions.filter(option => !option.id)
  const projects = command.filteredOptions.filter(option => option.id)
  const selectedProjects = projects.filter(option => command.isSelected(option.id))
  const otherProjects = projects.filter(option => !command.isSelected(option.id))
  const createName = command.query.trim()
  const canCreateLabel = kind === 'labels' && Boolean(onCreate && createName && !options.some(option => option.label.toLocaleLowerCase() === createName.toLocaleLowerCase()))
  const canCreateMilestone = kind === 'milestone' && Boolean(onCreate && createName && !options.some(option => option.label.toLocaleLowerCase() === createName.toLocaleLowerCase()))
  const showLabelCreateHint = kind === 'labels' && Boolean(emptyLabel && !createName && !command.filteredOptions.length)
  const createLabelText = kind === 'labels' ? 'Create new workspace label' : t('Create new label')
  const createLabel = () => { if (!canCreateLabel || !onCreate) return; setOpen(false); void onCreate(createName) }
  const createMilestone = () => { if (!canCreateMilestone || !onCreate) return; setOpen(false); void onCreate(createName) }
  const onCommandKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' && canCreateLabel && !command.filteredOptions.length) { event.preventDefault(); createLabel(); return }
    if (event.key === 'Enter' && canCreateMilestone && !command.filteredOptions.length) { event.preventDefault(); createMilestone(); return }
    command.onKeyDown(event)
  }
  const placeholder = searchPlaceholder ?? (kind === 'labels' ? 'Change or add labels…' : kind === 'project' ? 'Add to project…' : `Change ${label.toLowerCase()}…`)
  const triggerButton = <button type="button" role={triggerRole === 'combobox' ? 'combobox' : undefined} className={triggerClassName ?? (compact ? 'mini-property-trigger' : 'property-row')} aria-label={ariaLabel ?? `Change ${label}. Current value is ${value || 'none'}`} aria-haspopup="dialog" aria-expanded={open} data-state={open ? 'open' : 'closed'}>
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
          <div className={`property-command-search${hideSearch ? ' is-visually-hidden' : ''}`}>
            <input ref={command.inputRef} value={command.query} onFocus={() => setOpenLabelGroupId(undefined)} onChange={event => command.onQueryChange(event.target.value)} aria-label={placeholder} aria-controls={listboxId} aria-activedescendant={command.activeId ? `${listboxId}-${command.activeId || 'none'}` : undefined} placeholder={placeholder} autoComplete="off" spellCheck={false}/>
            {(searchShortcut ?? (kind === 'labels' ? 'L' : kind === 'project' ? 'Shift P' : undefined)) && <SearchShortcut value={searchShortcut ?? (kind === 'labels' ? 'L' : 'Shift P')}/>}
          </div>
          <div id={listboxId} className="property-command-options" role="listbox" aria-label={label} aria-multiselectable={multiple || undefined} onWheel={event => event.stopPropagation()}>
            {kind === 'labels' && <>{labelMenu.selectedOptions.length > 0 && <>{showGroupHeadings && <div className="property-command-group">{t('Frequently used')}</div>}{labelMenu.selectedOptions.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked labelHover listboxId={listboxId} icon={iconFor(label)} multi showGroupLabel onChoose={() => command.choose(option)} onActive={() => { setOpenLabelGroupId(undefined); command.setActiveId(option.id) }}/>)}</>}{showGroupHeadings && (labelMenu.options.length > 0 || labelMenu.groups.length > 0) && <div className="property-command-group">{t('Labels')}</div>}{labelMenu.groups.map(group => <LabelGroupOption key={group.id} group={group} open={openLabelGroupId === group.id} selectedIds={selectedSet} listboxId={listboxId} onOpenChange={next => setOpenLabelGroupId(next ? group.id : undefined)} onChoose={option => command.choose(option)}/>)}{labelMenu.options.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={false} labelHover listboxId={listboxId} icon={iconFor(label)} multi onChoose={() => command.choose(option)} onActive={() => { setOpenLabelGroupId(undefined); command.setActiveId(option.id) }}/>) }{canCreateLabel && <button data-i18n-ignore type="button" className="property-command-create" role="option" aria-label={`${createLabelText}: ${createName}`} onClick={createLabel}><Plus size={15}/><span data-i18n-ignore>{createLabelText}: <strong data-i18n-ignore>"{createName}"</strong></span></button>}{showLabelCreateHint && <div aria-disabled="true" className="property-command-create-hint" data-i18n-ignore role="option"><Plus size={16}/><span>{emptyLabel}</span></div>}</>}
            {kind === 'project' && <>{noProject.map(option => <CommandOption key="none" option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{selectedProjects.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>)}{otherProjects.length > 0 && <div className="property-command-group">{teamName ? t('Projects in {team} team').replace('{team}', teamName) : t('Projects')}</div>}{otherProjects.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={false} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{onCreate&&<><div className="property-command-group">{t('New project')}</div><button type="button" className="property-command-create" role="option" aria-label={t('Create new project…')} onClick={()=>{setOpen(false);void onCreate('')}}><Plus size={15}/><span>{t('Create new project…')}</span></button></>}</>}
            {kind === 'milestone' && <>{command.filteredOptions.map(option => <CommandOption key={option.id || 'none'} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>)}{canCreateMilestone&&<><div className="property-command-group">{t('New project milestone')}</div><button type="button" className="property-command-create" role="option" aria-label={t('Create new milestone…')} onClick={createMilestone}><Plus size={15}/><span>{t('Create new milestone…')}</span></button></>}</>}
            {kind === 'standard' && standardSections.map(section => <div className="property-command-section" key={section.id}>{section.label && <div className="property-command-group">{t(section.label)}</div>}{section.options.map(option => <CommandOption key={option.id || 'none'} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} multi={multiple} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>)}</div>) }
            {!command.filteredOptions.length && !canCreateLabel && !canCreateMilestone && !showLabelCreateHint && <div className="core-property-empty">{t(emptyLabel ?? 'No results')}</div>}
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
  return <Popover.Root open={open} onOpenChange={changeOpen}><Popover.Anchor asChild><button type="button" className="property-command-label-group-option" role="option" aria-selected={open} aria-haspopup="listbox" aria-expanded={open} onPointerEnter={() => changeOpen(true)} onFocus={() => changeOpen(true)} onClick={() => changeOpen(true)}>
    <span className="property-command-option-background"/><span className="property-command-checkbox"/><span className="property-command-icon"><LabelGroupIcon color={group.color ?? group.options[0]?.color ?? 'currentColor'} size={16}/></span><span className="property-command-group-copy"><span className="property-command-label">{group.label}</span></span><ChevronRight className="property-command-group-chevron" size={13}/>
  </button></Popover.Anchor><Popover.Portal><Popover.Content className="property-command-label-submenu" side="right" align="start" alignOffset={-6} sideOffset={-2} collisionPadding={10} onClick={event => event.stopPropagation()} onOpenAutoFocus={event => event.preventDefault()}><div onKeyDown={onKeyDown}><div className="property-command-search"><input ref={inputRef} value={query} onChange={event=>{setQuery(event.target.value);const normalized=event.target.value.trim().toLocaleLowerCase();setActiveId(group.options.find(option=>option.label.toLocaleLowerCase().includes(normalized))?.id)}} aria-label={group.label} placeholder={group.label} autoComplete="off" spellCheck={false}/></div><div className="property-command-options" role="listbox" aria-label={group.label}>{filtered.map(option => <CommandOption key={option.id} option={option} active={option.id === activeId} checked={selectedIds.has(option.id)} listboxId={`${listboxId}-${group.id}`} icon={<LabelIcon size={14}/>} onChoose={() => choose(option)} onActive={() => setActiveId(option.id)}/>)}{!filtered.length&&<div className="core-property-empty">No results</div>}</div></div></Popover.Content></Popover.Portal></Popover.Root>
}

function CommandOption({ option, active, checked, icon, labelHover = false, listboxId, multi = false, showGroupLabel = false, onChoose, onActive }: { option: PropertyOption; active: boolean; checked: boolean; icon: ReactNode; labelHover?: boolean; listboxId: string; multi?: boolean; showGroupLabel?: boolean; onChoose: () => void; onActive: () => void }) {
  const { t } = useI18n()
  const row = <button aria-disabled={option.disabled || undefined} className={showGroupLabel && option.groupLabel ? 'is-grouped-label' : undefined} disabled={option.disabled} id={`${listboxId}-${option.id || 'none'}`} role="option" type="button" aria-selected={active} aria-checked={checked} onPointerMove={onActive} onFocus={onActive} onClick={onChoose}>
    <span className="property-command-option-background"/>{multi && <span className="property-command-checkbox">{checked && <CheckboxMark/>}</span>}<span className="property-command-icon">{option.icon ?? (option.color ? <i className="option-dot" style={{ background: option.color }}/> : icon)}</span><span className="property-command-label" data-i18n-ignore={option.i18nIgnore || undefined}>{option.i18nIgnore ? option.label : t(option.label)}</span>{showGroupLabel && option.groupLabel && <span className="property-command-option-group" data-i18n-ignore>{option.groupLabel}</span>}{!multi && checked && <span className="property-command-check"><Check size={14}/></span>}{option.end && <span className="property-command-option-end">{t(option.end)}</span>}{option.shortcut && <kbd>{option.shortcut}</kbd>}
  </button>
  if (option.hoverContent) return <OptionHover className={option.hoverClassName} content={option.hoverContent}>{row}</OptionHover>
  return labelHover && option.color ? <LabelHoverPreview label={{ name: option.label, color: option.color, description: option.description, issueCount: option.issueCount, scope: option.scope, resourceType: option.resourceType }}>{row}</LabelHoverPreview> : row
}

function OptionHover({ children, className, content }: { children: ReactNode; className?: string; content: ReactNode }) {
  const anchorRef = useRef<HTMLSpanElement>(null)
  const cardRef = useRef<HTMLDivElement>(null)
  const hideTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const [open, setOpen] = useState(false)
  const [position, setPosition] = useState({ left: 0, top: 0 })
  const show = () => {
    if (hideTimer.current) clearTimeout(hideTimer.current)
    const rect = anchorRef.current?.getBoundingClientRect()
    if (rect) {
      const estimatedWidth = className?.includes('lp-new-project-person-hover') ? 278 : 198
      const left = rect.right + estimatedWidth + 6 > window.innerWidth - 8 ? Math.max(8, rect.left - estimatedWidth - 6) : rect.right + 6
      setPosition({ left, top: rect.top })
    }
    setOpen(true)
  }
  const hide = () => {
    if (hideTimer.current) clearTimeout(hideTimer.current)
    hideTimer.current = setTimeout(() => setOpen(false), 100)
  }
  useLayoutEffect(() => {
    if (!open) return
    const updatePosition = () => {
      const anchor = anchorRef.current?.getBoundingClientRect()
      const card = cardRef.current?.getBoundingClientRect()
      if (!anchor || !card || !card.width || !card.height) return
      const padding = 8
      const gap = 6
      let left = anchor.right + gap
      if (left + card.width > window.innerWidth - padding) left = anchor.left - card.width - gap
      let top = anchor.top
      left = Math.max(padding, Math.min(left, window.innerWidth - card.width - padding))
      top = Math.max(padding, Math.min(top, window.innerHeight - card.height - padding))
      setPosition({ left, top })
    }
    const frame = requestAnimationFrame(updatePosition)
    window.addEventListener('resize', updatePosition)
    return () => {
      cancelAnimationFrame(frame)
      window.removeEventListener('resize', updatePosition)
    }
  }, [open])
  useEffect(() => () => {
    if (hideTimer.current) clearTimeout(hideTimer.current)
  }, [])
  return <>
    <span className="property-option-hover-trigger" onBlur={hide} onFocus={show} onPointerEnter={show} onPointerLeave={hide} ref={anchorRef} role="presentation">{children}</span>
    {open && createPortal(<div className={`property-option-hover-card ${className ?? 'property-rich-hover'}`} onPointerEnter={show} onPointerLeave={hide} ref={cardRef} role="tooltip" style={{ left: position.left, top: position.top }}>{content}</div>, document.body)}
  </>
}

function iconFor(label: string) {
  if (label === 'Priority') return <PriorityIcon priority={0} size={14}/>
  if (label === 'Assignee') return <NoAssigneeIcon size={14}/>
  if (label === 'Labels') return <LabelIcon size={14}/>
  if (label === 'Project') return <ProjectIcon size={14}/>
  return <Layers3 size={14}/>
}

function SearchShortcut({ value }: { value: string }) {
  const sequence = value.match(/^(.+?)(?:,)?\s+then\s+(.+)$/i)
  if (!sequence) return <kbd>{value}</kbd>
  return <span className="property-command-search-shortcut"><kbd>{sequence[1]}</kbd><span data-i18n-ignore>then</span><kbd>{sequence[2]}</kbd></span>
}
