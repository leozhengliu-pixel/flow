import * as Popover from '@radix-ui/react-popover'
import { Check, Plus, X } from 'lucide-react'
import { Fragment, useMemo, useState, type ReactNode } from 'react'

import { usePropertyCommand } from '@/components/property/use-property-command'
import styles from './applied-filter-bar.module.css'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { PersonHover } from '@/components/property/person-info'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { isPeopleProperty } from '@/lib/people'

export type AppliedFilterOperator = 'is' | 'isNot'
export type AppliedFilterOption = { id: string; label: string; color?: string; count?: number; icon?: ReactNode }
export type AppliedFilterOperatorChoice<TOperator extends string = string> = { operator: TOperator; label: string }
export type AppliedFilterItem<TOption extends AppliedFilterOption = AppliedFilterOption, TOperator extends string = AppliedFilterOperator> = {
  id: string
  fieldLabel: string
  operator: TOperator
  values: TOption[]
  operatorLabel?: string
  negativeOperatorLabel?: string
  /** Linear operator menus ("is any of", "include all of", "before"…); defaults to is / is not. */
  operatorChoices?: AppliedFilterOperatorChoice<TOperator>[]
  /** Several values summarised ("2 priorities"); defaults to the joined labels. */
  valueSummary?: string
}

type Translate = (value: string) => string

export function AppliedFilterBar<TFilter extends AppliedFilterItem<TOption, string>, TOption extends AppliedFilterOption>({ ariaLabel, className, clearLabel = 'Clear', commands, compact = false, countLabel, fieldVisual, filters, onAdd, onClear, onOperatorChange, onRemove, onSave, onValuesChange, optionsFor, renderFilter, saveLabel = 'Save', saveState = 'idle', showEmpty = false, translate = value => value, wrap = false }: {
  ariaLabel: string
  className?: string
  clearLabel?: string
  /** Replaces the default Clear / Save commands on the right. */
  commands?: ReactNode
  /** Linear's 24px chips (saved-view band, edit card). */
  compact?: boolean
  countLabel?: (count: number) => string
  fieldVisual?: (filter: TFilter) => ReactNode
  filters: TFilter[]
  onAdd: () => void
  onClear: () => void
  onOperatorChange: (filter: TFilter, operator: TFilter['operator']) => void
  onRemove: (filter: TFilter) => void
  onSave?: () => void
  onValuesChange: (filter: TFilter, values: TOption[]) => void
  optionsFor: (filter: TFilter) => TOption[]
  /** Custom chip for a filter (for example the advanced-filter chip); return undefined for the default chip. */
  renderFilter?: (filter: TFilter) => ReactNode | undefined
  saveLabel?: string
  saveState?: 'idle'|'saving'|'saved'|'error'
  showEmpty?: boolean
  translate?: Translate
  wrap?: boolean
}) {
  if (!filters.length && !showEmpty) return null
  return <div className={`${styles.bar} ${compact ? styles.compact : ''} ${className ?? ''}`} aria-label={ariaLabel}>
    <AppliedFilterChips wrap={wrap} compact={compact} countLabel={countLabel} fieldVisual={fieldVisual} filters={filters} onAdd={onAdd} onOperatorChange={onOperatorChange} onRemove={onRemove} onValuesChange={onValuesChange} optionsFor={optionsFor} renderFilter={renderFilter} translate={translate}/>
    {commands ?? <div className={styles.commands}><button aria-label="Clear all filters" onClick={onClear} type="button">{clearLabel}</button>{onSave && <button aria-label="Create new view" data-state={saveState} disabled={saveState === 'saving'} onClick={onSave} type="button">{saveState === 'saving' ? 'Saving...' : saveState === 'saved' ? 'Saved' : saveState === 'error' ? 'Retry save' : saveLabel}</button>}</div>}
  </div>
}

/** The chip row on its own (Linear's edit card shows it without the Clear / Save commands). */
export function AppliedFilterChips<TFilter extends AppliedFilterItem<TOption, string>, TOption extends AppliedFilterOption>({ addLabel = 'Add Filter', compact = false, countLabel, fieldVisual, filters, onAdd, onOperatorChange, onRemove, onValuesChange, optionsFor, renderFilter, translate = value => value, wrap = false }: {
  addLabel?: string
  compact?: boolean
  countLabel?: (count: number) => string
  fieldVisual?: (filter: TFilter) => ReactNode
  filters: TFilter[]
  onAdd: () => void
  onOperatorChange: (filter: TFilter, operator: TFilter['operator']) => void
  onRemove: (filter: TFilter) => void
  onValuesChange: (filter: TFilter, values: TOption[]) => void
  optionsFor: (filter: TFilter) => TOption[]
  renderFilter?: (filter: TFilter) => ReactNode | undefined
  translate?: Translate
  wrap?: boolean
}) {
  return <div className={`${styles.filters} ${compact ? styles.compact : ''} ${wrap ? styles.wrap : ''}`}>
    {filters.map(filter => renderFilter?.(filter) ?? <FilterConditionChip key={filter.id} countLabel={countLabel} fieldVisual={fieldVisual?.(filter)} filter={filter} onOperatorChange={operator => onOperatorChange(filter, operator)} onRemove={() => onRemove(filter)} onValuesChange={values => onValuesChange(filter, values)} options={optionsFor(filter)} translate={translate}/>)}
    {filters.length > 0 && <ScopedFlowTooltip label={translate(addLabel)} shortcut="F"><button className={styles.add} aria-label={translate('Add another filter')} onClick={onAdd} type="button"><Plus size={14}/></button></ScopedFlowTooltip>}
  </div>
}

/** One segmented condition: property | operator | value | ×. Shared by the filter bar and the advanced editor. */
export function FilterConditionChip<TOption extends AppliedFilterOption, TFilter extends AppliedFilterItem<TOption, string>>({ countLabel, fieldVisual, filter, onOperatorChange, onRemove, onValuesChange, options, translate = value => value, valueVisual }: {
  countLabel?: (count: number) => string
  fieldVisual?: ReactNode
  filter: TFilter
  onOperatorChange: (operator: TFilter['operator']) => void
  onRemove: () => void
  onValuesChange: (values: TOption[]) => void
  options: TOption[]
  translate?: Translate
  valueVisual?: ReactNode
}) {
  return <div className={styles.condition} data-filter-chip="">
    <span className={styles.field}>{fieldVisual ?? <i/>}<span>{translate(filter.fieldLabel)}</span></span>
    <OperatorMenu filter={filter} onChange={onOperatorChange} translate={translate}/>
    <ValueMenu countLabel={countLabel} filter={filter} onChange={onValuesChange} options={options} translate={translate} valueVisual={valueVisual}/>
    <button className={styles.remove} aria-label={translate('Remove {field} filter').replace('{field}', translate(filter.fieldLabel))} onClick={onRemove} type="button"><X size={13}/></button>
  </div>
}

function OperatorMenu<TOption extends AppliedFilterOption, TFilter extends AppliedFilterItem<TOption, string>>({ filter, onChange, translate }: { filter: TFilter; onChange: (operator: TFilter['operator']) => void; translate: Translate }) {
  const choices: AppliedFilterOperatorChoice[] = filter.operatorChoices ?? [{ operator: 'is', label: filter.operatorLabel ?? 'is' }, { operator: 'isNot', label: filter.negativeOperatorLabel ?? 'is not' }]
  const current = choices.find(choice => choice.operator === filter.operator) ?? choices[0]
  return <Popover.Root><Popover.Trigger asChild><button className={styles.operator} type="button" aria-label={translate('{field} operator').replace('{field}', translate(filter.fieldLabel))}>{translate(current.label)}</button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" align="start" className={styles.operatorMenu} collisionPadding={8} sideOffset={4} role="menu">
    {choices.map(choice => <Popover.Close asChild key={choice.operator}><button aria-checked={choice.operator === filter.operator} onClick={() => onChange(choice.operator)} role="menuitemradio" type="button"><span>{translate(choice.label)}</span>{choice.operator === filter.operator && <Check size={13}/>}</button></Popover.Close>)}
  </Popover.Content></Popover.Portal></Popover.Root>
}

function ValueMenu<TOption extends AppliedFilterOption>({ countLabel, filter, onChange, options, translate, valueVisual }: { countLabel?: (count: number) => string; filter: AppliedFilterItem<TOption, string>; onChange: (values: TOption[]) => void; options: TOption[]; translate: Translate; valueVisual?: ReactNode }) {
  const [open,setOpen]=useState(false),selectedIds=useMemo(()=>filter.values.map(value=>value.id),[filter.values])
  // Linear: ticked values on top, a divider, then the rest.
  const ordered=useMemo(()=>{const known=new Map(options.map(option=>[option.id,option]));const selected=filter.values.map(value=>known.get(value.id)??value);return [...selected,...options.filter(option=>!selectedIds.includes(option.id))]},[filter.values,options,selectedIds])
  const command=usePropertyCommand({personOptions:isPeopleProperty(filter.fieldLabel),closeOnSelect:false,onOpenChange:setOpen,open,options:ordered,selectedIds,onSelect:option=>onChange(selectedIds.includes(option.id)?filter.values.filter(value=>value.id!==option.id):[...filter.values,option])})
  const firstUnselected=command.filteredOptions.findIndex(option=>!selectedIds.includes(option.id))
  return <Popover.Root open={open} onOpenChange={setOpen}><Popover.Trigger asChild><button aria-label={translate('{field} values').replace('{field}', translate(filter.fieldLabel))} className={styles.value} type="button"><ValueSummary summary={filter.valueSummary} translate={translate} values={filter.values} visual={valueVisual}/></button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" align="start" className={styles.valueMenu} collisionPadding={8} onKeyDown={command.onKeyDown} onOpenAutoFocus={event=>event.preventDefault()} sideOffset={4}>
    <div className={styles.search}><input aria-label="Filter values" onChange={event=>command.onQueryChange(event.target.value)} placeholder={translate('Filter...')} ref={command.inputRef} value={command.query}/></div>
    <div className={styles.options} role="listbox" aria-multiselectable="true">{command.filteredOptions.map((option,index)=><Fragment key={option.id||'none'}>{index>0&&index===firstUnselected&&<hr className={styles.divider}/>}<PersonHover userId={isPeopleProperty(filter.fieldLabel)?option.id:undefined}><button aria-checked={command.isSelected(option.id)} aria-selected={command.activeId===option.id} onClick={()=>command.choose(option)} onMouseMove={()=>command.setActiveId(option.id)} role="option" type="button"><span className={styles.checkbox}>{command.isSelected(option.id)&&<CheckboxMark/>}</span>{option.icon ?? <i style={{background:option.color??'var(--theme-text-secondary)'}}/>}<span data-i18n-ignore>{option.label}</span>{option.count!==undefined&&<small>{option.count} {countLabel?.(option.count)}</small>}</button></PersonHover></Fragment>)}{!command.filteredOptions.length&&<span className={styles.empty}>{translate('No results')}</span>}</div>
  </Popover.Content></Popover.Portal></Popover.Root>
}

function ValueSummary({ summary, translate, values, visual }: { summary?: string; translate: Translate; values: AppliedFilterOption[]; visual?: ReactNode }) {
  if(values.length===1)return <>{visual ?? values[0].icon ?? <i style={{background:values[0].color??'var(--theme-text-secondary)'}}/>}<span data-i18n-ignore>{values[0].label}</span></>
  return <><span className={styles.stack}>{values.slice(0,3).map((value,index)=><i key={value.id} style={{background:value.color??'var(--theme-text-secondary)',zIndex:3-index}}/>)}</span><span data-i18n-ignore={summary ? undefined : true}>{summary ? translate(summary) : values.map(value=>value.label).join(', ')}</span></>
}
