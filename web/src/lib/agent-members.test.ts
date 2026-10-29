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

  it('sorts remaining members by display name, preserves equal-name order and leaves input and top rows alone', () => {
    const zoe = { id: 'zoe', displayName: 'Zoë', name: 'A' }
    const accent = { id: 'accent', displayName: 'Áda' }, ada = { id: 'ada', displayName: 'ada' }
    const fallback = { id: 'fallback', displayName: '', name: 'Bo' }
    const candidates = [zoe, me, accent, ann, fallback, ada, flow, coder]
    const original = [...candidates]
    for (const delegateId of [undefined, coder.id]) {
      const layout = assigneeMenuLayout(candidates, { assigneeId: ann.id, viewerId: me.id, delegateId, locale: 'en-US' })
      expect(ids(layout.members)).toEqual([accent.id, ada.id, fallback.id, zoe.id])
      expect(ids(layout.top)).toEqual(delegateId ? [ann.id, me.id] : [ann.id, me.id, flow.id])
    }
    expect(candidates).toEqual(original)
  })

  it('orders Chinese display names by pinyin and excludes the viewer and current assignee', () => {
    const members = [{ id: 'zhang', displayName: '张三' }, { id: 'wang', displayName: '王五' }, { id: 'li', displayName: '李四' }]
    const layout = assigneeMenuLayout([members[0], me, members[1], ann, members[2]], { assigneeId: ann.id, viewerId: me.id, locale: 'zh-CN' })
    expect(ids(layout.members)).toEqual(['li', 'wang', 'zhang'])
    expect(ids(layout.top)).toEqual([ann.id, me.id])
  })
})
