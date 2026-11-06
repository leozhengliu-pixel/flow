import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, MemoryRouter, RouterProvider } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ createAgentSkill: vi.fn(), updateAgentSkill: vi.fn(), deleteAgentSkill: vi.fn(), updateStructuredTeamSettings: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
const dialogs = vi.hoisted(() => ({ confirmAction: vi.fn() }))
vi.mock('@/components/ui/action-dialog-service', async importOriginal => ({ ...(await importOriginal<typeof import('@/components/ui/action-dialog-service')>()), ...dialogs }))
const toast = vi.hoisted(() => ({ warning: vi.fn(), error: vi.fn(), success: vi.fn() }))
vi.mock('@/lib/toast', () => ({ toast }))

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, PersonalAgentSkill } from '@/types/flow'
import { AgentSkillEditor } from './agent-skill-page'
import { AgentSkillsList } from './agent-skills-list'

const markdown = '# Release notes\n\nWrite **user-facing** notes.\n\n- Group by area\n- Keep lines under `80` chars\n\nSee [the log](https://example.com/log).'
const now = new Date().toISOString()
function skill(overrides: Partial<PersonalAgentSkill>): PersonalAgentSkill {
  return { id: 'skill-1', userId: 'user-1', name: 'Release notes', instructions: markdown, createdAt: now, updatedAt: now, ...overrides }
}
const skills = [
  skill({}),
  skill({ id: 'skill-2', name: '中文技能', instructions: '## 代码审查\n\n请**逐条检查**。' }),
  skill({ id: 'skill-3', name: 'Long line', instructions: 'x'.repeat(400), updatedAt: new Date(Date.now() + 3 * 60 * 60 * 1000).toISOString() }),
]
const data = (agentSkills: PersonalAgentSkill[]): BootstrapData => makeBootstrap({ agentSkills })

function renderList(agentSkills: PersonalAgentSkill[], locale: 'en' | 'zh-CN' = 'en', onReload = vi.fn().mockResolvedValue(undefined)) {
  localStorage.setItem('flow:locale', locale)
  render(<MemoryRouter><I18nProvider><AgentSkillsList data={data(agentSkills)} onReload={onReload} /></I18nProvider></MemoryRouter>)
  return { onReload }
}

function renderPage(agentSkills: PersonalAgentSkill[], mode: 'new' | 'edit', id?: string) {
  localStorage.setItem('flow:locale', 'en')
  const onCancel = vi.fn()
  const onReload = vi.fn().mockResolvedValue(undefined)
  const router = createMemoryRouter([{ path: '*', element: <I18nProvider><AgentSkillEditor data={data(agentSkills)} id={id} mode={mode} onCancel={onCancel} onReload={onReload} /></I18nProvider> }])
  render(<RouterProvider router={router} />)
  return { onCancel, onReload }
}

beforeEach(() => {
  for (const fn of [...Object.values(api), ...Object.values(toast)]) fn.mockReset()
})

describe('skills list', () => {
  it('shows each skill name with when it changed and never the raw Markdown instructions', () => {
    renderList(skills)
    expect(screen.getByRole('heading', { name: '3 skills' })).toBeInTheDocument()
    const rows = screen.getAllByRole('listitem')
    expect(rows).toHaveLength(3)
    expect(within(rows[0]).getByText('Release notes')).toBeInTheDocument()
    expect(within(rows[0]).getByText(/^Created /)).toBeInTheDocument()
    // A skill edited after it was created reads "Updated".
    expect(within(rows[2]).getByText(/^Updated /)).toBeInTheDocument()
    for (const marker of ['# Release', '**user-facing**', '`80`', '](https://', 'xxxxxxxx']) expect(document.body.textContent).not.toContain(marker)
  })

  it('links every row to the skill page and the plus button to the create page', () => {
    renderList(skills)
    expect(screen.getByRole('link', { name: 'Release notes' })).toHaveAttribute('href', '/workspace/settings/skill/skill-1')
    expect(screen.getByRole('link', { name: 'Create skill' })).toHaveAttribute('href', '/workspace/settings/skill/new')
  })

  it('shows the empty state with the create button', () => {
    renderList([])
    expect(screen.getByRole('heading', { name: 'No skills created' })).toBeInTheDocument()
    expect(screen.queryByRole('list')).toBeNull()
    expect(screen.getByRole('link', { name: 'Create skill' })).toBeInTheDocument()
  })

  it('translates the count, timestamps and menu into Chinese', async () => {
    renderList(skills.slice(0, 2), 'zh-CN')
    expect(await screen.findByRole('heading', { name: '2 项技能' })).toBeInTheDocument()
    expect(screen.getAllByText(/^已创建 /).length).toBe(2)
    await userEvent.click(screen.getAllByRole('button', { name: '打开菜单' })[0])
    expect(await screen.findByRole('menuitem', { name: '在聊天中使用' })).toBeInTheDocument()
  })

  it('offers Edit, Use in chat and Delete in the row menu, and deletes after confirming', async () => {
    api.deleteAgentSkill.mockResolvedValue(undefined)
    const { onReload } = renderList(skills.slice(0, 1))
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    expect(await screen.findByRole('menuitem', { name: 'Edit' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Use in chat' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('menuitem', { name: 'Delete' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Delete the skill "Release notes"?')).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.deleteAgentSkill).toHaveBeenCalledWith('skill-1'))
    expect(onReload).toHaveBeenCalled()
  })

  it('moves a skill to a team from the row menu: a team skill with the same text, then the personal copy goes', async () => {
    api.deleteAgentSkill.mockResolvedValue(undefined)
    api.updateStructuredTeamSettings.mockResolvedValue({})
    const { onReload } = renderList(skills.slice(0, 1))
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Move to team' }))
    const team = data([]).teams[0]
    await userEvent.click(await screen.findByRole('menuitem', { name: new RegExp(team.name) }))
    await waitFor(() => expect(api.deleteAgentSkill).toHaveBeenCalledWith('skill-1'))
    expect(api.updateStructuredTeamSettings).toHaveBeenCalledWith(team.id, { agentSkills: [expect.objectContaining({ name: 'Release notes', instructions: markdown, enabled: true })] })
    expect(onReload).toHaveBeenCalled()
  })

  it('keeps the skill when the delete confirmation is cancelled', async () => {
    renderList(skills.slice(0, 1))
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }))
    expect(api.deleteAgentSkill).not.toHaveBeenCalled()
  })
})

describe('skill page', () => {
  it('renders the instructions as formatted Markdown in the editor', async () => {
    renderPage(skills, 'edit', 'skill-1')
    const editor = await screen.findByRole('textbox', { name: 'Skill instructions' })
    expect(editor).toHaveAttribute('contenteditable', 'true')
    await waitFor(() => expect(editor.querySelector('h1')).toHaveTextContent('Release notes'))
    expect(editor.querySelector('strong')).toHaveTextContent('user-facing')
    expect(editor.querySelectorAll('li')).toHaveLength(2)
    expect(editor.querySelector('code')).toHaveTextContent('80')
    expect(editor.querySelector('a')).toHaveAttribute('href', 'https://example.com/log')
    // No Markdown punctuation is left in the visible text.
    expect(editor.textContent).not.toContain('**')
    expect(editor.textContent).not.toContain('# ')
    expect(screen.getByRole('textbox', { name: 'Skill name' })).toHaveValue('Release notes')
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument()
  })

  it('renders CJK Markdown too', async () => {
    renderPage(skills, 'edit', 'skill-2')
    const editor = await screen.findByRole('textbox', { name: 'Skill instructions' })
    await waitFor(() => expect(editor.querySelector('h2')).toHaveTextContent('代码审查'))
    expect(editor.querySelector('strong')).toHaveTextContent('逐条检查')
  })

  it('saves a renamed skill with its Markdown line breaks intact', async () => {
    api.updateAgentSkill.mockResolvedValue(skills[0])
    const { onCancel, onReload } = renderPage(skills, 'edit', 'skill-1')
    await screen.findByRole('textbox', { name: 'Skill instructions' })
    fireEvent.change(screen.getByRole('textbox', { name: 'Skill name' }), { target: { value: 'Release notes v2' } })
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateAgentSkill).toHaveBeenCalledWith('skill-1', { name: 'Release notes v2', instructions: markdown }))
    expect(onReload).toHaveBeenCalled()
    expect(onCancel).toHaveBeenCalled()
  })

  it('warns instead of saving when nothing changed or a field is empty', async () => {
    renderPage(skills, 'edit', 'skill-1')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(toast.warning).toHaveBeenLastCalledWith('No changes to save')
    fireEvent.change(screen.getByRole('textbox', { name: 'Skill name' }), { target: { value: '' } })
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(toast.warning).toHaveBeenLastCalledWith('Title cannot be empty')
    expect(api.updateAgentSkill).not.toHaveBeenCalled()
  })

  it('creates a skill from an empty page and asks for instructions first', async () => {
    renderPage([], 'new')
    expect(screen.getByRole('textbox', { name: 'Skill name' })).toHaveFocus()
    fireEvent.change(screen.getByRole('textbox', { name: 'Skill name' }), { target: { value: 'Triage' } })
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(toast.warning).toHaveBeenLastCalledWith('Instructions cannot be empty')
    expect(api.createAgentSkill).not.toHaveBeenCalled()
  })

  it('asks before discarding edits and leaves straight away when nothing changed', async () => {
    const { onCancel } = renderPage(skills, 'edit', 'skill-1')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onCancel).toHaveBeenCalledTimes(1)
    fireEvent.change(screen.getByRole('textbox', { name: 'Skill name' }), { target: { value: 'Changed' } })
    // The reference app's "Discard changes?" dialog: a title with Cancel and a red Confirm, no description.
    dialogs.confirmAction.mockResolvedValueOnce(false)
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(dialogs.confirmAction).toHaveBeenCalledWith('Discard changes?', { confirmLabel: 'Confirm', danger: true })
    expect(onCancel).toHaveBeenCalledTimes(1)
    dialogs.confirmAction.mockResolvedValueOnce(true)
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(onCancel).toHaveBeenCalledTimes(2))
  })

  it('shows the skill menu without Edit on the skill page', async () => {
    renderPage(skills, 'edit', 'skill-1')
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    expect(await screen.findByRole('menuitem', { name: 'Use in chat' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Edit' })).toBeNull()
  })
})
