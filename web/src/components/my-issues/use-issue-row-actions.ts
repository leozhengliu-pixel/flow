import { createContext, useCallback, useContext } from 'react'
import type { BootstrapData, Issue, IssueUpdateInput, TeamSettings } from '@/types/flow'
import type { IssueConversionKind } from '@/components/issue/issue-options-menu'
import { resolvedTeamSettings } from '@/lib/team-hierarchy'
import type { MyIssuesCreateContext } from './my-issues-list'

/**
 * Host callbacks for the full row context menu. Pages that render issue lists provide this once;
 * every row menu below them gets the same actions.
 */
export interface IssueRowActionContextValue {
  data: BootstrapData
  onUpdateIssue: (issueId: string, input: IssueUpdateInput) => Promise<unknown>
  onDeleteIssues?: (issueIds: string[]) => Promise<void>
  onOpenIssue?: (issue: Issue) => void
  onCreateIssue?: (context: MyIssuesCreateContext) => void
  /** Uses the same conversion handler as the issue detail actions menu. */
  onConvertIssue?: (issue: Issue, kind: IssueConversionKind) => Promise<void>
}

export const IssueRowActionContext = createContext<IssueRowActionContextValue | undefined>(undefined)

export function useIssueRowActions() { return useContext(IssueRowActionContext) }

/**
 * Estimate settings (inheritance resolved) for a row's team. Returns undefined
 * for every team when the list is rendered outside an issue page provider.
 */
export function useTeamEstimateSettings() {
  const teamSettings = useIssueRowActions()?.data.teamSettings
  return useCallback((teamId?: string): TeamSettings | undefined => teamId && teamSettings ? resolvedTeamSettings(teamSettings, teamId) : undefined, [teamSettings])
}
