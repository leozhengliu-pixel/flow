import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState, type ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import type { BootstrapData } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn(), realtimeClientId: () => 'field-test' }))
vi.mock('@/lib/api', () => api)

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { MentionTextField } from './mention-text-field'

function Shell({ children, data }: { children: ReactNode; data: BootstrapData }) {
  return <I18nProvider><MemoryRouter><WorkspaceStoreProvider account={null} data={data} session={null}>{children}</WorkspaceStoreProvider></MemoryRouter></I18nProvider>
}

function Harness({ initial = '', onValue, onSubmit }: { initial?: string; onValue?: (value: string) => void; onSubmit?: () => void }) {
  const [value, setValue] = useState(initial)
  return <>
    <MentionTextField ariaLabel="Body" placeholder="Write…" value={value} onChange={next => { setValue(next); onValue?.(next) }} onSubmit={() => { onSubmit?.(); setValue('') }}/>
    <output data-testid="value">{value}</output>
  </>
}

function paste(element: Element, text: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { types: ['text/plain'], files: [], items: [], getData: (type: string) => type === 'text/plain' ? text : '' } })
  act(() => { element.dispatchEvent(event) })
}

beforeEach(() => {
  for (const mock of [api.fetchIssueRecord, api.listProjectRecords, api.listIssueRecords]) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  resetAgentRecordCache()
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList
  Range.prototype.getBoundingClientRect = () => new DOMRect()
  document.elementFromPoint = () => document.body
})
afterEach(() => { vi.unstubAllGlobals() })

describe('MentionTextField', () => {
  it('turns "@" + a name into a chip and reports the markdown, and shows the chip again after a reload', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    const onValue = vi.fn()
    const first = render(<Shell data={data}><Harness onValue={onValue}/></Shell>)
    await user.click(await screen.findByRole('textbox', { name: 'Body' }))
    await user.keyboard('See @Road')
    await user.click(await screen.findByRole('option', { name: /Roadmap/ }))
    await waitFor(() => expect(onValue).toHaveBeenLastCalledWith('See [Roadmap](/workspace/initiative/roadmap/overview) '))
    expect(document.querySelector('a[data-agent-entity="initiative"]')).toHaveTextContent('Roadmap')
    first.unmount()
    render(<Shell data={data}><Harness initial="See [Roadmap](/workspace/initiative/roadmap/overview)"/></Shell>)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="initiative"]')).toHaveTextContent('Roadmap'))
  })

  it('offers people from the workspace and writes @name', async () => {
    const user = userEvent.setup()
    const onValue = vi.fn()
    render(<Shell data={mentionFixture()}><MentionTextField ariaLabel="Body" value="" users={mentionFixture().users} onChange={onValue}/></Shell>)
    await user.click(await screen.findByRole('textbox', { name: 'Body' }))
    await user.keyboard('@Team')
    await user.click(await screen.findByRole('option', { name: /Teammate/ }))
    await waitFor(() => expect(onValue).toHaveBeenLastCalledWith('@Teammate ', expect.anything()))
  })

  it('turns a pasted Flow URL into a chip that survives a reload', async () => {
    const data = mentionFixture()
    const onValue = vi.fn()
    const first = render(<Shell data={data}><Harness onValue={onValue}/></Shell>)
    const box = await screen.findByRole('textbox', { name: 'Body' })
    act(() => box.focus())
    paste(box, `${window.location.origin}${mentionUrls.document}`)
    await waitFor(() => expect(onValue).toHaveBeenLastCalledWith('[Launch plan](/workspace/document/plan-abc)'))
    first.unmount()
    render(<Shell data={data}><Harness initial="[Launch plan](/workspace/document/plan-abc)"/></Shell>)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })

  it('empties the editor when the value is cleared after a submit, even with focus', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(<Shell data={mentionFixture()}><Harness onSubmit={onSubmit}/></Shell>)
    await user.click(await screen.findByRole('textbox', { name: 'Body' }))
    await user.keyboard('Hello')
    await waitFor(() => expect(screen.getByTestId('value')).toHaveTextContent('Hello'))
    await user.keyboard('{Control>}{Enter}{/Control}')
    expect(onSubmit).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Body' })).not.toHaveTextContent('Hello'))
  })
})
