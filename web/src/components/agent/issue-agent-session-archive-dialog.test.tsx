import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import { archiveApplicationTask, getApplicationTask, listApplicationTasks, type ApplicationTask } from '@/lib/application-agents'
import { ActionDialogHost } from '@/components/ui/action-dialogs'
import { IssueAgentTasks } from './issue-agent-tasks'

vi.mock('@/lib/application-agents', async original => ({ ...await original<typeof import('@/lib/application-agents')>(), listApplicationTasks: vi.fn(), getApplicationTask: vi.fn(), archiveApplicationTask: vi.fn() }))
vi.mock('./agent-rich-text', () => ({ AgentRichText: ({ content }: { content: string }) => <div>{content}</div> }))

const app = { ...viewer, id: 'app-one', displayName: 'Agent', app: true, appScopes: ['app:assignable'], appTeamIds: ['team-1'] }
const task: ApplicationTask = { id: 'session', issueId: 'issue-1', teamId: 'team-1', appUserId: app.id, creatorId: viewer.id, status: 'active', version: 3, prompt: 'Inspect', trigger: 'delegation', updatedAt: '' }

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listApplicationTasks).mockResolvedValue([task])
  vi.mocked(getApplicationTask).mockResolvedValue({ session: task, activities: [] })
  vi.mocked(archiveApplicationTask).mockResolvedValue({ session: { ...task, status: 'canceled', archivedAt: '2026-10-26T00:00:00Z' }, archived: true, alreadyArchived: false, stopped: true, previousStatus: 'active' })
})

// The real confirmation dialog opens from the session's menu once the menu has closed, and confirming archives the
// session. (In a browser the menu handing focus back to its trigger dismissed the dialog at once; jsdom does not move
// focus that way, so this covers the flow, not that race.)
it('keeps the archive confirmation open after the session menu closes and archives on confirm', async () => {
  const user = userEvent.setup()
  render(<I18nProvider><ActionDialogHost/><IssueAgentTasks issue={makeIssue({ delegate: app })} data={makeBootstrap({ users: [viewer, app] })}/></I18nProvider>)
  await screen.findByRole('status')
  const header = document.querySelector('.issue-agent-delegation') as HTMLElement
  await user.click(within(header).getByRole('button', { name: 'Agent session options' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Archive session…' }))
  const dialog = await screen.findByRole('dialog', { name: 'Archive this agent session?' })
  await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument())
  expect(dialog).toBeVisible()
  await user.click(within(dialog).getByRole('button', { name: 'Archive session' }))
  await waitFor(() => expect(archiveApplicationTask).toHaveBeenCalledWith('workspace', 'session'))
  await waitFor(() => expect(document.querySelector('[data-agent-task-id="session"]')).toBeNull())
})
