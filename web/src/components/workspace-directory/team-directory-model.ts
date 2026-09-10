import { subDays, subMonths, subYears } from 'date-fns'
import type { Team, TeamMember } from '@/types/flow'

export type TeamOrdering = 'name' | 'created' | 'updated'
export type TeamFilterField = 'members' | 'owners' | 'private' | 'created'
export type TeamFilterOperator = 'is' | 'isNot'
export interface TeamDirectoryFilters {
  members: string[]
  owners: string[]
  private: string[]
  created: string[]
  operators: Partial<Record<TeamFilterField, TeamFilterOperator>>
  conjunction: 'and' | 'or'
  advanced: boolean
}
export const emptyTeamFilters = (): TeamDirectoryFilters => ({ members: [], owners: [], private: [], created: [], operators: {}, conjunction: 'and', advanced: false })
export const teamDateChoices = [
  { id: '1', label: '1 day ago' }, { id: '3', label: '3 days ago' },
  { id: '7', label: '1 week ago' }, { id: '30', label: '1 month ago' },
  { id: '90', label: '3 months ago' }, { id: '180', label: '6 months ago' },
  { id: '365', label: '1 year ago' },
]

export function teamTimestamp(team: Team, field: 'created' | 'updated'): number | undefined {
  const value = field === 'created' ? team.createdAt : team.updatedAt
  const timestamp = value ? Date.parse(value) : NaN
  return Number.isFinite(timestamp) ? timestamp : undefined
}

export function compareDirectoryTeams(left: Team, right: Team, ordering: TeamOrdering, descending: boolean) {
  const names = () => left.name.localeCompare(right.name) || left.id.localeCompare(right.id)
  if (ordering === 'name') return names() * (descending ? -1 : 1)
  const a = teamTimestamp(left, ordering), b = teamTimestamp(right, ordering)
  // Unknown historical timestamps stay last in either direction.
  if (a === undefined || b === undefined) return a === b ? names() : a === undefined ? 1 : -1
  return (a - b) * (descending ? -1 : 1) || names()
}

export function matchesTeamDate(team: Team, value: string, now = new Date()) {
  const timestamp = teamTimestamp(team, 'created')
  if (timestamp === undefined) return false
  if (value.startsWith('date:')) {
    const [from, to] = value.slice(5).split('/')
    const start = new Date(`${from}T00:00:00`).getTime()
    const end = to ? new Date(`${to}T00:00:00`) : undefined
    if (end) end.setDate(end.getDate() + 1)
    return timestamp >= start && (!end || timestamp < end.getTime())
  }
  const days = Number(value)
  const start = days === 365 ? subYears(now, 1) : days >= 30 ? subMonths(now, days / 30) : subDays(now, days)
  return timestamp >= start.getTime() && timestamp <= now.getTime()
}

export function indexTeamPeople(memberships: TeamMember[]) {
  const index = new Map<string, { members: Set<string>; owners: Set<string> }>()
  for (const membership of memberships) {
    const entry = index.get(membership.teamId) ?? { members: new Set<string>(), owners: new Set<string>() }
    entry.members.add(membership.userId)
    if (membership.role === 'owner') entry.owners.add(membership.userId)
    index.set(membership.teamId, entry)
  }
  return index
}

export function matchesTeamFilters(team: Team, filters: TeamDirectoryFilters, people: ReturnType<typeof indexTeamPeople>, now = new Date()) {
  if (team.retiredAt) return false
  const conditions: boolean[] = []
  for (const field of ['members', 'owners', 'private', 'created'] as const) {
    const values = filters[field]
    if (!values.length) continue
    if (field === 'created' && teamTimestamp(team, 'created') === undefined) { conditions.push(false); continue }
    const match = field === 'created' ? values.some(value => matchesTeamDate(team, value, now))
      : field === 'private' ? values.includes(String(Boolean(team.private)))
      : values.some(value => people.get(team.id)?.[field].has(value))
    conditions.push(filters.operators[field] === 'isNot' ? !match : match)
  }
  return !conditions.length || (filters.conjunction === 'or' ? conditions.some(Boolean) : conditions.every(Boolean))
}

export function parseTeamFilters(raw: string | null): TeamDirectoryFilters {
  const result = emptyTeamFilters()
  try {
    const value = JSON.parse(raw ?? '{}')
    for (const field of ['members', 'owners', 'private', 'created'] as const) {
      if (Array.isArray(value[field])) result[field] = value[field].filter((id: unknown) => typeof id === 'string')
      if (value.operators?.[field] === 'isNot') result.operators[field] = 'isNot'
    }
    result.private = result.private.filter(id => id === 'true' || id === 'false')
    result.created = result.created.filter(id => teamDateChoices.some(choice => choice.id === id) || /^date:\d{4}-\d{2}-\d{2}(\/\d{4}-\d{2}-\d{2})?$/.test(id)).slice(0, 1)
    result.advanced = value.advanced === true
    result.conjunction = result.advanced && value.conjunction === 'or' ? 'or' : 'and'
  } catch { /* Invalid shared URLs start with an unfiltered directory. */ }
  return result
}

export function teamDateLabel(value: string) {
  return teamDateChoices.find(choice => choice.id === value)?.label ?? value.replace('date:', '').replace('/', ' - ')
}
