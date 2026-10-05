import { filterValues, type MyIssuesAppliedFilter, type MyIssuesFilterOperator } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey } from '@/components/my-issues/my-issues-surface'
import { advancedFilterTree, isAdvancedGroup } from './advanced-filter'

/**
 * Deterministic view suggestions from the filters (Linear suggests these with AI): a name for the
 * placeholder ("Urgent triage issues", "Unprioritized to-dos", "Skyler's to-dos"), a description
 * ("Issues in Triage with urgent priority and either a Bug or Feature label") and an icon that follows
 * the main filter (triage, urgent, bug, the status circle…).
 */
export interface ViewSuggestion { name?: string; description?: string; icon?: string }
interface Clause { field: MyIssuesFilterKey; operator: MyIssuesFilterOperator; labels: string[]; ids: string[] }
type StateInfo = { id: string; name: string; type: string }

const PRIORITY_ADJECTIVES = ['Unprioritized', 'Urgent', 'High priority', 'Medium priority', 'Low priority']
const PRIORITY_ZH = ['未设置优先级的', '紧急', '高优先级', '中优先级', '低优先级']

function clauses(filters: MyIssuesAppliedFilter[]): Clause[] {
  const result: Clause[] = []
  for (const filter of filters) {
    if (filter.field === 'advanced') {
      const tree = advancedFilterTree(filter)
      // Only conditions every issue must meet: an AND root's direct conditions.
      if (tree.conjunction !== 'and') continue
      for (const item of tree.items) if (!isAdvancedGroup(item) && item.values.length) result.push({ field: item.field, operator: item.operator, labels: item.values.map(value => value.valueLabel), ids: item.values.map(value => value.value) })
      continue
    }
    const values = filterValues(filter)
    result.push({ field: filter.field, operator: filter.operator, labels: values.map(value => value.valueLabel), ids: values.map(value => value.value) })
  }
  return result
}

const positive = (clause?: Clause) => clause && (clause.operator === 'is' || clause.operator === 'includesAll') ? clause : undefined
const single = (clause?: Clause) => clause && clause.ids.length === 1 ? clause : undefined
const joinOr = (values: string[], word = 'or') => values.length <= 1 ? values.join('') : `${values.slice(0, -1).join(', ')} ${word} ${values.at(-1)}`

export function suggestView(filters: MyIssuesAppliedFilter[], options: { states?: StateInfo[]; locale?: 'en-US' | 'zh-CN' } = {}): ViewSuggestion {
  const all = clauses(filters)
  if (!all.length) return {}
  const zh = options.locale === 'zh-CN'
  const find = (field: MyIssuesFilterKey) => all.find(clause => clause.field === field)
  const status = single(positive(find('status'))), priority = single(positive(find('priority')))
  const assignee = single(positive(find('assignee'))), label = single(positive(find('labels')))
  const state = status ? options.states?.find(item => item.id === status.ids[0] || item.type === status.ids[0]) : undefined
  const statusName = status?.labels[0] ?? ''
  const isTriage = /triage|分流/i.test(statusName) || state?.type === 'triage'
  const isTodo = state?.type === 'unstarted' || /^to-?do$/i.test(statusName)

  // Name: [Owner's] [Priority] [status / label] issues.
  let name: string | undefined
  if (status || priority || assignee || label) {
    if (zh) {
      const owner = assignee?.ids[0] ? `${assignee.labels[0]}的` : assignee ? '未分配的' : ''
      const prio = priority ? PRIORITY_ZH[Number(priority.ids[0])] ?? priority.labels[0] : ''
      const subject = status ? `${statusName}事项` : label ? `${label.labels[0]}事项` : '事项'
      name = `${owner}${prio}${subject}`
    } else {
      const parts: string[] = []
      if (assignee) parts.push(assignee.ids[0] ? `${assignee.labels[0].split(' ')[0]}'s` : 'Unassigned')
      if (priority) parts.push(PRIORITY_ADJECTIVES[Number(priority.ids[0])] ?? priority.labels[0])
      if (status) parts.push(isTodo ? 'to-dos' : isTriage ? 'triage issues' : `${statusName} issues`)
      else if (label) parts.push(`${label.labels[0]} issues`)
      else parts.push('issues')
      const text = parts.join(' ')
      name = text[0].toUpperCase() + text.slice(1)
    }
  }

  // Description: "Issues in Triage with urgent priority and either a Bug or Feature label".
  const phrases = all.flatMap(clause => describe(clause, zh))
  // "… with urgent priority and either a Bug or Feature label": the last "with" is implied.
  if (!zh && phrases.length > 1 && phrases.at(-1)!.startsWith('with ') && phrases.slice(0, -1).some(phrase => phrase.startsWith('with '))) phrases[phrases.length - 1] = phrases.at(-1)!.slice(5)
  const description = phrases.length ? zh ? `${phrases.join('，')}的事项` : `Issues ${phrases.length > 1 ? `${phrases.slice(0, -1).join(' ')} and ${phrases.at(-1)}` : phrases[0]}` : undefined

  // Icon: follows the main (first) filter.
  const main = all[0]
  let icon: string | undefined
  if (main.field === 'status') icon = isTriage && main === status ? 'IssueStatusTriage' : statusIcon(options.states?.find(item => main.ids.includes(item.id))?.type)
  else if (main.field === 'priority') icon = main.ids.includes('1') ? 'Alert' : 'BarChart'
  else if (main.field === 'labels') icon = main.labels.some(value => /bug|缺陷/i.test(value)) ? 'Bug' : 'Label'
  else if (main.field === 'assignee' || main.field === 'creator') icon = 'MyIssues'
  else if (main.field === 'project') icon = 'Project'
  else if (main.field === 'cycle' || main.field === 'dates') icon = 'Calendar'
  return { name, description, icon }
}

function statusIcon(type?: string) {
  return type === 'triage' ? 'IssueStatusTriage' : type === 'backlog' ? 'IssueStatusBacklog' : type === 'unstarted' ? 'IssueStatusTodo' : type === 'started' ? 'IssueStatusStarted' : type === 'completed' || type === 'canceled' ? 'IssueStatusDone' : 'IssueStatusTodo'
}

function describe(clause: Clause, zh: boolean): string[] {
  const negative = clause.operator === 'isNot' || clause.operator === 'excludesAll'
  const values = clause.labels
  if (zh) {
    const list = values.join(clause.operator === 'includesAll' ? '和' : '或')
    switch (clause.field) {
      case 'status': return [`${negative ? '不在' : '处于'}${list}`]
      case 'priority': return [`优先级${negative ? '不是' : '为'}${list}`]
      case 'labels': return [`${negative ? '不带' : '带有'}${list}标签`]
      case 'assignee': return [clause.ids.every(id => !id) ? '未分配' : `${negative ? '未指派给' : '指派给'}${list}`]
      case 'project': return [`${negative ? '不属于' : '属于'}项目${list}`]
      default: return []
    }
  }
  switch (clause.field) {
    case 'status': return [`${negative ? 'not in' : 'in'} ${joinOr(values)}`]
    case 'priority': return [`${negative ? 'without' : 'with'} ${joinOr(values.map(value => value.toLocaleLowerCase()))} priority`]
    case 'labels': {
      if (clause.operator === 'includesAll') return [`with ${joinOr(values, 'and')} labels`]
      if (negative) return [`without ${joinOr(values)} labels`]
      return [values.length > 1 ? `with either a ${joinOr(values)} label` : `with a ${values[0]} label`]
    }
    case 'assignee': return [clause.ids.every(id => !id) ? (negative ? 'that are assigned' : 'that are unassigned') : `${negative ? 'not assigned to' : 'assigned to'} ${joinOr(values)}`]
    case 'creator': return [`created by ${joinOr(values)}`]
    case 'project': return [`${negative ? 'outside' : 'in'} ${joinOr(values)}`]
    case 'cycle': return [`in ${joinOr(values)}`]
    default: return []
  }
}
