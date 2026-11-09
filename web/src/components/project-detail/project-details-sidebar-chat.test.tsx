import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ProjectDetailsSidebar } from './project-details-sidebar'
import { I18nProvider } from '@/i18n/i18n'
import { resetRememberedChatPlatform } from '@/lib/chat-platform'
import * as chatApi from '@/lib/chat-integrations-api'
import { makeBootstrap, project } from '@/test/fixtures'
import type { ChatBinding, ChatIntegrationSettings } from '@/types/chat-integrations'

vi.mock('@/lib/chat-integrations-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/chat-integrations-api')>()),
  fetchChatIntegration: vi.fn(),
}))

const binding = (overrides: Partial<ChatBinding>): ChatBinding => ({
  id: 'binding-1', connectionId: 'connection-1', chatId: 'oc_1', chatName: 'Release room', teamId: 'team-1',
  events: ['project.update_created'], enabled: true, allowCreate: false, allowUpdate: false, ...overrides,
})

const settings = (bindings: ChatBinding[]): ChatIntegrationSettings => ({
  connection: null, identity: null, bindings, bindableTeamIds: [], credentialsConfigured: true, webhookURL: '', canManage: false,
} as unknown as ChatIntegrationSettings)

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
  afterEach(() => vi.mocked(chatApi.fetchChatIntegration).mockReset())

  it('offers to connect a group while none is bound to the project', async () => {
    vi.mocked(chatApi.fetchChatIntegration).mockResolvedValue(settings([binding({ projectIds: ['another-project'] })]))
    renderSidebar()
    await waitFor(() => expect(chatApi.fetchChatIntegration).toHaveBeenCalledWith('feishu'))
    const trigger = screen.getByRole('button', { name: 'Feishu group' })
    expect(trigger).toHaveTextContent('Connect Feishu group')
    expect(trigger).toHaveClass('is-empty')
  })

  it('names the bound group once the project is connected', async () => {
    vi.mocked(chatApi.fetchChatIntegration).mockResolvedValue(settings([binding({ projectIds: ['project-chat'] })]))
    renderSidebar()
    const trigger = screen.getByRole('button', { name: 'Feishu group' })
    await waitFor(() => expect(trigger).toHaveTextContent('Release room'))
    expect(trigger).not.toHaveClass('is-empty')
  })

  it('counts several bound groups and ignores revoked bindings', async () => {
    vi.mocked(chatApi.fetchChatIntegration).mockResolvedValue(settings([
      binding({ projectId: 'project-chat' }),
      binding({ id: 'binding-2', chatId: 'oc_2', chatName: 'Ops room', projectIds: ['project-chat'] }),
      binding({ id: 'binding-3', chatId: 'oc_3', chatName: 'Old room', projectIds: ['project-chat'], revokedAt: '2026-11-01T00:00:00Z' }),
    ]))
    renderSidebar()
    const trigger = screen.getByRole('button', { name: 'Feishu group' })
    await waitFor(() => expect(trigger).toHaveTextContent('2 groups'))
  })
})
