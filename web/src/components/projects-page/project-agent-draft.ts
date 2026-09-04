/** Fields that the project drafting assistant can suggest for the create form. */
export type ProjectAgentDraft = {
  name?: string
  summary?: string
  description?: string
  status?: string
  priority?: string
  startDate?: string
  targetDate?: string
  milestones?: string[]
  team?: string
  lead?: string
  members?: string[]
  initiatives?: string[]
  labels?: string[]
  dependencies?: string[]
}

/**
 * Keep the model response useful even when a provider does not follow the JSON
 * instruction. The parser accepts a JSON object first, then common labelled
 * lines used by conversational providers.
 */
export function parseProjectAgentDraft(text: string): ProjectAgentDraft | undefined {
  const source = text.trim()
  if (!source) return undefined

  const object = findJsonObject(source)
  if (object) {
    const parsed = normalizeObject(object)
    if (Object.keys(parsed).length) return parsed
  }

  const result: ProjectAgentDraft = {}
  const lines = source.split(/\r?\n/)
  for (const line of lines) {
    const match = line.match(/^\s*(?:[-*]\s*)?(project\s+)?(name|summary|description|status|priority|start\s*date|target\s*date|milestones?|team|lead|members?|initiatives?|labels?|dependencies?)\s*[:：-]\s*(.+?)\s*$/i)
    if (!match) continue
    const key = match[2].toLowerCase().replace(/\s+/g, '')
    const value = cleanValue(match[3])
    if (!value) continue
    if (key === 'name') result.name = value
    else if (key === 'summary') result.summary = value
    else if (key === 'description') result.description = value
    else if (key === 'status') result.status = value
    else if (key === 'priority') result.priority = value
    else if (key === 'startdate') result.startDate = normalizeDate(value)
    else if (key === 'targetdate') result.targetDate = normalizeDate(value)
    else if (key === 'milestones') result.milestones = splitMilestones(value)
    else if (key === 'team') result.team = value
    else if (key === 'lead') result.lead = value
    else if (key === 'member' || key === 'members') result.members = splitMilestones(value)
    else if (key === 'initiative' || key === 'initiatives') result.initiatives = splitMilestones(value)
    else if (key === 'label' || key === 'labels') result.labels = splitMilestones(value)
    else if (key === 'dependencie' || key === 'dependencies') result.dependencies = splitMilestones(value)
  }
  return Object.keys(result).length ? result : undefined
}

export function projectAgentPrompt(message: string): string {
  return [
    'Draft a new project from the request below.',
    'Return a concise explanation followed by a JSON object with these optional keys:',
    'name, summary, description, status, priority, startDate, targetDate, milestones, team, lead, members, initiatives, labels, dependencies.',
    'Dates must use YYYY-MM-DD. milestones must be an array of short strings.',
    '',
    message.trim(),
  ].join('\n')
}

function findJsonObject(value: string): Record<string, unknown> | undefined {
  const fenced = value.match(/```(?:json)?\s*([\s\S]*?)```/i)?.[1]
  const candidate = fenced ?? value
  const start = candidate.indexOf('{')
  if (start < 0) return undefined
  let depth = 0
  let quoted = false
  let escaped = false
  for (let index = start; index < candidate.length; index += 1) {
    const char = candidate[index]
    if (quoted) {
      if (escaped) escaped = false
      else if (char === '\\') escaped = true
      else if (char === '"') quoted = false
      continue
    }
    if (char === '"') quoted = true
    else if (char === '{') depth += 1
    else if (char === '}') {
      depth -= 1
      if (depth === 0) {
        try {
          const parsed = JSON.parse(candidate.slice(start, index + 1)) as unknown
          return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : undefined
        } catch {
          return undefined
        }
      }
    }
  }
  return undefined
}

function normalizeObject(value: Record<string, unknown>): ProjectAgentDraft {
  const result: ProjectAgentDraft = {}
  const read = (...keys: string[]) => keys.map(key => value[key] ?? value[key.toLowerCase()]).find(item => typeof item === 'string') as string | undefined
  const name = read('name', 'projectName', 'project name')
  const summary = read('summary', 'shortSummary')
  const description = read('description', 'details')
  const status = read('status')
  const priority = read('priority')
  const startDate = read('startDate', 'start_date', 'start date')
  const targetDate = read('targetDate', 'target_date', 'target date', 'endDate')
  const team = read('team', 'teamName', 'team name')
  const lead = read('lead', 'leadName', 'projectLead')
  if (name?.trim()) result.name = cleanValue(name)
  if (summary?.trim()) result.summary = cleanValue(summary)
  if (description?.trim()) result.description = cleanValue(description)
  if (status?.trim()) result.status = cleanValue(status)
  if (priority?.trim()) result.priority = cleanValue(priority)
  if (startDate?.trim()) result.startDate = normalizeDate(startDate)
  if (targetDate?.trim()) result.targetDate = normalizeDate(targetDate)
  if (team?.trim()) result.team = cleanValue(team)
  if (lead?.trim()) result.lead = cleanValue(lead)
  for (const [key, aliases] of Object.entries({
    members: ['members', 'memberNames', 'memberIds'],
    initiatives: ['initiatives', 'initiativeNames', 'initiativeIds'],
    labels: ['labels', 'labelNames', 'labelIds'],
    dependencies: ['dependencies', 'dependencyNames', 'dependencyIds'],
  })) {
    const raw = aliases.map(alias => value[alias]).find(item => Array.isArray(item) || typeof item === 'string')
    const list = readList(raw)
    if (list.length) result[key as 'members' | 'initiatives' | 'labels' | 'dependencies'] = list
  }
  const milestones = value.milestones
  if (Array.isArray(milestones)) {
    const items = milestones.filter((item): item is string => typeof item === 'string').map(cleanValue).filter(Boolean)
    if (items.length) result.milestones = items
  } else if (typeof milestones === 'string') {
    result.milestones = splitMilestones(milestones)
  }
  return result
}

function cleanValue(value: string) {
  return value.replace(/^['"`]|['"`]$/g, '').replace(/\s+$/, '').trim()
}

function normalizeDate(value: string) {
  const cleaned = cleanValue(value)
  const match = cleaned.match(/^(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})$/)
  return match ? `${match[1]}-${match[2].padStart(2, '0')}-${match[3].padStart(2, '0')}` : cleaned
}

function splitMilestones(value: string) {
  return value.split(/[,;|]/).map(cleanValue).filter(Boolean)
}

function readList(value: unknown) {
  if (Array.isArray(value)) return value.filter((item): item is string => typeof item === 'string').map(cleanValue).filter(Boolean)
  return typeof value === 'string' ? splitMilestones(value) : []
}
