import { useMemo, useState } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Check, Plus, X } from 'lucide-react'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { filterValues, type MyIssuesAppliedFilter, type MyIssuesFilterOperator } from './my-issues-filter-types'
import type { MyIssuesFilterOption } from './my-issues-surface'
import styles from './my-issues-filter-bar.module.css'

export type { MyIssuesAppliedFilter, MyIssuesFilterOperator } from './my-issues-filter-types'

export function MyIssuesFilterBar({ filters, filterOptions, saveState = 'idle', onAdd, onClear, onOperatorChange, onRemove, onSave, onValuesChange }: {
  filters: MyIssuesAppliedFilter[]
  filterOptions?: (filter: MyIssuesAppliedFilter) => MyIssuesFilterOption[] | undefined
  saveState?: 'idle' | 'saving' | 'saved' | 'error'
  onAdd?: () => void
  onClear: () => void
  onOperatorChange?: (id: string, operator: MyIssuesFilterOperator) => void
  onRemove: (id: string) => void
  onSave?: () => void
  onValuesChange?: (id: string, options: MyIssuesFilterOption[]) => void
}) {
  if (!filters.length) return null
  return <div className={styles.bar} aria-label="Applied filters">
    <div className={styles.filters}>
      {filters.map(filter => <div className={styles.condition} key={filter.id}>
        <span className={styles.field}><FilterGlyph field={filter.field}/><span>{filter.fieldLabel}</span></span>
        <OperatorMenu filter={filter} onChange={operator => onOperatorChange?.(filter.id, operator)}/>
        <ValueEditor filter={filter} options={filterOptions?.(filter)} onChange={values => onValuesChange?.(filter.id, values)}/>
        <button type="button" className={styles.remove} aria-label={`Remove ${filter.fieldLabel} filter`} onClick={() => onRemove(filter.id)}><X size={13}/></button>
      </div>)}
      <button type="button" className={styles.add} aria-label="Add another filter" onClick={onAdd}><Plus size={14}/></button>
    </div>
    <div className={styles.commands}>
      <button type="button" aria-label="Clear all filters" onClick={onClear}>Clear</button>
      {onSave && <button type="button" aria-label="Create new view" data-state={saveState} disabled={saveState === 'saving'} onClick={onSave}>{saveState === 'saving' ? 'Saving...' : saveState === 'saved' ? 'Saved' : saveState === 'error' ? 'Retry save' : 'Save'}</button>}
    </div>
  </div>
}

function OperatorMenu({ filter, onChange }: { filter: MyIssuesAppliedFilter; onChange: (operator: MyIssuesFilterOperator) => void }) {
  return <Popover.Root>
    <Popover.Trigger asChild><button type="button" className={styles.operator} aria-label={`${filter.fieldLabel} operator`}>{filter.operator === 'is' ? 'is' : 'is not'}</button></Popover.Trigger>
    <Popover.Portal><Popover.Content className={styles.operatorMenu} side="bottom" align="start" sideOffset={4} collisionPadding={8}>
      {(['is', 'isNot'] as const).map(operator => <Popover.Close asChild key={operator}><button type="button" role="menuitemradio" aria-checked={filter.operator === operator} onClick={() => onChange(operator)}><span>{operator === 'is' ? 'is' : 'is not'}</span>{filter.operator === operator && <Check size={13}/>}</button></Popover.Close>)}
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function ValueEditor({ filter, onChange, options = [] }: { filter: MyIssuesAppliedFilter; options?: MyIssuesFilterOption[]; onChange: (values: MyIssuesFilterOption[]) => void }) {
  const [open, setOpen] = useState(false)
  const selected = filterValues(filter)
  const selectedIds = useMemo(() => selected.map(value => value.value), [selected])
  const selectedOptions = useMemo(() => selected.map(value => options.find(option => option.id === value.value) ?? { id: value.value, label: value.valueLabel, color: value.color }), [options, selected])
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild><button type="button" className={styles.value} aria-label={`${filter.fieldLabel} value`}>
      <ValueSummary values={selected}/>
    </button></Popover.Trigger>
    {options.length > 0 && <FilterValuePopover open={open} options={options} selectedIds={selectedIds} onChange={onChange} onOpenChange={setOpen} selectedOptions={selectedOptions}/>}
  </Popover.Root>
}

function FilterValuePopover({ onChange, onOpenChange, open, options, selectedIds, selectedOptions }: { open: boolean; options: MyIssuesFilterOption[]; selectedIds: string[]; selectedOptions: MyIssuesFilterOption[]; onChange: (values: MyIssuesFilterOption[]) => void; onOpenChange: (open: boolean) => void }) {
  const command = usePropertyCommand({
    closeOnSelect: false,
    onOpenChange,
    open,
    options,
    selectedIds,
    onSelect: option => onChange(selectedIds.includes(option.id) ? selectedOptions.filter(value => value.id !== option.id) : [...selectedOptions, option]),
  })
  return <Popover.Portal><Popover.Content className={styles.valueMenu} side="bottom" align="start" sideOffset={4} collisionPadding={8} onOpenAutoFocus={event => event.preventDefault()} onKeyDown={command.onKeyDown}>
    <div className={styles.menuSearch}><input ref={command.inputRef} aria-label="Filter values" placeholder="Filter..." value={command.query} onChange={event => command.onQueryChange(event.target.value)}/></div>
    <div className={styles.valueOptions} role="listbox" aria-multiselectable="true">
      {command.filteredOptions.map(option => <button type="button" role="option" aria-selected={command.activeId === option.id} aria-checked={command.isSelected(option.id)} key={option.id || 'none'} onMouseMove={() => command.setActiveId(option.id)} onClick={() => command.choose(option)}>
        <span className={styles.checkbox}>{command.isSelected(option.id) && <Check size={11}/>}</span>
        <i style={{ backgroundColor: option.color ?? 'currentColor' }}/><span>{option.label}</span>
      </button>)}
      {!command.filteredOptions.length && <span className={styles.empty}>No results</span>}
    </div>
  </Popover.Content></Popover.Portal>
}

function ValueSummary({ values }: { values: ReturnType<typeof filterValues> }) {
  if (values.length === 1) return <>{values[0].color && <i style={{ backgroundColor: values[0].color }}/>}<span>{values[0].valueLabel}</span></>
  return <><span className={styles.valueStack}>{values.slice(0, 3).map((value, index) => <i key={value.value} style={{ backgroundColor: value.color ?? `lch(${62 - index * 8}% 1.2 272)`, zIndex: 3 - index }}/>)}</span><span>{values.map(value => value.valueLabel).join(', ')}</span></>
}

function FilterGlyph({ field }: { field: MyIssuesAppliedFilter['field'] }) {
  if (field === 'priority') return <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><rect x="1" y="8" width="3" height="5" rx="1"/><rect x="5.5" y="5" width="3" height="8" rx="1"/><rect x="10" y="2" width="3" height="11" rx="1"/></svg>
  return <span className={styles.dot}/>
}
