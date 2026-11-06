import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { BootstrapData, PersonalAgentSkill } from '@/types/flow'

vi.mock('@/lib/api', () => ({ listIssueRecords: vi.fn(() => new Promise(() => undefined)) }))

import { AgentMentionInput, type AgentMention, type AgentSkillMention } from './agent-mention-input'

const skill = (id: string, name: string, createdAt: string, instructions = `${name} does its job.\nMore detail.`): PersonalAgentSkill => ({ id, userId: 'user-1', name, instructions, createdAt, updatedAt: createdAt })
// Newest first: Gamma, Delta, Beta, Alpha, Epsilon is the oldest.
const skills = [
  skill('alpha', 'Alpha', '2026-01-04T00:00:00Z'),
  skill('beta', 'Beta', '2026-01-05T00:00:00Z'),
  skill('gamma', 'Gamma', '2026-01-07T00:00:00Z'),
  skill('delta', 'Delta', '2026-01-06T00:00:00Z'),
  skill('epsilon', 'Epsilon', '2026-01-01T00:00:00Z'),
]
const data = { workspace: { urlKey: 'ws' }, users: [], issues: [], projects: [], documents: [], initiatives: [], agentSkills: skills } as unknown as BootstrapData

function renderInput(overrides: Partial<Parameters<typeof AgentMentionInput>[0]> = {}) {
  const onChange = vi.fn<(value: string, mentions: AgentMention[], skills: AgentSkillMention[]) => void>()
  const onSubmit = vi.fn()
  render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onChange={onChange} onSubmit={onSubmit} placeholder="Ask" value="" {...overrides}/></I18nProvider>)
  return { onChange, onSubmit, editor: screen.getByRole('textbox', { name: 'Ask' }) }
}
const rows = () => within(screen.getByRole('listbox', { name: 'Insert block' })).getAllByRole('option').map(row => row.textContent)

describe('the composer "/" menu', () => {
  it('opens on "/" with a Skills section of the newest four, then the commands the composer can run', async () => {
    const user = userEvent.setup()
    const { editor } = renderInput({ onUploadFile: vi.fn() })
    await user.click(editor)
    await user.keyboard('/')
    const menu = await screen.findByRole('listbox', { name: 'Insert block' })
    expect(menu).toHaveTextContent('Skills')
    expect(rows().slice(0, 4)).toEqual(['Gamma', 'Delta', 'Beta', 'Alpha'])
    expect(rows().slice(4)).toEqual([expect.stringContaining('Insert media…'), expect.stringContaining('Attach files…')])
    expect(within(menu).getAllByRole('separator')).toHaveLength(1)
    expect(within(menu).getAllByRole('option')[0]).toHaveAttribute('aria-selected', 'true')
  })

  it('opens at the start of the text or after a space, not inside a word', async () => {
    const user = userEvent.setup()
    const { editor } = renderInput()
    await user.click(editor)
    await user.keyboard('a/')
    expect(screen.queryByRole('listbox', { name: 'Insert block' })).not.toBeInTheDocument()
    await user.keyboard('{Backspace}{Backspace} /')
    expect(await screen.findByRole('listbox', { name: 'Insert block' })).toBeVisible()
  })

  it('searches every skill by the characters typed, in order, and says so when nothing matches', async () => {
    const user = userEvent.setup()
    const { editor } = renderInput()
    await user.click(editor)
    await user.keyboard('/ta')
    await screen.findByRole('listbox', { name: 'Insert block' })
    // "ta" in order: Delta and Beta; the older Epsilon is searched too (the list of four is only the unfiltered view).
    expect(rows()).toEqual(['Delta', 'Beta'])
    await user.keyboard('zz')
    expect(await screen.findByText('No results found')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByRole('listbox', { name: 'Insert block' })).not.toBeInTheDocument()
    expect(editor).toHaveTextContent('/tazz')
  })

  it('moves with the arrow keys, wrapping around, and closes on Escape keeping the text', async () => {
    const user = userEvent.setup()
    const { editor } = renderInput()
    await user.click(editor)
    await user.keyboard('/')
    await screen.findByRole('listbox', { name: 'Insert block' })
    const selected = () => within(screen.getByRole('listbox', { name: 'Insert block' })).getAllByRole('option').findIndex(row => row.getAttribute('aria-selected') === 'true')
    await user.keyboard('{ArrowDown}')
    expect(selected()).toBe(1)
    await user.keyboard('{ArrowUp}{ArrowUp}')
    expect(selected()).toBe(3)
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('listbox', { name: 'Insert block' })).not.toBeInTheDocument()
    expect(editor).toHaveTextContent('/')
    // The same token stays closed while it is typed on.
    await user.keyboard('x')
    expect(screen.queryByRole('listbox', { name: 'Insert block' })).not.toBeInTheDocument()
  })

  it('shows the focused skill in a hover card: its link, what it does and whose it is', async () => {
    const user = userEvent.setup()
    const { editor } = renderInput()
    await user.click(editor)
    await user.keyboard('/')
    const card = await waitFor(() => {
      const found = document.querySelector<HTMLElement>('[data-agent-slash-card]')
      if (!found) throw new Error('no card')
      return found
    })
    expect(within(card).getByRole('link', { name: /Gamma/ })).toHaveAttribute('href', '/ws/settings/skill/gamma')
    expect(card).toHaveTextContent('Gamma does its job.')
    expect(card).not.toHaveTextContent('More detail.')
    expect(card).toHaveTextContent('Personal')
    await user.keyboard('{ArrowDown}')
    expect(document.querySelector('[data-agent-slash-card]')).toHaveTextContent('Delta does its job.')
  })

  it('turns a chosen skill into a chip that reads "/Name" in the message and is reported as a skill', async () => {
    const user = userEvent.setup()
    const { editor, onChange } = renderInput()
    await user.click(editor)
    await user.keyboard('/del')
    await screen.findByRole('listbox', { name: 'Insert block' })
    await user.keyboard('{Tab}')
    expect(screen.queryByRole('listbox', { name: 'Insert block' })).not.toBeInTheDocument()
    const chip = editor.querySelector<HTMLElement>('[data-mention-type="skill"]')!
    expect(chip).toHaveAttribute('data-mention-id', 'delta')
    expect(chip).toHaveTextContent('Delta')
    expect(editor).not.toHaveTextContent('/del')
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith('/Delta ', [], [{ id: 'delta', label: 'Delta' }]))
  })

  it('chooses with a click too, and Enter in an open menu chooses rather than sends', async () => {
    const user = userEvent.setup()
    const { editor, onChange, onSubmit } = renderInput()
    await user.click(editor)
    await user.keyboard('/')
    await user.click(await screen.findByRole('option', { name: 'Beta' }))
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith('/Beta ', [], [{ id: 'beta', label: 'Beta' }]))
    await user.keyboard('/')
    await screen.findByRole('listbox', { name: 'Insert block' })
    await user.keyboard('{Enter}')
    expect(onSubmit).not.toHaveBeenCalled()
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(expect.stringContaining('/Gamma'), [], expect.arrayContaining([{ id: 'beta', label: 'Beta' }, { id: 'gamma', label: 'Gamma' }])))
  })

  it('puts a chip in at the caret for the "+" menu and does not repeat a skill already there', async () => {
    const insertSkillRef = { current: null as ((skill: PersonalAgentSkill) => void) | null }
    const { editor, onChange } = renderInput({ insertSkillRef })
    insertSkillRef.current!(skills[0])
    insertSkillRef.current!(skills[0])
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(expect.stringContaining('/Alpha'), [], [{ id: 'alpha', label: 'Alpha' }]))
    expect(editor.querySelectorAll('[data-mention-type="skill"]')).toHaveLength(1)
  })

  it('turns "/Name" in a restored draft back into the skill\'s chip', async () => {
    const { editor, onChange } = renderInput({ value: 'Use /Beta please' })
    await waitFor(() => expect(editor.querySelector('[data-mention-type="skill"]')).toHaveAttribute('data-mention-id', 'beta'))
    expect(editor).toHaveTextContent('Use Beta please')
    expect(onChange).not.toHaveBeenCalledWith(expect.anything(), expect.anything(), [])
  })

  it('lists Insert media and Attach files only when the composer can upload', async () => {
    const user = userEvent.setup()
    const { editor } = renderInput()
    await user.click(editor)
    await user.keyboard('/')
    await screen.findByRole('listbox', { name: 'Insert block' })
    expect(rows()).toEqual(['Gamma', 'Delta', 'Beta', 'Alpha'])
  })
})
