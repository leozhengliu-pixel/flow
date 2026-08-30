export type ProjectFilterField = 'status' | 'priority' | 'lead' | 'members' | 'health' | 'dates' | 'milestones' | 'labels' | 'teams' | 'project'
export type ProjectFilterOperator = 'is' | 'isNot'
export type ProjectFilterOption = { id: string; label: string; color?: string; count?: number }
export type ProjectFilter = {
  id: string
  field: ProjectFilterField
  fieldLabel: string
  operator: ProjectFilterOperator
  values: ProjectFilterOption[]
}

export function createProjectFilter(field: ProjectFilterField, fieldLabel: string, value: ProjectFilterOption): ProjectFilter {
  return { id: `${field}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, field, fieldLabel, operator: 'is', values: [value] }
}

export function isProjectFilter(value: unknown): value is ProjectFilter {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<ProjectFilter>
  return typeof candidate.id === 'string' && typeof candidate.field === 'string' && (candidate.operator === 'is' || candidate.operator === 'isNot') && Array.isArray(candidate.values)
}
