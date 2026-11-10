import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ProjectDetailsSidebar } from './project-details-sidebar'
import { I18nProvider } from '@/i18n/i18n'
import { resetRememberedChatPlatform } from '@/lib/chat-platform'
import * as chatApi from '@/lib/chat-integrations-api'
import { makeBootstrap, project } from '@/test/fixtures'
import type { ChatProjectGroup, ChatProjectGroups } from '@/types/chat-integrations'

vi.mock('@/lib/chat-integrations-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/chat-integrations-api')>()),
  fetchChatIntegration: vi.fn(),
  fetchChatProjectGroups: vi.fn(),
}))

const group = (id: string, chatName: string): ChatProjectGroup => ({ id, chatName, provider: 'feishu' })
const groups = (canManage: boolean, ...items: ChatProjectGroup[]): ChatProjectGroups => ({ groups: items, canManage })

function renderSidebar() {
  const data = { ...makeBootstrap(), chatPlatform: 'feishu' as const }
  const current = { ...project, id: 'project-chat', createdAt: '2026-09-27T00:00:00.000Z', updatedAt: '2026-09-27T00:00:00.000Z' } as typeof project
  return render(<I18nProvider><ProjectDetailsSidebar
    data={data}
    initiatives={[]}
    integrationConnections={[]}
    labelGroups={[]}
    labels={[]}
    onConvertMilestone={vi.fn()}
    onCreateMilestone={vi.fn()}
    onDeleteMilestone={vi.fn()}
    onMoveMilestone={vi.fn()}
    onOpenIssueFilter={vi.fn()}
    onOpenMilestoneIssues={vi.fn()}
    onReorderMilestones={vi.fn()}
    onTabChange={vi.fn()}
    onUpdate={vi.fn().mockResolvedValue(undefined)}
    onUpdateProject={vi.fn().mockResolvedValue(current)}
    onUpdateMilestone={vi.fn()}
    project={current}
    projectIssues={[]}
    projectRelations={[]}
    projects={[current]}
    projectStatuses={[current.status]}
    projectUpdates={[]}
    teams={data.teams}
    users={data.users}
    viewer={data.viewer}
  /></I18nProvider>)
}

describe('project sidebar chat group property', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    resetRememberedChatPlatform()
  })
  afterEach(() => {
    vi.mocked(chatApi.fetchChatIntegration).mockReset()
    vi.mocked(chatApi.fetchChatProjectGroups).mockReset()
  })

  it("reads the project's groups, not the binding settings, for every viewer", async () => {
    vi.mocked(chatApi.fetchChatProjectGroups).mockResolvedValue(groups(true))
    renderSidebar()
    await waitFor(() => expect(chatApi.fetchChatProjectGroups).toHaveBeenCalledWith('feishu', 'project-chat'))
    expect(chatApi.fetchChatIntegration).not.toHaveBeenCalled()
  })

  it('offers a manager to connect a group while none is connected', async () => {
    vi.mocked(chatApi.fetchChatProjectGroups).mockResolvedValue(groups(true))
    renderSidebar()
    await waitFor(() => expect(chatApi.fetchChatProjectGroups).toHaveBeenCalled())
    const trigger = screen.getByRole('button', { name: 'Feishu group' })
    expect(trigger).toHaveTextContent('Connect Feishu group')
    expect(trigger).toHaveClass('is-empty')
    expect(trigger).not.toHaveAttribute('aria-disabled')
  })

  for (const canManage of [true, false]) {
    const viewer = canManage ? 'manager' : 'member'
    it(`names the connected group for a ${viewer}`, async () => {
      vi.mocked(chatApi.fetchChatProjectGroups).mockResolvedValue(groups(canManage, group('binding-1', 'Release room')))
      renderSidebar()
      const trigger = screen.getByRole('button', { name: 'Feishu group' })
      await waitFor(() => expect(trigger).toHaveTextContent('Release room'))
      expect(trigger).not.toHaveClass('is-empty')
      expect(trigger).not.toHaveAttribute('aria-disabled')
    })

    it(`counts several connected groups for a ${viewer}`, async () => {
      vi.mocked(chatApi.fetchChatProjectGroups).mockResolvedValue(groups(canManage, group('binding-1', 'Release room'), group('binding-2', 'Ops room')))
      renderSidebar()
      const trigger = screen.getByRole('button', { name: 'Feishu group' })
      await waitFor(() => expect(trigger).toHaveTextContent('2 groups'))
      expect(trigger).not.toHaveClass('is-empty')
    })
  }

  it('opens the read-only group list for a member', async () => {
    vi.mocked(chatApi.fetchChatProjectGroups).mockResolvedValue(groups(false, group('binding-1', 'Release room')))
    renderSidebar()
    const trigger = screen.getByRole('button', { name: 'Feishu group' })
    await waitFor(() => expect(trigger).toHaveTextContent('Release room'))
    await userEvent.setup().click(trigger)
    const dialog = await screen.findByRole('dialog')
    await waitFor(() => expect(dialog).toHaveTextContent("Only owners of this project's teams can change its groups."))
    expect(dialog.querySelector('form')).toBeNull()
    expect(chatApi.fetchChatIntegration).not.toHaveBeenCalled()
  })

  it("doesn't offer a member a connect action they can't complete", async () => {
    vi.mocked(chatApi.fetchChatProjectGroups).mockResolvedValue(groups(false))
    renderSidebar()
    const trigger = screen.getByRole('button', { name: 'Feishu group' })
    await waitFor(() => expect(trigger).toHaveTextContent('No Feishu group'))
    expect(trigger).toHaveClass('is-empty')
    expect(trigger).toHaveAttribute('aria-disabled', 'true')
    await userEvent.setup().click(trigger)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
