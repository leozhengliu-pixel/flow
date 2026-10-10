import { render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { AgentMessage } from '@/types/flow'
import { AgentErrorDetail, classifyAgentError } from './agent-error-detail'
import { AgentWorkGroup } from './agent-work-group'
import { applyAgentStreamEvent } from './agent-stream-state'
import { translatePhaseTitle, workingLabel } from './agent-step-labels'
import { translateToChinese } from '@/i18n/translate'

type Parts = NonNullable<AgentMessage['parts']>

function renderGroup(parts: Parts, locale: 'en-US' | 'zh-CN' = 'en-US') {
  localStorage.setItem('flow:locale', locale)
  return render(<I18nProvider><AgentWorkGroup message={{ durationMs: 3000 }} parts={parts} onToolApproval={vi.fn()} running={false}/></I18nProvider>)
}

afterEach(() => localStorage.removeItem('flow:locale'))

describe('AgentWorkGroup', () => {
  it('renders reasoning and step narration as markdown, without literal asterisks', async () => {
    const { container } = renderGroup([
      { id: 'reasoning', type: 'reasoning', text: '**Planning** the work', status: 'completed' },
      { id: 'step', type: 'step', title: 'Reviewing issues', text: 'Check the _open_ issues first', status: 'completed' },
    ])
    container.querySelector('details')!.open = true
    const bold = await screen.findByText('Planning', { selector: 'strong' })
    expect(bold).toBeInTheDocument()
    expect(await screen.findByText('open', { selector: 'em' })).toBeInTheDocument()
    expect(container.textContent).not.toContain('**')
    expect(container.textContent).toContain('Planning the work')
  })

  it('labels tool steps in Chinese, including server loop labels and unknown tools', () => {
    renderGroup([
      { id: 't1', type: 'toolCall', status: 'completed', toolCall: { id: 't1', name: 'list_projects', status: 'completed' } },
      { id: 't2', type: 'toolCall', status: 'completed', toolCall: { id: 't2', name: 'save_issue', arguments: { id: 'issue_1' }, status: 'completed' } },
      { id: 't3', type: 'toolCall', status: 'completed', toolCall: { id: 't3', name: 'list_release_notes', title: 'Listed release notes', status: 'completed' } },
      { id: 't4', type: 'toolCall', status: 'completed', toolCall: { id: 't4', name: 'list_issues', title: 'Read project', status: 'completed' } },
      { id: 't5', type: 'toolCall', status: 'completed', toolCall: { id: 't5', name: 'github_search_code', status: 'completed' } },
    ], 'zh-CN')
    for (const label of ['已查看项目', '已更新事项', '已列出发布说明', '已读取项目', '已运行 github search code']) expect(screen.getByText(label)).toBeInTheDocument()
  })
})

describe('AgentErrorDetail', () => {
  it('translates server phase titles by template and keeps model-written titles', () => {
    const zh = (title: string) => translatePhaseTitle(title, translateToChinese)
    expect(zh('Looking up DEV-16, DEV-24')).toBe('查找 DEV-16、DEV-24')
    expect(zh('Searching issues for "导出", "export, csv"')).toBe('搜索"导出"、"export, csv"相关事项')
    expect(zh('Updating priority of DEV-16')).toBe('更新 DEV-16 的优先级')
    expect(zh('Looking at projects, users')).toBe('查看项目、成员')
    expect(zh('Creating issue Fix login')).toBe('创建事项 Fix login')
    expect(zh('查找 DEV-16')).toBe('查找 DEV-16')
    expect(translatePhaseTitle('Updating priority of DEV-16', source => source)).toBe('Updating priority of DEV-16')
  })

  it('shows the phase title above its tools and Linear-style live headers', () => {
    const tool = (id: string, name: string, status: 'running' | 'completed', args?: Record<string, unknown>) => ({ id, type: 'toolCall' as const, status, toolCall: { id, name, arguments: args, status } })
    const en = (source: string) => source
    expect(workingLabel([], en)).toBe('Thinking…')
    expect(workingLabel([{ id: 'r', type: 'reasoning', status: 'running' }], en)).toBe('Thinking…')
    expect(workingLabel([tool('a', 'get_issue', 'running', { id: 'DEV-16' })], en)).toBe('Looking at issue…')
    expect(workingLabel([{ id: 's', type: 'step', title: 'Looking up DEV-16, DEV-24', status: 'completed' }, tool('a', 'get_issue', 'completed'), tool('b', 'get_issue', 'running')], translateToChinese)).toBe('查找 DEV-16、DEV-24…')
    expect(workingLabel([{ id: 's', type: 'step', title: 'Looking up DEV-16, DEV-24', status: 'completed' }, tool('a', 'get_issue', 'completed'), { id: 'x', type: 'reasoning', status: 'completed', text: 'next' }, tool('c', 'search_issues', 'running')], en)).toBe('Searching issues…')
    renderGroup([{ id: 's', type: 'step', title: 'Looking up DEV-16, DEV-24', status: 'completed' }, tool('a', 'get_issue', 'completed', { id: 'DEV-16' }), tool('b', 'get_issue', 'completed', { id: 'DEV-24' })], 'zh-CN')
    expect(screen.getByText('查找 DEV-16、DEV-24')).toBeInTheDocument()
    expect(screen.getByText('处理耗时 3 秒')).toBeInTheDocument()
  })

  it('streams a phase title in above the tool rows it names', () => {
    const started = applyAgentStreamEvent(undefined, { type: 'session.started', messageId: 'm', session: { id: 's', title: '', messages: [] } as never })
    let session = applyAgentStreamEvent(started, { type: 'tool.started', messageId: 'm', part: { id: 'a', type: 'toolCall', status: 'running' } })
    session = applyAgentStreamEvent(session, { type: 'tool.started', messageId: 'm', part: { id: 'b', type: 'toolCall', status: 'running' } })
    session = applyAgentStreamEvent(session, { type: 'tool.completed', messageId: 'm', beforePartId: 'a', part: { id: 'phase', type: 'step', title: 'Looking up DEV-16, DEV-24', status: 'completed' } })
    session = applyAgentStreamEvent(session, { type: 'tool.completed', messageId: 'm', beforePartId: 'a', part: { id: 'phase', type: 'step', title: 'Looking up DEV-16, DEV-24', status: 'completed' } })
    expect(session!.messages.at(-1)!.parts!.map(part => part.id)).toEqual(['phase', 'a', 'b'])
  })

  it('classifies the server error text', () => {
    expect(classifyAgentError('Flow Agent provider returned status 429: Rate limit reached')).toBe('rate_limit')
    expect(classifyAgentError('Flow Agent provider returned status 401: Incorrect API key provided')).toBe('auth')
    expect(classifyAgentError('Flow Agent provider returned status 503: upstream connect error')).toBe('unavailable')
    expect(classifyAgentError('Flow Agent provider returned status 504: gateway timeout')).toBe('timeout')
    expect(classifyAgentError('Flow Agent provider returned status 400: This model\'s maximum context length is 128000 tokens')).toBe('context')
    expect(classifyAgentError('Flow Agent provider is unavailable')).toBe('unavailable')
    expect(classifyAgentError('Flow Agent provider stopped responding')).toBe('timeout')
    expect(classifyAgentError('Flow Agent provider returned an empty response')).toBe('empty')
    expect(classifyAgentError('Flow Agent exceeded the tool turn limit')).toBe('turn_limit')
    expect(classifyAgentError('Generation stopped')).toBe('stopped')
    expect(classifyAgentError('Partial warning')).toBe('generic')
  })

  it('shows a translated message with the raw provider text under Details', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const raw = 'Flow Agent provider returned status 429: {"error":{"message":"Rate limit reached"}}'
    const { container } = render(<I18nProvider><span role="alert"><AgentErrorDetail error={raw}/></span></I18nProvider>)
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('模型服务商正在限制请求频率。请稍候再试。')
    expect(within(alert).getByText('详情', { selector: 'summary' })).toBeInTheDocument()
    expect(container.querySelector('details code')).toHaveTextContent(raw)
    expect(container.querySelector('details')).not.toHaveAttribute('open')
  })

  it('passes readable errors through without details', () => {
    const { container } = render(<I18nProvider><AgentErrorDetail error="Partial warning"/></I18nProvider>)
    expect(screen.getByText('Partial warning')).toBeInTheDocument()
    expect(container.querySelector('details')).toBeNull()
  })
})
