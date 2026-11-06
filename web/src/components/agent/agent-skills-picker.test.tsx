import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { PersonalAgentSkill } from '@/types/flow'
import { AgentSkillsPicker } from './agent-skills-picker'

const now = new Date().toISOString()
const skill: PersonalAgentSkill = { id: 'skill-1', userId: 'user-1', name: 'Release notes', instructions: 'Write notes.', createdAt: now, updatedAt: now }

function renderPicker(agentSkills: PersonalAgentSkill[]) {
  localStorage.setItem('flow:locale', 'en')
  const onNavigate = vi.fn()
  render(<I18nProvider><AgentSkillsPicker data={makeBootstrap({ agentSkills })} selectedIds={[]} onChange={vi.fn()} onNavigate={onNavigate}/></I18nProvider>)
  return { onNavigate }
}

describe('composer skills menu', () => {
  it('lists only the skills while there are some, like the reference skill menu', async () => {
    renderPicker([skill])
    await userEvent.click(screen.getByRole('button', { name: 'Skills' }))
    expect(await screen.findByRole('option', { name: 'Release notes' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Create skill/ })).not.toBeInTheDocument()
  })

  it('offers "Create skill" while there are no skills and opens the new skill page', async () => {
    const { onNavigate } = renderPicker([])
    await userEvent.click(screen.getByRole('button', { name: 'Skills' }))
    await userEvent.click(await screen.findByRole('option', { name: /Create skill/ }))
    expect(onNavigate).toHaveBeenLastCalledWith(expect.stringMatching(/\/settings\/skill\/new$/))
  })
})
