import type { BootstrapData, IssueLabel, LabelResourceType } from '@/types/flow'

export function countLabelsByResource(labels: readonly IssueLabel[], resourceType: LabelResourceType) {
  let count = 0
  for (const label of labels) {
    if ((label.resourceType ?? 'issue') === resourceType) count += 1
  }
  return count
}

/** `array.map` that returns the input array itself when no element changed, so memoised consumers keep their cache. */
function mapStable<T>(items: T[], change: (item: T) => T): T[] {
  let next: T[] | undefined
  items.forEach((item, index) => {
    const mapped = change(item)
    if (mapped !== item && !next) next = items.slice(0, index)
    next?.push(mapped)
  })
  return next ?? items
}

export function deriveResourceCounts(data: BootstrapData): BootstrapData {
  if (data.issueCollectionPaged) return { ...data, issues: data.issues.slice(0, 2000) }
  const labelCounts = new Map<string, number>()
  const projectIssueCounts = new Map<string, number>()
  const increment = (ids: string[]) => {
    for (const id of new Set(ids.filter(Boolean))) labelCounts.set(id, (labelCounts.get(id) ?? 0) + 1)
  }

  for (const issue of data.issues) {
    if (issue.archivedAt) continue
    increment((issue.labels ?? []).map(label => label.id))
    if (issue.project) projectIssueCounts.set(issue.project.id, (projectIssueCounts.get(issue.project.id) ?? 0) + 1)
  }
  for (const project of data.projects) {
    if (!project.archivedAt) increment(project.labelIds ?? [])
  }
  for (const initiative of data.initiatives) increment(initiative.labelIds ?? [])

  const labels = mapStable(data.labels, label => { const count = labelCounts.get(label.id) ?? 0; return label.issueCount === count ? label : { ...label, issueCount: count } })
  const labelsById = new Map<string, IssueLabel>(labels.map(label => [label.id, label]))
  return {
    ...data,
    labels,
    issues: mapStable(data.issues, issue => {
      // Slim projections may omit labels.
      if (!issue.labels || issue.labels.every(label => !labelsById.has(label.id) || labelsById.get(label.id) === label)) return issue
      return { ...issue, labels: issue.labels.map(label => labelsById.get(label.id) ?? label) }
    }),
    projects: mapStable(data.projects, project => { const count = projectIssueCounts.get(project.id) ?? 0; return project.issueCount === count ? project : { ...project, issueCount: count } }),
  }
}
