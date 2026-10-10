import { createContext, useContext } from 'react'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
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
}

export const IssueRowActionContext = createContext<IssueRowActionContextValue | undefined>(undefined)

export function useIssueRowActions() { return useContext(IssueRowActionContext) }
