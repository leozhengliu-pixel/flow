import type { ComponentProps } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { ProjectOverview } from './project-overview'

beforeEach(() => {
  for (const mock of [api.fetchIssueRecord, api.listProjectRecords, api.listIssueRecords]) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  resetAgentRecordCache()
  stubEditorEnvironment()
})
afterEach(() => { vi.unstubAllGlobals() })

function overviewProps(data: ReturnType<typeof mentionFixture>, milestones: unknown[], extra: Record<string, unknown> = {}) {
  const project = data.projects[0]
  return {
    issueData: data, project: { ...project, milestones, resources: [], customers: [] }, projects: [project], initiatives: [], documents: data.documents,
    projectStatuses: [project.status], projectUpdates: [], users: data.users, teams: data.teams, labels: [], labelGroups: [], projectIssues: [],
    save: vi.fn(), onTabChange: vi.fn(), onCreateResource: vi.fn(), onUpdateResource: vi.fn(), onDeleteResource: vi.fn(),
    onCreateMilestone: vi.fn().mockResolvedValue({ id: 'milestone-new', name: 'Launch' }), onUpdateMilestone: vi.fn().mockResolvedValue(undefined), onDeleteMilestone: vi.fn(),
    ...extra,
  } as unknown as ComponentProps<typeof ProjectOverview>
}

describe('project overview milestone descriptions with mentions', () => {
  it('creates a milestone whose description holds a mention and a pasted Flow URL', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    const props = overviewProps(data, [])
    render(<MentionShell data={data}><ProjectOverview {...props}/></MentionShell>)

    await user.click(screen.getByRole('button', { name: 'Milestone' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Launch')
    const box = screen.getByRole('textbox', { name: 'Milestone description' })
    await user.click(box)
    await user.keyboard('Spec: @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    pasteText(box, `${window.location.origin}${mentionUrls.initiative}`)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="initiative"]')).toHaveTextContent('Roadmap'))
    await user.keyboard('{Control>}{Enter}{/Control}')

    await waitFor(() => expect(props.onCreateMilestone).toHaveBeenCalled())
    const input = (props.onCreateMilestone as unknown as ReturnType<typeof vi.fn>).mock.calls[0][1]
    expect(input.description).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(input.description).toContain(`[Roadmap](${mentionUrls.initiative})`)
  })

  it('edits an existing milestone description with a mention, saves it on blur and shows the chip after a reload', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    const milestone = { id: 'milestone-alpha', projectId: data.projects[0].id, name: 'Alpha', description: '', createdAt: '', updatedAt: '' }
    const props = overviewProps(data, [milestone])
    const first = render(<MentionShell data={data}><ProjectOverview {...props}/></MentionShell>)

    await user.click(screen.getByRole('button', { name: 'Expand' }))
    await user.click(await screen.findByRole('textbox', { name: 'Milestone description' }))
    await user.keyboard('See @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    await user.tab()
    const update = props.onUpdateMilestone as unknown as ReturnType<typeof vi.fn>
    await waitFor(() => expect(update).toHaveBeenCalledWith(data.projects[0].id, 'milestone-alpha', { description: 'See [Launch plan](/workspace/document/plan-abc)' }))
    first.unmount()

    const saved = overviewProps(data, [{ ...milestone, description: 'See [Launch plan](/workspace/document/plan-abc)' }])
    render(<MentionShell data={data}><ProjectOverview {...saved}/></MentionShell>)
    await user.click(screen.getByRole('button', { name: 'Expand' }))
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })
})
