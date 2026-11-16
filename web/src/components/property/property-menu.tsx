import * as Popover from '@radix-ui/react-popover'
import * as Tooltip from '@radix-ui/react-tooltip'
import { ChevronRight, Layers3, Plus } from 'lucide-react'
import { Fragment, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import './virtual-property-options.css'
import { createPortal } from 'react-dom'
import { LabelGroupIcon, LabelIcon, NoAssigneeIcon, ProjectIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { LabelHoverPreview } from './label-hover-preview'
import { usePropertyCommand } from './use-property-command'
import { groupOptionSections } from '@/lib/group-options'
import { matchesSearch } from '@/lib/search-match'
import { treeGuideLevels } from '@/lib/team-hierarchy'
import { fitTreeMenu } from '@/lib/tree-menu-width'
import { readLabelUsage, recordLabelUsage } from '@/lib/label-usage'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import type { LabelResourceType } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import { ProjectLabelMenuContent } from './project-label-menu-content'
import { labelMenuModel, type LabelMenuEntry, type LabelMenuGroup } from './label-menu-model'
import { directoryPerson, isPeopleProperty, personSearchText, type PersonIdentity } from '@/lib/people'
import { usePeopleDirectory } from './people-context'
import { PersonInfo } from './person-info'
import { UserAvatar } from '@/components/ui/user-avatar'
import { displayUserName } from '@/lib/runtime-preferences'
import { TooltipBody } from '@/components/ui/tooltip'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import { PhoneMenu } from '@/components/ui/phone-menu'
import { usePhoneSheetRecord } from './phone-sheet-context'
import { usePhoneSubmenuLevel } from '@/components/ui/use-phone-submenu-level'
import { SubmenuChevron } from '@/components/ui/submenu-chevron'

export interface PropertyOption {
  searchOnly?: boolean
  person?: PersonIdentity
  id: string
  label: string
  labelContent?: ReactNode
  ariaLabel?: string
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
  archived?: boolean
  icon?: ReactNode
  i18nIgnore?: boolean
  /** An action row (e.g. "Invite and add…") that never shows a selection checkbox. */
  action?: boolean
  hoverContent?: ReactNode
  hoverClassName?: string
  /** Opens the hover card beside the whole menu (to its left) instead of beside the row. */
  hoverBesideMenu?: boolean
  /** Nesting level of a sub-item (a sub-team under its parent): the icon and label shift right by 24px per level and a tree elbow is drawn. */
  indent?: number
  /** A row that opens a single-select submenu to its right (e.g. a project's lead team). Choosing the row itself opens the submenu. */
  submenu?: PropertyOptionSubmenu
}

export interface PropertyOptionSubmenu {
  placeholder: string
  heading: string
  options: PropertyOption[]
  selectedId: string
  onChoose: (id: string) => void
  /** A plain list (no search row or heading) whose first row sits level with the parent row, and a small ▶ caret on the parent (the assignee menu's agent submenu). */
  bare?: boolean
  /** Extra class on the submenu surface. */
  className?: string
}

export type PropertyMenuKind = 'standard' | 'labels' | 'project-labels' | 'project' | 'milestone'

export function PropertyMenu({ label, value, icon, options, onChange, onCreate, multiple = false, closeOnSelect, keepSelectedVisible = false, selectedId, selectedIds = [], compact = false, emptyLabel, hideSearch = false, createOptionLabel, searchPlaceholder, searchShortcut, showGroupHeadings = true, groupHeading, kind: explicitKind, teamName, trigger, customTrigger, triggerClassName, triggerRole = 'combobox', surfaceClassName, tree = false, phoneSurfaceClassName, side = 'bottom', align = 'start', alignOffset = 0, ariaLabel, hoverContent, hoverClassName, hoverPlacement = 'left', valueIsEntityName = false, open: controlledOpen, onOpenChange, labelGroupId, embedded = false, tooltip, tooltipShortcut, menuContext, disabled = false }: {
  label: string
  disabled?: boolean
  value?: string
  icon?: ReactNode
  options: PropertyOption[]
  onChange?: (id: string) => void | Promise<unknown>
  onCreate?: (name: string, groupId?: string) => void | Promise<unknown>
  multiple?: boolean
  closeOnSelect?: boolean
  keepSelectedVisible?: boolean
  selectedId?: string
  selectedIds?: string[]
  compact?: boolean
  emptyLabel?: string
  hideSearch?: boolean
  /** Standard pickers: label for a "create" row offered when the query matches no option. */
  createOptionLabel?: (name: string) => string
  searchPlaceholder?: string
  searchShortcut?: string
  showGroupHeadings?: boolean
  groupHeading?: string
  kind?: PropertyMenuKind
  teamName?: string
  trigger?: ReactNode
  customTrigger?: (controls: { open: boolean; activeTrigger?: string; openMenu: (triggerId?: string) => void }) => ReactNode
  triggerClassName?: string
  triggerRole?: 'button' | 'combobox'
  surfaceClassName?: string
  /** Team menus: rows are teams, sub-teams (options with `indent`) hang under their parent with the shared tree elbow.
   *  The menu hugs its widest row (integer width, 175px to 401px). `'check'`: every row reserves the trailing tick column
   *  of the selected team (Team property, create-issue team). */
  tree?: boolean | 'check'
  phoneSurfaceClassName?: string
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  alignOffset?: number
  ariaLabel?: string
  hoverContent?: ReactNode
  hoverClassName?: string
  /** Where the rich hover card opens: beside the trigger (property sidebars) or below it (list rows). */
  hoverPlacement?: 'left' | 'below' | 'above'
  valueIsEntityName?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
  labelGroupId?: string
  embedded?: boolean
  /** Plain hover tooltip for the trigger (label plus optional shortcut); `hoverContent` takes precedence. */
  tooltip?: string
  tooltipShortcut?: string
  menuContext?: string
}) {
  const { t } = useI18n()
  const phone = usePhoneLayout()
  const sheetRecord = usePhoneSheetRecord()
  const directory = usePeopleDirectory()
  const menuOptions = useMemo(() => options.map(option => {
    const person = option.person ?? (isPeopleProperty(label) && option.id && !option.id.startsWith('__') && !['*','me','none','all'].includes(option.id) ? directoryPerson(directory.users, option.id) ?? { id: option.id, label: option.label } : undefined)
    return person ? { ...option, label: displayUserName({displayName:person.displayName || option.label,name:person.name && person.name!==person.id ? person.name : option.label}), person, icon: <span aria-hidden="true">{option.icon ?? <UserAvatar className="people-menu-avatar" avatarUrl={person.avatarUrl} name={person.displayName || ('label' in person ? person.label : undefined) || person.name || person.id} userId={person.id}/>}</span>, keywords: `${option.keywords ?? ''} ${personSearchText({ ...person, ...directoryPerson(directory.users, person.id) })}`, hoverContent: option.hoverContent ?? <PersonInfo person={person}/>, hoverClassName: option.hoverClassName ?? 'person-info-surface' } : option
  }), [options, directory.users, label])
  const [localOpen, setLocalOpen] = useState(false)
  const open = embedded || (controlledOpen ?? localOpen)
  const setOpen = (next: boolean) => { setLocalOpen(next); onOpenChange?.(next) }
  const [activeTrigger, setActiveTrigger] = useState<string>()
  const [hoverOpen, setHoverOpen] = useState(false)
  const hoverSurface = useRef<HTMLDivElement>(null)
  const [openLabelGroupId, setOpenLabelGroupId] = useState<string>()
  const [openSubmenuId, setOpenSubmenuId] = useState<string>()
  const listboxId = useId()
  // A team tree hugs its widest row at a whole-pixel width; refit when the rows change.
  const [treeSurface, setTreeSurface] = useState<HTMLDivElement | null>(null)
  const treeRowsKey = tree ? menuOptions.map(option => `${option.id}:${option.label}`).join('|') : ''
  useLayoutEffect(() => { if (tree && treeSurface) fitTreeMenu(treeSurface) }, [tree, treeSurface, treeRowsKey])
  const selected = multiple || selectedIds.length ? selectedIds : [selectedId ?? options.find(option => option.label === value)?.id ?? '']
  const selectionKey = JSON.stringify(selected)
  const selectedSet = useMemo(() => new Set<string>(JSON.parse(selectionKey)), [selectionKey])
  const selectedPerson = selected.length === 1 ? menuOptions.find(option => option.id === selected[0])?.person : undefined
  if (!hoverContent && !tooltip && selectedPerson) {
    hoverContent = <PersonInfo person={selectedPerson}/>
    hoverClassName = 'person-info-surface'
  }
  const personCard = hoverClassName === 'assignee-person-surface'
  const kind = explicitKind ?? (multiple && label === 'Labels' ? 'labels' : label === 'Project' ? 'project' : 'standard')
  const orderedOptions = useMemo(() => kind === 'labels' ? menuOptions.map(option => option.groupLabel ? { ...option, keywords: `${option.keywords ?? ''} ${option.groupLabel}` } : option) : multiple ? [...menuOptions.filter(option => selectedSet.has(option.id)), ...menuOptions.filter(option => !selectedSet.has(option.id))] : menuOptions, [menuOptions, multiple, kind, selectedSet])
  const { inputRef: commandInputRef, ...command } = usePropertyCommand({
    highlightOnOpen: kind !== 'labels',
    autoFocus: kind !== 'project-labels',
    closeOnSelect: closeOnSelect ?? !multiple,
    keepSelectedVisible,
    open,
    options: orderedOptions,
    selectedIds: selected,
    onOpenChange: setOpen,
    intercept: option => { if (!option.submenu) return false; setOpenSubmenuId(option.id); return true },
    onSelect: option => { if (kind === 'labels') recordLabelUsage(option.id); void onChange?.(option.id) },
  })
  const labelMatches = useMemo(() => new Set(command.filteredOptions.map(option => option.id)), [command.filteredOptions])
  const labelMenu = labelMenuModel(kind === 'labels' ? orderedOptions : [], selectedSet, command.query.trim() ? { query: command.query, matches: option => labelMatches.has(option.id), groupMatches: name => matchesSearch(command.query.trim(), [name], undefined, { fuzzy: true }) } : undefined)
  // The reference's label picker: with nothing selected and no query, labels already in use (on issues, or picked by the viewer before) list first under "Frequently used";
  // a label of a group names its group after it there.
  const labelUsage = kind === 'labels' && open ? readLabelUsage() : {}
  const frequentLabels: LabelMenuEntry[] = kind === 'labels' && !groupHeading && !command.query && labelMenu.selected.length === 0 ? orderedOptions.filter(option => !selectedSet.has(option.id) && ((option.issueCount ?? 0) > 0 || (labelUsage[option.id] ?? 0) > 0)).map(option => ({ type: 'label', option, groupName: option.groupId && option.groupLabel ? 'detail' : undefined })) : []
  const frequentIds = new Set(frequentLabels.map(entry => entry.type === 'label' ? entry.option.id : ''))
  const notFrequent = (entry: LabelMenuEntry) => entry.type === 'group' || !frequentIds.has(entry.option.id)
  // Team labels (and groups) list under their own heading above the workspace's, as in the reference.
  const labelSections = [
    { id: 'frequent', heading: 'Frequently used', entries: frequentLabels },
    { id: 'team', heading: 'Team labels', entries: labelMenu.team.filter(notFrequent) },
    { id: 'workspace', heading: groupHeading ?? 'Workspace labels', entries: labelMenu.workspace.filter(notFrequent) },
  ].filter(section => section.entries.length > 0).map((section, index, list) => section.id === 'workspace' && list[index - 1]?.id === 'frequent' ? { ...section, id: 'workspace-after-frequent' } : section)
  const labelRowCount = labelMenu.selected.length + labelSections.reduce((count, section) => count + section.entries.length, 0)
  const showLabelHeadings = showGroupHeadings && (Boolean(groupHeading) || !command.query)
  const labelEntry = (entry: LabelMenuEntry, checked: boolean) => entry.type === 'group'
    ? <LabelGroupOption key={`group:${entry.group.id}`} group={entry.group} open={openLabelGroupId === entry.group.id} selectedIds={selectedSet} listboxId={listboxId} onOpenChange={next => setOpenLabelGroupId(next ? entry.group.id : undefined)} onChoose={option => { command.choose(option); setOpen(false) }} onClear={() => { const option = entry.group.options.find(item => selectedSet.has(item.id)); if (option) command.choose(option) }} onCreate={onCreate ? name => { setOpen(false); void onCreate(name, entry.group.id) } : undefined}/>
    : <CommandOption key={entry.option.id} option={entry.option} groupName={entry.groupName} active={entry.option.id === command.activeId} checked={checked} labelHover listboxId={listboxId} icon={iconFor(label)} multi onChoose={() => command.choose(entry.option)} onActive={() => { setOpenLabelGroupId(undefined); command.setActiveId(entry.option.id) }}/>
  const noProject = command.filteredOptions.filter(option => !option.id)
  const projects = command.filteredOptions.filter(option => option.id)
  const selectedProjects = projects.filter(option => command.isSelected(option.id))
  const otherProjects = projects.filter(option => !command.isSelected(option.id))
  const createName = command.query.trim()
  const canCreateLabel = kind === 'labels' && Boolean(onCreate && createName && !options.some(option => option.label.toLocaleLowerCase() === createName.toLocaleLowerCase()))
  const canCreateStandard = kind === 'standard' && Boolean(createOptionLabel && onCreate && createName && !options.some(option => option.label.toLocaleLowerCase() === createName.toLocaleLowerCase()))
  const createStandard = () => { if (!canCreateStandard || !onCreate) return; setOpen(false); void onCreate(createName) }
  const canCreateMilestone = kind === 'milestone' && Boolean(onCreate && createName && !options.some(option => option.label.toLocaleLowerCase() === createName.toLocaleLowerCase()))
  const showLabelCreateHint = kind === 'labels' && Boolean(emptyLabel && !createName && !labelRowCount)
  const createLabelText = kind === 'labels' ? 'Create new workspace label' : t('Create new label')
  const createLabel = () => { if (!canCreateLabel || !onCreate) return; setOpen(false); void onCreate(createName) }
  const createMilestone = () => { if (!canCreateMilestone || !onCreate) return; setOpen(false); void onCreate(createName) }
  const onCommandKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (embedded) {
      event.stopPropagation()
      if (event.key === 'ArrowLeft') { event.preventDefault(); setOpen(false); return }
    }
    if (event.key === 'Enter' && canCreateLabel && !command.filteredOptions.length) { event.preventDefault(); createLabel(); return }
    if (event.key === 'Enter' && canCreateMilestone && !command.filteredOptions.length) { event.preventDefault(); createMilestone(); return }
    if (event.key === 'Enter' && canCreateStandard && !command.filteredOptions.length) { event.preventDefault(); createStandard(); return }
    if (event.key === 'ArrowRight' && !command.query) {
      const submenuOption = command.filteredOptions.find(option => option.id === command.activeId && option.submenu)
      if (submenuOption) { event.preventDefault(); setOpenSubmenuId(submenuOption.id); return }
    }
    command.onKeyDown(event)
  }
  const placeholder = searchPlaceholder ?? (kind === 'labels' ? 'Change or add labels…' : kind === 'project' ? 'Add to project…' : `Change ${label.toLowerCase()}…`)
  // The sheet's title is the search prompt without its ellipsis, translated whole when the prompt has an entry ("Set priority to…").
  const translatedPlaceholder = t(placeholder)
  const sheetLabel = (translatedPlaceholder === placeholder ? t(placeholder.replace(/…$/, '')) : translatedPlaceholder).replace(/…$/, '')
  const triggerButton = <button type="button" disabled={disabled} data-property={label.toLowerCase()} data-empty={selected.every(id => !id)} role={triggerRole === 'combobox' ? 'combobox' : undefined} className={triggerClassName ?? (compact ? 'mini-property-trigger' : 'property-row')} aria-label={ariaLabel ?? `Change ${label}. Current value is ${value || 'none'}`} aria-haspopup="dialog" aria-expanded={open} data-state={open ? 'open' : 'closed'} onPointerLeave={event => {
    if (!personCard || !hoverSurface.current) return
    const trigger = event.currentTarget.getBoundingClientRect(), card = hoverSurface.current.getBoundingClientRect()
    // Keep the gap traversable, but close even when a pointer jumps beyond Radix's grace polygon in one event.
    if (event.clientX < Math.min(trigger.left, card.left) || event.clientX > Math.max(trigger.right, card.right) || event.clientY < Math.min(trigger.top, card.top) || event.clientY > Math.max(trigger.bottom, card.bottom)) setHoverOpen(false)
  }}>
        {trigger ?? (compact ? <>{icon ?? iconFor(label)}<span data-i18n-ignore={valueIsEntityName || undefined}>{value || label}</span></> : <><span>{icon ?? iconFor(label)}</span><span className="property-label">{label}</span><span className="property-value" data-i18n-ignore={valueIsEntityName || undefined}>{value || `Add ${label.toLowerCase()}`}</span></>)}
      </button>
  const popoverTrigger = <Popover.Trigger asChild>{triggerButton}</Popover.Trigger>
  const plainTooltip = !hoverContent && Boolean(tooltip)
  const tooltipContent = hoverContent ?? (tooltip ? <TooltipBody label={tooltip} shortcut={tooltipShortcut}/> : undefined)
  const menuTrigger = customTrigger
    ? <Popover.Anchor asChild>{customTrigger({ open, activeTrigger, openMenu: triggerId => { setActiveTrigger(triggerId); setHoverOpen(false); setOpen(true) } })}</Popover.Anchor>
    : tooltipContent ? <Tooltip.Trigger asChild>{popoverTrigger}</Tooltip.Trigger> : popoverTrigger
  const content = kind === 'project-labels' ? <ProjectLabelMenuContent options={options} selectedIds={selected} groupId={labelGroupId} onChoose={id => { void onChange?.(id) }} onCreate={onCreate} onClose={() => setOpen(false)}/> : <div data-property-multiple={multiple ? kind : undefined} onKeyDown={onCommandKeyDown}>
          {kind === 'standard' && groupHeading && <h3 className="sr-only">{t(groupHeading)}</h3>}
          <div className={`property-command-search${hideSearch && !command.query ? ' is-visually-hidden' : ''}`}>
            <input ref={commandInputRef} value={command.query} onFocus={() => setOpenLabelGroupId(undefined)} onChange={event => command.onQueryChange(event.target.value)} aria-label={placeholder} aria-controls={listboxId} aria-activedescendant={command.activeId ? `${listboxId}-${command.activeId || 'none'}` : undefined} placeholder={placeholder} autoComplete="off" spellCheck={false}/>
            {(searchShortcut ?? (kind === 'labels' ? 'L' : kind === 'project' ? 'Shift P' : undefined)) && <SearchShortcut value={searchShortcut ?? (kind === 'labels' ? 'L' : 'Shift P')}/>}
          </div>
          {kind === 'standard' && command.filteredOptions.length > 60 ? <VirtualStandardOptions options={command.filteredOptions} activeId={command.activeId} selected={selectedSet} label={label} multiple={multiple} listboxId={listboxId} onChoose={command.choose} onActive={command.setActiveId}/> : <div id={listboxId} className="property-command-options" role="listbox" aria-label={label} aria-multiselectable={multiple || undefined} onWheel={event => event.stopPropagation()}>
            {kind === 'labels' && <>{labelMenu.selected.length > 0 && <>{labelMenu.selected.map(entry => labelEntry(entry, true))}{!command.query && labelSections.length > 0 && <div className="property-command-separator" role="separator"/>}</>}{labelSections.map((section, index) => <Fragment key={section.id}>{index > 0 && section.id !== 'workspace-after-frequent' && showLabelHeadings && <div className="property-command-separator" role="separator"/>}{showLabelHeadings && <div className="property-command-group">{t(section.heading)}</div>}{section.entries.map(entry => labelEntry(entry, false))}</Fragment>)}{canCreateLabel && <button data-i18n-ignore type="button" className="property-command-create" role="option" aria-label={`${createLabelText}: ${createName}`} onClick={createLabel}><Plus size={15}/><span data-i18n-ignore>{createLabelText}: <strong data-i18n-ignore>"{createName}"</strong></span></button>}{showLabelCreateHint && <div aria-disabled="true" className="property-command-create-hint" data-i18n-ignore role="option"><Plus size={16}/><span>{emptyLabel}</span></div>}</>}
            {kind === 'project' && <>{noProject.map(option => <CommandOption key="none" option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{selectedProjects.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>)}{otherProjects.length > 0 && <div className="property-command-group">{teamName ? t('Projects in {team} team').replace('{team}', () => teamName) : t('Projects')}</div>}{otherProjects.map(option => <CommandOption key={option.id} option={option} active={option.id === command.activeId} checked={false} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>) }{onCreate&&<><div className="property-command-group">{t('New project')}</div><button type="button" className="property-command-create" role="option" aria-label={t('Create new project…')} onClick={()=>{setOpen(false);void onCreate('')}}><Plus size={15}/><span>{t('Create new project…')}</span></button></>}</>}
            {kind === 'milestone' && <>{command.filteredOptions.map(option => <CommandOption key={option.id || 'none'} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} onChoose={() => command.choose(option)} onActive={() => command.setActiveId(option.id)}/>)}{canCreateMilestone&&<><div className="property-command-group">{t('New project milestone')}</div><button type="button" className="property-command-create" role="option" aria-label={t('Create new milestone…')} onClick={createMilestone}><Plus size={15}/><span>{t('Create new milestone…')}</span></button></>}</>}
            {kind === 'standard' && groupOptionSections(command.filteredOptions).map(section => { const guides = treeGuideLevels(section.options.map(option => option.indent ?? 0)); return <div className="property-command-section" data-unlabelled={!section.label || undefined} key={section.id} style={{ '--property-option-count': section.options.length } as CSSProperties}>{section.label && <div className="property-command-group">{t(section.label)}</div>}{section.options.map((option, index) => <CommandOption rowIndex={index} treeThrough={guides[index]} key={option.id || 'none'} option={option} active={option.id === command.activeId} checked={command.isSelected(option.id)} listboxId={listboxId} icon={iconFor(label)} multi={multiple} submenuOpen={openSubmenuId === option.id} onSubmenuOpenChange={next => setOpenSubmenuId(next ? option.id : undefined)} onSubmenuChosen={() => setOpen(false)} onChoose={() => command.choose(option)} onActive={() => { if (openSubmenuId && openSubmenuId !== option.id) setOpenSubmenuId(undefined); command.setActiveId(option.id) }}/>)}</div> }) }
            {canCreateStandard && createOptionLabel && <button type="button" className="property-command-create" role="option" aria-label={createOptionLabel(createName)} onClick={createStandard}><Plus size={16}/><span data-i18n-ignore>{createOptionLabel(createName)}</span></button>}
            {!(kind === 'labels' ? labelRowCount : command.filteredOptions.length) && !canCreateLabel && !canCreateMilestone && !canCreateStandard && !showLabelCreateHint && <div className="core-property-empty">{t(emptyLabel ?? 'No results')}</div>}
          </div>}
        </div>
  if (embedded) return <Tooltip.Provider delayDuration={450} skipDelayDuration={400}>{content}</Tooltip.Provider>
  if (phone) return <>{customTrigger
    ? customTrigger({ open, activeTrigger, openMenu: triggerId => { setActiveTrigger(triggerId); setOpen(true) } })
    : <span className="phone-property-trigger" onClick={() => { if (!disabled) setOpen(true) }}>{triggerButton}</span>}
    <PhoneMenu heading={menuContext ? t(menuContext) : sheetRecord ? <><span className="phone-menu-context-key">{sheetRecord.key} ⋅</span> {sheetRecord.title}</> : true} open={open} onOpenChange={setOpen} label={sheetLabel} className={`property-command-surface property-command-${kind}${phoneSurfaceClassName ? ` ${phoneSurfaceClassName}` : ''}`}>
      {content}
    </PhoneMenu>
  </>
  return <Tooltip.Provider delayDuration={450} skipDelayDuration={400}><Tooltip.Root open={Boolean(tooltipContent) && !open && hoverOpen} onOpenChange={setHoverOpen}><Popover.Root open={open} onOpenChange={next => { setOpen(next); if (next) setHoverOpen(false); else { setActiveTrigger(undefined); setOpenLabelGroupId(undefined); setOpenSubmenuId(undefined) } }}>
    {menuTrigger}
    <Popover.Portal>
      <Popover.Content ref={setTreeSurface} data-flow-motion="floating" className={`property-command-surface property-command-${kind}${labelGroupId ? ' is-label-group' : ''}${tree ? ' is-tree' : ''}${tree === 'check' ? ' is-tree-check' : ''}${surfaceClassName ? ` ${surfaceClassName}` : ''}`} role="dialog" aria-label={`Change ${label}`} align={align} alignOffset={alignOffset} side={side} sideOffset={4} collisionPadding={10} onClick={event => event.stopPropagation()} onOpenAutoFocus={event => event.preventDefault()}>
        {content}
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>{tooltipContent&&<Tooltip.Portal><Tooltip.Content ref={hoverSurface} data-flow-motion="tooltip" className={plainTooltip ? 'flow-tooltip-content' : hoverClassName ?? 'property-hover-tooltip'} side={hoverPlacement === 'above' ? 'top' : plainTooltip || hoverPlacement === 'below' ? 'bottom' : 'left'} align="center" sideOffset={plainTooltip || personCard || hoverPlacement === 'above' || hoverPlacement === 'below' ? 8 : 6} collisionPadding={8} onClick={event => event.stopPropagation()}>{tooltipContent}</Tooltip.Content></Tooltip.Portal>}</Tooltip.Root></Tooltip.Provider>
}

function VirtualStandardOptions({ options, activeId, selected, label, multiple, listboxId, onChoose, onActive }: {
  options: PropertyOption[]; activeId?: string; selected: Set<string>; label: string; multiple: boolean; listboxId: string; onChoose: (option: PropertyOption) => void; onActive: (id: string) => void
}) {
  const { t } = useI18n()
  const phone = usePhoneLayout()
  const ref = useRef<VirtuosoHandle>(null)
  const sections = useMemo(() => groupOptionSections(options), [options])
  const rows = useMemo(() => sections.flatMap(section => { const guides = treeGuideLevels(section.options.map(option => option.indent ?? 0)); return [...(section.label ? [{ key: `header:${section.id}`, heading: section.label, option: undefined as PropertyOption | undefined, through: undefined as number[] | undefined }] : []), ...section.options.map((option, index) => ({ key: `option:${option.id}`, heading: '', option, through: guides[index] }))] }), [sections])
  const indices = useMemo(() => new Map(rows.flatMap((row, index) => row.option ? [[row.option.id, index] as const] : [])), [rows])
  const activeIndex = activeId === undefined ? 0 : indices.get(activeId) ?? 0
  useEffect(() => { if (activeId !== undefined) ref.current?.scrollIntoView({ index: activeIndex }) }, [activeId, activeIndex])
  return <Virtuoso ref={ref} id={listboxId} role="listbox" aria-label={label} aria-multiselectable={multiple || undefined} className="property-command-options virtual-property-options" data={rows} fixedItemHeight={phone ? undefined : 32} defaultItemHeight={phone ? 46 : 32} initialTopMostItemIndex={activeIndex} increaseViewportBy={64} computeItemKey={(_index, row) => row.key} style={{height:Math.min(phone ? 369 : 319, rows.length*(phone ? 46 : 32))}} itemContent={(_index,row) => row.option ? <CommandOption option={row.option} treeThrough={row.through} active={row.option.id===activeId} checked={selected.has(row.option.id)} listboxId={listboxId} icon={iconFor(label)} multi={multiple} onChoose={()=>onChoose(row.option!)} onActive={()=>onActive(row.option!.id)}/> : <div className="property-command-group" style={{height:phone ? 30 : 32}}>{t(row.heading)}</div>}/>
}

function LabelGroupOption({ group, open, selectedIds, listboxId, onOpenChange, onChoose, onClear, onCreate }: { group: LabelMenuGroup; open: boolean; selectedIds: Set<string>; listboxId: string; onOpenChange: (open: boolean) => void; onChoose: (option: PropertyOption) => void; onClear: () => void; onCreate?: (name: string) => void }) {
  const [query, setQuery] = useState('')
  const [activeId, setActiveId] = useState<string>()
  const inputRef = useRef<HTMLInputElement>(null)
  const openTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const [trigger, setTrigger] = useState<HTMLButtonElement | null>(null)
  const [content, setContent] = useState<HTMLDivElement | null>(null)
  // Phones replace the label list with the group's labels (a search field named after the group, then its labels), as in the reference,
  // instead of opening a side menu off-screen.
  const phone = usePhoneSubmenuLevel(open, trigger, content)
  const { t } = useI18n()
  const createName = query.trim()
  const canCreate = Boolean(onCreate && createName && !group.options.some(option => option.label.toLocaleLowerCase() === createName.toLocaleLowerCase()))
  const filtered = createName ? group.options.filter(option => option.label.toLocaleLowerCase().includes(createName.toLocaleLowerCase())) : group.options
  useEffect(() => () => { if (openTimer.current) clearTimeout(openTimer.current) }, [])
  const changeOpen = (next: boolean) => {
    onOpenChange(next)
    if (!next) return
    setQuery('')
    setActiveId(group.options.find(option => selectedIds.has(option.id))?.id ?? group.options[0]?.id)
    requestAnimationFrame(() => inputRef.current?.focus())
  }
  // Choosing a label of a group closes the whole picker, as in the reference.
  const choose = (option: PropertyOption) => { onOpenChange(false); onChoose(option) }
  const openFromHover = () => {
    if (phone || open || openTimer.current) return
    openTimer.current = setTimeout(() => { openTimer.current = undefined; changeOpen(true) }, 300)
  }
  const cancelHoverOpen = () => {
    if (openTimer.current) { clearTimeout(openTimer.current); openTimer.current = undefined }
  }
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
      if (!filtered.length && canCreate) { event.preventDefault(); onCreate?.(createName); return }
      const option = filtered.find(item => item.id === activeId) ?? filtered[0]
      if (option) { event.preventDefault(); choose(option) }
    }
  }
  return <Popover.Root open={open} onOpenChange={changeOpen}><Popover.Anchor asChild><button ref={setTrigger} type="button" className="property-command-label-group-option" role="option" aria-label={group.detail ? `${group.label} ${group.detail}` : group.label} aria-selected={open} aria-checked={group.checked} aria-haspopup="listbox" aria-expanded={open} onPointerEnter={openFromHover} onPointerLeave={cancelHoverOpen} onFocus={() => { if (!phone) changeOpen(true) }} onClick={() => changeOpen(true)}>
    <span className="property-command-option-background"/>{group.checked && <span className="property-command-checkbox" onPointerDown={event => event.preventDefault()} onClick={event => { event.stopPropagation(); cancelHoverOpen(); onOpenChange(false); onClear() }}><CheckboxMark/></span>}<span className="property-command-icon"><LabelGroupIcon color={group.color ?? group.options[0]?.color ?? 'currentColor'} size={16}/></span><span className="property-command-group-copy"><span className="property-command-label" data-i18n-ignore>{group.label}</span>{group.detail && <span className="property-command-group-detail" data-i18n-ignore>{group.detail}</span>}</span><span className="property-command-group-chevron"><SubmenuChevron/></span>
  </button></Popover.Anchor><Popover.Portal><Popover.Content ref={setContent} data-flow-motion="floating" data-phone-submenu={phone ? '' : undefined} className="property-command-label-submenu" side="right" align="start" alignOffset={-6} sideOffset={-2} collisionPadding={10} onClick={event => event.stopPropagation()} onOpenAutoFocus={event => event.preventDefault()} onCloseAutoFocus={event => { if (phone) event.preventDefault() }} onInteractOutside={event => { if (phone && event.target instanceof Node && trigger?.contains(event.target)) event.preventDefault() }}><div onKeyDown={onKeyDown}><div className="property-command-search"><input ref={inputRef} value={query} onChange={event=>{setQuery(event.target.value);const normalized=event.target.value.trim().toLocaleLowerCase();setActiveId(group.options.find(option=>option.label.toLocaleLowerCase().includes(normalized))?.id)}} aria-label={group.label} placeholder={group.label} autoComplete="off" spellCheck={false}/></div><div className="property-command-options" role="listbox" aria-label={group.label}>{filtered.map(option => <CommandOption key={option.id} option={option} active={option.id === activeId} checked={selectedIds.has(option.id)} listboxId={`${listboxId}-${group.id}`} icon={<LabelIcon size={14}/>} onChoose={() => choose(option)} onActive={() => setActiveId(option.id)}/>)}{canCreate&&<button type="button" className="property-command-create" role="option" aria-label={`${t('Create label in group')} "${group.label}": "${createName}"`} onClick={() => onCreate?.(createName)}><Plus size={15}/><span data-i18n-ignore>{t('Create label in group')} "{group.label}": <strong data-i18n-ignore>"{createName}"</strong></span></button>}{!filtered.length&&!canCreate&&<div className="core-property-empty">{t('No results')}</div>}</div></div></Popover.Content></Popover.Portal></Popover.Root>
}

function CommandOption({ option, active, checked, icon, groupName, labelHover = false, listboxId, multi = false, rowIndex, submenuOpen = false, treeThrough, onSubmenuOpenChange, onSubmenuChosen, onChoose, onActive }: { option: PropertyOption; active: boolean; checked: boolean; icon: ReactNode; groupName?: 'detail' | 'path'; labelHover?: boolean; listboxId: string; multi?: boolean; rowIndex?: number; treeThrough?: number[]; submenuOpen?: boolean; onSubmenuOpenChange?: (open: boolean) => void; onSubmenuChosen?: () => void; onChoose: () => void; onActive: () => void }) {
  const { t } = useI18n()
  const phone = usePhoneLayout()
  if (option.submenu) {
    const submenuRow = <OptionSubmenuRow option={option} submenu={option.submenu} active={active} checked={checked} listboxId={listboxId} multi={multi} treeThrough={treeThrough} open={submenuOpen} onOpenChange={next => onSubmenuOpenChange?.(next)} onChosen={onSubmenuChosen} onActive={onActive}/>
    return option.hoverContent && !phone ? <OptionHover besideMenu={option.hoverBesideMenu} className={option.hoverClassName} content={option.hoverContent} suppressed={submenuOpen}>{submenuRow}</OptionHover> : submenuRow
  }
  const indent = option.indent ?? 0
  const row = <button aria-label={option.ariaLabel} aria-disabled={option.disabled || undefined} data-indent={indent || undefined} style={indent || rowIndex !== undefined ? { '--option-indent': indent || undefined, '--property-option-index': rowIndex } as CSSProperties : undefined} disabled={option.disabled} id={`${listboxId}-${option.id || 'none'}`} role="option" type="button" aria-selected={active} aria-checked={checked} onPointerMove={onActive} onFocus={onActive} onClick={onChoose}>
    <span className="property-command-option-background"/>{multi && !option.action && <span className="property-command-checkbox">{checked && <CheckboxMark/>}</span>}{indent > 0 && <TreeElbow through={treeThrough}/>}<span className="property-command-icon">{option.icon ?? (option.color ? <i className="option-dot" style={{ background: option.color }}/> : icon)}</span>{(() => { const name = <span className="property-command-label" data-i18n-ignore={option.i18nIgnore || undefined}>{option.labelContent ?? (option.i18nIgnore ? option.label : t(option.label))}</span>
      // A label of a group names the group: after it (a frequently used label) or before it as "Group › Label" (a search result).
      if (!groupName || !option.groupLabel) return name
      return groupName === 'detail'
        ? <span className="property-command-group-copy">{name}<span className="property-command-group-detail" data-i18n-ignore>{option.groupLabel}</span></span>
        : <span className="property-command-group-copy is-path"><span className="property-command-group-path" data-i18n-ignore>{option.groupLabel}</span><span className="property-command-path-chevron" aria-hidden="true"><svg width="5" height="9" viewBox="0 0 5 9"><path d="M1 1.25 3.75 4.5 1 7.75" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.25"/></svg></span>{name}</span>
    })()}{!multi && checked && <span className="property-command-check"><FlowGlyph name="check" width={16} height={16}/></span>}{option.end && <span className="property-command-option-end" data-i18n-ignore={option.i18nIgnore || undefined}>{option.i18nIgnore ? option.end : t(option.end)}</span>}{option.shortcut && <kbd>{option.shortcut}</kbd>}
  </button>
  // A touch focus must not open a desktop hover card over the next tap target.
  if (phone) return row
  if (option.hoverContent) return <OptionHover besideMenu={option.hoverBesideMenu} className={option.hoverClassName} content={option.hoverContent}>{row}</OptionHover>
  return labelHover && option.color ? <LabelHoverPreview label={{ name: option.label, color: option.color, description: option.description, issueCount: option.issueCount, scope: option.scope, resourceType: option.resourceType }}>{row}</LabelHoverPreview> : row
}

/** A row that opens a single-select submenu to its right on hover, click or ArrowRight. */
function OptionSubmenuRow({ option, submenu, active, checked, listboxId, multi, treeThrough, open, onOpenChange, onChosen, onActive }: { option: PropertyOption; submenu: PropertyOptionSubmenu; active: boolean; checked: boolean; listboxId: string; multi: boolean; treeThrough?: number[]; open: boolean; onOpenChange: (open: boolean) => void; onChosen?: () => void; onActive: () => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [activeId, setActiveId] = useState<string>()
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const openTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const normalized = query.trim().toLocaleLowerCase()
  const filtered = normalized ? submenu.options.filter(item => `${item.label} ${item.keywords ?? ''}`.toLocaleLowerCase().includes(normalized)) : submenu.options
  useEffect(() => () => { if (openTimer.current) clearTimeout(openTimer.current) }, [])
  useEffect(() => {
    if (!open) return
    setQuery('')
    setActiveId(submenu.selectedId || submenu.options[0]?.id)
    requestAnimationFrame(() => (submenu.bare ? listRef.current : inputRef.current)?.focus())
    // Opening resets the search and highlights the current choice.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  const choose = (item: PropertyOption) => { onOpenChange(false); onChosen?.(); submenu.onChoose(item.id) }
  const openFromHover = () => {
    onActive()
    if (open || openTimer.current) return
    openTimer.current = setTimeout(() => { openTimer.current = undefined; onOpenChange(true) }, 300)
  }
  const cancelHoverOpen = () => { if (openTimer.current) { clearTimeout(openTimer.current); openTimer.current = undefined } }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape' || (event.key === 'ArrowLeft' && !query)) { event.preventDefault(); event.stopPropagation(); onOpenChange(false); return }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const current = filtered.findIndex(item => item.id === activeId)
      const offset = event.key === 'ArrowDown' ? 1 : -1
      const next = current < 0 ? (offset > 0 ? 0 : filtered.length - 1) : (current + offset + filtered.length) % filtered.length
      if (filtered[next]) setActiveId(filtered[next].id)
      return
    }
    if (event.key === 'Enter') {
      const item = filtered.find(candidate => candidate.id === activeId) ?? filtered[0]
      if (item) { event.preventDefault(); choose(item) }
    }
  }
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Anchor asChild><button aria-checked={checked} aria-expanded={open} aria-haspopup="listbox" aria-selected={active} className="has-submenu" data-indent={option.indent || undefined} style={option.indent ? { '--option-indent': option.indent } as CSSProperties : undefined} id={`${listboxId}-${option.id}`} role="option" type="button" onClick={() => onOpenChange(true)} onFocus={onActive} onPointerEnter={openFromHover} onPointerLeave={cancelHoverOpen} onPointerMove={onActive}>
      <span className="property-command-option-background"/>{multi && <span className="property-command-checkbox is-locked">{checked && <CheckboxMark/>}</span>}{Boolean(option.indent) && <TreeElbow through={treeThrough}/>}<span className="property-command-icon">{option.icon}</span><span className="property-command-label" data-i18n-ignore={option.i18nIgnore || undefined}>{option.labelContent ?? (option.i18nIgnore ? option.label : t(option.label))}</span>{submenu.bare ? <span aria-hidden="true" className="property-command-submenu-caret">▶</span> : <ChevronRight className="property-command-group-chevron" size={13}/>}
    </button></Popover.Anchor>
    <Popover.Portal><Popover.Content data-flow-motion="floating" className={`property-command-surface ${submenu.bare ? 'property-command-bare-submenu' : 'property-command-option-submenu'}${submenu.className ? ` ${submenu.className}` : ''}`} side="right" align="start" alignOffset={submenu.bare ? -6 : -67} sideOffset={-2} collisionPadding={10} onClick={event => event.stopPropagation()} onOpenAutoFocus={event => event.preventDefault()} role="dialog" aria-label={submenu.placeholder}>
      <div onKeyDown={onKeyDown}>
        {!submenu.bare && <div className="property-command-search"><input ref={inputRef} value={query} onChange={event => { setQuery(event.target.value); const next = event.target.value.trim().toLocaleLowerCase(); setActiveId(submenu.options.find(item => `${item.label} ${item.keywords ?? ''}`.toLocaleLowerCase().includes(next))?.id) }} aria-label={submenu.placeholder} placeholder={submenu.placeholder} autoComplete="off" spellCheck={false}/></div>}
        <div ref={listRef} tabIndex={submenu.bare ? -1 : undefined} className="property-command-options" role="listbox" aria-label={submenu.heading} aria-activedescendant={submenu.bare && activeId !== undefined ? `${listboxId}-submenu-${activeId || 'none'}` : undefined}>
          {!submenu.bare && filtered.length > 0 && <div className="property-command-group">{t(submenu.heading)}</div>}
          {filtered.map(item => <CommandOption key={item.id} option={item} active={item.id === activeId} checked={item.id === submenu.selectedId} listboxId={`${listboxId}-submenu`} icon={null} onChoose={() => choose(item)} onActive={() => setActiveId(item.id)}/>)}
          {!filtered.length && <div className="core-property-empty">{t('No results')}</div>}
        </div>
      </div>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

/** The reference app's gap between a menu and a hover card opened beside it (the project teams picker's team card). */
const BESIDE_MENU_GAP = 5

/** The reference app's tree elbow tying a sub-item to its parent row: a 1x8 line down from the row's top edge,
 *  then the 10x9 curve from y7 toward the icon. `through` lists the levels (from `treeGuideLevels`) whose guide
 *  runs the full row height because a later sibling follows: the row's own level unless it is the last child, and
 *  each ancestor level whose ancestor still has a later sibling. */
export function TreeElbow({ through = [] }: { through?: number[] }) {
  return <>
    <svg aria-hidden="true" className="property-command-tree-elbow" width="10" height="16" viewBox="0 0 10 16"><path d="M0 0h1v8H0z"/><path transform="translate(0 7)" d={TREE_CURVE_PATH}/></svg>
    {through.map(level => <i aria-hidden="true" className="property-command-tree-line" key={level} style={{ '--tree-level': level } as CSSProperties}/>)}
  </>
}

const TREE_CURVE_PATH = 'M0 0h1v1c0 2.5 2.212 3.546 2.212 3.546L9.737 8.06c.568.306.094 1.186-.474.88l-6.48-3.488S0 4 0 1V0Z'

/** The elbow's 10x9 curve on its own, for trees that draw their vertical line separately (Settings › Teams). */
export function TreeCurve({ className, style }: { className?: string; style?: CSSProperties }) {
  return <svg aria-hidden="true" className={className} style={style} width="10" height="9" viewBox="0 0 10 9"><path d={TREE_CURVE_PATH}/></svg>
}

function OptionHover({ children, besideMenu = false, className, content, suppressed = false }: { children: ReactNode; besideMenu?: boolean; className?: string; content: ReactNode; suppressed?: boolean }) {
  const anchorRef = useRef<HTMLSpanElement>(null)
  const cardRef = useRef<HTMLDivElement>(null)
  const hideTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const showTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const [open, setOpen] = useState(false)
  const [position, setPosition] = useState({ left: 0, top: 0 })
  const showNow = () => {
    if (showTimer.current) clearTimeout(showTimer.current)
    if (hideTimer.current) clearTimeout(hideTimer.current)
    const rect = anchorRef.current?.getBoundingClientRect()
    if (rect) {
      const estimatedWidth = className?.includes('person-info-surface') ? 300 : className?.includes('lp-new-project-person-hover') ? 278 : 198
      const left = rect.right + estimatedWidth + 6 > window.innerWidth - 8 ? Math.max(8, rect.left - estimatedWidth - 6) : rect.right + 6
      setPosition({ left, top: rect.top })
    }
    setOpen(true)
  }
  const show = () => {
    if (showTimer.current) clearTimeout(showTimer.current)
    showTimer.current = setTimeout(() => { showTimer.current = undefined; showNow() }, 450)
  }
  const hide = () => {
    if (showTimer.current) clearTimeout(showTimer.current)
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
      let top = anchor.top
      if (besideMenu) {
        // To the left of the whole menu, 5px from it with its 0.5px border just above the row; the right side only
        // when there is no room.
        const menu = anchorRef.current?.closest('.property-command-surface')?.getBoundingClientRect() ?? anchor
        left = menu.left - card.width - BESIDE_MENU_GAP
        if (left < padding) left = menu.right + BESIDE_MENU_GAP
        top = anchor.top - 0.5
      } else if (left + card.width > window.innerWidth - padding) left = anchor.left - card.width - gap
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
  }, [open, besideMenu])
  useEffect(() => () => {
    if (hideTimer.current) clearTimeout(hideTimer.current)
    if (showTimer.current) clearTimeout(showTimer.current)
  }, [])
  return <>
    <span className="property-option-hover-trigger" onBlur={hide} onFocus={showNow} onPointerEnter={show} onPointerLeave={hide} ref={anchorRef} role="presentation">{children}</span>
    {open && !suppressed && createPortal(<div className={`property-option-hover-card ${className ?? 'property-rich-hover'}`} onPointerEnter={showNow} onPointerLeave={hide} ref={cardRef} role="tooltip" style={{ left: position.left, top: position.top }}>{content}</div>, document.body)}
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
  if (!sequence) {
    // Modifier combos ("⇧ E", "Shift P") render one keycap per key.
    const keys = value.split(/\s+/).map(key => key === 'Shift' ? '⇧' : key)
    return keys.length > 1 ? <span className="property-command-search-shortcut">{keys.map((key, index) => <kbd key={index}>{key}</kbd>)}</span> : <kbd>{value}</kbd>
  }
  return <span className="property-command-search-shortcut"><kbd>{sequence[1]}</kbd><span data-i18n-ignore>then</span><kbd>{sequence[2]}</kbd></span>
}
