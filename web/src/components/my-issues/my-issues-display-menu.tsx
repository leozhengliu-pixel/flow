import * as Popover from '@radix-ui/react-popover'
import * as Select from '@radix-ui/react-select'
import { ArrowDownUp, Check, ChevronDown, LayoutGrid, List } from 'lucide-react'
import { DisplayIcon } from './my-issues-icons'
import { useI18n } from '@/i18n/i18n'
import { Toggle } from '@/components/ui/toggle'
import type { MyIssuesDisplayOptions, MyIssuesGrouping, MyIssuesOrdering, MyIssuesProperty } from './my-issues-surface'
import { GROUPING_LABELS, ORDERING_LABELS, defaultOrderDirection } from '@/components/issue-explorer/issue-grouping'
import styles from './my-issues-display-menu.module.css'

export interface MyIssuesDisplayMenuProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  options: MyIssuesDisplayOptions
  onChange: (options: MyIssuesDisplayOptions) => void
  hiddenProperties?: MyIssuesProperty[]
  availableGroupings?: MyIssuesGrouping[]
  /** Orderings the backing query can execute (server-paged lists support fewer). */
  availableOrderings?: MyIssuesOrdering[]
  /** View toggles this surface can honor (Linear showTriageIssues / showArchivedItems / showSubTeamIssues). */
  toggles?: ('triage' | 'archived' | 'subTeam')[]
  hideSubGrouping?: boolean
  /** Footer actions (Linear "Reset to view default" / "Save as default for view"). */
  onReset?: () => void
  resetLabel?: string
  onSaveDefault?: () => void
  saveDefaultLabel?: string
}

type DisplayPatch = Partial<MyIssuesDisplayOptions>

const GROUPING_ORDER: MyIssuesGrouping[] = ['none', 'focus', 'status', 'assignee', 'agent', 'project', 'milestone', 'priority', 'cycle', 'label', 'team', 'parent', 'sla', 'customer', 'release', 'activityDate']
const groupingOptions: { value: MyIssuesGrouping; label: string }[] = GROUPING_ORDER.map(value => ({ value, label: GROUPING_LABELS[value] }))
const ORDERING_ORDER: MyIssuesOrdering[] = ['importance', 'title', 'status', 'assignee', 'priority', 'estimate', 'created', 'updated', 'myActivity', 'dueDate', 'linkCount', 'customerCount', 'customerRevenue', 'timeInStatus']
const orderingOptions: { value: MyIssuesOrdering; label: string }[] = ORDERING_ORDER.map(value => ({ value, label: ORDERING_LABELS[value] }))

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
  { value: 'cycle', label: 'Cycle' },
  { value: 'dueDate', label: 'Due date' },
  { value: 'milestone', label: 'Milestone' },
  { value: 'sla', label: 'SLA' },
  { value: 'estimate', label: 'Estimate' },
  { value: 'release', label: 'Release' },
  { value: 'labels', label: 'Labels' },
  { value: 'links', label: 'Links' },
  { value: 'customers', label: 'Customers' },
  { value: 'customerRevenue', label: 'Customer revenue' },
  { value: 'timeInStatus', label: 'Time in status' },
  { value: 'myActivity', label: 'My activity date' },
  { value: 'created', label: 'Created' },
  { value: 'updated', label: 'Updated' },
  { value: 'pullRequests', label: 'Pull requests' },
]

export function MyIssuesDisplayMenu({ hiddenProperties = [], availableGroupings, availableOrderings, toggles = [], hideSubGrouping = false, open, onOpenChange, options, onChange, onReset, resetLabel = 'Reset', onSaveDefault, saveDefaultLabel = 'Save as default for view' }: MyIssuesDisplayMenuProps) {
  const { t } = useI18n()
  const change = (patch: DisplayPatch) => onChange({ ...options, ...patch })
  const toggleProperty = (property: MyIssuesProperty) => {
    const properties = new Set(options.properties)
    if (properties.has(property)) properties.delete(property)
    else properties.add(property)
    change({ properties })
  }
  const visibleGroupingOptions = availableGroupings ? groupingOptions.filter(option => availableGroupings.includes(option.value)) : groupingOptions
  const visibleSubGroupingOptions = (availableGroupings ? subGroupingOptions.filter(option => availableGroupings.includes(option.value)) : subGroupingOptions).filter(option => option.value === 'none' || option.value !== options.grouping)
  const visibleOrderingOptions = availableOrderings ? orderingOptions.filter(option => availableOrderings.includes(option.value)) : orderingOptions
  const direction = options.orderDirection ?? defaultOrderDirection(options.ordering)

  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Trigger asChild>
      <button type="button" className={`${styles.trigger} ui-pill`} aria-label={t('Display options')} aria-pressed={open}>
        <DisplayIcon />
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" className={styles.popover} side="bottom" align="end" sideOffset={3} collisionPadding={11} aria-label={t('Display options')}>
        <div className={styles.layoutTabs} role="tablist" aria-label="Layout">
          <button type="button" role="tab" aria-selected={options.layout === 'list'} onClick={() => change({ layout: 'list' })}><List size={14} />{t('List')}</button>
          <button type="button" role="tab" aria-selected={options.layout === 'board'} disabled={options.grouping === 'focus'} onClick={() => change({ layout: 'board' })}><LayoutGrid size={13} />{t('Board')}</button>
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
              <SelectControl ariaLabel="Grouping" value={options.grouping} options={visibleGroupingOptions} onChange={grouping => change({ grouping, layout: grouping === 'focus' ? 'list' : options.layout })} />
            </div>
          </div>
          {!hideSubGrouping && <SelectField label={options.layout === 'board' ? 'Rows' : 'Sub-grouping'} value={options.subGrouping} options={visibleSubGroupingOptions} onChange={subGrouping => change({ subGrouping })} />}
          <div className={styles.groupingControl}>
            <span className={styles.rowLabel}>{t('Ordering')}</span>
            <div className={styles.groupingActions}>
              <button
                type="button"
                className={styles.orderButton}
                disabled={options.grouping === 'focus'}
                aria-label={`Ordering direction: ${direction === 'asc' ? 'ascending' : 'descending'}`}
                title={t('Direction')}
                data-order={direction}
                onClick={() => change({ orderDirection: direction === 'asc' ? 'desc' : 'asc' })}
              ><ArrowDownUp size={14} /></button>
              <SelectControl ariaLabel="Ordering" disabled={options.grouping === 'focus'} value={options.ordering} options={visibleOrderingOptions} onChange={ordering => change({ ordering, orderDirection: undefined })} />
            </div>
          </div>
          <SwitchRow label="Order completed by recency" checked={options.orderCompletedByRecency} onChange={orderCompletedByRecency => change({ orderCompletedByRecency })} />
          <SelectField label="Completed issues" value={options.completedWindow} options={completedOptions} onChange={completedWindow => change({ completedWindow })} />
          <SwitchRow label="Show sub-issues" checked={options.showSubIssues} onChange={showSubIssues => change({ showSubIssues, nestedSubIssues: showSubIssues ? options.nestedSubIssues : false })} />
          {toggles.includes('triage') && <SwitchRow label="Show triage issues" checked={options.showTriageIssues !== false} onChange={showTriageIssues => change({ showTriageIssues })} />}
          {toggles.includes('archived') && <SwitchRow label="Show archived issues" checked={Boolean(options.showArchived)} onChange={showArchived => change({ showArchived })} />}
          {toggles.includes('subTeam') && <SwitchRow label="Show sub-team issues" checked={options.showSubTeamIssues !== false} onChange={showSubTeamIssues => change({ showSubTeamIssues })} />}
        </section>

        <section className={styles.section} aria-label={t(options.layout === 'board' ? 'Board options' : 'List options')}>
          <span className={styles.sectionLabel}>{t(options.layout === 'board' ? 'Board options' : 'List options')}</span>
          {options.layout === 'board' && <SwitchRow label="Show empty columns" checked={options.showEmptyGroups} onChange={showEmptyGroups => change({ showEmptyGroups })} />}
          {options.layout === 'list' && <SwitchRow label="Show empty groups" checked={options.showEmptyGroups} onChange={showEmptyGroups => change({ showEmptyGroups })} />}
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
        {(onReset || onSaveDefault) && <footer className={styles.footer}>
          {onReset && <button type="button" className={styles.footerButton} onClick={onReset}>{t(resetLabel)}</button>}
          {onSaveDefault && <button type="button" className={`${styles.footerButton} ${styles.footerPrimary}`} onClick={onSaveDefault}>{t(saveDefaultLabel)}</button>}
        </footer>}
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
      <Select.Content data-flow-motion="floating" className={styles.selectMenu} position="popper" side="bottom" align="end" sideOffset={5} collisionPadding={10}>
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
