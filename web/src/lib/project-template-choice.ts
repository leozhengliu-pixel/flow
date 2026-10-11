import type { NewProjectTemplateChoice } from '@/components/projects-page/new-project-dialog'
import type { ProjectDependencyRelationInput, ProjectTemplate } from '@/types/flow'

const PRIORITY_NAMES = ['No priority', 'Urgent', 'High', 'Medium', 'Low']

export type ProjectTemplateChoiceContext = {
  /** Project statuses, to turn the template's status id into the dialog's status name. */
  projectStatuses: ReadonlyArray<{ id: string; name: string }>
  /** Labels a project can carry; template labels outside this list are dropped. */
  projectLabels: ReadonlyArray<{ id: string }>
  /** Existing projects; dependencies on projects outside this list are dropped (an empty list filters nothing). */
  projects?: ReadonlyArray<{ id: string }>
}

/** The template's preset dependencies, falling back to the legacy blocked-by list. */
export function projectTemplateDependencyRelations(template: Pick<ProjectTemplate, 'dependencyIds' | 'dependencyRelations'>): ProjectDependencyRelationInput[] {
  if (template.dependencyRelations?.length) return template.dependencyRelations.map(relation => ({ projectId: relation.projectId, type: relation.type }))
  return (template.dependencyIds ?? []).map(projectId => ({ projectId, type: 'blocked_by' as const }))
}

/** Maps a project template to what the new-project dialog applies when it is picked. */
export function projectTemplateChoice(template: ProjectTemplate, context: ProjectTemplateChoiceContext): NewProjectTemplateChoice {
  const labelIds = new Set(context.projectLabels.map(label => label.id))
  // An empty list means the projects have not loaded yet: keep the template's dependencies.
  const projectIds = context.projects?.length ? new Set(context.projects.map(project => project.id)) : undefined
  const dependencyRelations = projectTemplateDependencyRelations(template).filter(relation => !projectIds || projectIds.has(relation.projectId))
  return {
    id: template.id,
    label: template.name,
    name: template.projectName?.trim() || template.name,
    description: template.description,
    summary: template.summary,
    icon: template.icon,
    color: template.color,
    status: template.statusId ? context.projectStatuses.find(status => status.id === template.statusId)?.name : undefined,
    priority: PRIORITY_NAMES[template.priority] ?? 'No priority',
    leadId: template.leadId || undefined,
    memberIds: template.memberIds ?? [],
    teamIds: template.teamIds ?? [],
    initiativeIds: template.initiativeIds ?? [],
    labelIds: (template.labelIds ?? []).filter(id => labelIds.has(id)),
    dependencyIds: dependencyRelations.filter(relation => relation.type === 'blocked_by').map(relation => relation.projectId),
    dependencyRelations,
    milestones: (template.milestones ?? []).map(milestone => ({ templateMilestoneId: milestone.id, name: milestone.name, description: milestone.description || undefined })),
  }
}

export function projectTemplateChoices(templates: readonly ProjectTemplate[], context: ProjectTemplateChoiceContext): NewProjectTemplateChoice[] {
  return templates.map(template => projectTemplateChoice(template, context))
}
