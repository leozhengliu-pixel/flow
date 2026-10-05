import { describe, expect, it } from 'vitest'
import type { MyIssuesAppliedFilter, MyIssuesFilterOperator } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey } from '@/components/my-issues/my-issues-surface'
import { createAdvancedFilter, createCondition } from './advanced-filter'
import { suggestView } from './view-suggestions'

const chip = (field: MyIssuesFilterKey, values: [string, string][], operator: MyIssuesFilterOperator = 'is'): MyIssuesAppliedFilter => ({ id: field, field, fieldLabel: field, operator, value: values[0][0], valueLabel: values[0][1], values: values.map(([value, valueLabel]) => ({ value, valueLabel })) })
const states = [{ id: 'triage', name: 'Triage', type: 'triage' }, { id: 'todo', name: 'Todo', type: 'unstarted' }]

describe('deterministic view suggestions', () => {
  it('suggests Linear-style names, descriptions and icons', () => {
    const urgentTriage = suggestView([chip('status', [['triage', 'Triage']]), chip('priority', [['1', 'Urgent']]), chip('labels', [['bug', 'Bug'], ['feature', 'Feature']])], { states })
    expect(urgentTriage).toEqual({ name: 'Urgent triage issues', description: 'Issues in Triage with urgent priority and either a Bug or Feature label', icon: 'IssueStatusTriage' })
    expect(suggestView([chip('priority', [['0', 'No priority']]), chip('status', [['todo', 'Todo']])], { states })).toMatchObject({ name: 'Unprioritized to-dos', icon: 'BarChart' })
    expect(suggestView([chip('assignee', [['u1', 'Skyler Lee']]), chip('status', [['todo', 'Todo']])], { states }).name).toBe("Skyler's to-dos")
    expect(suggestView([chip('labels', [['bug', 'Bug']])]).icon).toBe('Bug')
    expect(suggestView([chip('priority', [['1', 'Urgent']])]).icon).toBe('Alert')
    expect(suggestView([])).toEqual({})
  })

  it('uses an AND advanced filter, ignores OR trees and negations read as such', () => {
    const tree = createAdvancedFilter({ id: 'r', conjunction: 'and', items: [createCondition('status', 'Status', { value: 'todo', valueLabel: 'Todo' })] })
    expect(suggestView([tree], { states }).name).toBe('To-dos')
    expect(suggestView([createAdvancedFilter({ id: 'r', conjunction: 'or', items: [createCondition('status', 'Status', { value: 'todo', valueLabel: 'Todo' })] })], { states })).toEqual({})
    expect(suggestView([chip('labels', [['bug', 'Bug']], 'isNot')]).description).toBe('Issues without Bug labels')
  })

  it('writes Chinese suggestions in zh-CN', () => {
    expect(suggestView([chip('priority', [['1', 'Urgent']]), chip('status', [['todo', 'Todo']])], { states, locale: 'zh-CN' })).toMatchObject({ name: '紧急Todo事项', description: '优先级为Urgent，处于Todo的事项' })
  })
})
