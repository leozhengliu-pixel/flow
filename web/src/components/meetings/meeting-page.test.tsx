import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import type { Meeting } from '@/types/flow'

const api = vi.hoisted(() => ({
  fetchMeetings: vi.fn(), createMeeting: vi.fn(), updateMeeting: vi.fn(),
  fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn(),
  realtimeClientId: () => 'meeting-test',
}))
vi.mock('@/lib/api', () => api)

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { MeetingPage } from './meeting-page'

const meeting = (notes: string): Meeting => ({
  id: 'meeting-1', title: 'Weekly sync', organizerId: 'user-1', attendeeIds: ['user-1'], teamIds: [], projectIds: [], issueIds: [],
  startsAt: '2026-10-01T10:00:00.000Z', durationMinutes: 30, notes, transcript: '', subscriberIds: [], createdAt: '2026-10-01T09:00:00.000Z', updatedAt: '2026-10-01T09:00:00.000Z',
})

const page = () => {
  const data = mentionFixture()
  return <MentionShell data={data}><MeetingPage data={data} meetingId="meeting-1" onNavigate={vi.fn()}/></MentionShell>
}

beforeEach(() => {
  for (const mock of [api.fetchMeetings, api.updateMeeting, api.fetchIssueRecord, api.listProjectRecords, api.listIssueRecords]) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  resetAgentRecordCache()
  stubEditorEnvironment()
})
afterEach(() => { vi.unstubAllGlobals() })

describe('meeting notes', () => {
  it('saves mentions as markdown links when the notes lose focus, and shows chips when the meeting is opened again', async () => {
    const user = userEvent.setup()
    api.fetchMeetings.mockResolvedValue({ items: [meeting('')], hasMore: false, total: 1 })
    api.updateMeeting.mockImplementation(async (_id: string, patch: { notes?: string }) => meeting(patch.notes ?? ''))
    const first = render(page())
    const box = await screen.findByRole('textbox', { name: 'Meeting notes' })
    await user.click(box)
    await user.keyboard('Agreed @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    pasteText(box, `${window.location.origin}${mentionUrls.project}`)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
    expect(api.updateMeeting).not.toHaveBeenCalled()
    await user.click(screen.getByRole('textbox', { name: 'Meeting transcript' }))
    await waitFor(() => expect(api.updateMeeting).toHaveBeenCalledTimes(1))
    const notes = api.updateMeeting.mock.calls[0][1].notes as string
    expect(notes).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(notes).toContain('[Project one](/workspace/project/project-one/overview)')
    first.unmount()
    api.fetchMeetings.mockResolvedValue({ items: [meeting(notes)], hasMore: false, total: 1 })
    render(page())
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })

  it('keeps the transcript a plain text area', async () => {
    api.fetchMeetings.mockResolvedValue({ items: [meeting('')], hasMore: false, total: 1 })
    render(page())
    expect((await screen.findByRole('textbox', { name: 'Meeting transcript' })).tagName).toBe('TEXTAREA')
  })
})
