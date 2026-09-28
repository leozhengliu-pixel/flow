/**
 * LS-0567 SubIssuesEditor depth — FastIssueDraft / embedded bootstrap / recurring factories.
 */
import type { BootstrapData, Issue } from '@/types/flow'
import type { SubIssueInput } from './sub-issue-editor'

export interface FastIssueDraftSeed {
  title?: string
  description?: string
  priority?: number
  assigneeId?: string
  projectId?: string
  labelIds?: string[]
  dueDate?: string
}

/** Seed a SubIssueInput from a parent + optional fast draft (composer / template). */
export function createSubIssueInputFromDraft(
  parent: Issue,
  data: BootstrapData,
  draft: FastIssueDraftSeed = {},
): SubIssueInput {
  const teamStates = data.states.filter(state =>
    data.states.some(item => item.teamId === parent.team.id)
      ? state.teamId === parent.team.id
      : !state.teamId,
  )
  const defaultState =
    teamStates.find(state => state.type === 'unstarted') ?? teamStates[0] ?? parent.state

  return {
    title: draft.title ?? '',
    description: draft.description ?? '',
    stateId: defaultState.id,
    priority: draft.priority ?? parent.priority,
    assigneeId: draft.assigneeId ?? parent.assignee?.id,
    projectId: draft.projectId ?? parent.project?.id,
    labelIds: draft.labelIds ?? [],
    dueDate: draft.dueDate,
    attachments: [],
  }
}

/** Embedded bootstrap for inline SubIssuesEditor (detail pane / split). */
export function bootstrapEmbeddedSubIssueEditor(args: {
  parent: Issue
  data: BootstrapData
  draft?: FastIssueDraftSeed
}): { parent: Issue; seed: SubIssueInput } {
  return {
    parent: args.parent,
    seed: createSubIssueInputFromDraft(args.parent, args.data, args.draft),
  }
}

/** Recurring template → sub-issue seed (title + cadence fields only today). */
export function createSubIssueInputFromRecurringTemplate(
  parent: Issue,
  data: BootstrapData,
  template: { title: string; description?: string; priority?: number },
): SubIssueInput {
  return createSubIssueInputFromDraft(parent, data, {
    title: template.title,
    description: template.description,
    priority: template.priority,
  })
}

export const subIssueEditorFactories = {
  createSubIssueInputFromDraft,
  bootstrapEmbeddedSubIssueEditor,
  createSubIssueInputFromRecurringTemplate,
}
