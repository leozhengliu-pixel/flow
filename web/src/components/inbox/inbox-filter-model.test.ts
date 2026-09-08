import { describe, expect, it } from 'vitest'
import { inboxActorOptions, inboxNotificationCategory, inboxValueMenuHeight, matchesInboxFilter, partitionInboxOptions, type InboxFilterTarget } from './inbox-filter-model'
import type { InboxFilterCondition } from './inbox-filter-types'
import type { User } from '@/types/flow'

const review: InboxFilterTarget = { issueId:'', notificationType:'review', actorId:'bot', initiativeIds:[], issuePriority:0, issueStatusType:'started', reviewStatus:'open' }
const filter = (property: InboxFilterCondition['property'], value: string, operator: InboxFilterCondition['operator'] = 'is'): InboxFilterCondition => ({id:'test',property,operator,values:[{value,valueLabel:value}]})

describe('Inbox filter model', () => {
  it('does not treat projection defaults on non-issue notifications as issue attributes', () => {
    for (const [property,value] of [['issuePriority','0'],['issueStatusType','started'],['project','__none__']] as const) {
      expect(matchesInboxFilter(review,filter(property,value))).toBe(false)
      expect(matchesInboxFilter(review,filter(property,value,'isNot'))).toBe(true)
      expect(matchesInboxFilter({...review,issueId:'issue'},filter(property,value))).toBe(true)
    }
    expect(matchesInboxFilter(review,filter('reviewStatus','open'))).toBe(true)
  })

  it('partitions only when counts are available and economical, keeping selected zero-count options', () => {
    const options = Array.from({length:16},(_,i)=>({id:String(i),label:String(i),count:i===10?35:0}))
    expect(partitionInboxOptions(options,35,['2']).matching.map(x=>x.id)).toEqual(['2','10'])
    expect(partitionInboxOptions(options,0).unmatched).toEqual([])
    expect(partitionInboxOptions(options,626).unmatched).toEqual([])
    expect(partitionInboxOptions(options.slice(0,9),35).unmatched).toEqual([])
    expect(partitionInboxOptions(options.map(({id,label})=>({id,label})),35).unmatched).toEqual([])
  })

  it('clips a long menu on a partial row and leaves short menus at natural height', () => {
    expect(inboxValueMenuHeight([32,12,...Array(16).fill(32)],true,702,91)).toBe(554)
    expect(inboxValueMenuHeight([32,12,32],false,702,91)).toBe(89)
    expect(inboxValueMenuHeight(Array(5).fill(32),true,702,219)).toBe(209.5)
  })

  it('uses actual actors including bots and preserves enterprise user search metadata', () => {
    const users = [{id:'person',userId:'EMP-1042',displayName:'Alex'}] as User[]
    const options = inboxActorOptions([{actorId:'bot',actor:'Build bot'},{actorId:'person',actor:'Alex'},{actorId:'bot',actor:'Build bot'}],users)
    expect(options.map(x=>[x.label,x.count])).toEqual([['Alex',1],['Build bot',2]])
    expect(options[0].keywords).toContain('EMP-1042')
    expect(options[1].person?.id).toBe('bot')
  })

  it('normalizes notification categories before type-specific fallback', () => {
    expect(inboxNotificationCategory({category:'mentions',type:'issueMention'})).toBe('mention')
    expect(inboxNotificationCategory({category:'statusChanges',type:'issueStatusChanged'},'status')).toBe('status')
  })
})
