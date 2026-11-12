import type { Project } from '@/types/flow'

const fields = ['id', 'slugId', 'name', 'summary', 'icon', 'color', 'priority', 'priorityLabel', 'position', 'status', 'lead', 'memberIds', 'labelIds', 'teamIds', 'dependencyIds', 'initiatives', 'customers', 'startDate', 'targetDate', 'startDateResolution', 'targetDateResolution', 'progress', 'issueCount', 'health', 'createdAt', 'updatedAt', 'archivedAt', 'updateCadence'] as const

/** Reading a cached detail for a newer row must not copy its histories or body into a window. */
export function scaleP19ProjectSummary(project: Project): Project {
  return {
    ...Object.fromEntries(fields.filter(field => Object.hasOwn(project, field)).map(field => [field, project[field]])),
    description: '', resources: [], comments: [], descriptionRevisions: [],
    milestones: (project.milestones ?? []).map(({ description: _body, descriptionRevisions: _history, ...milestone }) => milestone),
  } as unknown as Project
}
