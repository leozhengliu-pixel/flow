import type { Initiative } from '@/types/flow'

export function countInitiativeFilterValues(initiatives: Initiative[], field: string): Map<string, number> {
  const counts = new Map<string, number>()
  for (const initiative of initiatives) {
    const values = field === 'teamId' ? initiative.contributingTeamIds : field === 'labelId' ? initiative.labelIds :
      [field === 'ownerId' ? initiative.owner?.id ?? '' : field === 'creatorId' ? initiative.creator.id : field === 'leadTeamId' ? initiative.leadTeamId ?? '' : field === 'priority' ? String(initiative.priority) : field === 'status' ? initiative.status : field === 'health' ? initiative.health : initiative.id]
    for (const value of new Set(values)) counts.set(value, (counts.get(value) ?? 0) + 1)
  }
  return counts
}
