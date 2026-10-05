import { describe, expect, it } from 'vitest'
import type { AdvancedFilterGroup, MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import { filterOperatorChoices, normalizeFilterOperator, toggleFilterOption } from '@/components/my-issues/my-issues-filter-types'
import {
  addConditionToGroup, addGroupToGroup, advancedFilterSummary, advancedFilterTree, countFilterConditions, createAdvancedFilter, createCondition,
  decodeFiltersParam, encodeFiltersParam, groupDepth, normalizeStoredFilters, pruneEmptyGroups, removeNode, savableFilters, setConditionValues,
  toggleGroupConjunction, treeConditions, valueSummaryLabel,
} from './advanced-filter'

const urgent = createCondition('priority', 'Priority', { value: '1', valueLabel: 'Urgent' })
const triage = createCondition('status', 'Status', { value: 'state-triage', valueLabel: 'Triage' })

describe('advanced filter tree model', () => {
  it('adds conditions and groups with Linear depth limits and alternating defaults', () => {
    let tree = createAdvancedFilter().tree!
    tree = addConditionToGroup(tree, tree.id, triage)
    const group = addGroupToGroup(tree, tree.id)
    tree = group.tree
    expect(tree.items).toHaveLength(2)
    const child = tree.items[1] as AdvancedFilterGroup
    expect(child.conjunction).toBe('or')
    const nested = addGroupToGroup(tree, child.id)
    tree = nested.tree
    const grandchild = (tree.items[1] as AdvancedFilterGroup).items[0] as AdvancedFilterGroup
    expect(grandchild.conjunction).toBe('and')
    expect(groupDepth(tree, grandchild.id)).toBe(2)
    // top → group → nested group: no deeper group.
    expect(addGroupToGroup(tree, grandchild.id).groupId).toBeUndefined()
    expect(addGroupToGroup(tree, grandchild.id).tree).toBe(tree)
  })

  it('toggles one group, deletes groups and prunes empty groups', () => {
    let tree: AdvancedFilterGroup = { id: 'root', conjunction: 'and', items: [triage, { id: 'g1', conjunction: 'or', items: [urgent] }, { id: 'g2', conjunction: 'or', items: [] }] }
    tree = toggleGroupConjunction(tree, 'g1')
    expect(tree.conjunction).toBe('and')
    expect((tree.items[1] as AdvancedFilterGroup).conjunction).toBe('and')
    expect(pruneEmptyGroups(tree).items.map(item => item.id)).toEqual([triage.id, 'g1'])
    expect(removeNode(tree, 'g1').items.map(item => item.id)).toEqual([triage.id, 'g2'])
    expect(treeConditions(setConditionValues(tree, urgent.id, [])).map(item => item.id)).toEqual([triage.id])
  })

  it('reads old flat views and legacy advanced chips', () => {
    const legacy = { id: 'adv', field: 'advanced', fieldLabel: 'Advanced filter', operator: 'is', value: 'status:s1', valueLabel: 'Todo', values: [{ value: 'status:s1', valueLabel: 'Todo' }, { value: 'labels:l1', valueLabel: 'Bug' }] } as MyIssuesAppliedFilter
    const tree = advancedFilterTree(legacy)
    expect(tree.conjunction).toBe('and')
    expect(treeConditions(tree).map(item => [item.field, item.values[0].value])).toEqual([['status', 's1'], ['labels', 'l1']])
    const flat = [{ id: 'p', field: 'priority', fieldLabel: 'Priority', operator: 'is', value: '1', valueLabel: 'Urgent' }]
    const normalized = normalizeStoredFilters([...flat, legacy, null, 'junk'])
    expect(normalized).toHaveLength(2)
    expect(normalized[0]).toEqual(flat[0])
    expect(normalized[1].tree?.items).toHaveLength(2)
    expect(countFilterConditions([...flat, legacy])).toBe(3)
    expect(countFilterConditions('not-an-array')).toBe(0)
  })

  it('round-trips the shareable ?filter= parameter and drops empty advanced chips when saving', () => {
    const filters: MyIssuesAppliedFilter[] = [
      { id: 'p', field: 'priority', fieldLabel: 'Priority', operator: 'is', value: '1', valueLabel: 'Urgent é', values: [{ value: '1', valueLabel: 'Urgent é' }] },
      { ...createAdvancedFilter({ id: 'root', conjunction: 'or', items: [triage, { id: 'g', conjunction: 'and', items: [urgent] }] }), id: 'adv' },
    ]
    const encoded = encodeFiltersParam(filters)
    expect(encoded).not.toMatch(/[+/=]/)
    expect(decodeFiltersParam(encoded)).toEqual(normalizeStoredFilters(filters))
    expect(decodeFiltersParam('%%%')).toEqual([])
    expect(savableFilters([...filters, createAdvancedFilter()])).toHaveLength(2)
  })

  it('summarises the first two conditions with their conjunction', () => {
    const tree: AdvancedFilterGroup = { id: 'root', conjunction: 'or', items: [triage, urgent, { id: 'g', conjunction: 'and', items: [createCondition('labels', 'Labels', { value: 'l1', valueLabel: 'Bug' })] }] }
    const summary = advancedFilterSummary(tree)
    expect(summary.parts.map(part => [part.conjunction, part.condition.fieldLabel, part.operatorLabel])).toEqual([[undefined, 'Status', 'is'], ['or', 'Priority', 'is']])
    expect(summary.more).toBe(1)
    expect(valueSummaryLabel('priority', [{ valueLabel: 'Urgent' }, { valueLabel: 'High' }])).toBe('2 priorities')
    expect(valueSummaryLabel('labels', [{ valueLabel: 'Bug' }, { valueLabel: 'Feature' }])).toBe('2 labels')
  })
})

describe('Linear operator sets', () => {
  it('offers is / is not, "is any of" for several values, label set operators and date comparisons', () => {
    expect(filterOperatorChoices('status', ['a']).map(choice => choice.label)).toEqual(['is', 'is not'])
    expect(filterOperatorChoices('priority', ['1', '2']).map(choice => choice.label)).toEqual(['is any of', 'is not'])
    expect(filterOperatorChoices('labels', ['a']).map(choice => choice.label)).toEqual(['include', 'do not include'])
    expect(filterOperatorChoices('labels', ['a', 'b']).map(choice => choice.operator)).toEqual(['includesAll', 'is', 'isNot', 'excludesAll'])
    expect(filterOperatorChoices('dates', ['due:+1w']).map(choice => choice.operator)).toEqual(['before', 'after'])
    expect(normalizeFilterOperator('labels', 'includesAll', ['a'])).toBe('is')
    expect(normalizeFilterOperator('labels', 'excludesAll', ['a'])).toBe('isNot')
  })

  it('defaults future date presets to before and past presets to after, one comparison per chip', () => {
    let filters = toggleFilterOption([], 'dates', 'Dates', { id: 'due:+1w', label: '1 week from now', filterLabel: 'Due date' })
    expect(filters[0]).toMatchObject({ fieldLabel: 'Due date', operator: 'before' })
    filters = toggleFilterOption(filters, 'dates', 'Dates', { id: 'due:+1m', label: '1 month from now', filterLabel: 'Due date' })
    expect(filters).toHaveLength(1)
    expect(filters[0].values?.map(value => value.value)).toEqual(['due:+1m'])
    filters = toggleFilterOption(filters, 'dates', 'Dates', { id: 'created:-1w', label: '1 week ago', filterLabel: 'Created date' })
    expect(filters[1]).toMatchObject({ fieldLabel: 'Created date', operator: 'after' })
  })
})
