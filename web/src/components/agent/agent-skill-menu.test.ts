import { describe, expect, it } from 'vitest'
import type { PersonalAgentSkill } from '@/types/flow'
import { orderedAgentSkills, skillIdsInText, skillMatches, skillSummary, splitSkillTokens } from './agent-skill-menu'

const skill = (id: string, name: string, createdAt: string, instructions = ''): PersonalAgentSkill => ({ id, userId: 'u', name, instructions, createdAt, updatedAt: createdAt })
const release = skill('r', 'Release notes', '2026-01-01T00:00:00Z')
const triage = skill('t', 'Triage', '2026-02-01T00:00:00Z')

describe('agent skill menu model', () => {
  it('lists the newest skill first and leaves the source alone', () => {
    const source = [release, triage]
    expect(orderedAgentSkills(source).map(item => item.id)).toEqual(['t', 'r'])
    expect(source.map(item => item.id)).toEqual(['r', 't'])
    expect(orderedAgentSkills(undefined)).toEqual([])
  })

  it('matches a query as characters in order, ignoring case', () => {
    expect(skillMatches(release, 'rn')).toBe(true)
    expect(skillMatches(release, 'NOTES')).toBe(true)
    expect(skillMatches(release, 'ner')).toBe(false)
    expect(skillMatches(release, '')).toBe(true)
  })

  it('summarises a skill by the first non-empty line of its instructions', () => {
    expect(skillSummary({ instructions: '\n  Reply with parity.\nMore.' })).toBe('Reply with parity.')
    expect(skillSummary({ instructions: '' })).toBe('')
  })

  it('finds the skills a text names with "/Name" at word boundaries, longest name first', () => {
    const long = skill('l', 'Release notes weekly', '2026-03-01T00:00:00Z')
    expect(skillIdsInText('Run /Release notes weekly now', [release, long])).toEqual(['l'])
    expect(skillIdsInText('/Triage and /Release notes', [release, triage])).toEqual(['t', 'r'])
    expect(skillIdsInText('see http://x/Triage and a/Triage', [triage])).toEqual([])
    expect(skillIdsInText('/Triaged', [triage])).toEqual([])
  })

  it('splits a text into plain parts and skills', () => {
    expect(splitSkillTokens('Use /Triage, then /Triage', [triage])).toEqual(['Use /Triage, then ', triage])
    expect(splitSkillTokens('/Triage x', [triage])).toEqual([triage, ' x'])
  })
})
