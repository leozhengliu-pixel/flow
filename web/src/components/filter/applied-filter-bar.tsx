import * as Popover from '@radix-ui/react-popover'
import { Check, Plus, X } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'

import { usePropertyCommand } from '@/components/property/use-property-command'
import styles from './applied-filter-bar.module.css'
import { CheckboxMark } from '@/components/ui/checkbox-mark'

export type AppliedFilterOperator = 'is' | 'isNot'
export type AppliedFilterOption = { id: string; label: string; color?: string; count?: number }
export type AppliedFilterItem<TOption extends AppliedFilterOption = AppliedFilterOption> = { id: string; fieldLabel: string; operator: AppliedFilterOperator; values: TOption[]; operatorLabel?: string; negativeOperatorLabel?: string }

export function AppliedFilterBar<TFilter extends AppliedFilterItem<TOption>, TOption extends AppliedFilterOption>({ ariaLabel, clearLabel = 'Clear', countLabel, fieldVisual, filters, onAdd, onClear, onOperatorChange, onRemove, onSave, onValuesChange, optionsFor, saveLabel = 'Save', saveState = 'idle', translate = value => value }: {
  ariaLabel: string
  clearLabel?: string
  countLabel?: (count: number) => string
  fieldVisual?: (filter: TFilter) => ReactNode
  filters: TFilter[]
  onAdd: () => void
  onClear: () => void
  onOperatorChange: (filter: TFilter, operator: AppliedFilterOperator) => void
  onRemove: (filter: TFilter) => void
  onSave?: () => void
  onValuesChange: (filter: TFilter, values: TOption[]) => void
  optionsFor: (filter: TFilter) => TOption[]
  saveLabel?: string
  saveState?: 'idle'|'saving'|'saved'|'error'
  translate?: (value: string) => string
}) {
  if (!filters.length) return null
  return <div className={styles.bar} aria-label={ariaLabel}>
    <div className={styles.filters}>{filters.map(filter => <div className={styles.condition} key={filter.id}>
      <span className={styles.field}>{fieldVisual?.(filter) ?? <i/>}<span>{filter.fieldLabel}</span></span>
      <OperatorMenu filter={filter} onChange={operator => onOperatorChange(filter, operator)} translate={translate}/>
      <ValueMenu countLabel={countLabel} filter={filter} onChange={values => onValuesChange(filter, values)} options={optionsFor(filter)}/>
      <button className={styles.remove} aria-label={`Remove ${filter.fieldLabel} filter`} onClick={() => onRemove(filter)} type="button"><X size={13}/></button>
    </div>)}<button className={styles.add} aria-label="Add another filter" onClick={onAdd} type="button"><Plus size={14}/></button></div>
    <div className={styles.commands}><button aria-label="Clear all filters" onClick={onClear} type="button">{clearLabel}</button>{onSave && <button aria-label="Create new view" data-state={saveState} disabled={saveState === 'saving'} onClick={onSave} type="button">{saveState === 'saving' ? 'Saving...' : saveState === 'saved' ? 'Saved' : saveState === 'error' ? 'Retry save' : saveLabel}</button>}</div>
  </div>
}

function OperatorMenu<TOption extends AppliedFilterOption>({ filter, onChange, translate }: { filter: AppliedFilterItem<TOption>; onChange: (operator: AppliedFilterOperator) => void; translate: (value: string) => string }) {
  const positive = translate(filter.operatorLabel ?? 'is'), negative = translate(filter.negativeOperatorLabel ?? 'is not')
  return <Popover.Root><Popover.Trigger asChild><button className={styles.operator} type="button" aria-label={`${filter.fieldLabel} operator`}>{filter.operator === 'is' ? positive : negative}</button></Popover.Trigger><Popover.Portal><Popover.Content align="start" className={styles.operatorMenu} collisionPadding={8} sideOffset={4}>
    {(['is','isNot'] as const).map(operator => <Popover.Close asChild key={operator}><button aria-checked={operator === filter.operator} onClick={() => onChange(operator)} role="menuitemradio" type="button"><span>{operator === 'is' ? positive : negative}</span>{operator === filter.operator && <Check size={13}/>}</button></Popover.Close>)}
  </Popover.Content></Popover.Portal></Popover.Root>
}

function ValueMenu<TOption extends AppliedFilterOption>({ countLabel, filter, onChange, options }: { countLabel?: (count: number) => string; filter: AppliedFilterItem<TOption>; onChange: (values: TOption[]) => void; options: TOption[] }) {
  const [open,setOpen]=useState(false),selectedIds=useMemo(()=>filter.values.map(value=>value.id),[filter.values])
  const command=usePropertyCommand({closeOnSelect:false,onOpenChange:setOpen,open,options,selectedIds,onSelect:option=>onChange(selectedIds.includes(option.id)?filter.values.filter(value=>value.id!==option.id):[...filter.values,option])})
  return <Popover.Root open={open} onOpenChange={setOpen}><Popover.Trigger asChild><button aria-label={`${filter.fieldLabel} values`} className={styles.value} type="button"><ValueSummary values={filter.values}/></button></Popover.Trigger><Popover.Portal><Popover.Content align="start" className={styles.valueMenu} collisionPadding={8} onKeyDown={command.onKeyDown} onOpenAutoFocus={event=>event.preventDefault()} sideOffset={4}>
    <div className={styles.search}><input aria-label="Filter values" onChange={event=>command.onQueryChange(event.target.value)} placeholder="Filter..." ref={command.inputRef} value={command.query}/></div>
    <div className={styles.options} role="listbox" aria-multiselectable="true">{command.filteredOptions.map(option=><button aria-checked={command.isSelected(option.id)} aria-selected={command.activeId===option.id} key={option.id||'none'} onClick={()=>command.choose(option)} onMouseMove={()=>command.setActiveId(option.id)} role="option" type="button"><span className={styles.checkbox}>{command.isSelected(option.id)&&<CheckboxMark/>}</span><i style={{background:option.color??'var(--theme-text-secondary)'}}/><span data-i18n-ignore>{option.label}</span>{option.count!==undefined&&<small>{option.count} {countLabel?.(option.count)}</small>}</button>)}{!command.filteredOptions.length&&<span className={styles.empty}>No results</span>}</div>
  </Popover.Content></Popover.Portal></Popover.Root>
}

function ValueSummary({ values }: { values: AppliedFilterOption[] }) {
  if(values.length===1)return <><i style={{background:values[0].color??'var(--theme-text-secondary)'}}/><span data-i18n-ignore>{values[0].label}</span></>
  return <><span className={styles.stack}>{values.slice(0,3).map((value,index)=><i key={value.id} style={{background:value.color??'var(--theme-text-secondary)',zIndex:3-index}}/>)}</span><span data-i18n-ignore>{values.map(value=>value.label).join(', ')}</span></>
}
