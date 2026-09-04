import { describe, expect, it } from 'vitest'
import { parseProjectAgentDraft, projectAgentPrompt } from './project-agent-draft'

describe('project agent draft parsing', () => {
  it('parses a fenced JSON draft and normalizes dates', () => {
    expect(parseProjectAgentDraft('Here is a draft:\n```json\n{"name":"Launch","startDate":"2027/5/2","milestones":["Beta","GA"]}\n```')).toEqual({
      name: 'Launch',
      startDate: '2027-05-02',
      milestones: ['Beta', 'GA'],
    })
  })

  it('accepts labelled fallback text', () => {
    expect(parseProjectAgentDraft('- Project name: Website refresh\nSummary: Improve conversion\nTarget date: 2027-06-30\nMilestones: Research; Launch')).toEqual({
      name: 'Website refresh',
      summary: 'Improve conversion',
      targetDate: '2027-06-30',
      milestones: ['Research', 'Launch'],
    })
  })

  it('builds a structured prompt without losing the request', () => {
    const prompt = projectAgentPrompt('Build a mobile onboarding project')
    expect(prompt).toContain('startDate')
    expect(prompt).toContain('Build a mobile onboarding project')
  })

  it('normalizes people and project relation suggestions', () => {
    expect(parseProjectAgentDraft('```json\n{"team":"Platform","lead":"Zheng Liu","members":["Hui"],"dependencies":"Core; API"}\n```')).toEqual({
      team: 'Platform',
      lead: 'Zheng Liu',
      members: ['Hui'],
      dependencies: ['Core', 'API'],
    })
  })
})
