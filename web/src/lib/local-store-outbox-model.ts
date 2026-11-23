import type { IssueUpdateInput } from '@/types/flow'

export type OutboxOutcome = { ok: true; rebaseVersion?: number } | { ok: false; status: number; code?: string; message: string }

export interface OutboxEdit {
  id: string
  issueId: string
  editor: string
  input: IssueUpdateInput
  order: number
  createdAt: number
  sent?: true
  bytes: number
  outcome?: OutboxOutcome
}
