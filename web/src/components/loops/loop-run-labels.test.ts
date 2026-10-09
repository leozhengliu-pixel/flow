import { describe, expect, it } from 'vitest'
import { translateToChinese } from '@/i18n/i18n'
import type { LoopRun } from '@/types/flow'
import { describeRunFailure, fixedFailureMessage, parseTriggerLabel, runTriggerText } from './loop-run-labels'

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

  it('names the failed change and keeps its error as the detail', () => {
    const run: LoopRun = {
      ...base, failureReason: 'tool_error', expectedOutputs: ['issue'], error: 'No output produced: expected a new issue, but Created issue failed: team not found',
      toolCalls: [{ name: 'list_issues', label: 'Listed issues', status: 'completed' }, { name: 'save_issue', label: 'Created issue', status: 'error', error: 'team not found' }],
    }
    expect(describeRunFailure(run, zh, 'zh-CN')).toEqual({ message: '预期产出一个新事项，但“已创建事项”失败了。', detail: 'team not found' })
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
