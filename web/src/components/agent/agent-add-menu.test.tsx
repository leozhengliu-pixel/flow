import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { PersonalAgentSkill } from '@/types/flow'
import { AgentAddMenu } from './agent-add-menu'

const skill = (id: string, name: string, createdAt: string): PersonalAgentSkill => ({ id, userId: 'user-1', name, instructions: `${name} instructions`, createdAt, updatedAt: createdAt })

function renderMenu(skills: PersonalAgentSkill[], overrides: Partial<Parameters<typeof AgentAddMenu>[0]> = {}) {
  const props = { onFile: vi.fn(), onSkill: vi.fn(), onCreateSkill: vi.fn(), skills, ...overrides }
  render(<I18nProvider><AgentAddMenu {...props}/></I18nProvider>)
  return props
}

describe('AgentAddMenu', () => {
  it('lists File and Skill, and the skills newest first without a search field up to three', async () => {
    const user = userEvent.setup()
    const props = renderMenu([skill('a', 'Older', '2026-01-01T00:00:00Z'), skill('b', 'Newer', '2026-02-01T00:00:00Z')])
    await user.click(screen.getByRole('button', { name: 'Add files and more' }))
    const [file, skillRow] = screen.getAllByRole('menuitem')
    expect(file).toHaveTextContent(/^File.*U$/)
    expect(skillRow).toHaveTextContent('Skill▶')
    await user.click(screen.getByRole('menuitem', { name: /^Skill/ }))
    expect(screen.getAllByRole('menuitem').slice(2).map(item => item.textContent)).toEqual(['Newer', 'Older'])
    expect(screen.queryByPlaceholderText('Search skills…')).not.toBeInTheDocument()
    await user.click(screen.getByRole('menuitem', { name: 'Older' }))
    expect(props.onSkill).toHaveBeenCalledWith(expect.objectContaining({ id: 'a' }))
  })

  it('offers Create skill only while there are no skills', async () => {
    const user = userEvent.setup()
    const props = renderMenu([])
    await user.click(screen.getByRole('button', { name: 'Add files and more' }))
    await user.click(screen.getByRole('menuitem', { name: /^Skill/ }))
    await user.click(screen.getByRole('menuitem', { name: 'Create skill' }))
    expect(props.onCreateSkill).toHaveBeenCalledTimes(1)
  })

  it('shows a search field once there are more than three skills and filters by characters in order', async () => {
    const user = userEvent.setup()
    renderMenu([skill('1', 'Alpha', '2026-01-01T00:00:00Z'), skill('2', 'Beta', '2026-01-02T00:00:00Z'), skill('3', 'Gamma', '2026-01-03T00:00:00Z'), skill('4', 'Delta', '2026-01-04T00:00:00Z')])
    await user.click(screen.getByRole('button', { name: 'Add files and more' }))
    await user.click(screen.getByRole('menuitem', { name: /^Skill/ }))
    await user.type(screen.getByPlaceholderText('Search skills…'), 'ta')
    expect(screen.getAllByRole('menuitem').slice(2).map(item => item.textContent)).toEqual(['Delta', 'Beta'])
  })
})
