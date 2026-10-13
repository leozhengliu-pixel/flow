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
      conversationDraftKey="test"
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

  it('puts the live work above the reply text once the reply has started, not after it', () => {
    const tool: AgentMessagePart = { id: 'm1_tool_1', type: 'toolCall', status: 'completed', toolCall: { id: 'c1', name: 'list_projects', status: 'completed' } }
    const { container } = render(<I18nProvider><EntityAgentThread
      messages={[
        { id: 'u1', role: 'user', content: 'Summarize', createdAt: new Date().toISOString() },
        { id: 'm1', role: 'assistant', content: 'Here is the summary after looking at projects.', createdAt: new Date().toISOString() },
      ]}
      conversationDraftKey="test"
      streamParts={[tool]}
      loading
      input=""
      onInputChange={vi.fn()}
      onSubmit={vi.fn()}
    /></I18nProvider>)
    const reply = screen.getByText(/Here is the summary/)
    const work = container.querySelector('[data-agent-work-group], [class*="work"]')
    expect(work).not.toBeNull()
    expect(work!.compareDocumentPosition(reply) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(reply.closest('article')).toContainElement(work as HTMLElement)
  })
})
