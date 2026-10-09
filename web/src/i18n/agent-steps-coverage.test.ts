import { describe, expect, it } from 'vitest'
import { AGENT_ERROR_MESSAGES } from '@/components/agent/agent-error-detail'
import { CHAT_TOOL_FALLBACK, CHAT_TOOL_LABELS, CHAT_TOOL_UPDATE_LABELS, CHAT_TOOL_VERBS, LOOP_TOOL_TEMPLATES, toolStatusLabel, translateToolTitle } from '@/components/agent/agent-step-labels'
// Kept equal to api/cmd/server/flow_mcp_tools.json by TestAgentToolNamesFixtureCurrent (Go).
import toolNames from '@/components/agent/agent-tool-names.json'
import { FAILURE_MESSAGES, OUTPUT_LABELS, TRIGGER_REASON_NONE, TRIGGER_REASON_TEXTS, triggerReasonTemplate } from '@/components/loops/loop-run-labels'
import { REASON_LABELS } from '@/components/loops/loop-run-status'
import { translateToChinese } from './i18n'
import { zhCN } from './translations'

const cjk = /[㐀-鿿]/
/** English words left in a translation, ignoring product names. */
const englishWords = (text: string) => text.replace(/\b(?:Agent|Loop|Flow|FLOW_LOOP_MAX_TOOL_CALLS|KB)\b/g, '').match(/[A-Za-z]{2,}/g) ?? []

function expectTranslated(source: string) {
  const translated = zhCN[source]
  expect(translated, source).toBeTruthy()
  expect(translated, source).toMatch(cjk)
  for (const placeholder of source.match(/\{\w+\}/g) ?? []) expect(translated, source).toContain(placeholder)
}

describe('agent step and loop run vocabulary', () => {
  it('translates every chat tool label, verb template and the fallback', () => {
    for (const pair of [...Object.values(CHAT_TOOL_LABELS), ...Object.values(CHAT_TOOL_UPDATE_LABELS), ...Object.values(CHAT_TOOL_VERBS), CHAT_TOOL_FALLBACK]) {
      for (const source of pair) expectTranslated(source)
    }
  })

  it('labels every Flow tool in Chinese, running and done, created and updated', () => {
    expect(toolNames.length).toBeGreaterThan(50)
    for (const name of toolNames) {
      for (const running of [true, false]) {
        for (const args of [undefined, { id: 'x' }]) {
          const label = toolStatusLabel(name, running, translateToChinese, args)
          expect(label, name).toMatch(cjk)
          expect(englishWords(label), `${name}: ${label}`).toEqual([])
        }
      }
    }
  })

  it('keeps unknown tools readable in Chinese and English', () => {
    expect(toolStatusLabel('github_search_code', false, translateToChinese)).toBe('已运行 github search code')
    expect(toolStatusLabel('github_search_code', true, source => source)).toBe('Running github search code…')
    expect(toolStatusLabel('list_issues', false, source => source)).toBe('Looked at issues')
    expect(toolStatusLabel('list_release_notes', true, source => source)).toBe('Looking at release notes…')
  })

  it('translates the server loop run labels, by entry or by verb template', () => {
    for (const template of LOOP_TOOL_TEMPLATES) expectTranslated(template)
    expect(translateToolTitle('Read project', translateToChinese)).toBe('已读取项目')
    expect(translateToolTitle('Listed release notes', translateToChinese)).toBe('已列出发布说明')
    expect(translateToolTitle('Used github search', translateToChinese)).toBe('已使用 github search')
    expect(translateToolTitle('Updated automation', translateToChinese)).toBe('已更新自动化')
    expect(translateToolTitle('Listed release notes', source => source)).toBe('Listed release notes')
  })

  it('translates every run trigger, output kind, failure message and agent error', () => {
    for (const source of ['Manual run', 'Manual run on {identifier}', 'Scheduled run', 'Triggered run', 'Triggered by {name}']) expectTranslated(source)
    for (const code of Object.keys(TRIGGER_REASON_TEXTS)) expectTranslated(triggerReasonTemplate(code))
    for (const source of Object.values(TRIGGER_REASON_NONE)) expectTranslated(source)
    for (const source of Object.values(OUTPUT_LABELS)) expectTranslated(source)
    for (const source of FAILURE_MESSAGES) expectTranslated(source)
    for (const source of Object.values(AGENT_ERROR_MESSAGES)) expectTranslated(source)
    for (const source of Object.values(REASON_LABELS)) expect(zhCN[source], source).toBeTruthy()
    for (const source of ["The loop's instructions call for {outputs}, but the run made none.", 'The agent reported there was nothing to do.', 'Expected {outputs}, but {tool} failed.', 'Details', 'Agent reasoning']) expectTranslated(source)
  })
})
