import type { MyIssuesFilterKey, MyIssuesFilterOption } from './my-issues-surface'

/**
 * AI filter: turn a sentence ("urgent bugs assigned to me due this week") into filter-bar filters by
 * matching the words against the workspace's own statuses, priorities, people, labels, projects and
 * cycles, plus date phrases. Words that match nothing become a content (title) search.
 */
export interface ParsedFilter { field: MyIssuesFilterKey; option: MyIssuesFilterOption }

const FIELDS: MyIssuesFilterKey[] = ['status', 'priority', 'assignee', 'labels', 'project', 'cycle', 'creator']
const DATE_PHRASES: { pattern: RegExp; id: string; label: string }[] = [
  { pattern: /\boverdue\b|逾期/, id: 'overdue', label: 'Overdue' },
  { pattern: /\bdue (?:today)\b|今天到期/, id: 'today', label: 'Due today' },
  { pattern: /\bdue (?:this|next) week\b|本周到期|下周到期/, id: 'next-week', label: 'Due in the next week' },
  { pattern: /\bno due date\b|没有截止/, id: 'no-due-date', label: 'No due date' },
  { pattern: /\bcreated (?:today|in the past day)\b|今天创建/, id: 'created-past-day', label: 'Created in the past day' },
  { pattern: /\bcreated (?:this|last|past) week\b|本周创建|上周创建/, id: 'created-past-week', label: 'Created in the past week' },
  { pattern: /\bcreated (?:this|last|past) month\b|本月创建|上月创建/, id: 'created-past-month', label: 'Created in the past month' },
  { pattern: /\bupdated (?:this|last|past) week\b|本周更新/, id: 'updated-past-week', label: 'Updated in the past week' },
  { pattern: /\bcompleted\b.*\b(?:last|past) month\b|上月完成/, id: 'completed-last-month', label: 'Completed in the past month' },
]
const SELF = /\b(?:assigned to me|my issues|mine|assigned to myself)\b|指派给我|我的/

export function parseNaturalLanguageFilter(query: string, optionsFor: (field: MyIssuesFilterKey) => MyIssuesFilterOption[] | undefined, viewerId?: string): ParsedFilter[] {
  let rest = ` ${query.toLocaleLowerCase()} `
  const parsed: ParsedFilter[] = []
  const take = (pattern: RegExp | string) => { rest = typeof pattern === 'string' ? rest.split(pattern).join(' ') : rest.replace(pattern, ' ') }
  if (SELF.test(rest)) { parsed.push(viewerId ? { field: 'assignee', option: { id: viewerId, label: 'Me' } } : { field: 'ai', option: { id: 'assigned-to-me', label: 'Assigned to me' } }); take(SELF) }
  for (const phrase of DATE_PHRASES) if (phrase.pattern.test(rest)) {
    parsed.push(phrase.id === 'completed-last-month' ? { field: 'ai', option: { id: phrase.id, label: phrase.label } } : { field: 'dates', option: { id: phrase.id, label: phrase.label } })
    take(phrase.pattern)
  }
  const assignedTo = rest.match(/\bassigned to ([\p{L}\p{N}._-]+(?: [\p{L}\p{N}._-]+)?)/u)
  for (const field of FIELDS) {
    const options = flatten(optionsFor(field) ?? []).filter(option => option.id && option.label.length > 1)
    // Longest names first so "In Review" wins over "Review".
    for (const option of [...options].sort((a, b) => b.label.length - a.label.length)) {
      const name = option.label.toLocaleLowerCase()
      const firstName = name.split(' ')[0]
      if (field === 'assignee' && assignedTo && assignedTo[1].split(' ')[0] === firstName && !parsed.some(item => item.field === 'assignee')) {
        parsed.push({ field, option })
        take(`assigned to ${assignedTo[1]}`)
        continue
      }
      if (!containsWord(rest, name) && !(field === 'labels' && containsWord(rest, plural(name)))) continue
      if (parsed.some(item => item.field === field && item.option.id === option.id)) continue
      parsed.push({ field, option })
      take(containsWord(rest, name) ? name : plural(name))
    }
  }
  const leftover = rest.replace(/\b(?:issues?|tickets?|tasks?|with|in|the|and|that|are|is|to|by|of|for|show|me|all|priority|assigned|label(?:ed|s)?|project|status)\b/g, ' ').replace(/[，,。.]/g, ' ').replace(/\s+/g, ' ').trim()
  if (leftover.length > 1) parsed.push({ field: 'content', option: { id: `query:${leftover}`, label: leftover } })
  return parsed
}

function flatten(options: MyIssuesFilterOption[]): MyIssuesFilterOption[] {
  return options.flatMap(option => option.children?.length ? flatten(option.children) : [option])
}
function plural(value: string) { return value.endsWith('s') ? value : `${value}s` }
function containsWord(text: string, word: string) {
  if (/[㐀-鿿]/.test(word)) return text.includes(word)
  return new RegExp(`(^|[^\\p{L}\\p{N}])${word.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}($|[^\\p{L}\\p{N}])`, 'u').test(text)
}
