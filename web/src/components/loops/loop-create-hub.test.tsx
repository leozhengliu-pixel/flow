import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { Loop, LoopTemplate } from '@/types/flow'

const api = vi.hoisted(() => ({
  createLoop: vi.fn(),
  listLoopTemplates: vi.fn(),
  uploadLoopAttachment: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { LoopCreateHub } from './loop-create-hub'
import { resetLoopTemplateCache, takeLoopAgentAutostart } from './loop-data'

const templates: LoopTemplate[] = [
  { id: 'triage-agent', name: 'Triage agent', description: 'Reviews incoming issues, adds context, and routes each one to the right owner.', icon: 'Inbox', color: '#5e6ad2', triggerLabel: 'On triage', triggerIcon: 'triage', actionLabel: 'Route the issue to an owner', actionIcon: 'route', requiresTeam: true, levelHint: 'Triage loops must belong to a team', triggerType: 'issue', triggerConfig: { event: 'triage' }, instructions: 'Route it' },
  { id: 'weekly-wrap', name: 'Weekly wrap', description: "Summarizes the week's accomplishments.", icon: 'Calendar', color: '#4cb782', triggerLabel: 'Weekly', triggerIcon: 'weekly', actionLabel: 'Share the highlights', actionIcon: 'share', requiresTeam: false, triggerType: 'schedule', triggerConfig: { unit: 'week' }, instructions: 'Wrap up' },
]

function renderHub() {
  const onNavigate = vi.fn()
  const data = makeBootstrap()
  const view = render(<I18nProvider><LoopCreateHub data={data} onNavigate={onNavigate}/></I18nProvider>)
  return { onNavigate, view }
}

describe('LoopCreateHub', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    resetLoopTemplateCache()
    sessionStorage.clear()
    api.createLoop.mockReset().mockResolvedValue({ id: 'loop-new' } as Loop)
    api.listLoopTemplates.mockReset().mockResolvedValue(templates)
  })

  it('shows Linear’s hub copy and template cards with trigger → action footers', async () => {
    renderHub()
    expect(screen.getByRole('heading', { name: 'Create a new loop' })).toBeVisible()
    expect(screen.getByText('Automate manual work for your team and keep your process moving')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Start from scratch' })).toBeVisible()
    expect(screen.getByPlaceholderText('Ask Flow to build the loop for you')).toBeVisible()
    expect(screen.getByText('or pick a template')).toBeVisible()
    const card = await screen.findByRole('button', { name: /Triage agent/ })
    expect(card).toHaveTextContent('On triage')
    expect(card).toHaveTextContent('Route the issue to an owner')
    expect(screen.getByRole('button', { name: /Weekly wrap/ })).toHaveTextContent('Share the highlights')
  })

  it('asks where a team-only template goes, disables Workspace, then creates the draft and opens the editor', async () => {
    const user = userEvent.setup()
    const { onNavigate } = renderHub()
    await user.click(await screen.findByRole('button', { name: /Triage agent/ }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByPlaceholderText('Where should the loop be created?')).toBeVisible()
    const workspace = within(dialog).getByRole('option', { name: /Workspace/ })
    expect(workspace).toHaveAttribute('aria-disabled', 'true')
    expect(workspace).toHaveTextContent('Triage loops must belong to a team')
    await user.click(within(dialog).getByRole('option', { name: /Test team/ }))
    await waitFor(() => expect(api.createLoop).toHaveBeenCalledWith({ level: 'team', teamId: 'team-1', templateId: 'triage-agent', status: 'draft' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loops/new?draftId=loop-new')
    expect(takeLoopAgentAutostart('loop-new')).toBe(true)
  })

  it('starts from scratch in the workspace without the agent', async () => {
    const user = userEvent.setup()
    const { onNavigate } = renderHub()
    await user.click(screen.getByRole('button', { name: 'Start from scratch' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('option', { name: /Workspace/ }))
    await waitFor(() => expect(api.createLoop).toHaveBeenCalledWith({ level: 'workspace', status: 'draft' }))
    expect(onNavigate).toHaveBeenCalledWith('/workspace/loops/new?draftId=loop-new')
    expect(takeLoopAgentAutostart('loop-new')).toBe(false)
  })

  it('builds a loop from a prompt after choosing the location', async () => {
    const user = userEvent.setup()
    renderHub()
    await user.type(screen.getByPlaceholderText('Ask Flow to build the loop for you'), 'Post a weekly summary every Friday{Enter}')
    await user.click(within(await screen.findByRole('dialog')).getByRole('option', { name: /Test team/ }))
    await waitFor(() => expect(api.createLoop).toHaveBeenCalledWith({ level: 'team', teamId: 'team-1', prompt: 'Post a weekly summary every Friday', status: 'draft' }))
    expect(takeLoopAgentAutostart('loop-new')).toBe(true)
  })

  it('attaches files through the upload flow and sends attachmentIds with the prompt', async () => {
    const user = userEvent.setup()
    let finish: (value: unknown) => void = () => undefined
    api.uploadLoopAttachment.mockReset().mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const { view } = renderHub()
    const attach = screen.getByRole('button', { name: 'Attach images, files, or videos' })
    expect(attach).toBeEnabled()
    const file = new File(['hello'], 'brief.md', { type: 'text/markdown' })
    await user.upload(view.container.querySelector('input[type="file"]') as HTMLInputElement, file)
    expect(api.uploadLoopAttachment).toHaveBeenCalledWith(file)
    const chips = screen.getByRole('list', { name: 'Attachments' })
    expect(chips).toHaveTextContent('brief.md')
    await user.type(screen.getByPlaceholderText('Ask Flow to build the loop for you'), 'Summarize this brief weekly')
    // Sending waits for uploads to finish.
    expect(screen.getByRole('button', { name: 'Create loop from prompt' })).toBeDisabled()
    finish({ id: 'att-1', name: 'brief.md', contentType: 'text/markdown', size: 5, url: '/uploads/brief.md', createdAt: '2026-09-29T00:00:00Z' })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Create loop from prompt' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Create loop from prompt' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('option', { name: /Workspace/ }))
    await waitFor(() => expect(api.createLoop).toHaveBeenCalledWith({ level: 'workspace', prompt: 'Summarize this brief weekly', attachmentIds: ['att-1'], status: 'draft' }))
  })

  it('removes an attachment chip', async () => {
    const user = userEvent.setup()
    api.uploadLoopAttachment.mockReset().mockResolvedValue({ id: 'att-2', name: 'shot.png', contentType: 'image/png', size: 5, url: '/uploads/shot.png', createdAt: '2026-09-29T00:00:00Z' })
    const { view } = renderHub()
    await user.upload(view.container.querySelector('input[type="file"]') as HTMLInputElement, new File(['png'], 'shot.png', { type: 'image/png' }))
    await user.click(await screen.findByRole('button', { name: 'Remove attachment shot.png' }))
    expect(screen.queryByRole('list', { name: 'Attachments' })).toBeNull()
  })
})
