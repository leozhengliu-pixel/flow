import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { AgentMessagePart } from '@/types/flow'
import { EntityAgentThread } from './entity-agent-thread'

// ask_user's question streams in as the model writes it (a running question part without options yet), so a reply that
// ends in a question no longer sits silent until the call finishes.
describe('EntityAgentThread streaming question', () => {
  it('renders the question text while it streams, without option pills', () => {
    const part: AgentMessagePart = { id: 'm1_tool_2', type: 'question', status: 'running', text: 'Which **date** should I set', question: { options: [], allowFreeText: false } }
    render(<I18nProvider><EntityAgentThread
      messages={[{ id: 'u1', role: 'user', content: 'Set the date', createdAt: new Date().toISOString() }]}
      streamParts={[part]}
      loading
      input=""
      onInputChange={vi.fn()}
      onSubmit={vi.fn()}
    /></I18nProvider>)
    expect(screen.getByText('date', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByText(/Which/)).toBeInTheDocument()
    expect(screen.queryByRole('group', { name: /Choose an option/ })).not.toBeInTheDocument()
  })
})
