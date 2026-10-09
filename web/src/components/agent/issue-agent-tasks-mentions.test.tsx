import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import { getApplicationTask, listApplicationTasks, type ApplicationTask } from '@/lib/application-agents'
import { IssueAgentTasks } from './issue-agent-tasks'

vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), listApplicationTasks: vi.fn(), getApplicationTask: vi.fn(), replyApplicationTask: vi.fn() }))

const app = { ...viewer, id: 'app-one', displayName: 'Agent', app: true, appScopes: ['app:assignable'], appTeamIds: ['team-1'] }
const task = { id: 'session', issueId: 'issue-1', teamId: 'team-1', appUserId: app.id, creatorId: viewer.id, status: 'complete', version: 3, prompt: 'Inspect', trigger: 'delegation', updatedAt: '' } as ApplicationTask

describe('agent task responses', () => {
  beforeEach(() => {
    vi.mocked(listApplicationTasks).mockResolvedValue([task])
    vi.mocked(getApplicationTask).mockResolvedValue({
      session: task,
      activities: [{ id: 'a1', sessionId: 'session', actorId: app.id, type: 'response', body: 'Done: [Project one](/workspace/project/project-one/overview) and TST-1.', createdAt: '' }],
    })
  })

  it('renders resource references in an agent response as chips', async () => {
    render(<I18nProvider><MemoryRouter><IssueAgentTasks data={makeBootstrap({ users: [viewer, app] })} issue={makeIssue({ delegate: app })}/></MemoryRouter></I18nProvider>)
    await waitFor(() => expect(document.querySelectorAll('.issue-agent-markdown a[data-agent-entity]')).toHaveLength(2))
    expect(screen.getByText('Project one')).toBeInTheDocument()
    expect(document.querySelector('.issue-agent-markdown a[data-agent-entity="issue"]')).toHaveAttribute('href', '/workspace/issue/TST-1/test-issue')
  })
})
