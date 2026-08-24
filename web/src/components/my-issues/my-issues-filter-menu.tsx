import { cloneElement, Fragment, isValidElement, useMemo, useState, type ReactElement } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Command } from 'cmdk'
import { Archive, Bot, CalendarDays, Check, CircleDot, ExternalLink, FileText, Flag, GitBranch, Layers3, Link2, ListFilter, RefreshCw, Rocket, Sparkles, Tags, UserRound, Users } from 'lucide-react'
import { ChevronRightIcon } from './my-issues-icons'
import { CalendarIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import type { MyIssuesAppliedFilter } from './my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { useI18n } from '@/i18n/i18n'
import styles from './my-issues-filter-menu.module.css'

export interface MyIssuesFilterMenuProps {
  open: boolean
  trigger: ReactElement
  filters?: MyIssuesAppliedFilter[]
  options?: (filter: MyIssuesFilterKey) => MyIssuesFilterOption[] | undefined
  onOpenChange: (open: boolean) => void
  onToggle: (filter: MyIssuesFilterKey, option: MyIssuesFilterOption) => void
  availableFields?: MyIssuesFilterKey[]
}

const MY_ISSUES_FILTER_GROUPS = [
  [{ id: 'ai', label: 'AI filter' }],
  [{ id: 'advanced', label: 'Advanced filter' }],
  [
    { id: 'status', label: 'Status', submenu: true }, { id: 'assignee', label: 'Assignee', submenu: true },
    { id: 'agent', label: 'Agent', submenu: true }, { id: 'agentSession', label: 'Agent Session', submenu: true },
    { id: 'creator', label: 'Creator', submenu: true }, { id: 'priority', label: 'Priority', submenu: true },
    { id: 'labels', label: 'Labels', submenu: true }, { id: 'relations', label: 'Relations', submenu: true },
    { id: 'suggestedLabel', label: 'Suggested label', submenu: true }, { id: 'dates', label: 'Dates', submenu: true },
  ],
  [
    { id: 'project', label: 'Project', submenu: true }, { id: 'projectProperties', label: 'Project properties', submenu: true },
    { id: 'initiative', label: 'Initiative', submenu: true }, { id: 'cycle', label: 'Cycle', submenu: true },
    { id: 'addedToCycle', label: 'Added to cycle', submenu: true }, { id: 'releases', label: 'Releases', submenu: true },
  ],
  [
    { id: 'subscribers', label: 'Subscribers', submenu: true }, { id: 'externalSource', label: 'External source', submenu: true }, { id: 'autoClosed', label: 'Auto-closed' },
    { id: 'content', label: 'Content', submenu: true }, { id: 'links', label: 'Links', submenu: true },
    { id: 'template', label: 'Template', submenu: true },
  ],
] as const

export function MyIssuesFilterMenu({ availableFields, filters = [], onOpenChange, onToggle, open, options, trigger }: MyIssuesFilterMenuProps) {
  const { t } = useI18n()
  const [activeField, setActiveField] = useState<MyIssuesFilterKey>()
  const close = (next: boolean) => {
    if (!next) setActiveField(undefined)
    onOpenChange(next)
  }
  const openValues = (field: MyIssuesFilterKey) => {
    if (options?.(field)?.length) setActiveField(field)
  }
  const visibleGroups = MY_ISSUES_FILTER_GROUPS.map(group => group.filter(item => !availableFields || availableFields.includes(item.id as MyIssuesFilterKey))).filter(group => group.length)

  return <Popover.Root open={open} onOpenChange={close}>
    <Popover.Trigger asChild>{isValidElement(trigger) ? cloneElement(trigger, { 'aria-expanded': open } as object) : trigger}</Popover.Trigger>
    <Popover.Portal>
      <Popover.Content className={styles.rootMenu} side="bottom" align="center" alignOffset={-15} sideOffset={3} collisionPadding={11} onOpenAutoFocus={event => event.preventDefault()} onEscapeKeyDown={() => close(false)} onKeyDownCapture={event=>{if(event.key==='Escape'&&!activeField){event.preventDefault();close(false)}}}>
        <Command className={styles.rootCommand} loop>
          <div className={styles.rootSearch}>
            <Command.Input aria-label={t('Add Filter…')} placeholder={t('Add Filter…')} autoFocus/>
            <kbd aria-hidden="true">F</kbd>
          </div>
          <Command.List className={styles.rootList}>
            <Command.Empty className={styles.empty}>{t('No filters found')}</Command.Empty>
            {visibleGroups.map((visibleItems, groupIndex) => {
              return <Fragment key={groupIndex}>{groupIndex > 0 && <Command.Separator className={styles.rootSeparator}/>}<Command.Group className={styles.rootGroup}>
              {visibleItems.map(item => {
                const field = item.id as MyIssuesFilterKey
                const hasValues = Boolean(options?.(field)?.length)
                const hasSubmenu = 'submenu' in item && item.submenu
                const directApply = field === 'autoClosed'
                return <Popover.Root key={field} open={activeField === field} onOpenChange={next => setActiveField(next ? field : undefined)}>
                  <Popover.Anchor asChild>
                    <Command.Item
                      className={styles.rootItem}
                      value={item.label}
                      aria-disabled={!hasValues}
                      data-unavailable={!hasValues ? '' : undefined}
                      disabled={!hasValues}
                      onMouseMove={() => { if (activeField && hasValues) setActiveField(field) }}
                      onSelect={() => { if (directApply) { const option=options?.(field)?.[0]; if(option){onToggle(field,option);close(false)} } else openValues(field) }}
                    >
                      <span className={styles.rootIcon}><FilterFieldIcon field={field}/></span><span>{t(item.label)}</span>{hasSubmenu && <ChevronRightIcon/>}
                    </Command.Item>
                  </Popover.Anchor>
                  {hasValues && !directApply && <ValueMenu field={field} filters={filters} label={item.label} options={options?.(field) ?? []} onClose={() => setActiveField(undefined)} onToggle={(nextField,option)=>{onToggle(nextField,option);close(false)}}/>}
                </Popover.Root>
              })}
            </Command.Group></Fragment>})}
          </Command.List>
        </Command>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function ValueMenu({ field, filters, label, onClose, onToggle, options }: { field: MyIssuesFilterKey; filters: MyIssuesAppliedFilter[]; label: string; onClose: () => void; onToggle: MyIssuesFilterMenuProps['onToggle']; options: MyIssuesFilterOption[] }) {
  const { t } = useI18n()
  const selectedIds = useMemo(() => filters.filter(filter => filter.field === field).flatMap(filter => filter.values?.map(value => value.value) ?? [filter.value]), [field, filters])
  const command = usePropertyCommand({ closeOnSelect: false, onOpenChange: open => { if (!open) onClose() }, onSelect: option => onToggle(field, option), open: true, options, selectedIds })

  return <Popover.Portal>
    <Popover.Content className={styles.valueMenu} data-field={field} side="left" align="start" alignOffset={-43} sideOffset={-2} collisionPadding={11} onOpenAutoFocus={event => event.preventDefault()} onEscapeKeyDown={event => { event.preventDefault(); onClose() }} onKeyDown={command.onKeyDown}>
      <div className={styles.valueSearch}>
        <input ref={command.inputRef} role="searchbox" aria-label={`${t('Filter')} ${t(label)}`} placeholder={field==='content'?t('Filter by content…'):field==='ai'?t('AI filter'):t('Filter…')} value={command.query} onChange={event => command.onQueryChange(event.target.value)} onKeyDown={event=>{if(event.key!=='Enter'||!command.query.trim())return;if(field==='content'){event.preventDefault();event.stopPropagation();onToggle(field,{id:`query:${command.query.trim()}`,label:command.query.trim()})}else if(field==='ai'){event.preventDefault();event.stopPropagation();onToggle(field,interpretAIQuery(command.query))}}}/>
      </div>
      <div className={styles.valueList} role="listbox" aria-label={label} aria-multiselectable="true">
        {!command.filteredOptions.length && <div className={styles.empty}>{t('No results')}</div>}
        {command.filteredOptions.map(option => <FilterValueItem field={field} key={option.id || 'none'} option={option} active={command.activeId === option.id} selected={command.isSelected(option.id)} onActive={()=>command.setActiveId(option.id)} onChoose={next=>next.id==='content-prompt'?command.inputRef.current?.focus():command.choose(next)}/>) }
      </div>
    </Popover.Content>
  </Popover.Portal>
}

function FilterValueItem({ field, option, active, selected, onActive, onChoose }: { field: MyIssuesFilterKey; option: MyIssuesFilterOption; active:boolean; selected:boolean; onActive:()=>void; onChoose:(option:MyIssuesFilterOption)=>void }) {
  const {t}=useI18n()
  if(option.children?.length)return <Popover.Root><Popover.Trigger asChild><button type="button" role="option" aria-selected={active} aria-haspopup="listbox" className={styles.valueItem} onMouseMove={onActive}><span className={styles.optionSpacer}/><OptionMark field={field} option={option}/><span className={styles.valueLabel}>{t(option.label)}</span><ChevronRightIcon/></button></Popover.Trigger><Popover.Portal><Popover.Content className={`${styles.valueMenu} ${styles.nestedValueMenu}`} side="left" align="start" sideOffset={-2} collisionPadding={11} onOpenAutoFocus={event=>event.preventDefault()}><div className={styles.valueSearch}><input role="searchbox" aria-label={`${t('Filter')} ${t(option.label)}`} placeholder={t('Filter…')}/></div><div className={styles.valueList} role="listbox" aria-label={t(option.label)}>{option.children.map(child=><FilterValueItem field={field} key={child.id} option={child} active={false} selected={false} onActive={()=>{}} onChoose={onChoose}/>)}</div></Popover.Content></Popover.Portal></Popover.Root>
  return <button type="button" role="option" aria-selected={active} aria-checked={selected} className={styles.valueItem} onMouseMove={onActive} onClick={()=>onChoose(option)}><span className={styles.checkbox} role="checkbox" aria-checked={selected}>{selected&&<Check size={11}/>}</span><OptionMark field={field} option={option}/><span className={styles.valueLabel} data-i18n-ignore>{option.label}</span>{optionCount(option)!=null&&<span className={styles.count}>{optionCount(option)} {t(optionCount(option)===1?'issue':'issues')}</span>}</button>
}

function OptionMark({ field, option }: { field: MyIssuesFilterKey; option: MyIssuesFilterOption }) {
  const kind=option.kind??field
  if(kind==='status'&&option.stateType)return <StatusIcon state={{id:option.id,name:option.label,type:option.stateType,color:option.color??'var(--theme-text-secondary)'}} size={14}/>
  if(kind==='priority'){const priority=(option.priority??Number(option.id))||0;return <PriorityIcon priority={priority} size={14} style={{color:priorityColor(priority)}}/>}
  if(kind==='assignee')return option.id?<span className={styles.optionAvatar} style={option.avatarUrl?{backgroundImage:`url(${option.avatarUrl})`}:undefined}>{option.avatarUrl?'':initials(option.label)}</span>:<NoAssigneeIcon size={14}/>
  if(kind==='project')return option.id?<ProjectIcon size={14} style={{color:option.color}}/>:<NoProjectIcon size={14}/>
  if(kind==='labels')return option.color?<i className={styles.optionMark} style={{backgroundColor:option.color}}/>:<LabelIcon size={14}/>
  if(kind==='dueDate')return <CalendarIcon size={14}/>
  if(option.color)return <i className={styles.optionMark} style={{backgroundColor:option.color}}/>
  return <span className={styles.optionIcon}><FilterFieldIcon field={field}/></span>
}

function FilterFieldIcon({field}:{field:MyIssuesFilterKey}){const props={size:15};if(field==='ai')return <Sparkles {...props}/>;if(field==='advanced')return <ListFilter {...props}/>;if(field==='status')return <CircleDot {...props}/>;if(field==='assignee'||field==='creator')return <UserRound {...props}/>;if(field==='agent'||field==='agentSession')return <Bot {...props}/>;if(field==='priority')return <Flag {...props}/>;if(field==='labels'||field==='suggestedLabel')return <Tags {...props}/>;if(field==='relations')return <GitBranch {...props}/>;if(field==='dates'||field==='addedToCycle')return <CalendarDays {...props}/>;if(field==='project')return <ProjectIcon {...props}/>;if(field==='projectProperties'||field==='initiative')return <Layers3 {...props}/>;if(field==='cycle')return <RefreshCw {...props}/>;if(field==='releases')return <Rocket {...props}/>;if(field==='subscribers')return <Users {...props}/>;if(field==='externalSource'||field==='links')return <ExternalLink {...props}/>;if(field==='autoClosed')return <Archive {...props}/>;if(field==='content')return <FileText {...props}/>;if(field==='template')return <FileText {...props}/>;return <Link2 {...props}/>}
function priorityColor(priority:number|undefined){return ['var(--theme-text-tertiary)','var(--priority-urgent)','var(--priority-high)','var(--priority-medium)','var(--priority-low)'][priority??0]}
function initials(value:string){return value.split(/\s+/).filter(Boolean).slice(0,2).map(part=>part[0]).join('').toUpperCase()}

function optionCount(option: MyIssuesFilterOption) {
  return 'count' in option && typeof option.count === 'number' ? option.count : undefined
}
function interpretAIQuery(query:string):MyIssuesFilterOption{const normalized=query.toLocaleLowerCase();if(normalized.includes('assign')&&(normalized.includes('me')||normalized.includes('current user')))return{id:'assigned-to-me',label:query};if(normalized.includes('complete')&&normalized.includes('month'))return{id:'completed-last-month',label:query};if(normalized.includes('due')&&(normalized.includes('week')||normalized.includes('14')))return{id:'due-next-two-weeks',label:query};return{id:`query:${query.trim()}`,label:query.trim()}}
