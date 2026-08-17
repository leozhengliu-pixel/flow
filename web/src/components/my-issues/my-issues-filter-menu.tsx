import { cloneElement, isValidElement, useMemo, useState, type ReactElement } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Command } from 'cmdk'
import { Check } from 'lucide-react'
import { ChevronRightIcon } from './my-issues-icons'
import type { MyIssuesAppliedFilter } from './my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'
import { usePropertyCommand } from '@/components/property/use-property-command'
import styles from './my-issues-filter-menu.module.css'

export interface MyIssuesFilterMenuProps {
  open: boolean
  trigger: ReactElement
  filters?: MyIssuesAppliedFilter[]
  options?: (filter: MyIssuesFilterKey) => MyIssuesFilterOption[] | undefined
  onOpenChange: (open: boolean) => void
  onToggle: (filter: MyIssuesFilterKey, option: MyIssuesFilterOption) => void
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
    { id: 'initiative', label: 'Initiative', submenu: true }, { id: 'customers', label: 'Customers', submenu: true },
  ],
  [
    { id: 'subscribers', label: 'Subscribers', submenu: true }, { id: 'autoClosed', label: 'Auto-closed' },
    { id: 'content', label: 'Content', submenu: true }, { id: 'links', label: 'Links', submenu: true },
    { id: 'template', label: 'Template', submenu: true },
  ],
] as const

export function MyIssuesFilterMenu({ filters = [], onOpenChange, onToggle, open, options, trigger }: MyIssuesFilterMenuProps) {
  const [activeField, setActiveField] = useState<MyIssuesFilterKey>()
  const close = (next: boolean) => {
    if (!next) setActiveField(undefined)
    onOpenChange(next)
  }
  const openValues = (field: MyIssuesFilterKey) => {
    if (options?.(field)?.length) setActiveField(field)
  }

  return <Popover.Root open={open} onOpenChange={close}>
    <Popover.Trigger asChild>{isValidElement(trigger) ? cloneElement(trigger, { 'aria-expanded': open } as object) : trigger}</Popover.Trigger>
    <Popover.Portal>
      <Popover.Content className={styles.rootMenu} side="bottom" align="center" alignOffset={-15} sideOffset={3} collisionPadding={11} onOpenAutoFocus={event => event.preventDefault()}>
        <Command className={styles.rootCommand} loop>
          <div className={styles.rootSearch}>
            <Command.Input aria-label="Add Filter..." placeholder="Add Filter..." autoFocus/>
            <kbd aria-hidden="true">F</kbd>
          </div>
          <Command.List className={styles.rootList}>
            <Command.Empty className={styles.empty}>No filters found</Command.Empty>
            {MY_ISSUES_FILTER_GROUPS.map((group, groupIndex) => <Command.Group className={styles.rootGroup} key={groupIndex}>
              {group.map(item => {
                const field = item.id as MyIssuesFilterKey
                const hasValues = Boolean(options?.(field)?.length)
                const hasSubmenu = 'submenu' in item && item.submenu
                return <Popover.Root key={field} open={activeField === field} onOpenChange={next => setActiveField(next ? field : undefined)}>
                  <Popover.Anchor asChild>
                    <Command.Item
                      className={styles.rootItem}
                      value={item.label}
                      data-unavailable={hasSubmenu && !hasValues ? '' : undefined}
                      onMouseMove={() => { if (activeField && hasValues) setActiveField(field) }}
                      onSelect={() => openValues(field)}
                    >
                      <span>{item.label}</span>{hasSubmenu && <ChevronRightIcon/>}
                    </Command.Item>
                  </Popover.Anchor>
                  {hasValues && <ValueMenu field={field} filters={filters} label={item.label} options={options?.(field) ?? []} onClose={() => setActiveField(undefined)} onToggle={onToggle}/>}
                </Popover.Root>
              })}
            </Command.Group>)}
          </Command.List>
        </Command>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function ValueMenu({ field, filters, label, onClose, onToggle, options }: { field: MyIssuesFilterKey; filters: MyIssuesAppliedFilter[]; label: string; onClose: () => void; onToggle: MyIssuesFilterMenuProps['onToggle']; options: MyIssuesFilterOption[] }) {
  const selectedIds = useMemo(() => filters.filter(filter => filter.field === field).flatMap(filter => filter.values?.map(value => value.value) ?? [filter.value]), [field, filters])
  const command = usePropertyCommand({ closeOnSelect: false, onOpenChange: open => { if (!open) onClose() }, onSelect: option => onToggle(field, option), open: true, options, selectedIds })

  return <Popover.Portal>
    <Popover.Content className={styles.valueMenu} side="left" align="start" alignOffset={-43} sideOffset={-2} collisionPadding={11} onOpenAutoFocus={event => event.preventDefault()} onKeyDown={command.onKeyDown}>
      <div className={styles.valueSearch}>
        <input ref={command.inputRef} role="searchbox" aria-label={`Filter ${label}`} placeholder="Filter..." value={command.query} onChange={event => command.onQueryChange(event.target.value)}/>
      </div>
      <div className={styles.valueList} role="listbox" aria-label={label} aria-multiselectable="true">
        {!command.filteredOptions.length && <div className={styles.empty}>No results</div>}
        {command.filteredOptions.map(option => {
          const selected = command.isSelected(option.id)
          return <button
            type="button"
            role="option"
            aria-selected={command.activeId === option.id}
            aria-checked={selected}
            className={styles.valueItem}
            key={option.id || 'none'}
            onMouseMove={() => command.setActiveId(option.id)}
            onClick={() => command.choose(option)}
          >
            <span className={styles.checkbox}>{selected && <Check size={11}/>}</span>
            <OptionMark option={option}/>
            <span className={styles.valueLabel}>{option.label}</span>
            {optionCount(option) != null && <span className={styles.count}>{optionCount(option)} {optionCount(option) === 1 ? 'issue' : 'issues'}</span>}
          </button>
        })}
      </div>
    </Popover.Content>
  </Popover.Portal>
}

function OptionMark({ option }: { option: MyIssuesFilterOption }) {
  if (!option.color) return <span className={styles.optionMark}/>
  return <i className={styles.optionMark} style={{ backgroundColor: option.color }}/>
}

function optionCount(option: MyIssuesFilterOption) {
  return 'count' in option && typeof option.count === 'number' ? option.count : undefined
}
