import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { DiaryView } from './diary-view'
import type { BootstrapData, FlowDocument } from '@/types/flow'

vi.mock('@/lib/api', () => ({
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

import { createDocument, listDocuments } from '@/lib/api'

function bootstrap(): BootstrapData {
  return {
    viewer: { id: 'u1', name: 'ada', displayName: 'Ada', email: 'a@x', active: true, emailVerified: true },
    documents: [],
  } as unknown as BootstrapData
}

describe('DiaryView', () => {
  beforeEach(() => {
    vi.mocked(listDocuments).mockResolvedValue([])
  })

  it('renders empty state and creates an entry', async () => {
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <DiaryView data={bootstrap()} />
      </I18nProvider>,
    )
    expect(await screen.findByText('No entries yet')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'New entry' }))
    expect(createDocument).toHaveBeenCalled()
    expect(await screen.findByLabelText('Diary entry')).toBeInTheDocument()
  })
})
