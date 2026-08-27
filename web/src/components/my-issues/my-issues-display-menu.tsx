import * as Popover from '@radix-ui/react-popover'
import * as Select from '@radix-ui/react-select'
import { ArrowDownUp, Check, ChevronDown, LayoutGrid, List } from 'lucide-react'
import { DisplayIcon } from './my-issues-icons'
import { useI18n } from '@/i18n/i18n'
import { Toggle } from '@/components/ui/toggle'
import type { MyIssuesDisplayOptions, MyIssuesGrouping, MyIssuesProperty } from './my-issues-surface'
import styles from './my-issues-display-menu.module.css'

export interface MyIssuesDisplayMenuProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  options: MyIssuesDisplayOptions
  onChange: (options: MyIssuesDisplayOptions) => void
  hiddenProperties?: MyIssuesProperty[]
  availableGroupings?: MyIssuesGrouping[]
  hideSubGrouping?: boolean
}

type DisplayPatch = Partial<MyIssuesDisplayOptions>

const groupingOptions: { value: MyIssuesGrouping; label: string }[] = [
  { value: 'none', label: 'No grouping' },
  { value: 'focus', label: 'Focus' },
  { value: 'status', label: 'Status' },
  { value: 'agent', label: 'Agent' },
  { value: 'project', label: 'Project' },
  { value: 'priority', label: 'Priority' },
  { value: 'cycle', label: 'Cycle' },
  { value: 'label', label: 'Label' },
  { value: 'team', label: 'Team' },
  { value: 'customer', label: 'Customer' },
]

const subGroupingOptions = groupingOptions.filter(option => option.value !== 'focus')

const completedOptions: { value: MyIssuesDisplayOptions['completedWindow']; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'pastDay', label: 'Past day' },
  { value: 'pastWeek', label: 'Past week' },
  { value: 'pastMonth', label: 'Past month' },
  { value: 'currentCycle', label: 'Current cycle' },
  { value: 'none', label: 'None' },
]

const propertyOptions: { value: MyIssuesProperty; label: string }[] = [
  { value: 'id', label: 'ID' },
  { value: 'status', label: 'Status' },
  { value: 'assignee', label: 'Assignee' },
  { value: 'priority', label: 'Priority' },
  { value: 'project', label: 'Project' },
  { value: 'dueDate', label: 'Due date' },
  { value: 'milestone', label: 'Milestone' },
  { value: 'labels', label: 'Labels' },
  { value: 'links', label: 'Links' },
  { value: 'customers', label: 'Customers' },
  { value: 'customerRevenue', label: 'Customer revenue' },
  { value: 'timeInStatus', label: 'Time in status' },
  { value: 'created', label: 'Created' },
  { value: 'updated', label: 'Updated' },
]

export function MyIssuesDisplayMenu({ hiddenProperties = [], availableGroupings, hideSubGrouping = false, open, onOpenChange, options, onChange }: MyIssuesDisplayMenuProps) {
  const { t } = useI18n()
  const change = (patch: DisplayPatch) => onChange({ ...options, ...patch })
  const toggleProperty = (property: MyIssuesProperty) => {
    const properties = new Set(options.properties)
    if (properties.has(property)) properties.delete(property)
    else properties.add(property)
    change({ properties })
  }
  const visibleGroupingOptions = availableGroupings ? groupingOptions.filter(option => availableGroupings.includes(option.value)) : groupingOptions
  const visibleSubGroupingOptions = availableGroupings ? subGroupingOptions.filter(option => availableGroupings.includes(option.value)) : subGroupingOptions

  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Trigger asChild>
      <button type="button" className={`${styles.trigger} ui-pill`} aria-label={t('Display options')} aria-pressed={open}>
        <DisplayIcon />
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content className={styles.popover} side="bottom" align="end" sideOffset={3} collisionPadding={11} aria-label={t('Display options')}>
        <div className={styles.layoutTabs} role="tablist" aria-label="Layout">
          <button type="button" role="tab" aria-selected={options.layout === 'list'} onClick={() => change({ layout: 'list' })}><List size={14} />{t('List')}</button>
          <button type="button" role="tab" aria-selected={options.layout === 'board'} onClick={() => change({ layout: 'board' })}><LayoutGrid size={13} />{t('Board')}</button>
        </div>

        <section className={styles.section} aria-label={t('Grouping options')}>
          <div className={styles.groupingControl}>
            <span className={styles.rowLabel}>{t(options.layout === 'board' ? 'Columns' : 'Grouping')}</span>
            <div className={styles.groupingActions}>
              <button
                type="button"
                className={styles.orderButton}
                aria-label={`Group ordering: ${options.groupOrder === 'asc' ? 'ascending' : 'descending'}`}
                title="Group ordering"
                data-order={options.groupOrder}
                onClick={() => change({ groupOrder: options.groupOrder === 'asc' ? 'desc' : 'asc' })}
              ><ArrowDownUp size={14} /></button>
              <SelectControl ariaLabel="Grouping" value={options.grouping} options={visibleGroupingOptions} onChange={grouping => change({ grouping })} />
            </div>
          </div>
          {!hideSubGrouping && <SelectField label={options.layout === 'board' ? 'Rows' : 'Sub-grouping'} value={options.subGrouping} options={visibleSubGroupingOptions} onChange={subGrouping => change({ subGrouping })} />}
          <SelectField label="Ordering" value={options.ordering} options={[{ value: 'importance' as const, label: 'Importance' }, { value: 'priority' as const, label: 'Priority' }, { value: 'created' as const, label: 'Created' }, { value: 'updated' as const, label: 'Updated' }]} onChange={ordering => change({ ordering })} />
          <SwitchRow label="Order completed by recency" checked={options.orderCompletedByRecency} onChange={orderCompletedByRecency => change({ orderCompletedByRecency })} />
          <SelectField label="Completed issues" value={options.completedWindow} options={completedOptions} onChange={completedWindow => change({ completedWindow })} />
          <SwitchRow label="Show sub-issues" checked={options.showSubIssues} onChange={showSubIssues => change({ showSubIssues, nestedSubIssues: showSubIssues ? options.nestedSubIssues : false })} />
        </section>

        <section className={styles.section} aria-label={t(options.layout === 'board' ? 'Board options' : 'List options')}>
          <span className={styles.sectionLabel}>{t(options.layout === 'board' ? 'Board options' : 'List options')}</span>
          {options.layout === 'board' && <SwitchRow label="Show empty columns" checked={options.showEmptyGroups} onChange={showEmptyGroups => change({ showEmptyGroups })} />}
          {options.layout === 'list' && <SwitchRow label="Nested sub-issues" checked={options.nestedSubIssues} onChange={nestedSubIssues => change({ nestedSubIssues, showSubIssues: nestedSubIssues || options.showSubIssues })} />}
          <span className={styles.sectionLabel}>{t('Display properties')}</span>
          <div className={styles.propertyGrid}>
            {propertyOptions.filter(property => !hiddenProperties.includes(property.value)).map(property => {
              const active = options.properties.has(property.value)
              return <button key={property.value} type="button" data-active={active} aria-pressed={active} onClick={() => toggleProperty(property.value)}>
                <span>{t(property.label)}</span>
              </button>
            })}
          </div>
        </section>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function SelectField<T extends string>({ disabled = false, label, onChange, options, value }: {
  disabled?: boolean
  label: string
  onChange: (value: T) => void
  options: { value: T; label: string }[]
  value: T
}) {
  const { t } = useI18n()
  return <label className={styles.selectField}>
    <span>{t(label)}</span>
    <SelectControl ariaLabel={label} disabled={disabled} value={value} options={options} onChange={onChange} />
  </label>
}

function SelectControl<T extends string>({ ariaLabel, disabled = false, onChange, options, value }: {
  ariaLabel: string
  disabled?: boolean
  onChange: (value: T) => void
  options: { value: T; label: string }[]
  value: T
}) {
  const { t } = useI18n()
  return <Select.Root disabled={disabled} value={value} onValueChange={onChange}>
    <Select.Trigger className={styles.selectTrigger} aria-label={t(ariaLabel)}>
      <Select.Value /><Select.Icon><ChevronDown size={12} aria-hidden="true" /></Select.Icon>
    </Select.Trigger>
    <Select.Portal>
      <Select.Content className={styles.selectMenu} position="popper" side="bottom" align="end" sideOffset={5} collisionPadding={10}>
        <Select.Viewport className={styles.selectViewport}>
          {options.map(option => <Select.Item className={styles.selectItem} key={option.value} value={option.value}>
            <Select.ItemText>{t(option.label)}</Select.ItemText>
            <Select.ItemIndicator className={styles.selectIndicator}><Check size={13} aria-hidden="true" /></Select.ItemIndicator>
          </Select.Item>)}
        </Select.Viewport>
      </Select.Content>
    </Select.Portal>
  </Select.Root>
}

function SwitchRow({ checked, label, onChange }: { checked: boolean; label: string; onChange: (checked: boolean) => void }) {
  const { t } = useI18n()
  return <div className={styles.switchRow}>
    <span>{t(label)}</span>
    <Toggle checked={checked} label={t(label)} onChange={onChange}/>
  </div>
}
