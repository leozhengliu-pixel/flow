import { useMemo, type ReactNode } from 'react'
import { SelectionIssueContextValue, type SelectionIssueContext } from './selection-issue'

/** Where an issue made from a comment's selection on this page goes (team, project, and parent on an issue page). */
export function SelectionIssueProvider({ value, children }: { value: SelectionIssueContext; children: ReactNode }) {
  const { teamId, projectId, parentId } = value
  const memo = useMemo(() => ({ teamId, projectId, parentId }), [parentId, projectId, teamId])
  return <SelectionIssueContextValue.Provider value={memo}>{children}</SelectionIssueContextValue.Provider>
}
