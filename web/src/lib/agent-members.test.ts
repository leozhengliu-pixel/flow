import { describe, expect, it } from 'vitest'
import { CLEAR_ASSIGNEE_AND_AGENT_ID, NO_AGENT_ID, assigneeUpdate } from './agent-members'

describe('assigneeUpdate', () => {
  it('assigns people and delegates agents', () => {
    expect(assigneeUpdate('usr_1')).toEqual({ assigneeId: 'usr_1' })
    expect(assigneeUpdate('')).toEqual({ assigneeId: '' })
    expect(assigneeUpdate('app_1')).toEqual({ delegateId: 'app_1' })
  })

  it('removes the agent alone or together with the assignee from a list row', () => {
    expect(assigneeUpdate(NO_AGENT_ID)).toEqual({ delegateId: '' })
    expect(assigneeUpdate(CLEAR_ASSIGNEE_AND_AGENT_ID)).toEqual({ assigneeId: '', delegateId: '' })
  })
})
