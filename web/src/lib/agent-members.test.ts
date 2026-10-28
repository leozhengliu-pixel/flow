import { describe, expect, it } from 'vitest'
import { CLEAR_ASSIGNEE_AND_AGENT_ID, NO_AGENT_ID, assigneeMenuLayout, assigneeUpdate } from './agent-members'

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

describe("assigneeMenuLayout (one issue's assignee menu)", () => {
  const me = { id: 'usr_me' }, ann = { id: 'usr_ann' }, bo = { id: 'usr_bo' }
  const flow = { id: 'app_flow', app: true, builtinAgent: true }, coder = { id: 'app_coder', app: true }, helper = { id: 'app_helper', app: true }
  const people = [me, ann, bo, flow, coder, helper]
  const ids = (list: { id: string }[]) => list.map(person => person.id)

  it('not delegated: assignee, viewer and built-in agent on top, then other agents and team members', () => {
    const layout = assigneeMenuLayout(people, { assigneeId: ann.id, viewerId: me.id })
    expect(ids(layout.top)).toEqual([ann.id, me.id, flow.id])
    expect(ids(layout.otherAgents)).toEqual([coder.id, helper.id])
    expect(ids(layout.members)).toEqual([bo.id])
    expect(layout.delegateAnchorId).toBeUndefined()
    expect(layout.searchOnly).toEqual([])
  })

  it('lists the viewer once when they are the assignee, and only the viewer when nobody is assigned', () => {
    expect(ids(assigneeMenuLayout(people, { assigneeId: me.id, viewerId: me.id }).top)).toEqual([me.id, flow.id])
    expect(ids(assigneeMenuLayout(people, { viewerId: me.id }).top)).toEqual([me.id, flow.id])
  })

  it('delegated: hangs the agent under the assignee, drops every agent from the list and keeps them searchable', () => {
    const layout = assigneeMenuLayout(people, { assigneeId: ann.id, delegateId: coder.id, viewerId: me.id })
    expect(ids(layout.top)).toEqual([ann.id, me.id])
    expect(layout.delegateAnchorId).toBe(ann.id)
    expect(layout.otherAgents).toEqual([])
    expect(ids(layout.searchOnly)).toEqual([flow.id, helper.id])
    expect(ids(layout.members)).toEqual([bo.id])
  })

  it('delegated with nobody assigned: the agent hangs under the viewer, or has no anchor without one', () => {
    expect(assigneeMenuLayout(people, { delegateId: flow.id, viewerId: me.id }).delegateAnchorId).toBe(me.id)
    expect(assigneeMenuLayout(people, { delegateId: flow.id }).delegateAnchorId).toBeUndefined()
  })
})
