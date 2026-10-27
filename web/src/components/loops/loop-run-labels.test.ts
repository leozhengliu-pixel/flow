import { describe, expect, it } from 'vitest'
import { toolStatusLabel, translateToolTitle } from '@/components/agent/agent-step-labels'
import { translateToChinese } from '@/i18n/translate'
import type { LoopRun } from '@/types/flow'
import { runParts } from './loop-data'
import { describeRunFailure, fixedFailureMessage, parseTriggerLabel, runSkipNote, runTriggerText } from './loop-run-labels'

const zh = translateToChinese
const en = (source: string) => source
const base: LoopRun = { id: 'run', loopId: 'loop', status: 'needs_review', trigger: 'event', startedAt: '2026-10-01T00:00:00Z' }

describe('run trigger text', () => {
  it('composes the trigger from the reason code', () => {
    const run = { ...base, triggerLabel: 'Triggered by DEV-24 entering triage', triggerReason: 'triage', entityIdentifier: 'DEV-24' }
    expect(runTriggerText(run, en)).toBe('Triggered by DEV-24 entering triage')
    expect(runTriggerText(run, zh)).toBe('由 DEV-24 进入分流触发')
    const status = { ...run, triggerReason: 'status', triggerValue: 'In Progress' }
    expect(runTriggerText(status, zh)).toBe('由 DEV-24 状态变为 进行中 触发')
    const cleared = { ...run, triggerReason: 'assignee', triggerValue: '' }
    expect(runTriggerText(cleared, en)).toBe('Triggered by DEV-24 assignee → No assignee')
    expect(runTriggerText(cleared, zh)).toBe('由 DEV-24 负责人变为 无负责人 触发')
  })

  it('reads runs stored before reason codes off their English label', () => {
    expect(parseTriggerLabel('Triggered by DEV-24 label Needs design added', 'DEV-24')).toEqual({ code: 'label', value: 'Needs design', name: 'DEV-24' })
    expect(parseTriggerLabel('Triggered by Q4 Launch status changed', 'Q4 Launch')).toEqual({ code: 'statusChanged', value: '', name: 'Q4 Launch' })
    expect(runTriggerText({ ...base, triggerLabel: 'Triggered by DEV-24 entering triage', entityIdentifier: 'DEV-24' }, zh)).toBe('由 DEV-24 进入分流触发')
    expect(runTriggerText({ ...base, triggerLabel: 'Triggered by DEV-24 project → No project', entityIdentifier: 'DEV-24' }, zh)).toBe('由 DEV-24 项目变为 无项目 触发')
    expect(runTriggerText({ ...base, triggerLabel: 'Triggered by DEV-24', entityIdentifier: 'DEV-24' }, zh)).toBe('由 DEV-24 触发')
  })

  it('translates manual and scheduled runs', () => {
    expect(runTriggerText({ ...base, trigger: 'manual', triggerLabel: 'Manual run' }, zh)).toBe('手动运行')
    expect(runTriggerText({ ...base, trigger: 'manual', triggerLabel: 'Manual run', entityIdentifier: 'DEV-3' }, zh)).toBe('在 DEV-3 上手动运行')
    expect(runTriggerText({ ...base, trigger: 'schedule', triggerLabel: 'Scheduled run' }, zh)).toBe('定时运行')
  })
})

describe('run failure explanation', () => {
  it('names the missing outputs of a run that made none', () => {
    const run: LoopRun = { ...base, failureReason: 'no_output', expectedOutputs: ['statusUpdate', 'comment'], produced: { comment: 1 }, error: "No output produced: the loop's instructions call for a project or initiative status update, but the run made none" }
    expect(describeRunFailure(run, en, 'en-US')).toEqual({ message: "The loop's instructions call for a project or initiative status update, but the run made none." })
    expect(describeRunFailure(run, zh, 'zh-CN').message).toBe('Loop 的指令要求产出一条项目或目标状态更新，但本次运行没有产出。')
    // Older runs without expectedOutputs: the kinds are read off the stored text.
    const legacy: LoopRun = { ...base, failureReason: 'no_output', error: "No output produced: the loop's instructions call for a new issue and a comment, but the run made none (the agent reported there was nothing to do)" }
    expect(describeRunFailure(legacy, zh, 'zh-CN').message).toBe('Loop 的指令要求产出一个新事项和一条评论，但本次运行没有产出。Agent 报告没有需要处理的内容。')
  })

  it('names document outputs by operation, with the skip and document notes', () => {
    const append: LoopRun = {
      ...base, failureReason: 'no_output', expectedOutputs: ['document'], produced: { document: 1, change: 1 },
      error: "No output produced: the loop's instructions call for an append to an existing document, but the run made none (save_document ran but did not change the document)",
    }
    expect(describeRunFailure(append, en, 'en-US').message).toBe("The loop's instructions call for an append to an existing document, but the run made none. save_document ran but did not change the document.")
    expect(describeRunFailure(append, zh, 'zh-CN').message).toBe('Loop 的指令要求产出向现有文档追加的内容，但本次运行没有产出。save_document 已执行，但文档没有任何变化。')
    const skipped: LoopRun = {
      ...base, failureReason: 'no_output', expectedOutputs: ['document'], summary: { status: 'skipped', summary: 'Holiday', skipReason: 'public holiday' },
      error: "No output produced: the loop's instructions call for a new document, but the run made none (the agent skipped the run — public holiday — but the instructions name no skip condition)",
    }
    expect(describeRunFailure(skipped, zh, 'zh-CN').message).toBe('Loop 的指令要求产出一篇新文档，但本次运行没有产出。Agent 跳过了本次运行（public holiday），但指令中没有写明跳过条件。')
    const always: LoopRun = { ...base, failureReason: 'no_output', error: "No output produced: the loop's instructions call for a comment, but the run made none (the agent reported there was nothing to do, but the instructions require the output even when nothing changed)" }
    expect(describeRunFailure(always, en, 'en-US').message).toBe("The loop's instructions call for a comment, but the run made none. The agent reported there was nothing to do, but the instructions require the output even when nothing changed.")
  })

  it('labels a skipped run with its skip condition', () => {
    const run: LoopRun = { ...base, status: 'completed', summary: { status: 'skipped', summary: 'Skipped', skipReason: 'public holiday' } }
    expect(runSkipNote(run, zh)).toEqual({ label: '已跳过', reason: 'public holiday' })
    expect(runSkipNote({ ...run, summary: { status: 'done', summary: 'Done' } }, zh)).toBeUndefined()
  })

  it('translates unusable model turn failures', () => {
    expect(fixedFailureMessage('Model turns cut off at the output limit: 3 (stop reason length). The run stopped before it finished.', zh)).toBe('在输出上限处被截断的模型轮次：3（停止原因 length）。本次运行在完成前停止。')
    expect(fixedFailureMessage("Model turns with no text and no tool call after the run's tool calls: 2 (stop reason stop).", zh)).toBe('在本次运行的工具调用之后，既无文本也无工具调用的模型轮次：2（停止原因 stop）。')
  })

  it('names the failed change and keeps its error as the detail', () => {
    const run: LoopRun = {
      ...base, failureReason: 'tool_error', expectedOutputs: ['issue'], error: 'No output produced: expected a new issue, but Created issue failed: team not found',
      toolCalls: [{ name: 'list_issues', label: 'Listed issues', status: 'completed' }, { name: 'save_issue', label: 'Created issue', status: 'error', error: 'team not found' }],
    }
    expect(describeRunFailure(run, zh, 'zh-CN')).toEqual({ message: '预期产出一个新事项，但“已创建事项”失败了。', detail: 'team not found' })
  })

  it("says when the agent's done summary claims an output that is not there", () => {
    const failed: LoopRun = {
      ...base, failureReason: 'tool_error', expectedOutputs: ['document'], summary: { status: 'done', summary: '已将今日总结追加到团队文档。' },
      error: "No output produced: expected an append to an existing document, but Saved document failed: Invalid arguments for save_document: patch is a list of edits (the agent's summary says the work is done, but this output is not there)",
      toolCalls: [{ name: 'save_document', label: 'Saved document', status: 'error', error: 'Invalid arguments for save_document: patch is a list of edits' }],
    }
    expect(describeRunFailure(failed, en, 'en-US').message).toBe("Expected an append to an existing document, but Saved document failed. The agent's summary says the work is done, but this output is not there.")
    expect(describeRunFailure(failed, zh, 'zh-CN').message).toContain('Agent 的总结称工作已完成，但这项产出并不存在。')
    const unchanged: LoopRun = {
      ...base, failureReason: 'no_output', expectedOutputs: ['document'], produced: { change: 1 }, summary: { status: 'done', summary: 'Appended.' },
      error: "No output produced: the loop's instructions call for an append to an existing document, but the run made none (save_document ran but did not change the document) (the agent's summary says the work is done, but this output is not there)",
    }
    expect(describeRunFailure(unchanged, zh, 'zh-CN').message).toBe('Loop 的指令要求产出向现有文档追加的内容，但本次运行没有产出。save_document 已执行，但文档没有任何变化。Agent 的总结称工作已完成，但这项产出并不存在。')
    // The run notice of a refused forbidden change (loopRefusedWriteNotice).
    expect(zh("The agent tried a change this loop's instructions forbid. It was refused and not made, even if the summary lists it.")).toBe('Agent 尝试了此 Loop 指令禁止的更改。该更改已被拒绝，并未执行，即使总结中列出了它。')
  })

  it('lists unfinished work the agent reported', () => {
    const run: LoopRun = { ...base, failureReason: 'incomplete', summary: { status: 'incomplete', summary: 'Partly done', notDone: ['Could not reach Slack'] }, error: 'The agent reported unfinished work: Could not reach Slack' }
    expect(describeRunFailure(run, zh, 'zh-CN')).toEqual({ items: ['Could not reach Slack'] })
  })

  it('translates fixed server messages with their numbers', () => {
    expect(fixedFailureMessage('The run exceeded its 15m0s time limit', zh)).toBe('本次运行超出了 15m 的时间限制')
    expect(fixedFailureMessage('Too many loop runs are in progress in this workspace (limit 2); try again when one finishes', zh)).toBe('此工作区中正在进行的 Loop 运行过多（上限 2）；请等其中一个完成后重试')
    expect(describeRunFailure({ ...base, status: 'failed', failureReason: 'unavailable', error: 'the loop is paused' }, zh, 'zh-CN')).toEqual({ message: '该 Loop 已暂停' })
    expect(describeRunFailure({ ...base, status: 'failed', failureReason: 'budget_exhausted', error: 'The run made more than 80 tool calls, its budget. Narrow the instructions or raise FLOW_LOOP_MAX_TOOL_CALLS.' }, en, 'en-US').message).toContain('more than 80 tool calls')
  })

  it('turns raw provider errors into a friendly message with the raw text as detail', () => {
    const error = 'Flow Agent provider returned status 429: {"error":{"message":"Rate limit reached for requests"}}'
    expect(describeRunFailure({ ...base, status: 'failed', failureReason: 'provider_error', error }, zh, 'zh-CN')).toEqual({ message: '模型服务商正在限制请求频率。请稍候再试。', detail: error })
    expect(describeRunFailure({ ...base, status: 'failed', failureReason: 'provider_timeout', error: 'Flow Agent provider is unavailable' }, zh, 'zh-CN')).toEqual({ detail: 'Flow Agent provider is unavailable' })
  })
})

describe('connector tool rows in the run log', () => {
  const hash = 'external_13e42ac3f0b8a2d1c4e5f60718293a4b'
  const call = { id: 'c1', name: hash, label: 'Used search_code in Code search', connector: 'Code search', remoteTool: 'search_code', status: 'completed' as const }

  it('carries the connector and tool names into the work-group parts', () => {
    const [part] = runParts({ steps: [], toolCalls: [call] })
    expect(part.toolCall).toMatchObject({ name: hash, title: 'Used search_code in Code search', connector: 'Code search', remoteTool: 'search_code' })
  })

  it('reads "Used <tool> in <connector>" in both languages', () => {
    expect(translateToolTitle(call.label, en, call)).toBe('Used search_code in Code search')
    expect(translateToolTitle(call.label, zh, call)).toBe('已在 Code search 中使用 search_code')
    expect(translateToolTitle('Used search_code', zh, { remoteTool: 'search_code' })).toBe('已使用 search_code')
  })

  it('shows a run stored without the names as a connector tool, never as its hash', () => {
    const stored = 'Used 13e42ac3f0b8a2d1c4e5f60718293a4b'
    expect(translateToolTitle(stored, en)).toBe('Used a connector tool')
    expect(translateToolTitle(stored, zh)).toBe('已使用连接器工具')
    expect(toolStatusLabel(hash, true, en)).toBe('Running a connector tool…')
    expect(toolStatusLabel(hash, false, zh)).toBe('已运行连接器工具')
  })
})
