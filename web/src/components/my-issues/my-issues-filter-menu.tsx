import { cloneElement, Fragment, isValidElement, useMemo, useState, type ReactElement } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as Popover from '@radix-ui/react-popover'
import { Command } from 'cmdk'
import { Archive, Bot, CalendarDays, CircleDot, ExternalLink, FileText, Flag, GitBranch, Layers3, Link2, ListFilter, RefreshCw, Rocket, Sparkles, Tags, UserRound, Users } from 'lucide-react'
import { ChevronRightIcon } from './my-issues-icons'
import { CalendarIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, ProjectStatusIcon, StatusIcon } from '@/components/issue/issue-icons'
import type { MyIssuesAppliedFilter } from './my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { useI18n } from '@/i18n/i18n'
import styles from './my-issues-filter-menu.module.css'
import { CheckboxMark } from '@/components/ui/checkbox-mark'

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
  const [textCondition, setTextCondition] = useState<{ field: MyIssuesFilterKey; option: MyIssuesFilterOption }>()
  const close = (next: boolean) => {
    if (!next) setActiveField(undefined)
    onOpenChange(next)
  }
  const openValues = (field: MyIssuesFilterKey) => {
    if (options?.(field)?.length) setActiveField(field)
  }
  const visibleGroups = MY_ISSUES_FILTER_GROUPS.map(group => group.filter(item => !availableFields || availableFields.includes(item.id as MyIssuesFilterKey))).filter(group => group.length)
  const choose = (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => {
    if (option.textConditionPrefix) {
      setTextCondition({ field, option })
      close(false)
      return
    }
    onToggle(field, option)
    close(false)
  }

  return <><Popover.Root open={open} onOpenChange={close}>
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
                      onFocus={() => { if (hasValues && !directApply) setActiveField(field) }}
                      onMouseMove={() => { if (hasValues && !directApply) setActiveField(field) }}
                      onSelect={() => { if (directApply) { const option=options?.(field)?.[0]; if(option){onToggle(field,option);close(false)} } else openValues(field) }}
                    >
                      <span className={styles.rootIcon}><FilterFieldIcon field={field}/></span><span>{t(item.label)}</span>{hasSubmenu && <ChevronRightIcon/>}
                    </Command.Item>
                  </Popover.Anchor>
                  {hasValues && !directApply && <ValueMenu field={field} filters={filters} label={item.label} options={options?.(field) ?? []} onClose={() => setActiveField(undefined)} onToggle={choose}/>}
                </Popover.Root>
              })}
            </Command.Group></Fragment>})}
          </Command.List>
        </Command>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root><TextConditionDialog condition={textCondition} onClose={() => setTextCondition(undefined)} onApply={(field, option) => { onToggle(field, option); setTextCondition(undefined) }}/></>
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
        <FilterValueItems field={field} options={command.filteredOptions} activeId={command.activeId} isSelected={command.isSelected} onActive={command.setActiveId} onChoose={next=>next.id==='content-prompt'?command.inputRef.current?.focus():command.choose(next)}/>
      </div>
    </Popover.Content>
  </Popover.Portal>
}

function FilterValueItems({ activeId, field, isSelected = () => false, onActive = () => {}, onChoose, options }: { activeId?: string; field: MyIssuesFilterKey; isSelected?: (id: string) => boolean; onActive?: (id: string) => void; onChoose: (option: MyIssuesFilterOption) => void; options: MyIssuesFilterOption[] }) {
  const [openId, setOpenId] = useState<string>()
  return <>{options.map(option => <FilterValueItem field={field} key={option.id || 'none'} option={option} active={activeId === option.id} selected={isSelected(option.id)} nestedOpen={openId === option.id} onActive={() => onActive(option.id)} onNestedOpen={next => setOpenId(next ? option.id : undefined)} onChoose={onChoose}/>)}</>
}

function FilterValueItem({ field, option, active, selected, nestedOpen, onActive, onNestedOpen, onChoose }: { field: MyIssuesFilterKey; option: MyIssuesFilterOption; active:boolean; selected:boolean; nestedOpen:boolean; onActive:()=>void; onNestedOpen:(open:boolean)=>void; onChoose:(option:MyIssuesFilterOption)=>void }) {
  const {t}=useI18n()
  if(option.children?.length)return <Popover.Root open={nestedOpen} onOpenChange={onNestedOpen}><Popover.Trigger asChild><button type="button" role="option" aria-selected={active} aria-expanded={nestedOpen} aria-haspopup="listbox" className={styles.valueItem} onMouseMove={()=>{onActive();onNestedOpen(true)}}><span className={styles.optionSpacer}/><OptionMark field={field} option={option}/><span className={styles.valueLabel}>{t(option.label)}</span><ChevronRightIcon/></button></Popover.Trigger><Popover.Portal><NestedValueMenu field={field} label={option.label} options={option.children} onChoose={onChoose} onClose={()=>onNestedOpen(false)}/></Popover.Portal></Popover.Root>
  if(option.textConditionPrefix)return <button type="button" role="option" aria-selected={active} className={`${styles.valueItem} ${styles.textConditionItem}`} onMouseMove={onActive} onClick={()=>onChoose(option)}><span className={styles.optionSpacer}/><span className={styles.valueLabel}>{t(option.label)}</span></button>
  return <button type="button" role="option" aria-selected={active} aria-checked={selected} className={styles.valueItem} onMouseMove={onActive} onClick={()=>onChoose(option)}><span className={styles.checkbox} role="checkbox" aria-checked={selected}>{selected&&<CheckboxMark/>}</span><OptionMark field={field} option={option}/><span className={styles.valueLabel} data-i18n-ignore>{option.label}</span>{optionCount(option)!=null&&<span className={styles.count}>{optionCount(option)} {t(optionCount(option)===1?'issue':'issues')}</span>}</button>
}

function NestedValueMenu({ field, label, onChoose, onClose, options }: { field: MyIssuesFilterKey; label: string; onChoose: (option: MyIssuesFilterOption) => void; onClose: () => void; options: MyIssuesFilterOption[] }) {
  const { t } = useI18n()
  const command = usePropertyCommand({ autoFocus: false, closeOnSelect: false, onOpenChange: open => { if (!open) onClose() }, onSelect: onChoose, open: true, options })
  return <Popover.Content className={`${styles.valueMenu} ${styles.nestedValueMenu}`} side="left" align="start" sideOffset={-2} collisionPadding={11} onOpenAutoFocus={event=>event.preventDefault()} onCloseAutoFocus={event=>event.preventDefault()} onEscapeKeyDown={event=>{event.preventDefault();onClose()}} onKeyDown={command.onKeyDown}>
    <div className={styles.valueSearch}><input ref={command.inputRef} role="searchbox" aria-label={`${t('Filter')} ${t(label)}`} placeholder={t('Filter…')} value={command.query} onChange={event=>command.onQueryChange(event.target.value)}/></div>
    <div className={styles.valueList} role="listbox" aria-label={t(label)}><FilterValueItems field={field} options={command.filteredOptions} activeId={command.activeId} onActive={command.setActiveId} onChoose={command.choose}/>{!command.filteredOptions.length&&<div className={styles.empty}>{t('No results')}</div>}</div>
  </Popover.Content>
}

function TextConditionDialog({ condition, onApply, onClose }: { condition?: { field: MyIssuesFilterKey; option: MyIssuesFilterOption }; onApply: (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => void; onClose: () => void }) {
  const { t } = useI18n()
  const [value, setValue] = useState('')
  const open = Boolean(condition)
  const close = () => { setValue(''); onClose() }
  const apply = () => {
    const normalized = value.trim()
    if (!condition || !normalized || !condition.option.textConditionPrefix) return
    onApply(condition.field, { ...condition.option, id: `${condition.option.textConditionPrefix}${normalized}`, label: normalized, textConditionPrefix: undefined })
    setValue('')
  }
  return <Dialog.Root open={open} onOpenChange={next => { if (!next) close() }}><Dialog.Portal>
    <Dialog.Overlay className={styles.conditionOverlay}/>
    <Dialog.Content className={styles.conditionDialog} aria-describedby={undefined} onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => document.querySelector<HTMLInputElement>(`.${styles.conditionInput}`)?.focus()) }}>
      <form onSubmit={event => { event.preventDefault(); apply() }}>
        <div className={styles.conditionBody}><Dialog.Title>{t(condition?.option.label ?? '')}</Dialog.Title><input className={styles.conditionInput} aria-label={t(condition?.option.label ?? '')} value={value} onChange={event => setValue(event.target.value)}/></div>
        <footer><button type="button" onClick={close}>{t('Cancel')}</button><button type="submit" className={styles.conditionApply}>{t('Apply')}</button></footer>
      </form>
    </Dialog.Content>
  </Dialog.Portal></Dialog.Root>
}

function OptionMark({ field, option }: { field: MyIssuesFilterKey; option: MyIssuesFilterOption }) {
  const kind=option.kind??field
  if(kind==='status'&&option.stateType)return <StatusIcon state={{id:option.id,name:option.label,type:option.stateType,color:option.color??'var(--theme-text-secondary)'}} size={14}/>
  if(kind==='priority'){const priority=(option.priority??Number(option.id))||0;return <PriorityIcon priority={priority} size={14} style={{color:priorityColor(priority)}}/>}
  if(kind==='assignee')return option.id?<span className={styles.optionAvatar} style={option.avatarUrl?{backgroundImage:`url(${option.avatarUrl})`}:undefined}>{option.avatarUrl?'':initials(option.label)}</span>:<NoAssigneeIcon size={14}/>
  if(kind==='project')return option.id?<ProjectIcon size={14} style={{color:option.color}}/>:<NoProjectIcon size={14}/>
  if(kind==='labels')return option.color?<i className={styles.optionMark} style={{backgroundColor:option.color}}/>:<LabelIcon size={14}/>
  if(kind==='dueDate')return <CalendarIcon size={14}/>
  if(kind==='projectStatusCategory')return <ProjectPropertyCategoryIcon kind="status"/>
  if(kind==='projectStatusTypeCategory')return <ProjectPropertyCategoryIcon kind="statusType"/>
  if(kind==='projectStatus'||kind==='projectStatusType')return <ProjectStatusIcon aria-label="" color={option.color} name={option.label} type={option.projectType} size={14}/>
  if(kind==='projectPriorityCategory')return <ProjectPropertyCategoryIcon kind="priority"/>
  if(kind==='projectPriority')return <PriorityIcon aria-label={undefined} priority={option.priority??0} size={14}/>
  if(kind==='projectLabels')return option.color?<i className={styles.optionMark} style={{backgroundColor:option.color}}/>:<ProjectPropertyCategoryIcon kind="labels"/>
  if(kind==='projectLeadCategory')return <ProjectPropertyCategoryIcon kind="lead"/>
  if(kind==='projectLead'){const leadId=option.id.startsWith('project-lead:')?option.id.slice(13):option.id;return leadId&&option.label!=='Current user'?<span className={styles.optionAvatar} style={option.avatarUrl?{backgroundImage:`url(${option.avatarUrl})`}:undefined}>{option.avatarUrl?'':initials(option.label)}</span>:<NoAssigneeIcon size={14}/>}
  if(kind==='projectMilestone'||kind==='projectMilestoneCategory')return <ProjectPropertyCategoryIcon kind="milestone"/>
  if(option.color)return <i className={styles.optionMark} style={{backgroundColor:option.color}}/>
  return <span className={styles.optionIcon}><FilterFieldIcon field={field}/></span>
}

function ProjectPropertyCategoryIcon({ kind }: { kind: 'status'|'statusType'|'priority'|'labels'|'lead'|'milestone' }) {
  if (kind === 'status') return <svg className={styles.categoryIcon} width="16" height="16" viewBox="1 1 14 14" fill="currentColor" aria-hidden="true"><path d="M2 4.74695C2 4.68722 2.01039 4.62899 2.02989 4.57451L2.11601 4.42269C2.15266 4.37819 2.19711 4.33975 2.24806 4.30966L3.16473 3.76824L3.92054 5.08013L3.5 5.32852V5.8313H2V4.74695Z"/><path d="M4.8372 4.53871L4.0814 3.22682L5.91473 2.14398L6.67054 3.45588L4.8372 4.53871Z"/><path d="M7.5872 2.91446L6.8314 1.60257L7.74806 1.06115C7.7997 1.03065 7.85539 1.01027 7.91244 1H8.08756C8.14461 1.01027 8.2003 1.03065 8.25194 1.06115L9.1686 1.60257L8.4128 2.91446L8 2.67065L7.5872 2.91446Z"/><path d="M9.32946 3.45588L10.0853 2.14398L11.9186 3.22682L11.1628 4.53871L9.32946 3.45588Z"/><path d="M12.0795 5.08013L12.8353 3.76824L13.7519 4.30966C13.8029 4.33975 13.8473 4.37819 13.884 4.42269L13.9701 4.57451C13.9896 4.62899 14 4.68722 14 4.74695V5.8313H12.5V5.32852L12.0795 5.08013Z"/><path d="M12.5 6.91565H14V9.08435H12.5V6.91565Z"/><path d="M12.5 10.1687H14V11.253C14 11.3128 13.9896 11.371 13.9701 11.4255L13.884 11.5773C13.8473 11.6218 13.8029 11.6602 13.7519 11.6903L12.8353 12.2318L12.0795 10.9199L12.5 10.6715V10.1687Z"/><path d="M11.1628 11.4613L11.9186 12.7732L10.0853 13.856L9.32946 12.5441L11.1628 11.4613Z"/><path d="M8.4128 13.0855L9.1686 14.3974L8.25194 14.9389C8.2003 14.9694 8.14461 14.9897 8.08756 15H7.91244C7.85539 14.9897 7.7997 14.9694 7.74806 14.9389L6.8314 14.3974L7.5872 13.0855L8 13.3294L8.4128 13.0855Z"/><path d="M6.67054 12.5441L5.91473 13.856L4.0814 12.7732L4.8372 11.4613L6.67054 12.5441Z"/><path d="M3.92054 10.9199L3.16473 12.2318L2.24806 11.6903C2.19711 11.6602 2.15266 11.6218 2.11601 11.5773L2.02989 11.4255C2.01039 11.371 2 11.3128 2 11.253V10.1687H3.5V10.6715L3.92054 10.9199Z"/><path d="M3.5 9.08435H2V6.91565H3.5V9.08435Z"/></svg>
  if (kind === 'statusType') return <svg className={styles.categoryIcon} width="16" height="16" viewBox="1 1 14 14" fill="currentColor" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M12.5 5.36133L8 2.73633L3.5 5.36133L3.5 10.6382L8 13.2632L12.5 10.6382L12.5 5.36133ZM8.75581 1.44066C8.28876 1.16822 7.71124 1.16822 7.24419 1.44066L2.74419 4.06566C2.28337 4.33448 2 4.82783 2 5.36133V10.6382C2 11.1717 2.28337 11.6651 2.74419 11.9339L7.24419 14.5589C7.71124 14.8313 8.28876 14.8313 8.75581 14.5589L13.2558 11.9339C13.7166 11.6651 14 11.1717 14 10.6382V5.36133C14 4.82783 13.7166 4.33448 13.2558 4.06566L8.75581 1.44066Z"/></svg>
  if (kind === 'priority') return <svg className={styles.categoryIcon} width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><rect x="1" y="8" width="3" height="6" rx="1"/><rect x="6" y="5" width="3" height="9" rx="1"/><rect x="11" y="2" width="3" height="12" rx="1"/></svg>
  if (kind === 'labels') return <LabelIcon className={styles.categoryIcon} size={16}/>
  if (kind === 'lead') return <svg className={styles.categoryIcon} width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M8.57502 8C10.6434 8.00003 12.4741 9.33858 13.1014 11.3096L13.9647 14.0225C14.0902 14.4171 13.872 14.8392 13.4774 14.9648C13.0827 15.0903 12.6606 14.8721 12.535 14.4775L11.6717 11.7646C11.2426 10.416 9.99026 9.50003 8.57502 9.5H7.42462C6.00944 9.50011 4.75705 10.4161 4.32795 11.7646L3.46466 14.4775C3.33902 14.8722 2.91695 15.0904 2.52228 14.9648C2.12775 14.8392 1.90945 14.4171 2.03498 14.0225L2.89826 11.3096C3.52547 9.33861 5.35628 8.00011 7.42462 8H8.57502Z"/><path fillRule="evenodd" clipRule="evenodd" d="M7.99982 1C9.5186 1 10.7498 2.23122 10.7498 3.75C10.7498 5.26878 9.5186 6.5 7.99982 6.5C6.48119 6.49982 5.24982 5.26867 5.24982 3.75C5.24982 2.23133 6.48119 1.00018 7.99982 1Z"/></svg>
  return <svg className={styles.categoryIcon} width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M7.3406 2.32C7.68741 1.89333 8.31259 1.89333 8.6594 2.32L12.7903 7.402C13.0699 7.74597 13.0699 8.25403 12.7903 8.598L8.6594 13.68C8.31259 14.1067 7.68741 14.1067 7.3406 13.68L3.2097 8.598C2.9301 8.25403 2.9301 7.74597 3.2097 7.402L7.3406 2.32Z" stroke="currentColor" strokeWidth="2" strokeLinejoin="round"/></svg>
}

function FilterFieldIcon({field}:{field:MyIssuesFilterKey}){const props={size:15};if(field==='ai')return <Sparkles {...props}/>;if(field==='advanced')return <ListFilter {...props}/>;if(field==='status')return <CircleDot {...props}/>;if(field==='assignee'||field==='creator')return <UserRound {...props}/>;if(field==='agent'||field==='agentSession')return <Bot {...props}/>;if(field==='priority')return <Flag {...props}/>;if(field==='labels'||field==='suggestedLabel')return <Tags {...props}/>;if(field==='relations')return <GitBranch {...props}/>;if(field==='dates'||field==='addedToCycle')return <CalendarDays {...props}/>;if(field==='project')return <ProjectIcon {...props}/>;if(field==='projectProperties'||field==='initiative')return <Layers3 {...props}/>;if(field==='cycle')return <RefreshCw {...props}/>;if(field==='releases')return <Rocket {...props}/>;if(field==='subscribers')return <Users {...props}/>;if(field==='externalSource'||field==='links')return <ExternalLink {...props}/>;if(field==='autoClosed')return <Archive {...props}/>;if(field==='content')return <FileText {...props}/>;if(field==='template')return <FileText {...props}/>;return <Link2 {...props}/>}
function priorityColor(priority:number|undefined){return ['var(--theme-text-tertiary)','var(--priority-urgent)','var(--priority-high)','var(--priority-medium)','var(--priority-low)'][priority??0]}
function initials(value:string){return value.split(/\s+/).filter(Boolean).slice(0,2).map(part=>part[0]).join('').toUpperCase()}

function optionCount(option: MyIssuesFilterOption) {
  return 'count' in option && typeof option.count === 'number' ? option.count : undefined
}
function interpretAIQuery(query:string):MyIssuesFilterOption{const normalized=query.toLocaleLowerCase();if(normalized.includes('assign')&&(normalized.includes('me')||normalized.includes('current user')))return{id:'assigned-to-me',label:query};if(normalized.includes('complete')&&normalized.includes('month'))return{id:'completed-last-month',label:query};if(normalized.includes('due')&&(normalized.includes('week')||normalized.includes('14')))return{id:'due-next-two-weeks',label:query};return{id:`query:${query.trim()}`,label:query.trim()}}
