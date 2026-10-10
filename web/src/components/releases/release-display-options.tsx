import * as Select from '@radix-ui/react-select'
import { ArrowDown, ArrowDownWideNarrow, ArrowUp, ArrowUpNarrowWide, Check, ChevronDown } from 'lucide-react'

import { useI18n } from '@/i18n/i18n'

type Option<T extends string> = readonly [T, string]

function DisplaySelect<T extends string>({ ariaLabel, onChange, options, value }: { ariaLabel: string; onChange: (value: T) => void; options: readonly Option<T>[]; value: T }) {
  const { t } = useI18n()
  return <Select.Root onValueChange={next => onChange(next as T)} value={value}>
    <Select.Trigger aria-label={ariaLabel} className="flow-pipeline-display-select"><Select.Value/><Select.Icon><ChevronDown/></Select.Icon></Select.Trigger>
    <Select.Portal><Select.Content data-flow-motion="floating" align="end" className="flow-pipeline-display-select-menu" collisionPadding={8} position="popper" side="bottom" sideOffset={4}>
      <Select.Viewport>{options.map(([option, label]) => <Select.Item className="flow-pipeline-display-select-item" key={option} value={option}><Select.ItemText>{t(label)}</Select.ItemText><Select.ItemIndicator className="flow-pipeline-display-select-indicator"><Check/></Select.ItemIndicator></Select.Item>)}</Select.Viewport>
    </Select.Content></Select.Portal>
  </Select.Root>
}

export type DisplayProperty = { key: string; label: string; active: boolean; onToggle: () => void }

/** Linear's view options popover body: Grouping, Ordering (+ direction) and Display properties. */
export function DisplayOptionsBody<G extends string, O extends string>({ grouping, ordering, properties }: {
  grouping?: { value: G; onChange: (value: G) => void; options: readonly Option<G>[] }
  ordering?: { value: O; onChange: (value: O) => void; options: readonly Option<O>[]; direction: 'asc' | 'desc'; onDirection: (value: 'asc' | 'desc') => void }
  properties: DisplayProperty[]
}) {
  const { t } = useI18n()
  return <>
    {(grouping || ordering) && <div className="flow-pipeline-display-config">
      {grouping && <div className="flow-pipeline-display-row"><span>{t('Grouping')}</span><DisplaySelect ariaLabel={t('Grouping')} onChange={grouping.onChange} options={grouping.options} value={grouping.value}/></div>}
      {ordering && <div className="flow-pipeline-display-row"><span>{t('Ordering')}</span><div className="flow-pipeline-display-order">
        <button aria-label={t(ordering.direction === 'asc' ? 'Ascending' : 'Descending')} onClick={() => ordering.onDirection(ordering.direction === 'asc' ? 'desc' : 'asc')} type="button">{ordering.direction === 'asc' ? <ArrowDownWideNarrow/> : <ArrowUpNarrowWide/>}</button>
        <DisplaySelect ariaLabel={t('Ordering')} onChange={ordering.onChange} options={ordering.options} value={ordering.value}/>
      </div></div>}
    </div>}
    <div className="flow-pipeline-display-properties">
      <span>{t('Display properties')}</span>
      <div>{properties.map(property => <button aria-pressed={property.active} data-active={property.active || undefined} key={property.key} onClick={property.onToggle} type="button">{t(property.label)}</button>)}</div>
    </div>
  </>
}

/** A sortable column header ("Release pipeline ↓"). */
export function SortHeader({ label, active, direction, ariaLabel, onClick }: { label: string; active: boolean; direction: 'asc' | 'desc'; ariaLabel?: string; onClick: () => void }) {
  const { t } = useI18n()
  return <button className="flow-releases-sort" aria-label={ariaLabel ?? `${t('Order by')} ${t(label)}`} data-active={active || undefined} onClick={onClick} type="button">
    <span>{t(label)}</span>{active && (direction === 'asc' ? <ArrowDown aria-hidden="true"/> : <ArrowUp aria-hidden="true"/>)}
  </button>
}
