import type { FilterBlockDefinition } from '../filter-block-types'

/** Issue FilterBlocks — field keys align with Flow REST issue list AST. */
export const issueFilterBlocks: FilterBlockDefinition[] = [
  block('status', 'status', 'Status', 'equalValue', 10),
  block('assignee', 'assigneeId', 'Assignee', 'equalValue', 20),
  block('agent', 'agentId', 'Agent', 'equalValue', 25),
  block('agentSession', 'agentSessionId', 'Agent Session', 'equalValue', 26),
  block('creator', 'creatorId', 'Creator', 'equalValue', 30),
  block('priority', 'priority', 'Priority', 'equalValue', 40),
  block('labels', 'labelId', 'Labels', 'equalValue', 50),
  block('relations', 'relation', 'Relations', 'equalValue', 55),
  block('suggestedLabel', 'suggestedLabelId', 'Suggested label', 'equalValue', 56),
  block('dates', 'dateFilter', 'Dates', 'date', 60, 'fuzzyDate'),
  block('projectMilestone', 'projectMilestoneId', 'Project milestone', 'equalValue', 70),
  block('project', 'projectId', 'Project', 'equalValue', 80),
  block('projectProperties', 'project', 'Project properties', 'equalValue', 85),
  block('initiative', 'initiativeId', 'Initiative', 'equalValue', 90),
  block('cycle', 'cycleId', 'Cycle', 'equalValue', 100),
  block('addedToCycle', 'addedToCycle', 'Added to cycle', 'date', 105, 'fuzzyDate'),
  block('releases', 'releaseId', 'Releases', 'equalValue', 110),
  block('customers', 'customerId', 'Customers', 'equalValue', 120),
  block('subscribers', 'subscriberId', 'Subscribers', 'equalValue', 130),
  block('externalSource', 'externalSource', 'External source', 'equalValue', 140),
  block('autoClosed', 'autoClosed', 'Auto-closed', 'bool', 150),
  block('content', 'content', 'Content', 'str', 160, 'freeForm'),
  block('links', 'links', 'Links', 'str', 170, 'freeForm'),
  block('template', 'templateId', 'Template', 'equalValue', 180),
]

function block(
  id: string,
  key: string,
  name: string,
  valueType: FilterBlockDefinition['valueType'],
  sortPriority: number,
  inputType?: FilterBlockDefinition['inputType'],
): FilterBlockDefinition {
  return {
    id,
    key,
    name,
    valueType,
    inputType: inputType ?? 'options',
    entityType: 'issue',
    sortPriority,
    defaultCompareOption: valueType === 'date' ? 'within' : valueType === 'str' ? 'contains' : 'is',
  }
}
