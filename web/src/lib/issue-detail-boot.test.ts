import { describe, expect, it } from 'vitest'
import { makeIssue } from '@/test/fixtures'
import { issueDetailPane } from './issue-detail-boot'

describe('issue detail boot pane', () => {
  it('keeps a full issue editor mounted during background access revalidation', () => {
    expect(issueDetailPane({
      selectedIssue: makeIssue({ isSummary: false }),
      accessPending: true,
      issueContextLoading: true,
      missingIssueRecord: true,
      contextReady: false,
    })).toBe('editor')
  })

  it('shows a read-only preview instead of promoting a summary into the editor', () => {
    expect(issueDetailPane({
      selectedIssue: makeIssue({ isSummary: true, title: 'Summary only' }),
      previewIssue: makeIssue({ isSummary: false, title: 'Authorized body' }),
      accessPending: true,
      issueContextLoading: true,
      missingIssueRecord: true,
      contextReady: false,
    })).toBe('preview')
  })

  it('does not treat an incomplete summary as a persistable editor', () => {
    expect(issueDetailPane({
      selectedIssue: makeIssue({ isSummary: true }),
      previewIssue: makeIssue({ isSummary: true }),
      accessPending: false,
      issueContextLoading: false,
      missingIssueRecord: true,
      contextReady: true,
    })).toBe('not-found')
  })

  it('shows access checking or loading until the record is ready', () => {
    expect(issueDetailPane({
      selectedIssue: undefined,
      accessPending: true,
      issueContextLoading: false,
      missingIssueRecord: true,
      contextReady: false,
    })).toBe('checking-access')
    expect(issueDetailPane({
      selectedIssue: undefined,
      accessPending: false,
      issueContextLoading: true,
      missingIssueRecord: true,
      contextReady: false,
    })).toBe('loading')
    expect(issueDetailPane({
      selectedIssue: undefined,
      accessPending: false,
      issueContextLoading: false,
      missingIssueRecord: true,
      contextReady: false,
    })).toBe('loading')
    expect(issueDetailPane({
      selectedIssue: undefined,
      accessPending: false,
      issueContextLoading: false,
      missingIssueRecord: false,
      contextReady: true,
    })).toBe('not-found')
  })
})
