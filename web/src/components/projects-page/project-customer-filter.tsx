import { useState, type KeyboardEvent } from 'react'
import type { MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import { OptionMark } from '@/components/my-issues/my-issues-filter-menu'
import { CUSTOMER_NUMBER_COMPARISONS, parseCustomerNumberInput } from '@/components/issue-explorer/customer-filter'
import { useI18n } from '@/i18n/i18n'
import { usePropertyCommand } from '@/components/property/use-property-command'
import type { ProjectFilterOption } from './projects-filter-model'
import './project-customer-filter.css'

export type ProjectCustomerFilterChoice = ProjectFilterOption & { filterLabel: string; comparison?: MyIssuesFilterOption['comparison'] }
type NestedPosition = { top: number; openRight: boolean; maxHeight: number }

const ROW_HEIGHT = 32
/** The value list's search band sits above its rows, so the first row lines up with the hovered block. */
const SEARCH_HEIGHT = 42.5

/**
 * Projects › Filter › Customers: Linear's eight customer blocks (Customer name … Customer size).
 * Value blocks open a checkbox list, number blocks an "Enter customer count…" input with
 * greater than or equals / less than or equals / equals / not equals.
 */
export function ProjectCustomerFilterValues({ nestedPosition, onSelect, options, selectedIds }: { nestedPosition?: NestedPosition; onSelect: (option: ProjectCustomerFilterChoice) => void; options: MyIssuesFilterOption[]; selectedIds?: Set<string> }) {
  const { t } = useI18n()
  const [activeId, setActiveId] = useState<string>()
  const active = options.find(option => option.id === activeId)
  const activeIndex = active ? options.indexOf(active) : -1
  // Like Linear's collision handling: lift the panel so all eight blocks stay on screen.
  const listHeight = options.length * ROW_HEIGHT + 10
  const top = (nestedPosition?.top ?? 112) - Math.max(0, listHeight - (nestedPosition?.maxHeight ?? listHeight))
  const side = nestedPosition?.openRight ? 'is-end' : ''
  return <>
    <div aria-label={t('Customers filters')} className={`lp-projects-filter__nested project-customer-filter ${side}`} style={{ top }} role="dialog">
      <div className="lp-projects-filter__values" role="listbox">
        {options.map(option => <button aria-expanded={activeId === option.id} aria-haspopup="listbox" aria-selected={activeId === option.id} className={activeId === option.id ? 'is-active' : ''} key={option.id} onFocus={() => setActiveId(option.id)} onPointerMove={() => setActiveId(option.id)} role="option" type="button">
          <span className="project-customer-filter__icon" aria-hidden="true"><OptionMark field="customers" option={option}/></span>
          <span className="project-customer-filter__label">{t(option.label)}</span>
          <span className="project-customer-filter__chevron" aria-hidden="true">▶</span>
        </button>)}
      </div>
    </div>
    {active && <div className={`lp-projects-filter__nested project-customer-filter__values ${side}`} data-number-input={active.numberInput ? '' : undefined} style={{ top: top + 5 + activeIndex * ROW_HEIGHT - (!active.numberInput && active.kind !== 'customerStatusCategory' ? SEARCH_HEIGHT : 0) }} role="dialog" aria-label={t(active.label)}>
      {active.numberInput
        ? <NumberValues option={active} onSelect={onSelect}/>
        : <ValueList key={active.id} option={active} onSelect={onSelect} selectedIds={selectedIds}/>}
    </div>}
  </>
}

/** A block's values (Customer name, owner, status, tier) as a searchable checkbox list. */
function ValueList({ onSelect, option, selectedIds }: { onSelect: (option: ProjectCustomerFilterChoice) => void; option: MyIssuesFilterOption; selectedIds?: Set<string> }) {
  const { t } = useI18n()
  const children = option.children ?? []
  const system = (child: MyIssuesFilterOption) => child.id.endsWith(':') || child.label === 'Current user'
  const command = usePropertyCommand({ open: true, autoFocus: false, closeOnSelect: false, options: children, selectedIds: [...(selectedIds ?? [])], onOpenChange: () => {}, onSelect: child => onSelect({ id: child.id, label: child.label, color: child.color, filterLabel: option.label }) })
  // Linear hides the search on Customer status; the other blocks filter as you type.
  const searchable = option.kind !== 'customerStatusCategory'
  return <>
    {searchable && <div className="lp-projects-filter__search"><input ref={command.inputRef} aria-label={t('Filter…')} onChange={event => command.onQueryChange(event.target.value)} onKeyDown={command.onKeyDown} placeholder={t('Filter…')} value={command.query}/></div>}
    <div className="lp-projects-filter__values" role="listbox" aria-multiselectable="true" onKeyDown={command.onKeyDown}>
      {command.filteredOptions.map(child => {
        const selected = selectedIds?.has(child.id) ?? false
        return <button aria-checked={selected} aria-selected={command.activeId === child.id} className={command.activeId === child.id ? 'is-active' : ''} key={child.id || 'none'} onClick={() => command.choose(child)} onPointerMove={() => command.setActiveId(child.id)} role="option" type="button">
          <span className="lp-projects-filter__checkbox" aria-hidden="true">{selected && <svg viewBox="0 0 16 16" width="10" height="10" fill="currentColor"><path d="M6.2 11.4 2.9 8.1l1.1-1.1 2.2 2.2 5.8-5.8 1.1 1.1z"/></svg>}</span>
          {child.kind !== 'customerTier' && <span className="project-customer-filter__icon" aria-hidden="true"><OptionMark field="customers" option={child}/></span>}
          <span className="project-customer-filter__label" data-i18n-ignore={system(child) ? undefined : true}>{system(child) ? t(child.label) : child.label}</span>
        </button>
      })}
      {!command.filteredOptions.length && <div className="lp-projects-filter__empty">{t('No results')}</div>}
    </div>
  </>
}

function NumberValues({ onSelect, option }: { onSelect: (option: ProjectCustomerFilterChoice) => void; option: MyIssuesFilterOption }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const input = option.numberInput!
  const typed = parseCustomerNumberInput(query)
  const display = (amount: number) => {
    if (!input.revenue) return new Intl.NumberFormat().format(amount)
    try { return new Intl.NumberFormat(undefined, { style: 'currency', currency: input.revenue.currency, maximumFractionDigits: 0 }).format(amount) } catch { return String(amount) }
  }
  const choose = (index: number) => {
    const comparison = CUSTOMER_NUMBER_COMPARISONS[index]
    if (typed === undefined || !comparison) return
    onSelect({ id: `${input.prefix}${input.revenue?.monthly ? typed * 12 : typed}`, label: display(typed), filterLabel: option.label, comparison: comparison.value })
  }
  const onKeyDown = (event: KeyboardEvent) => {
    if (typed === undefined) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); setActiveIndex(index => (index + (event.key === 'ArrowDown' ? 1 : 3)) % 4) }
    else if (event.key === 'Enter') { event.preventDefault(); choose(activeIndex) }
  }
  return <>
    <div className="lp-projects-filter__search"><input aria-label={t(input.placeholder)} autoFocus inputMode="decimal" onChange={event => { setQuery(event.target.value); setActiveIndex(0) }} onKeyDown={onKeyDown} placeholder={t(input.placeholder)} value={query}/></div>
    {typed !== undefined && <div className="lp-projects-filter__values" role="listbox">
      {CUSTOMER_NUMBER_COMPARISONS.map((comparison, index) => <button aria-selected={index === activeIndex} className={index === activeIndex ? 'is-active' : ''} key={comparison.value} onClick={() => choose(index)} onPointerMove={() => setActiveIndex(index)} role="option" type="button">
        <span className="project-customer-filter__icon" aria-hidden="true"><OptionMark field="customers" option={option}/></span>
        <span className="project-customer-filter__label"><span className="project-customer-filter__muted">{t(comparison.label)} </span><span data-i18n-ignore>{display(typed)}</span></span>
      </button>)}
    </div>}
  </>
}
