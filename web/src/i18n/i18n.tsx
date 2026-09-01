/* oxlint-disable react/only-export-components -- locale hooks and components share one provider contract. */
import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from 'react'

import { SelectControl } from '@/components/ui/select-control'

import { zhCN } from './translations'

export type AppLocale = 'en-US' | 'zh-CN'

type I18nValue = {
  locale: AppLocale
  setLocale: (locale: AppLocale) => void
  t: (source: string) => string
  formatDate: (value: Date | string | number, options?: Intl.DateTimeFormatOptions) => string
  formatNumber: (value: number, options?: Intl.NumberFormatOptions) => string
}

const STORAGE_KEY = 'flow:locale'
const I18nContext = createContext<I18nValue | null>(null)

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<AppLocale>(initialLocale)
  const setLocale = useCallback((next: AppLocale) => {
    localStorage.setItem(STORAGE_KEY, next)
    setLocaleState(next)
  }, [])
  const t = useCallback((source: string) => locale === 'zh-CN' ? translateToChinese(source) : source, [locale])
  const value = useMemo<I18nValue>(() => ({
    locale,
    setLocale,
    t,
    formatDate: (input, options) => new Intl.DateTimeFormat(locale, options).format(new Date(input)),
    formatNumber: (input, options) => new Intl.NumberFormat(locale, options).format(input),
  }), [locale, setLocale, t])

  useEffect(() => {
    document.documentElement.lang = locale === 'zh-CN' ? 'zh-CN' : 'en'
    document.documentElement.dataset.locale = locale
  }, [locale])

  return <I18nContext.Provider value={value}>{children}<LegacyUiTranslator locale={locale}/></I18nContext.Provider>
}

export function useI18n() {
  const value = useContext(I18nContext)
  if (!value) throw new Error('useI18n must be used inside I18nProvider')
  return value
}

export function LanguageSelect({ className }: { className?: string }) {
  const { locale, setLocale, t } = useI18n()
  return <label className={className} data-i18n-control data-i18n-ignore>
    <span>{t('Language')}</span>
    <SelectControl
      className="language-select-control"
      label={t('Language')}
      onChange={next => setLocale(next as AppLocale)}
      options={[
        { value: 'en-US', label: 'English' },
        { value: 'zh-CN', label: '简体中文' },
      ]}
      value={locale}
    />
  </label>
}

function initialLocale(): AppLocale {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (stored === 'zh-CN' || stored === 'en-US') return stored
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US'
}

export function translateToChinese(source: string): string {
  if (!source || !/[A-Za-z]/.test(source)) return source
  const leading = source.match(/^\s*/)?.[0] ?? ''
  const trailing = source.match(/\s*$/)?.[0] ?? ''
  const text = source.trim()
  const exact = zhCN[text]
  if (exact) return `${leading}${exact}${trailing}`

  const patterns: Array<[RegExp, (...groups: string[]) => string]> = [
    [/^(\d+) issues?$/, count => `${count} 个事项`],
    [/^(\d+) projects?$/, count => `${count} 个项目`],
    [/^(\d+) initiatives?$/, count => `${count} 个目标`],
    [/^(\d+) labeled (issues?|projects?|initiatives?)$/, (count, resource) => `${count} 个带此标签的${countNoun(resource)}`],
    [/^(\d+) selected (issues?|projects?|labels?|members?|teams?|files?)$/, (count, resource) => `已选择 ${count} ${countResource(resource)}`],
    [/^(\d+) members?$/, count => `${count} 位成员`],
    [/^(\d+) teams?$/, count => `${count} 个团队`],
    [/^(\d+) requests?$/, count => `${count} 条需求`],
    [/^(\d+) comments?$/, count => `${count} 条评论`],
    [/^(\d+) updates?$/, count => `${count} 条更新`],
    [/^(\d+) notifications?$/, count => `${count} 条通知`],
    [/^(\d+) active connections across workspace members$/, count => `工作区成员共有 ${count} 个活跃连接`],
    [/^(\d+) pull requests?$/, count => `${count} 个合并请求`],
    [/^(\d+) reactions?$/, count => `${count} 个反应`],
    [/^(\d+) release pipelines?$/, count => `${count} 个发布流水线`],
    [/^(\d+) reviews?$/, count => `${count} 个评审`],
    [/^(\d+) filters?$/, count => `${count} 个筛选条件`],
    [/^(\d+) files? selected$/, count => `已选择 ${count} 个文件`],
    [/^(\d+) invitations? sent$/, count => `已发送 ${count} 份邀请`],
    [/^(\d+) other (?:person|people) viewing$/, count => `其他 ${count} 人正在查看`],
    [/^(\d+) drafts?$/, count => `${count} 份草稿`],
    [/^(\d+) releases?$/, count => `${count} 个发布版本`],
    [/^(\d+) views?$/, count => `${count} 个视图`],
    [/^(\d+) cycles?$/, count => `${count} 个周期`],
    [/^(\d+) loops?$/, count => `${count} 个 Loops`],
    [/^(\d+) characters?$/, count => `${count} 个字符`],
    [/^(\d+) rows?$/, count => `${count} 行`],
    [/^(\d+) warnings?$/, count => `${count} 条警告`],
    [/^(\d+) imported$/, count => `已导入 ${count} 条`],
    [/^(\d+) imported · (\d+) warnings?$/, (imported, warnings) => `已导入 ${imported} 条 · ${warnings} 条警告`],
    [/^aiCredits: (\d+) \/ (\d+)$/, (used, total) => `AI 额度：${used} / ${total}`],
    [/^(\d+) projects? · (\d+) issues?$/, (projects, issues) => `${projects} 个项目 · ${issues} 个事项`],
    [/^(\d+) (seconds?|minutes?|hours?|days?|weeks?|months?|years?)$/, (count, unit) => `${count}${durationUnit(unit)}`],
    [/^(\d+) weekdays? left$/, count => `剩余 ${count} 个工作日`],
    [/^(\d+)% success$/, count => `${count}% 完成率`],
    [/^(\d+) scope$/, count => `${count} 个范围项`],
    [/^(\d+) completed$/, count => `已完成 ${count} 项`],
    [/^Assigned, (\d+) issues?$/, count => `分配给我，${count} 个事项`],
    [/^(\d+) of (\d+) sub-issues completed$/, (completed, total) => `已完成 ${completed}/${total} 个子事项`],
    [/^(\d+) of (\d+) projects? completed\. Click to view projects\.$/, (completed, total) => `已完成 ${completed}/${total} 个项目。点击查看项目。`],
    [/^(\d+) projects? need an update\. Click to open updates\.$/, count => `${count} 个项目需要更新。点击打开更新。`],
    [/^(\d+)% project progress$/, count => `项目进度 ${count}%`],
    [/^(\d+)% of$/, count => `已完成 ${count}%，共`],
    [/^No milestone (\d+) issues?$/, count => `无里程碑，${count} 个事项`],
    [/^View (\d+) issues in (.+)$/, (count, milestone) => `查看 ${milestone} 中的 ${count} 个事项`],
    [/^Delete (\d+) selected projects?\?$/, count => `删除已选择的 ${count} 个项目吗？`],
    [/^Import (\d+) issues?$/, count => `导入 ${count} 个事项`],
    [/^(\d+) unmatched values$/, count => `${count} 个未匹配值`],
    [/^and (\d+) more$/, count => `以及另外 ${count} 项`],
    [/^(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) (\d{1,2})(?:, (\d{4}))?$/, (month, day, year) => `${year ? `${year}年` : ''}${shortMonth(month)}月${day}日`],
    [/^Activity, (\d+) issues?$/, count => `动态，${count} 个事项`],
    [/^Select issue(?: (.+))?$/, detail => `选择事项${detail ? `：${translateEmbedded(detail)}` : ''}`],
    [/^Change status\. Current status is (.+)$/, status => `更改状态。当前状态为${translateNoun(status)}`],
    [/^Change labels\. (.+) selected$/, labels => `更改标签。已选择 ${labels}`],
    [/^Change labels\. (.+)$/, labels => `更改标签。${labels}`],
    [/^Change project\. Current project is (.+)$/, project => `更改项目。当前项目为 ${project}`],
    [/^(No Priority|Urgent|High|Medium|Low) Priority$/, priority => `${translateNoun(priority)}优先级`],
    [/^Assign to\. Current assignee is (.+)$/, name => `指派负责人。当前负责人是 ${name}`],
    [/^Change priority\. No priority is selected$/, () => '更改优先级。当前未设置优先级'],
    [/^Change assignee\. (.+) is assigned$/, name => `更改负责人。当前负责人是 ${name}`],
    [/^Change project target date$/, () => '更改项目目标日期'],
    [/^Create (issue|project) view in (.+)$/, (resource, name) => `在 ${name} 中创建${resource === 'issue' ? '事项' : '项目'}视图`],
    [/^Save to (.+)$/, name => `保存到 ${name}`],
    [/^Add to (.+)$/, resource => `添加到${translateNoun(resource)}`],
    [/^Open (.+) menu$/, name => `打开 ${name} 菜单`],
    [/^Open (.+) issues$/, name => `打开 ${name} 的事项`],
    [/^No updates\. Click to open updates\.$/, () => '没有更新。点击打开更新。'],
    [/^Paused · (.+)$/, value => `已暂停 · ${translateDuration(value)}`],
    [/^(\d+h(?: \d+m)?|\d+d)$/, value => translateDuration(value)],
    [/^about (\d+) hours? ago$/, count => `大约 ${count} 小时前`],
    [/^about (\d+) minutes? ago$/, count => `大约 ${count} 分钟前`],
    [/^(\d+) hours? ago$/, count => `${count} 小时前`],
    [/^(\d+) minutes? ago$/, count => `${count} 分钟前`],
    [/^First response · (.+)$/, value => `首次响应 · ${translateDuration(value)}`],
    [/^Updates from (.+) in your workspace will show here\.$/, sourceName => `工作区中来自${translateNoun(sourceName)}的更新会显示在这里。`],
    [/^(.+) assigned$/, name => `${name} 已指派`],
    [/^set to (.+)$/, value => `设为 ${value}`],
    [/^Current version · (.+)$/, value => `当前版本 · ${value}`],
    [/^(.+) Workspace Menu$/, name => `${name} 工作区菜单`],
    [/^Delete [“"](.+)[”"]\?$/, name => `删除“${name}”吗？`],
    [/^Delete (.+)\?$/, name => `删除 ${name} 吗？`],
    [/^Leave (.+)\?$/, name => `离开 ${name}？`],
    [/^Retire (.+)\?$/, name => `停用 ${name}？`],
    [/^Restore (.+)\?$/, name => `恢复 ${name}？`],
    [/^Connected as (.+)$/, name => `连接身份：${name}`],
    [/^Next (.+)$/, value => `下次：${value}`],
    [/^(.+) cadence$/, () => '更改重复频率'],
    [/^This permanently deletes (.+) and all of its data\.$/, name => `这将永久删除 ${name} 及其所有数据。`],
    [/^Open (.+)$/, name => `打开 ${name}`],
    [/^Search (.+)$/, resource => `搜索${translateNoun(resource)}`],
    [/^Search (.+)…$/, resource => `搜索${translateNoun(resource)}…`],
    [/^New (.+)$/, resource => `新建${translateNoun(resource)}`],
    [/^Create (.+)$/, resource => `创建${translateNoun(resource)}`],
    [/^Create (.+)…$/, resource => `创建${translateNoun(resource)}…`],
    [/^Add (.+)$/, resource => `添加${translateNoun(resource)}`],
    [/^Remove (.+)$/, resource => `移除${translateNoun(resource)}`],
    [/^Change (.+)$/, resource => `更改${translateNoun(resource)}`],
    [/^No (.+)$/, resource => `没有${translateNoun(resource)}`],
    [/^Role for (.+)$/, name => `${name} 的角色`],
    [/^(.+) mapping action$/, name => `${name} 的映射操作`],
    [/^Select target for (.+)$/, name => `为 ${name} 选择目标`],
    [/^Actions for (.+)$/, name => `${name} 的操作`],
    [/^Saved by (.+)$/, name => `由 ${name} 保存`],
    [/^Created by (.+)$/, name => `由 ${name} 创建`],
    [/^(.+) · deleted by (.+)$/, (resource, name) => `${translateNoun(resource)} · 删除者 ${name}`],
    [/^Edited (.+)$/, value => `编辑于 ${value}`],
    [/^(.+) ago$/, value => `${translateEnglishDuration(value)}前`],
    [/^(.+) (restored release|deleted release|completed export|queued export|updated project update settings|approved ask|created ask|created sla rule|created release|created customer request|revision restored document|updated document|created document|created project template)$/, (actor, action) => `${actor} ${translateAuditAction(action)}`],
    [/^(.+) hidden by filters$/, count => `筛选条件隐藏了 ${count} 条`],
  ]
  for (const [pattern, render] of patterns) {
    const match = text.match(pattern)
    if (match) return `${leading}${render(...match.slice(1))}${trailing}`
  }
  return source
}

function translateNoun(value: string) {
  const normalized = value ? value[0].toUpperCase() + value.slice(1) : value
  return zhCN[value] ?? zhCN[normalized] ?? zhCN[normalized.replace(/s$/, '')] ?? value
}

function translateEmbedded(value: string) {
  return value
    .replace(/\bNo Priority\b/g, '无优先级')
    .replace(/\bUrgent\b/g, '紧急')
    .replace(/\bHigh\b/g, '高')
    .replace(/\bMedium\b/g, '中')
    .replace(/\bLow\b/g, '低')
}

function translateDuration(value: string) {
  return value
    .replace(/\bPaused\b/g, '已暂停')
    .replace(/(\d+)d\b/g, '$1天')
    .replace(/(\d+)h\b/g, '$1小时')
    .replace(/(\d+)m\b/g, '$1分钟')
}

function translateEnglishDuration(value: string) {
  return value
    .replace(/\bless than a minute\b/g, '不到 1 分钟')
    .replace(/\babout\s+/g, '约 ')
    .replace(/(\d+)\s*seconds?\b/g, '$1 秒')
    .replace(/(\d+)\s*minutes?\b/g, '$1 分钟')
    .replace(/(\d+)\s*hours?\b/g, '$1 小时')
    .replace(/(\d+)\s*days?\b/g, '$1 天')
    .replace(/(\d+)\s*weeks?\b/g, '$1 周')
    .replace(/(\d+)\s*months?\b/g, '$1 个月')
    .replace(/(\d+)\s*years?\b/g, '$1 年')
}

function durationUnit(value: string) {
  if (value.startsWith('second')) return ' 秒'
  if (value.startsWith('minute')) return ' 分钟'
  if (value.startsWith('hour')) return ' 小时'
  if (value.startsWith('day')) return ' 天'
  if (value.startsWith('week')) return ' 周'
  if (value.startsWith('month')) return ' 个月'
  return ' 年'
}

function countResource(value: string) {
  const resource = value.replace(/s$/, '')
  if (resource === 'issue') return '个事项'
  if (resource === 'project') return '个项目'
  if (resource === 'initiative') return '个目标'
  if (resource === 'label') return '个标签'
  if (resource === 'member') return '位成员'
  if (resource === 'team') return '个团队'
  return '个文件'
}

function countNoun(value: string) {
  const resource = value.replace(/s$/, '')
  if (resource === 'issue') return '事项'
  if (resource === 'project') return '项目'
  return '目标'
}

function translateAuditAction(value: string) {
  const actions: Record<string, string> = {
    'restored release': '恢复了发布版本',
    'deleted release': '删除了发布版本',
    'completed export': '完成了导出',
    'queued export': '创建了导出任务',
    'updated project update settings': '更新了项目更新设置',
    'approved ask': '批准了请求',
    'created ask': '创建了请求',
    'created sla rule': '创建了 SLA 规则',
    'created release': '创建了发布版本',
    'created customer request': '创建了客户需求',
    'revision restored document': '恢复了文档版本',
    'updated document': '更新了文档',
    'created document': '创建了文档',
    'created project template': '创建了项目模板',
  }
  return actions[value] ?? value
}

function shortMonth(value: string) {
  return String(['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'].indexOf(value) + 1)
}

type TextState = { source: string; rendered: string }
const textStates = new WeakMap<Text, TextState>()
const attributeStates = new WeakMap<Element, Map<string, TextState>>()
const attributes = ['aria-label', 'placeholder', 'title', 'data-placeholder'] as const

function LegacyUiTranslator({ locale }: { locale: AppLocale }) {
  useLayoutEffect(() => {
    const translateTree = (root: ParentNode) => {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
      let node: Node | null
      while ((node = walker.nextNode())) translateTextNode(node as Text, locale)
      if (root instanceof Element) translateAttributes(root, locale)
      root.querySelectorAll?.('*').forEach(element => translateAttributes(element, locale))
    }
    translateTree(document.body)
    const observer = new MutationObserver(records => {
      for (const record of records) {
        if (record.type === 'characterData') translateTextNode(record.target as Text, locale)
        if (record.type === 'attributes') translateAttributes(record.target as Element, locale)
        record.addedNodes.forEach(node => {
          if (node.nodeType === Node.TEXT_NODE) translateTextNode(node as Text, locale)
          else if (node instanceof Element) translateTree(node)
        })
      }
    })
    observer.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: [...attributes] })
    return () => observer.disconnect()
  }, [locale])
  return null
}

function translateTextNode(node: Text, locale: AppLocale) {
  if (shouldIgnore(node.parentElement)) return
  const current = node.data
  const previous = textStates.get(node)
  const source = previous && (current === previous.source || current === previous.rendered) ? previous.source : current
  const rendered = locale === 'zh-CN' ? translateToChinese(source) : source
  textStates.set(node, { source, rendered })
  if (current !== rendered) node.data = rendered
}

function translateAttributes(element: Element, locale: AppLocale) {
  if (shouldIgnore(element)) return
  let states = attributeStates.get(element)
  if (!states) {
    states = new Map()
    attributeStates.set(element, states)
  }
  for (const attribute of attributes) {
    const current = element.getAttribute(attribute)
    if (current == null) continue
    const previous = states.get(attribute)
    const source = previous && (current === previous.source || current === previous.rendered) ? previous.source : current
    const rendered = locale === 'zh-CN' ? translateToChinese(source) : source
    states.set(attribute, { source, rendered })
    if (current !== rendered) element.setAttribute(attribute, rendered)
  }
}

function shouldIgnore(element: Element | null) {
  if (!element) return true
  return Boolean(element.closest('[data-i18n-ignore], [contenteditable="true"], .ProseMirror, .tiptap, script, style, code'))
}
