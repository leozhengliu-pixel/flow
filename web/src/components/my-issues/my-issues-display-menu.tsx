import * as Popover from '@radix-ui/react-popover'
import * as Select from '@radix-ui/react-select'
import { ArrowDownUp, Check, ChevronDown, Columns2, Eye, EyeOff, LayoutGrid, List } from 'lucide-react'
import { DisplayIcon } from './my-issues-icons'
import { useI18n } from '@/i18n/i18n'
import { Toggle } from '@/components/ui/toggle'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
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
  /** View toggles this surface can honor (the reference app showTriageIssues / showArchivedItems / showSubTeamIssues). */
  toggles?: ('triage' | 'archived' | 'subTeam')[]
  hideSubGrouping?: boolean
  /** Surfaces without an issue preview pane cannot offer the split layout. */
  hideSplit?: boolean
  /** Current groups for the Group ordering panel (the reference app ViewOptionsGroupsPanel): hide / show each group. */
  groups?: { id: string; label: string; count: number }[]
  /** Parent labels offered by the "Label group" grouping; the grouping is hidden without any. */
  labelGroupOptions?: { id: string; name: string }[]
  /** Footer actions (the reference app "Reset to view default" / "Save as default for view"). */
  onReset?: () => void
  resetLabel?: string
  onSaveDefault?: () => void
  saveDefaultLabel?: string
  /** The issue explorer uses the compact phone parity sheet. */
  phoneIssueMenu?: boolean
}

type DisplayPatch = Partial<MyIssuesDisplayOptions>

const GROUPING_ORDER: MyIssuesGrouping[] = ['none', 'focus', 'status', 'assignee', 'agent', 'project', 'milestone', 'priority', 'cycle', 'label', 'team', 'parent', 'sla', 'customer', 'release', 'releaseDate', 'labelGroup', 'activityDate']
const groupingOptions: { value: MyIssuesGrouping; label: string }[] = GROUPING_ORDER.map(value => ({ value, label: GROUPING_LABELS[value] }))
const ORDERING_ORDER: MyIssuesOrdering[] = ['importance', 'title', 'status', 'assignee', 'priority', 'estimate', 'created', 'updated', 'myActivity', 'dueDate', 'linkCount', 'timeInStatus', 'customerCount', 'customerRevenue', 'customerImportantCount']
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
  { value: 'creator', label: 'Creator' },
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

export function MyIssuesDisplayMenu({ hiddenProperties = [], availableGroupings, availableOrderings, toggles = [], hideSubGrouping = false, hideSplit = false, groups = [], labelGroupOptions = [], open, onOpenChange, options, onChange, onReset, resetLabel = 'Reset', onSaveDefault, saveDefaultLabel = 'Save as default for view', phoneIssueMenu = false }: MyIssuesDisplayMenuProps) {
  const { t } = useI18n()
  const phone = usePhoneLayout()
  const change = (patch: DisplayPatch) => onChange({ ...options, ...patch })
  const toggleProperty = (property: MyIssuesProperty) => {
    const properties = new Set(options.properties)
    if (properties.has(property)) properties.delete(property)
    else properties.add(property)
    change({ properties })
  }
  const visibleGroupingOptions = (availableGroupings ? groupingOptions.filter(option => availableGroupings.includes(option.value)) : groupingOptions)
    .filter(option => option.value !== 'labelGroup' || labelGroupOptions.length > 0)
    .map(option => phone && phoneIssueMenu && option.value === 'activityDate' ? { ...option, label: t('My activity') } : option)
  const visibleSubGroupingOptions = (availableGroupings ? subGroupingOptions.filter(option => availableGroupings.includes(option.value)) : subGroupingOptions).filter(option => option.value === 'none' || option.value !== options.grouping)
  const visibleOrderingOptions = availableOrderings ? orderingOptions.filter(option => availableOrderings.includes(option.value)) : orderingOptions
  const direction = options.orderDirection ?? defaultOrderDirection(options.ordering)

  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Trigger asChild>
      <button type="button" data-issue-display-trigger={phoneIssueMenu ? 'true' : undefined} className={`${styles.trigger} ui-pill`} aria-label={t('Display options')} aria-pressed={open}>
        <DisplayIcon />
        {options.layout !== 'list' && <span className={styles.triggerDot} aria-hidden="true"/>}
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" data-phone-popover data-issue-display-menu={phoneIssueMenu ? 'true' : undefined} data-layout={phoneIssueMenu ? options.layout : undefined} className={styles.popover} side="bottom" align="end" sideOffset={3} collisionPadding={11} aria-label={t('Display options')}>
        <div className={styles.layoutTabs} role="tablist" aria-label="Layout">
          <button type="button" role="tab" aria-selected={options.layout === 'list'} onClick={() => change({ layout: 'list' })}><span className={styles.desktopModeIcon}><List size={14} /></span><PhoneModeIcon kind="list" />{t('List')}</button>
          <button type="button" role="tab" aria-selected={options.layout === 'board'} disabled={options.grouping === 'focus'} onClick={() => change({ layout: 'board' })}><span className={styles.desktopModeIcon}><LayoutGrid size={13} /></span><PhoneModeIcon kind="board" />{t('Board')}</button>
          {!hideSplit && options.layout === 'split' && <button type="button" role="tab" aria-selected={options.layout === 'split'} onClick={() => change({ layout: 'split' })}><Columns2 size={13} />{t('Split')}</button>}
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
              ><span className={styles.desktopSortIcon}><ArrowDownUp size={14} /></span><PhoneSortIcon kind="group" /></button>
              <SelectControl ariaLabel="Grouping" value={options.grouping} options={visibleGroupingOptions} onChange={grouping => change({ grouping, layout: grouping === 'focus' ? 'list' : options.layout, ...(grouping === 'labelGroup' && !options.labelGroupId ? { labelGroupId: labelGroupOptions[0]?.id } : {}) })} />
            </div>
          </div>
          {options.grouping === 'labelGroup' && labelGroupOptions.length > 0 && <SelectField label="Label group" value={options.labelGroupId ?? labelGroupOptions[0].id} options={labelGroupOptions.map(group => ({ value: group.id, label: group.name }))} onChange={labelGroupId => change({ labelGroupId })} />}
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
              ><span className={styles.desktopSortIcon}><ArrowDownUp size={14} /></span><PhoneSortIcon kind="ordering" /></button>
              <SelectControl ariaLabel="Ordering" disabled={options.grouping === 'focus'} value={options.ordering} options={visibleOrderingOptions} onChange={ordering => change({ ordering, orderDirection: undefined })} />
            </div>
          </div>
          <SwitchRow label="Order completed by recency" checked={options.orderCompletedByRecency} onChange={orderCompletedByRecency => change({ orderCompletedByRecency })} />
        </section>

        <section className={styles.section} aria-label={t('Visible issues')}>
          <SelectField label="Completed issues" value={options.completedWindow} options={completedOptions} onChange={completedWindow => change({ completedWindow })} />
          {toggles.includes('triage') && <SwitchRow label="Show triage issues" checked={options.showTriageIssues !== false} onChange={showTriageIssues => change({ showTriageIssues })} />}
          {toggles.includes('archived') && <SwitchRow label="Show archived issues" checked={Boolean(options.showArchived)} onChange={showArchived => change({ showArchived })} />}
          {toggles.includes('subTeam') && <SwitchRow label="Show sub-team issues" checked={options.showSubTeamIssues !== false} onChange={showSubTeamIssues => change({ showSubTeamIssues })} />}
          <SwitchRow label="Show sub-issues" checked={options.showSubIssues} onChange={showSubIssues => change({ showSubIssues, nestedSubIssues: showSubIssues ? options.nestedSubIssues : false })} />
        </section>

        <section className={styles.section} aria-label={t(options.layout === 'board' ? 'Board options' : 'List options')}>
          <span className={styles.sectionLabel}>{t(options.layout === 'board' ? 'Board options' : 'List options')}</span>
          {options.layout === 'board' && <SwitchRow label="Show empty columns" checked={options.showEmptyGroups} onChange={showEmptyGroups => change({ showEmptyGroups })} />}
          {options.layout === 'list' && <>
            <SwitchRow label="Nested sub-issues" checked={options.nestedSubIssues} onChange={nestedSubIssues => change({ nestedSubIssues, showSubIssues: nestedSubIssues || options.showSubIssues })} />
            <div className={styles.nestedSelectRow}>
              <span className={styles.rowLabel}>{t('Nested sub-issues')}</span>
              <SelectControl
                ariaLabel="Nested sub-issues"
                value={options.showSubIssues ? (options.nestedSubIssues ? 'all' : 'matching') : 'none'}
                options={[{ value: 'matching', label: 'Show matching' }, { value: 'all', label: 'Show all' }, { value: 'none', label: 'Hide all' }]}
                onChange={value => change({ nestedSubIssues: value === 'all', showSubIssues: value !== 'none' })}
              />
            </div>
          </>}
          {options.layout === 'list' && <SwitchRow label="Show empty groups" checked={options.showEmptyGroups} onChange={showEmptyGroups => change({ showEmptyGroups })} />}
          <span className={styles.sectionLabel}>{t('Display properties')}</span>
          <div className={styles.propertyGrid}>
            {propertyOptions.filter(property => !hiddenProperties.includes(property.value)).map(property => {
              const active = options.properties.has(property.value)
              return <button key={property.value} type="button" data-property={property.value} data-active={active} aria-pressed={active} onClick={() => toggleProperty(property.value)}>
                <span>{t(property.label)}</span>
              </button>
            })}
          </div>
        </section>
        {groups.length > 1 && options.grouping !== 'none' && <section className={styles.section} aria-label={t('Group ordering')}>
          <span className={styles.sectionLabel}>{t('Groups')}</span>
          <div className={styles.groupList}>
            {groups.map(group => {
              const hidden = options.hiddenGroupIds.includes(group.id)
              return <button key={group.id} type="button" className={styles.groupToggle} data-hidden={hidden || undefined} aria-pressed={!hidden} aria-label={`${hidden ? t('Show group') : t('Hide group')} ${group.label}`} onClick={() => change({ hiddenGroupIds: hidden ? options.hiddenGroupIds.filter(id => id !== group.id) : [...options.hiddenGroupIds, group.id] })}>
                {hidden ? <EyeOff size={13}/> : <Eye size={13}/>}<span data-i18n-ignore>{group.label}</span><small>{group.count}</small>
              </button>
            })}
          </div>
          {!options.showEmptyGroups && <p className={styles.groupHint}>{t('Empty groups are hidden')}</p>}
        </section>}
        {(onReset || onSaveDefault) && <footer className={styles.footer}>
          {onReset && <button type="button" className={styles.footerButton} onClick={onReset}>{t(resetLabel)}</button>}
          {onSaveDefault && <button type="button" className={`${styles.footerButton} ${styles.footerPrimary}`} onClick={onSaveDefault}>{t(saveDefaultLabel)}</button>}
        </footer>}
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function PhoneModeIcon({ kind }: { kind: 'list' | 'board' }) {
  return <svg className={styles.phoneModeIcon} aria-hidden="true" viewBox="0 0 16 16">
    {kind === 'list' ? <path d="M1 1.8c0-.28 0-.42.054-.527a.5.5 0 0 1 .219-.218C1.38 1 1.52 1 1.8 1h12.4c.28 0 .42 0 .527.054a.5.5 0 0 1 .218.219C15 1.38 15 1.52 15 1.8v.4c0 .28 0 .42-.055.527a.5.5 0 0 1-.218.219C14.62 3 14.48 3 14.2 3H1.8c-.28 0-.42 0-.527-.054a.5.5 0 0 1-.218-.219C1 2.62 1 2.48 1 2.2v-.4ZM1 13.8c0-.28 0-.42.054-.527a.5.5 0 0 1 .219-.218C1.38 13 1.52 13 1.8 13h12.4c.28 0 .42 0 .527.055a.5.5 0 0 1 .218.218c.055.107.055.247.055.527v.4c0 .28 0 .42-.055.527a.5.5 0 0 1-.218.218C14.62 15 14.48 15 14.2 15H1.8c-.28 0-.42 0-.527-.055a.5.5 0 0 1-.218-.218C1 14.62 1 14.48 1 14.2v-.4ZM1 9.8c0-.28 0-.42.054-.527a.5.5 0 0 1 .219-.218C1.38 9 1.52 9 1.8 9h12.4c.28 0 .42 0 .527.055a.5.5 0 0 1 .218.218C15 9.38 15 9.52 15 9.8v.4c0 .28 0 .42-.055.527a.5.5 0 0 1-.218.218C14.62 11 14.48 11 14.2 11H1.8c-.28 0-.42 0-.527-.055a.5.5 0 0 1-.218-.218C1 10.62 1 10.48 1 10.2v-.4ZM1 5.8c0-.28 0-.42.054-.527a.5.5 0 0 1 .219-.218C1.38 5 1.52 5 1.8 5h12.4c.28 0 .42 0 .527.054a.5.5 0 0 1 .218.219C15 5.38 15 5.52 15 5.8v.4c0 .28 0 .42-.055.527a.5.5 0 0 1-.218.218C14.62 7 14.48 7 14.2 7H1.8c-.28 0-.42 0-.527-.054a.5.5 0 0 1-.218-.219C1 6.62 1 6.48 1 6.2v-.4Z" /> : <path d="M1 2.6c0-.56 0-.84.109-1.054a1 1 0 0 1 .437-.437C1.76 1 2.04 1 2.6 1h2.8c.56 0 .84 0 1.054.109a1 1 0 0 1 .437.437C7 1.76 7 2.04 7 2.6v.8c0 .56 0 .84-.109 1.054a1 1 0 0 1-.437.437C6.24 5 5.96 5 5.4 5H2.6c-.56 0-.84 0-1.054-.109a1 1 0 0 1-.437-.437C1 4.24 1 3.96 1 3.4v-.8ZM9 2.6c0-.56 0-.84.109-1.054a1 1 0 0 1 .437-.437C9.76 1 10.04 1 10.6 1h2.8c.56 0 .84 0 1.054.109a1 1 0 0 1 .437.437C15 1.76 15 2.04 15 2.6v.8c0 .56 0 .84-.109 1.054a1 1 0 0 1-.437.437C14.24 5 13.96 5 13.4 5h-2.8c-.56 0-.84-.109-1.054-.437C9 4.24 9 3.96 9 3.4v-.8ZM1 7.6c0-.56 0-.84.109-1.054a1 1 0 0 1 .437-.437C1.76 6 2.04 6 2.6 6h2.8c.56 0 .84 0 1.054.109a1 1 0 0 1 .437.437C7 6.76 7 7.04 7 7.6v.8c0 .56 0 .84-.109 1.054a1 1 0 0 1-.437.437C6.24 10 5.96 10 5.4 10H2.6c-.56 0-.84-.109-1.054-.437C1 9.24 1 8.96 1 8.4v-.8ZM9 7.6c0-.56 0-.84.109-1.054a1 1 0 0 1 .437-.437C9.76 6 10.04 6 10.6 6h2.8c.56 0 .84 0 1.054.109a1 1 0 0 1 .437.437C15 6.76 15 7.04 15 7.6v.8c0 .56 0 .84-.109 1.054a1 1 0 0 1-.437.437C14.24 10 13.96 10 13.4 10h-2.8c-.56 0-.84-.109-1.054-.437C9 9.24 9 8.96 9 8.4v-.8ZM1 12.6c0-.56 0-.84.109-1.054a1 1 0 0 1 .437-.437C1.76 11 2.04 11 2.6 11h2.8c.56 0 .84 0 1.054.109a1 1 0 0 1 .437.437C7 11.76 7 12.04 7 12.6v.8c0 .56 0 .84-.109 1.054a1 1 0 0 1-.437.437C6.24 15 5.96 15 5.4 15H2.6c-.56 0-.84-.109-1.054-.437C1 14.24 1 13.96 1 13.4v-.8ZM9 12.6c0-.56 0-.84.109-1.054a1 1 0 0 1 .437-.437C9.76 11 10.04 11 10.6 11h2.8c.56 0 .84 0 1.054.109a1 1 0 0 1 .437.437C15 11.76 15 12.04 15 12.6v.8c0 .56 0 .84-.109 1.054a1 1 0 0 1-.437.437C14.24 15 13.96 15 13.4 15h-2.8c-.56 0-.84-.109-1.054-.437C9 14.24 9 13.96 9 13.4v-.8Z" />}
  </svg>
}

function PhoneSortIcon({ kind }: { kind: 'group' | 'ordering' }) {
  return <svg className={styles.phoneSortIcon} aria-hidden="true" viewBox="0 0 16 16">
    {kind === 'group' ? <><path d="M8.16958 11.524C7.90741 11.8445 7.95488 12.3163 8.27505 12.5787L11.025 14.8287C11.3013 15.0547 11.698 15.0547 11.9743 14.8287L14.7243 12.5787C15.0449 12.3164 15.092 11.8446 14.8297 11.524C14.5674 11.2039 14.0955 11.1564 13.775 11.4186L12.2506 12.6666V4.74961C12.2506 4.33554 11.9147 3.99984 11.5006 3.99961C11.0864 3.99961 10.7506 4.3354 10.7506 4.74961V12.6676L9.22427 11.4186C8.90371 11.1567 8.43176 11.2036 8.16958 11.524Z"/><path d="M1.27603 3.41953C0.955449 3.68183 0.908263 4.15364 1.17056 4.47422C1.43291 4.79449 1.90477 4.84188 2.22524 4.57969L3.75063 3.33164L3.75063 11.2496C3.75088 11.6636 4.08657 11.9996 4.50063 11.9996C4.91451 11.9994 5.25039 11.6635 5.25063 11.2496L5.25063 3.33164L6.77603 4.57969C7.09658 4.84166 7.5685 4.79462 7.83071 4.47422C8.09292 4.15375 8.04553 3.68188 7.72524 3.41953L4.97524 1.16953C4.69897 0.943489 4.3023 0.943489 4.02603 1.16953L1.27603 3.41953Z"/></> : <><path d="M8.23 3.674a.75.75 0 0 1-.961 1.152l-1.77-1.475V14.25a.75.75 0 0 1-1.5 0V3.351L2.23 4.826a.75.75 0 1 1-.96-1.152l3-2.5a.75.75 0 0 1 .96 0z"/><path d="M6.999 7.25a.75.75 0 0 1 .75-.75h6.5a.75.75 0 0 1 0 1.5h-6.5a.75.75 0 0 1-.75-.75M6.999 10.25a.75.75 0 0 1 .75-.75h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1-.75-.75M6.999 13.25a.75.75 0 0 1 .75-.75h1.5a.75.75 0 0 1 0 1.5h-1.5a.75.75 0 0 1-.75-.75"/></>}
  </svg>
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

export interface ListDisplayMenuOption<T extends string = string> { value: T; label: string }

export interface ListDisplayMenuProps<G extends string = string, O extends string = string, P extends string = string> {
  open: boolean
  onOpenChange: (open: boolean) => void
  grouping: { value: G; options: ListDisplayMenuOption<G>[]; onChange: (value: G) => void }
  ordering: { value: O; options: ListDisplayMenuOption<O>[]; onChange: (value: O) => void; descending: boolean; onDirection: () => void }
  /** On/off rows between the ordering and the properties (Documents: "Show inactive projects"). */
  toggles?: { id: string; label: string; checked: boolean; onChange: (checked: boolean) => void }[]
  properties: { options: ListDisplayMenuOption<P>[]; active: ReadonlySet<P>; onToggle: (value: P) => void }
}

/**
 * The reference app's compact display popover (Documents, and other flat lists): Grouping and Ordering selects with
 * the direction button, view toggles, then the display-property chips. Same surface as the issue popover.
 */
export function ListDisplayMenu<G extends string, O extends string, P extends string>({ grouping, onOpenChange, open, ordering, properties, toggles = [] }: ListDisplayMenuProps<G, O, P>) {
  const { t } = useI18n()
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Trigger asChild>
      <button type="button" className={`${styles.trigger} ui-pill`} aria-label={t('Display options')} aria-pressed={open}>
        <DisplayIcon />
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" data-phone-popover className={styles.popover} data-list-display="" side="bottom" align="end" sideOffset={3} collisionPadding={8} aria-label={t('Display options')}>
        <section className={styles.section} aria-label={t('Grouping options')}>
          <div className={styles.groupingControl}>
            <span className={styles.rowLabel}>{t('Grouping')}</span>
            <div className={styles.groupingActions}><SelectControl ariaLabel="Grouping" value={grouping.value} options={grouping.options} onChange={grouping.onChange} /></div>
          </div>
          <div className={styles.groupingControl}>
            <span className={styles.rowLabel}>{t('Ordering')}</span>
            <div className={styles.groupingActions}>
              <button type="button" className={styles.orderButton} aria-label={t(ordering.descending ? 'Descending' : 'Ascending')} title={t('Direction')} data-order={ordering.descending ? 'desc' : 'asc'} data-sort-direction="" onClick={ordering.onDirection}><SortDirectionIcon /></button>
              <SelectControl ariaLabel="Ordering" value={ordering.value} options={ordering.options} onChange={ordering.onChange} />
            </div>
          </div>
          {toggles.map(toggle => <SwitchRow key={toggle.id} label={toggle.label} checked={toggle.checked} onChange={toggle.onChange} />)}
        </section>
        <section className={styles.section} aria-label={t('Display properties')}>
          <span className={styles.sectionLabel}>{t('Display properties')}</span>
          <div className={styles.propertyGrid}>
            {properties.options.map(property => {
              const active = properties.active.has(property.value)
              return <button key={property.value} type="button" data-active={active} aria-pressed={active} onClick={() => properties.onToggle(property.value)}><span>{t(property.label)}</span></button>
            })}
          </div>
        </section>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

/** The reference app's sort-direction glyph: arrow plus three bars; `data-order="desc"` mirrors it vertically. */
function SortDirectionIcon() {
  return <svg aria-hidden="true" focusable="false" width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path fillRule="evenodd" d="M8.23 12.326a.75.75 0 1 0-.96-1.152L5.5 12.649V1.75a.75.75 0 0 0-1.5 0v10.899l-1.77-1.475a.75.75 0 1 0-.96 1.152l3 2.5a.75.75 0 0 0 .96 0z"/><path fillRule="evenodd" d="M7 8.75c0 .414.336.75.75.75h6.5a.75.75 0 0 0 0-1.5h-6.5a.75.75 0 0 0-.75.75M7 5.75c0 .414.336.75.75.75h4.5a.75.75 0 0 0 0-1.5h-4.5a.75.75 0 0 0-.75.75M7 2.75c0 .414.336.75.75.75h1.5a.75.75 0 1 0 0-1.5h-1.5a.75.75 0 0 0-.75.75"/></svg>
}
