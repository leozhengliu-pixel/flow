import { describe, expect, it } from 'vitest'
import { issueDeleteConfirmation } from './issue-delete-confirmation'

const t = (value: string) => value

describe('issueDeleteConfirmation', () => {
  it("uses the reference app's title, naming the sub-issues deleted with the issue", () => {
    expect(issueDeleteConfirmation({ identifier: 'ENG-1', title: 'Fix login', subIssueIds: [] }, t).title).toBe('Delete "ENG-1 – Fix login"?')
    expect(issueDeleteConfirmation({ identifier: 'ENG-1', title: 'Fix login', subIssueIds: ['a'] }, t).title).toBe('Delete "ENG-1 – Fix login" and 1 sub-issue?')
    expect(issueDeleteConfirmation({ identifier: 'ENG-1', title: 'Fix login', subIssueIds: ['a', 'b'] }, t).title).toBe('Delete "ENG-1 – Fix login" and 2 sub-issues?')
  })

  it('says the issue stays restorable for 30 days', () => {
    expect(issueDeleteConfirmation({ identifier: 'ENG-1', title: 'Fix login', subIssueIds: [] }, t)).toMatchObject({ description: expect.stringContaining('30 days'), confirmLabel: 'Delete' })
  })
})
