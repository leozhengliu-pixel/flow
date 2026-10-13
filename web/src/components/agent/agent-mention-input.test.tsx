import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'

vi.mock('@/lib/api', () => ({ listIssueRecords: vi.fn(() => new Promise(() => undefined)) }))

import { AgentMentionInput, type AgentMention } from './agent-mention-input'
import { mentionIcon } from './agent-mention-icon'

const data = {
  workspace: { urlKey: 'ws' },
  users: [],
  issues: [],
  projects: [],
  documents: [],
  initiatives: [{ id: 'initiative-1', slugId: 'payments-q4', name: 'Payments Q4', color: '#f2c94c', icon: 'Initiative' }],
} as unknown as BootstrapData

describe('AgentMentionInput', () => {
  it('offers initiatives under @ and reports a chosen one as an initiative mention', async () => {
    const onChange = vi.fn<(value: string, mentions: AgentMention[]) => void>()
    const user = userEvent.setup()
    render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onChange={onChange} onSubmit={vi.fn()} placeholder="Ask" value=""/></I18nProvider>)
    await user.click(screen.getByRole('textbox', { name: 'Ask' }))
    await user.keyboard('@Pay')
    const menu = await screen.findByRole('listbox', { name: 'Mention' })
    expect(menu).toHaveTextContent('Initiatives')
    await user.click(screen.getByRole('option', { name: /Payments Q4/ }))
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(expect.any(String), [{ type: 'initiative', id: 'initiative-1', label: 'Payments Q4' }]))
    expect(screen.getByRole('textbox', { name: 'Ask' })).toHaveTextContent('Payments Q4')
  })

  it('draws an initiative chip with its glyph', () => {
    const { container } = render(<>{mentionIcon({ type: 'initiative', id: 'initiative-1' }, data)}</>)
    expect(container.querySelector('svg')).not.toBeNull()
  })
})
