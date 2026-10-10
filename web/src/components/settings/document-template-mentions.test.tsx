import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BootstrapData, DocumentTemplate } from '@/types/flow'

const api = vi.hoisted(() => ({
  fetchIssueRecord: vi.fn(),
  listProjectRecords: vi.fn(),
  listIssueRecords: vi.fn(),
  createDocumentTemplate: vi.fn(),
  updateDocumentTemplate: vi.fn(),
  listRecurringIssues: vi.fn(),
  createRecurringIssue: vi.fn(),
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api, realtimeClientId: () => 'document-template-test' }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { makeIssue } from '@/test/fixtures'
import { FeatureSettingsPage } from './feature-settings'
import { Shell } from './mention-field-shell'
import { documentMarkdown, paste, pickDocument, stubEditorDom } from './mention-field-test-kit'
import { RecurringIssuesSettingsPage } from './recurring-issues-settings'
import { TeamWorkflowSettings } from './team-workflow-settings'

const docChip = () => document.querySelector('a[data-agent-entity="document"]')
const team = makeIssue().team
const noop = vi.fn()
function workspaceData(overrides: Partial<BootstrapData> = {}) {
  return { ...mentionFixture(), documentTemplates: [], projectTemplates: [], issueTemplates: [], viewerRole: 'admin', teamSettings: {}, ...overrides } as unknown as BootstrapData
}
const savedTemplate = (content: string, teamId = '') => ({ id: 'dt-1', teamId, name: 'Kickoff', description: '', title: '', content, createdAt: '', updatedAt: '' }) as unknown as DocumentTemplate

beforeEach(() => {
  for (const mock of Object.values(api)) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  api.createDocumentTemplate.mockResolvedValue({})
  api.updateDocumentTemplate.mockResolvedValue({})
  api.listRecurringIssues.mockResolvedValue({ issues: [] })
  api.createRecurringIssue.mockResolvedValue({ issue: makeIssue(), subIssues: [] })
  noop.mockReset()
  resetAgentRecordCache()
  stubEditorDom()
})
afterEach(() => { vi.unstubAllGlobals() })

function teamTemplates(data: BootstrapData, subPath: string) {
  return <Shell data={data}><TeamWorkflowSettings data={data} team={team} section="templates" subPath={subPath} onNavigate={noop} onReload={async () => {}}/></Shell>
}

describe('team document template editor', () => {
  it('saves the content with a picked mention and shows its chip when the template is reopened', async () => {
    const user = userEvent.setup()
    const data = workspaceData()
    const first = render(teamTemplates(data, 'document/new'))
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Kickoff')
    await pickDocument(user, await screen.findByRole('textbox', { name: 'Document content' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.createDocumentTemplate).toHaveBeenCalled())
    const saved = api.createDocumentTemplate.mock.calls[0][0].content as string
    expect(saved).toContain(documentMarkdown)
    first.unmount()
    render(teamTemplates(workspaceData({ documentTemplates: [savedTemplate(saved, team.id)] } as Partial<BootstrapData>), 'document/dt-1/edit'))
    await waitFor(() => expect(docChip()).toHaveTextContent('Launch plan'))
  })

  it('turns a pasted Flow URL into a chip saved as a link', async () => {
    const user = userEvent.setup()
    render(teamTemplates(workspaceData(), 'document/new'))
    await user.type(screen.getByRole('textbox', { name: 'Template name' }), 'Kickoff')
    const box = await screen.findByRole('textbox', { name: 'Document content' })
    await user.click(box)
    paste(box, `${window.location.origin}${mentionUrls.document}`)
    await waitFor(() => expect(docChip()).toHaveTextContent('Launch plan'))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.createDocumentTemplate).toHaveBeenCalledWith(expect.objectContaining({ content: documentMarkdown })))
  })
})

describe('workspace document template editor (full page)', () => {
  const page = (data: BootstrapData) => <Shell data={data}><FeatureSettingsPage page="documents" data={data} onCreateReleasePipeline={noop} onOpenReleasePipeline={noop} onOpenIntegration={noop} onNavigateSettings={noop} onReload={async () => {}}/></Shell>

  it('saves a mention picked with the keyboard and shows its chip when the template is reopened', async () => {
    const user = userEvent.setup()
    const first = render(page(workspaceData()))
    await user.click(screen.getAllByRole('button', { name: /New template/ })[0])
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await user.type(await screen.findByRole('textbox', { name: 'Template name' }), 'Kickoff')
    await pickDocument(user, await screen.findByRole('textbox', { name: 'Document content' }), { keyboard: true })
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.createDocumentTemplate).toHaveBeenCalled())
    const saved = api.createDocumentTemplate.mock.calls[0][0].content as string
    expect(saved).toContain(documentMarkdown)
    first.unmount()
    render(page(workspaceData({ documentTemplates: [savedTemplate(saved)] } as Partial<BootstrapData>)))
    await user.click(await screen.findByRole('button', { name: 'Edit: Kickoff' }))
    await waitFor(() => expect(docChip()).toHaveTextContent('Launch plan'))
  })
})
