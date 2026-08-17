import { useMemo, useState } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Check, Plus, X } from 'lucide-react'
import { usePropertyCommand } from '@/components/property/use-property-command'
import type { ProjectFilter, ProjectFilterField, ProjectFilterOperator, ProjectFilterOption } from './projects-filter-model'
import styles from './projects-filter-bar.module.css'

export function ProjectsFilterBar({ filters, options, onAdd, onChange, onClear, onRemove, onSave }: {
  filters: ProjectFilter[]
  options: Partial<Record<ProjectFilterField, ProjectFilterOption[]>>
  onAdd: () => void
  onChange: (filter: ProjectFilter) => void
  onClear: () => void
  onRemove: (id: string) => void
  onSave?: () => void
}) {
  if (!filters.length) return null
  return <div className={styles.bar} aria-label="Applied project filters">
    <div className={styles.filters}>{filters.map(filter => <div className={styles.condition} key={filter.id}>
      <span className={styles.field}><i style={{ background: filter.values[0]?.color }} />{filter.fieldLabel}</span>
      <OperatorMenu filter={filter} onChange={operator => onChange({ ...filter, operator })} />
      <ValueMenu filter={filter} options={options[filter.field] ?? []} onChange={values => values.length ? onChange({ ...filter, values }) : onRemove(filter.id)} />
      <button className={styles.remove} aria-label="Remove filter" onClick={() => onRemove(filter.id)} type="button"><X size={13} /></button>
    </div>)}<button className={styles.add} aria-label="Add another filter" onClick={onAdd} type="button"><Plus size={14} /></button></div>
    <div className={styles.commands}><button aria-label="Clear all filters" onClick={onClear} type="button">Clear</button>{onSave && <button aria-label="Create new view" onClick={onSave} type="button">Save</button>}</div>
  </div>
}

function OperatorMenu({ filter, onChange }: { filter: ProjectFilter; onChange: (operator: ProjectFilterOperator) => void }) {
  return <Popover.Root><Popover.Trigger asChild><button className={styles.operator} type="button">{filter.operator === 'is' ? 'is' : 'is not'}</button></Popover.Trigger><Popover.Portal><Popover.Content align="start" className={styles.operatorMenu} collisionPadding={8} sideOffset={4}>
    {(['is', 'isNot'] as const).map(operator => <Popover.Close asChild key={operator}><button aria-checked={operator === filter.operator} onClick={() => onChange(operator)} role="menuitemradio" type="button"><span>{operator === 'is' ? 'is' : 'is not'}</span>{operator === filter.operator && <Check size={13} />}</button></Popover.Close>)}
  </Popover.Content></Popover.Portal></Popover.Root>
}

function ValueMenu({ filter, options, onChange }: { filter: ProjectFilter; options: ProjectFilterOption[]; onChange: (values: ProjectFilterOption[]) => void }) {
  const [open, setOpen] = useState(false)
  const selectedIds = useMemo(() => filter.values.map(value => value.id), [filter.values])
  const command = usePropertyCommand({ closeOnSelect: false, onOpenChange: setOpen, open, options, selectedIds, onSelect: option => onChange(selectedIds.includes(option.id) ? filter.values.filter(value => value.id !== option.id) : [...filter.values, option]) })
  return <Popover.Root open={open} onOpenChange={setOpen}><Popover.Trigger asChild><button aria-label={`${filter.fieldLabel} values`} className={styles.value} type="button"><ValueSummary values={filter.values} /></button></Popover.Trigger><Popover.Portal><Popover.Content align="start" className={styles.valueMenu} collisionPadding={8} onKeyDown={command.onKeyDown} onOpenAutoFocus={event => event.preventDefault()} sideOffset={4}>
    <div className={styles.search}><input aria-label="Filter project values" onChange={event => command.onQueryChange(event.target.value)} placeholder="Filter..." ref={command.inputRef} value={command.query} /></div>
    <div className={styles.options} role="listbox" aria-multiselectable="true">{command.filteredOptions.map(option => <button aria-checked={command.isSelected(option.id)} aria-selected={command.activeId === option.id} key={option.id} onClick={() => command.choose(option)} onMouseMove={() => command.setActiveId(option.id)} role="option" type="button"><span className={styles.checkbox}>{command.isSelected(option.id) && <Check size={11} />}</span><i style={{ background: option.color ?? '#77777c' }} /><span>{option.label}</span>{option.count !== undefined && <small>{option.count} {option.count === 1 ? 'project' : 'projects'}</small>}</button>)}{!command.filteredOptions.length && <span className={styles.empty}>No results</span>}</div>
  </Popover.Content></Popover.Portal></Popover.Root>
}

function ValueSummary({ values }: { values: ProjectFilterOption[] }) {
  if (values.length === 1) return <><i style={{ background: values[0].color ?? '#77777c' }} /><span>{values[0].label}</span></>
  return <><span className={styles.stack}>{values.slice(0, 3).map((value, index) => <i key={value.id} style={{ background: value.color ?? '#77777c', zIndex: 3 - index }} />)}</span><span>{values.map(value => value.label).join(', ')}</span></>
}
