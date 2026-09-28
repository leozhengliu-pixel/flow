import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchAgentStatus: vi.fn() }))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentChatPanel } from './agent-chat-panel'
import { linkAgentEntities, parseAgentAnswer, splitAgentSuggestions } from './agent-answer-content'

const data = makeBootstrap({
  issues: [makeIssue(), makeIssue({ id: 'issue-2', identifier: 'TST-2', number: 2, title: 'Second issue' })],
})

describe('splitAgentSuggestions', () => {
  it('takes the fenced suggestions block out of the reply', () => {
    expect(splitAgentSuggestions('Done.\n\n```suggestions\nCompare TST-1 and TST-2\n- Review migration steps\n\n```')).toEqual({
      prose: 'Done.',
      suggestions: ['Compare TST-1 and TST-2', 'Review migration steps'],
    })
  })

  it('keeps at most three suggestions', () => {
    expect(splitAgentSuggestions('Hi\n```suggestions\na\nb\nc\nd\n```').suggestions).toEqual(['a', 'b', 'c'])
  })

  it('hides an unfinished block and a fence still being typed while streaming', () => {
    expect(splitAgentSuggestions('Done.\n```suggestions\nCompare')).toEqual({ prose: 'Done.', suggestions: [] })
    expect(splitAgentSuggestions('Done.\n```sugg')).toEqual({ prose: 'Done.', suggestions: [] })
  })

  it('leaves code blocks alone', () => {
    const content = 'Run:\n```sh\nnpm test\n```'
    expect(splitAgentSuggestions(content)).toEqual({ prose: content, suggestions: [] })
  })
})

describe('linkAgentEntities', () => {
  it('turns known identifiers and Flow links into entity chips and lists referenced issues', () => {
    const origin = window.location.origin
    const { markdown, issues } = linkAgentEntities(
      `See TST-1, [TST-2 Second issue](https://elsewhere.test/x), ${origin}/workspace/project/project-one/overview and DEV-9. \`TST-1\``,
      data,
    )
    expect(markdown).toBe(
      'See [agentEntity kind="issue" id="issue-1" label="TST-1"], [agentEntity kind="issue" id="issue-2" label="TST-2"], '
      + '[agentEntity kind="project" id="project-1" label="Project one"] and DEV-9. `TST-1`',
    )
    expect(issues.map(issue => issue.identifier)).toEqual(['TST-1', 'TST-2'])
  })

  it('keeps unrelated links and returns the text unchanged without data', () => {
    expect(linkAgentEntities('[docs](https://example.com)', data).markdown).toBe('[docs](https://example.com)')
    expect(linkAgentEntities('TST-1', undefined).markdown).toBe('TST-1')
  })

  it('links issue URLs by identifier', () => {
    expect(parseAgentAnswer('[here](/workspace/issue/TST-2/second-issue)', data).referencedIssues.map(issue => issue.id)).toEqual(['issue-2'])
  })
})

const session: AgentSession = {
  id: 'session-1', slugId: 'chat', userId: 'user-1', title: 'Chat', favorite: false, location: 'toolbar', issueIds: [], skillIds: [],
  messages: [
    { id: 'user-message', role: 'user', content: 'What is open?', createdAt: '2026-08-31T00:00:00Z' },
    {
      id: 'assistant-message', role: 'assistant', createdAt: '2026-08-31T00:00:01Z',
      content: 'TST-1 is started and [TST-2](/workspace/issue/TST-2/second-issue) is next. DEV-404 is unknown.\n\n```suggestions\nCompare TST-1 and TST-2\nReview migration steps\n```',
    },
  ],
  createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:01Z',
}

describe('Agent answer chrome in the floating panel', () => {
  beforeEach(() => {
    api.fetchAgentStatus.mockReset().mockResolvedValue({ enabled: true, model: 'model' })
    Object.values(streams).forEach(mock => mock.mockReset())
  })

  it('renders inline entity chips, referenced issues and suggestion chips that send the follow-up', async () => {
    streams.streamAgentSessionMessage.mockResolvedValue(session)
    const user = userEvent.setup()
    render(<I18nProvider><AgentChatPanel data={data} initialSession={session} issues={[]} onClose={vi.fn()} open/></I18nProvider>)

    const answer = await screen.findByRole('document', { name: 'AI message' })
    await waitFor(() => expect(answer.querySelectorAll('a[data-agent-entity="issue"]')).toHaveLength(2))
    const chip = answer.querySelector('a[data-agent-entity="issue"]')!
    expect(chip).toHaveAttribute('href', '/workspace/issue/TST-1/test-issue')
    expect(chip).toHaveTextContent('TST-1Test issue')
    expect(answer).toHaveTextContent('DEV-404 is unknown.')
    expect(answer).not.toHaveTextContent('suggestions')

    const references = screen.getByRole('list', { name: 'Referenced issues' })
    expect(within(references).getAllByRole('link').map(link => link.textContent)).toEqual(['TST-1Test issue', 'TST-2Second issue'])

    const chips = screen.getByRole('group', { name: 'Suggested follow-ups' })
    expect(within(chips).getAllByRole('button').map(button => button.textContent)).toEqual(['Compare TST-1 and TST-2', 'Review migration steps'])
    await waitFor(() => expect(within(chips).getByRole('button', { name: 'Review migration steps' })).toBeEnabled())
    await user.click(within(chips).getByRole('button', { name: 'Review migration steps' }))
    expect(streams.streamAgentSessionMessage).toHaveBeenCalledWith('session-1', 'Review migration steps', expect.any(Function), expect.any(AbortSignal), expect.objectContaining({ issueIds: [] }))
  })
})

describe('stripLeakedProgress', () => {
  it('hides printed report_progress payloads, even mid-stream', async () => {
    const { stripLeakedProgress } = await import('./agent-answer-content')
    expect(stripLeakedProgress('{"title":"Gathering activity","message":"I\'ll look."}{"title":"Checking history"}## Summary')).toBe('## Summary')
    expect(stripLeakedProgress('{"title":"Gathering act')).toBe('')
    expect(stripLeakedProgress('Plain answer {"title":"x"}')).toBe('Plain answer {"title":"x"}')
  })
})

