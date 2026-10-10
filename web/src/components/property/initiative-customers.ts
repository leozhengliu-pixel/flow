import type { Customer } from '@/types/flow'

/** Resolve customers linked to an initiative via its projects (name match or request). */
export function customersForInitiative(
  initiative: { projectIds: string[] },
  projects: Array<{ id: string; customers?: string[] }>,
  customers: Array<Pick<Customer, 'id' | 'name' | 'logoUrl'>>,
  requests: Array<{ projectId?: string; customerId: string; archivedAt?: string }>,
): Array<Pick<Customer, 'id' | 'name' | 'logoUrl'>> {
  const projectIdSet = new Set(initiative.projectIds)
  const byName = new Map(customers.map((item) => [item.name.toLowerCase(), item]))
  const seen = new Set<string>()
  const result: Array<Pick<Customer, 'id' | 'name' | 'logoUrl'>> = []

  for (const project of projects) {
    if (!projectIdSet.has(project.id)) continue
    for (const name of project.customers ?? []) {
      const match = byName.get(name.toLowerCase())
      if (match && !seen.has(match.id)) {
        seen.add(match.id)
        result.push(match)
      }
    }
  }
  for (const request of requests) {
    if (!request.projectId || request.archivedAt || !projectIdSet.has(request.projectId)) continue
    if (seen.has(request.customerId)) continue
    const match = customers.find((item) => item.id === request.customerId)
    if (match) {
      seen.add(match.id)
      result.push(match)
    }
  }
  return result
}
