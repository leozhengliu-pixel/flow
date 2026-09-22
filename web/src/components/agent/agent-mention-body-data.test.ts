import { describe, expect, it } from 'vitest'
import { AgentMentionBodyData } from './agent-mention-body-data'

describe('LS-0031 AgentMentionBodyData', () => {
  it('builds entityMention docs', () => {
    const doc = AgentMentionBodyData.build({ id: 'user-1', label: 'Ada' })
    expect(doc.content[0]?.content[0]).toEqual({
      type: 'entityMention',
      attrs: { id: 'user-1', label: 'Ada', href: '' },
    })
  })

  it('builds skill presets', () => {
    const doc = AgentMentionBodyData.buildPreset('summarize')
    expect(doc.content[0]?.content[0]).toMatchObject({
      type: 'entityMention',
      attrs: { id: 'skill-preset:summarize', label: 'Summarize' },
    })
    expect(AgentMentionBodyData.isSkillPresetId('skill-preset:summarize')).toBe(true)
    expect(AgentMentionBodyData.presetIdFromMention('skill-preset:summarize')).toBe('summarize')
  })

  it('flattens to plain text', () => {
    const doc = AgentMentionBodyData.buildSkill({ id: 'skill-9', name: 'Triage inbox' })
    expect(AgentMentionBodyData.toPlainText(doc, 'please')).toBe('@Triage inbox please')
  })
})
