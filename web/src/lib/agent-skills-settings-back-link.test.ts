import { describe, expect, it } from 'vitest'
import { agentSkillsSettingsBackLink } from './agent-skills-settings-back-link'

describe('agentSkillsSettingsBackLink', () => {
  it('points at personal agents settings', () => {
    const link = agentSkillsSettingsBackLink('acme')
    expect(link.page).toBe('agents')
    expect(link.href).toContain('/acme/settings/')
    expect(link.label).toBe('Agent personalization')
  })
})
