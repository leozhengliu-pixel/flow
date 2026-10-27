import { fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { AgentMessage, AgentMessageAttachment } from '@/types/flow'
import { AgentErrorDetail, classifyAgentError } from './agent-error-detail'
import { AgentWorkGroup } from './agent-work-group'
import { applyAgentStreamEvent } from './agent-stream-state'
import { toolStatusLabel, translatePhaseTitle, workingLabel } from './agent-step-labels'
import { translateToChinese } from '@/i18n/translate'

type Parts = NonNullable<AgentMessage['parts']>

function renderGroup(parts: Parts, locale: 'en-US' | 'zh-CN' = 'en-US', attachments?: AgentMessageAttachment[]) {
  localStorage.setItem('flow:locale', locale)
  return render(<I18nProvider><AgentWorkGroup attachments={attachments} message={{ durationMs: 3000 }} parts={parts} onToolApproval={vi.fn()} running={false}/></I18nProvider>)
}

afterEach(() => localStorage.removeItem('flow:locale'))

describe('AgentWorkGroup', () => {
  it('shows "Read file <name>" and "Transcribed media" rows, resolving a file reference to the attachment name', () => {
    const attachments = [{ name: 'Q3 report.pdf', contentType: 'application/pdf', size: 10, url: '/uploads/agent_upload_1_Q3_report.pdf' }]
    const parts: Parts = [
      { id: 't1', type: 'toolCall', status: 'completed', toolCall: { id: 't1', name: 'read_file', arguments: { file: '/uploads/agent_upload_1_Q3_report.pdf' }, status: 'completed' } },
      { id: 't2', type: 'toolCall', status: 'completed', toolCall: { id: 't2', name: 'read_file', arguments: { file: 'sales.xlsx' }, status: 'completed' } },
      { id: 't3', type: 'toolCall', status: 'completed', toolCall: { id: 't3', name: 'transcribe_media', arguments: { file: 'call.mp3' }, status: 'completed' } },
    ]
    const { container } = renderGroup(parts, 'en-US', attachments)
    fireEvent.click(screen.getByText(/Worked for/))
    const rows = [...container.querySelectorAll('[class*="toolCallRow"]')].map(row => row.textContent)
    expect(rows).toEqual(['Read fileQ3 report.pdf', 'Read filesales.xlsx', 'Transcribed media'])
  })

  it('translates the attachment tool rows', () => {
    const { container } = renderGroup([
      { id: 't1', type: 'toolCall', status: 'completed', toolCall: { id: 't1', name: 'read_file', arguments: { file: 'a.pdf' }, status: 'completed' } },
      { id: 't2', type: 'toolCall', status: 'completed', toolCall: { id: 't2', name: 'transcribe_media', arguments: { file: 'a.mp4' }, status: 'completed' } },
    ], 'zh-CN')
    expect(container.textContent).toContain('已读取文件')
    expect(container.textContent).toContain('已转写媒体')
  })

  it('labels a connector tool by its own name and connector instead of its opaque id', () => {
    const connectorCall = (id: string, status: 'running' | 'completed') => ({ id, type: 'toolCall' as const, status, toolCall: { id, name: 'external_13e42ac3f0b8a2d1c4e5f60718293a4b', connector: 'Code search', remoteTool: 'search_code', arguments: {}, status } })
    const done = renderGroup([connectorCall('t1', 'completed')])
    fireEvent.click(screen.getByText(/Worked for/))
    expect(done.container.querySelector('[class*="toolCallRow"]')?.textContent).toBe('Ran search_code in Code search')
    expect(done.container.textContent).not.toContain('13e42ac3')
    done.unmount()
    const zh = renderGroup([connectorCall('t1', 'completed')], 'zh-CN')
    expect(zh.container.querySelector('[class*="toolCallRow"]')?.textContent).toBe('已在 Code search 中运行 search_code')
  })

  it('labels a loop run connector row from its names, not from the stored title', () => {
    const part = (extra: object) => ({ id: 't1', type: 'toolCall' as const, status: 'completed' as const, toolCall: { id: 't1', name: 'external_13e42ac3f0b8a2d1c4e5f60718293a4b', status: 'completed' as const, ...extra } })
    const named = renderGroup([part({ title: 'Used search_code in Code search', connector: 'Code search', remoteTool: 'search_code' })], 'zh-CN')
    expect(named.container.querySelector('[class*="toolCallRow"]')?.textContent).toBe('已在 Code search 中使用 search_code')
    named.unmount()
    const older = renderGroup([part({ title: 'Used 13e42ac3f0b8a2d1c4e5f60718293a4b' })])
    fireEvent.click(screen.getByText(/Worked for/))
    expect(older.container.querySelector('[class*="toolCallRow"]')?.textContent).toBe('Used a connector tool')
  })

  it('names the running connector tool in the live header', () => {
    const parts: Parts = [{ id: 't1', type: 'toolCall', status: 'running', toolCall: { id: 't1', name: 'external_13e42ac3f0b8a2d1c4e5f60718293a4b', connector: 'Code search', remoteTool: 'search_code', status: 'running' } }]
    expect(workingLabel(parts, source => source)).toBe('Running search_code in Code search…')
    expect(workingLabel(parts, translateToChinese)).toBe('正在 Code search 中运行 search_code…')
    expect(toolStatusLabel('external_abc', false, source => source, {}, false, { remoteTool: 'search_code' })).toBe('Ran search_code')
    expect(toolStatusLabel('external_abc', true, source => source, {}, false, {})).toBe('Running external abc…')
  })

  it('hides raw provider reasoning, also in older chats, but keeps the agent\'s own narration and step labels', () => {
    const { container } = renderGroup([
      { id: 'agent_message_1_reasoning', type: 'reasoning', text: 'Figuring out query filters — I need to look into the priority filter', status: 'completed' },
      { id: 'agent_message_1_narration_0', type: 'reasoning', text: '我先查看相关事项。', status: 'completed' },
      { id: 't1', type: 'toolCall', status: 'completed', toolCall: { id: 't1', name: 'search_issues', arguments: { query: 'export' }, status: 'completed' } },
    ], 'zh-CN')
    expect(container.textContent).not.toContain('Figuring out')
    expect(container.textContent).toContain('我先查看相关事项。')
    expect(container.textContent).toContain('已搜索事项')
  })

  it('labels a save_* call as an update when it has an id, and neutrally while its arguments stream in', () => {
    expect(toolStatusLabel('save_project', true, translateToChinese, { id: 'project_1', name: 'Export' })).toBe('正在更新项目…')
    expect(toolStatusLabel('save_project', true, translateToChinese, { name: 'Export' })).toBe('正在创建项目…')
    expect(toolStatusLabel('save_project', true, translateToChinese, undefined, true)).toBe('正在保存项目…')
    expect(toolStatusLabel('save_project', false, translateToChinese, { id: 'project_1' })).toBe('已更新项目')
    expect(toolStatusLabel('save_milestone', false, translateToChinese, { id: 'm1' })).toBe('已更新里程碑')
  })

  it('shows nothing for a finished reply whose only work was provider reasoning', () => {
    const { container } = renderGroup([{ id: 'agent_message_1_reasoning', type: 'reasoning', text: 'Thinking about it', status: 'completed' }])
    expect(container.querySelector('details')).toBeNull()
  })

  it('translates the result of a step a stopped reply never finished', () => {
    const { container } = renderGroup([
      { id: 't1', type: 'toolCall', status: 'error', toolCall: { id: 't1', name: 'list_issues', status: 'error', error: 'Stopped before this step finished.' } },
    ], 'zh-CN')
    expect(container.textContent).toContain('此步骤完成前已停止。')
  })

  it('renders reasoning and step narration as markdown, without literal asterisks', async () => {
    const { container } = renderGroup([
      { id: 'msg_narration_0', type: 'reasoning', text: '**Planning** the work', status: 'completed' },
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

  it('shows the phase title above its tools and live headers', () => {
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
