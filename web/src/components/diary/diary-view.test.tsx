import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import { DiaryView } from './diary-view'
import type { BootstrapData, FlowDocument } from '@/types/flow'

vi.mock('@/lib/api', () => ({
  fetchIssueRecord: vi.fn(),
  listProjectRecords: vi.fn(),
  listIssueRecords: vi.fn(async () => ({ items: [], hasMore: false, total: 0 })),
  realtimeClientId: () => 'diary-test',
  listDocuments: vi.fn(async () => []),
  createDocument: vi.fn(async () => ({
    id: 'diary-1',
    slugId: 'diary-1',
    title: 'Tuesday, September 22',
    icon: 'Diary',
    content: '',
    contentData: { diaryDate: '2026-09-22', kind: 'diary' },
    creator: { id: 'u1', name: 'ada', displayName: 'Ada', email: 'a@x', active: true, emailVerified: true },
    projectIds: [],
    teamIds: [],
    subscriberIds: [],
    favorite: false,
    createdAt: '2026-09-22T10:00:00.000Z',
    updatedAt: '2026-09-22T10:00:00.000Z',
    revisions: [],
  } satisfies FlowDocument)),
  updateDocument: vi.fn(),
  deleteDocument: vi.fn(),
}))

import { createDocument, listDocuments, updateDocument } from '@/lib/api'
import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'

function bootstrap(): BootstrapData {
  return {
    viewer: { id: 'u1', name: 'ada', displayName: 'Ada', email: 'a@x', active: true, emailVerified: true },
    documents: [],
  } as unknown as BootstrapData
}

const entry = (content: string): FlowDocument => ({
  id: 'diary-1',
  slugId: 'diary-1',
  title: 'Tuesday, September 22',
  icon: 'Diary',
  content,
  contentData: { diaryDate: '2026-09-22', kind: 'diary' },
  creator: { id: 'u1', name: 'ada', displayName: 'Ada', email: 'a@x', active: true, emailVerified: true },
  projectIds: [],
  teamIds: [],
  subscriberIds: [],
  favorite: false,
  createdAt: '2026-09-22T10:00:00.000Z',
  updatedAt: '2026-09-22T10:00:00.000Z',
  revisions: [],
}) as unknown as FlowDocument

const view = () => <MentionShell data={mentionFixture()}><DiaryView data={bootstrap()} /></MentionShell>

describe('DiaryView', () => {
  beforeEach(() => {
    vi.mocked(listDocuments).mockResolvedValue([])
    vi.mocked(updateDocument).mockReset()
    resetAgentRecordCache()
    stubEditorEnvironment()
  })
  afterEach(() => { vi.unstubAllGlobals() })

  it('renders empty state and creates an entry', async () => {
    const user = userEvent.setup()
    render(view())
    expect(await screen.findByText('No entries yet')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'New entry' }))
    expect(createDocument).toHaveBeenCalled()
    expect(await screen.findByRole('textbox', { name: 'Diary entry' })).toBeInTheDocument()
  })

  it('saves mentions as markdown links when the entry loses focus, and shows chips when the entry is loaded again', async () => {
    const user = userEvent.setup()
    vi.mocked(listDocuments).mockResolvedValue([entry('')])
    vi.mocked(updateDocument).mockImplementation(async (_id, input) => entry((input as { content: string }).content))
    const first = render(view())
    const box = await screen.findByRole('textbox', { name: 'Diary entry' })
    await user.click(box)
    await user.keyboard('Met @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    pasteText(box, `${window.location.origin}${mentionUrls.project}`)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
    await user.click(screen.getByRole('button', { name: 'New entry' }))
    await waitFor(() => expect(updateDocument).toHaveBeenCalledTimes(1))
    const content = (vi.mocked(updateDocument).mock.calls[0][1] as { content: string }).content
    expect(content).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(content).toContain('[Project one](/workspace/project/project-one/overview)')
    first.unmount()
    vi.mocked(listDocuments).mockResolvedValue([entry(content)])
    render(view())
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })
})
