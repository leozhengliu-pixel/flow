/** Inbox detail host routing helpers for P1-A update + overview surfaces. */

export type InboxHostKind =
  | 'project-updates'
  | 'initiative-updates'
  | 'project-overview'
  | 'initiative-overview'
  | 'project-reminder'
  | 'other'

const PROJECT_UPDATE_TYPES = new Set([
  'projectUpdate',
  'projectUpdateCreated',
  'projectUpdateComment',
  'projectUpdateMention',
  'projectUpdateReminder',
  'projectUpdateDueReminder',
  'projectUpdatePrompt',
])

const INITIATIVE_UPDATE_TYPES = new Set([
  'initiativeUpdate',
  'initiativeUpdateCreated',
  'initiativeUpdateComment',
  'initiativeUpdateMention',
  'initiativeUpdateReminder',
  'initiativeUpdateDueReminder',
  'initiativeUpdatePrompt',
  'initiativeReminder',
])

const PROJECT_OVERVIEW_TYPES = new Set([
  'projectOverview',
  'projectSubscribed',
  'projectDescriptionChanged',
  'projectStatusChanged',
  'projectAddedAsMember',
  'projectAddedAsLead',
  'projectReminder',
])

const INITIATIVE_OVERVIEW_TYPES = new Set([
  'initiativeOverview',
  'initiativeSubscribed',
  'initiativeDescriptionChanged',
  'initiativeStatusChanged',
  'initiativeAddedAsOwner',
  'initiativeAddedAsMember',
])

export function classifyInboxHost(input: {
  type: string
  projectId?: string
  issueId?: string
  sourceType?: string
  sourceId?: string
  identifier?: string
}): InboxHostKind {
  const type = input.type
  if (input.identifier === 'pulseSummary') return 'other'
  if (PROJECT_UPDATE_TYPES.has(type)) return 'project-updates'
  if (INITIATIVE_UPDATE_TYPES.has(type)) return 'initiative-updates'
  if (PROJECT_OVERVIEW_TYPES.has(type)) return 'project-overview'
  if (INITIATIVE_OVERVIEW_TYPES.has(type)) return 'initiative-overview'
  if (input.projectId && !input.issueId && /project/i.test(type)) {
    return /update/i.test(type) ? 'project-updates' : 'project-overview'
  }
  if (!input.issueId && !input.projectId && input.sourceType === 'initiative') {
    return /update/i.test(type) ? 'initiative-updates' : 'initiative-overview'
  }
  if (!input.issueId && !input.projectId && /initiative/i.test(type)) {
    return /update/i.test(type) ? 'initiative-updates' : 'initiative-overview'
  }
  return 'other'
}

export function healthLabel(value: string) {
  switch (value) {
    case 'onTrack':
      return 'On track'
    case 'atRisk':
      return 'At risk'
    case 'offTrack':
      return 'Off track'
    case 'noUpdate':
      return 'No updates'
    default:
      return value.replace(/([a-z])([A-Z])/g, '$1 $2')
  }
}
