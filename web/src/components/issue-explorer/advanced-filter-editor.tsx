import * as Popover from '@radix-ui/react-popover'
import { X } from 'lucide-react'
import { PlusIcon } from '@/components/ui/view-action-icons'
import { useState, type ReactNode } from 'react'
import { FilterConditionChip } from '@/components/filter/applied-filter-bar'
import { MyIssuesFilterMenu, OptionMark } from '@/components/my-issues/my-issues-filter-menu'
import { FilterFieldGlyph } from '@/components/my-issues/my-issues-filter-bar'
import { chipValueOptions, filterChipItem } from '@/components/my-issues/my-issues-filter-chips'
import { ADVANCED_FILTER_MAX_DEPTH, defaultDateOperator, isComparableDateValue, type AdvancedFilterCondition, type AdvancedFilterGroup, type MyIssuesAppliedFilter, type MyIssuesFilterOperator } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import { ISSUE_FILTER_LABELS } from './issue-explorer-model'
import {
  addConditionToGroup, addGroupToGroup, advancedFilterSummary, advancedFilterTree, conditionAsFilter, createCondition, isAdvancedGroup,
  pruneEmptyGroups, removeNode, setConditionValues, toggleGroupConjunction, updateCondition, valueSummaryLabel,
} from './advanced-filter'
import styles from './advanced-filter-editor.module.css'

type OptionsFor = (field: MyIssuesFilterKey) => MyIssuesFilterOption[] | undefined

/**
 * Linear's advanced-filter chip: empty it reads "Advanced filter"; otherwise one segment summarising the
 * tree ("Status is Triage or Priority is Urgent", "… and 2 more conditions"). It opens the editor popover.
 */
export function AdvancedFilterChip({ defaultOpen = false, filter, filterOptions, onChange, onOpenChange, onRemove }: {
  defaultOpen?: boolean
  filter: MyIssuesAppliedFilter
  filterOptions: OptionsFor
  onChange: (tree: AdvancedFilterGroup) => void
  onOpenChange?: (open: boolean) => void
  onRemove: () => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(defaultOpen)
  const tree = advancedFilterTree(filter)
  const summary = advancedFilterSummary(tree)
  const changeOpen = (next: boolean) => {
    setOpen(next)
    onOpenChange?.(next)
    if (!next) {
      const pruned = pruneEmptyGroups(tree)
      if (JSON.stringify(pruned) !== JSON.stringify(tree)) onChange(pruned)
    }
  }
  return <Popover.Root open={open} onOpenChange={changeOpen}>
    <div className={styles.chip} data-empty={summary.parts.length ? undefined : ''} data-advanced-chip="">
      <Popover.Trigger asChild>
        <button type="button" className={styles.chipSummary} aria-label={t('Open advanced filter')}>
          {summary.parts.length ? <>
            {summary.parts.map((part, index) => <span key={part.condition.id} className={styles.summaryPart}>
              {part.conjunction && index > 0 && <><span className={styles.muted}>{t(part.conjunction)}</span>{' '}</>}
              <span>{t(part.condition.fieldLabel)}</span>{' '}
              <span className={styles.muted}>{t(part.operatorLabel)}</span>{' '}
              <SummaryValue condition={part.condition} options={filterOptions(part.condition.field)}/>{' '}
            </span>)}
            {summary.more > 0 && <span className={styles.muted}>{t(summary.more === 1 ? 'and 1 more condition' : 'and {count} more conditions').replace('{count}', String(summary.more))}</span>}
          </> : <span className={styles.muted}>{t('Advanced filter')}</span>}
        </button>
      </Popover.Trigger>
      <button type="button" className={styles.chipRemove} aria-label={t('Remove advanced filter')} onClick={onRemove}><X size={13}/></button>
    </div>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" className={styles.editor} align="start" side="bottom" sideOffset={4} collisionPadding={8} aria-label={t('Advanced filter')} onOpenAutoFocus={event => event.preventDefault()}>
        <AdvancedFilterEditor tree={tree} filterOptions={filterOptions} onChange={onChange}/>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function SummaryValue({ condition, options }: { condition: AdvancedFilterCondition; options?: MyIssuesFilterOption[] }) {
  const item = filterChipItem(conditionAsFilter(condition), options)
  if (item.values.length !== 1) return <span data-i18n-ignore>{valueSummaryLabel(condition.field, item.values)}</span>
  return <span className={styles.summaryValue}>{item.values[0].icon}<span data-i18n-ignore>{item.values[0].label}</span></span>
}

/** The editor body: rows joined by the group's and/or toggle, nested groups, "+ Filter". */
export function AdvancedFilterEditor({ filterOptions, onChange, tree }: { filterOptions: OptionsFor; onChange: (tree: AdvancedFilterGroup) => void; tree: AdvancedFilterGroup }) {
  return <div className={styles.body} data-depth={0}>
    <GroupRows depth={0} group={tree} tree={tree} filterOptions={filterOptions} onChange={onChange}/>
  </div>
}

function GroupRows({ depth, filterOptions, group, onChange, tree }: { depth: number; filterOptions: OptionsFor; group: AdvancedFilterGroup; onChange: (tree: AdvancedFilterGroup) => void; tree: AdvancedFilterGroup }) {
  const { t } = useI18n()
  const toggle = () => onChange(toggleGroupConjunction(tree, group.id))
  // Slot 0: no prefix; slot 1: the and/or toggle; later slots: the conjunction as text (Linear).
  const prefix = (slot: number): ReactNode => slot === 0 ? null : slot === 1
    ? <button type="button" className={styles.conjunctionToggle} aria-label={t(`Toggle filter operator, currently ${group.conjunction}`)} onClick={toggle}>{t(group.conjunction)}</button>
    : <span className={styles.conjunctionText}>{t(group.conjunction)}</span>
  return <>
    {group.items.map((item, index) => <div key={item.id} className={styles.row}>
      <div className={styles.conjunction}>{prefix(index)}</div>
      {isAdvancedGroup(item)
        ? <div className={styles.group} data-depth={depth + 1}>
            <ScopedFlowTooltip label={t('Delete group')}><button type="button" className={styles.deleteGroup} aria-label={t('Delete group')} onClick={() => onChange(removeNode(tree, item.id))}><X size={13}/></button></ScopedFlowTooltip>
            <GroupRows depth={depth + 1} group={item} tree={tree} filterOptions={filterOptions} onChange={onChange}/>
          </div>
        : <ConditionRow condition={item} options={filterOptions(item.field)} onChange={onChange} tree={tree}/>}
    </div>)}
    <div className={styles.row}>
      {group.items.length > 0 && <div className={styles.conjunction}>{prefix(group.items.length)}</div>}
      <AddFilterButton depth={depth} filterOptions={filterOptions} onAddCondition={condition => onChange(addConditionToGroup(tree, group.id, condition))} onAddGroup={depth + 1 < ADVANCED_FILTER_MAX_DEPTH ? () => onChange(addGroupToGroup(tree, group.id).tree) : undefined}/>
    </div>
  </>
}

function ConditionRow({ condition, onChange, options, tree }: { condition: AdvancedFilterCondition; onChange: (tree: AdvancedFilterGroup) => void; options?: MyIssuesFilterOption[]; tree: AdvancedFilterGroup }) {
  const { t } = useI18n()
  const item = filterChipItem(conditionAsFilter(condition), options)
  return <div className={styles.condition}>
    <FilterConditionChip
      filter={item}
      fieldVisual={<FilterFieldGlyph field={condition.field}/>}
      options={chipValueOptions(condition.fieldLabel, options).map((option): MyIssuesFilterOption & { icon?: ReactNode } => ({ ...option, icon: <OptionMark field={condition.field} option={option}/> }))}
      translate={t}
      onOperatorChange={operator => onChange(updateCondition(tree, condition.id, current => ({ ...current, operator: operator as MyIssuesFilterOperator })))}
      onRemove={() => onChange(removeNode(tree, condition.id))}
      onValuesChange={values => onChange(setConditionValues(tree, condition.id, values.map(value => ({ value: value.id, valueLabel: value.label, color: value.color }))))}
    />
  </div>
}

function AddFilterButton({ depth, filterOptions, onAddCondition, onAddGroup }: { depth: number; filterOptions: OptionsFor; onAddCondition: (condition: AdvancedFilterCondition) => void; onAddGroup?: () => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  return <MyIssuesFilterMenu
    variant="advanced"
    align="start"
    open={open}
    onOpenChange={setOpen}
    options={filterOptions}
    onAddGroup={onAddGroup}
    onToggle={(field, option) => onAddCondition(createCondition(field, option.filterLabel ?? ISSUE_FILTER_LABELS[field] ?? field, { value: option.id, valueLabel: option.label, color: option.color }, field === 'dates' && isComparableDateValue(option.id) ? defaultDateOperator(option.id) : 'is'))}
    trigger={<button type="button" className={styles.addFilter} data-depth={depth} aria-label={t('Add filter')}><PlusIcon aria-hidden/><span>{t('Filter')}</span></button>}
  />
}
