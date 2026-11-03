/**
 * The English source text is the key; Simplified Chinese strings live in the large `./translations`
 * dictionary, which is loaded on demand so English sessions never download it.
 */
let zhCN: Record<string, string> = {}
let dictionaryLoaded = false
let dictionaryLoad: Promise<void> | null = null

export function isChineseDictionaryLoaded() {
  return dictionaryLoaded
}

export function loadChineseDictionary(): Promise<void> {
  dictionaryLoad ??= import('./translations').then(module => {
    zhCN = module.zhCN
    dictionaryLoaded = true
  }, error => {
    dictionaryLoad = null
    throw error
  })
  return dictionaryLoad
}

export function translateToChinese(source: string): string {
  if (!source || !/[A-Za-z]/.test(source)) return source
  const leading = source.match(/^\s*/)?.[0] ?? ''
  const trailing = source.match(/\s*$/)?.[0] ?? ''
  const text = source.trim()
  const exact = lookup(text)
  if (exact) return `${leading}${exact}${trailing}`

  // A renderer returns null when it cannot translate the whole phrase: the next pattern is tried and, failing
  // all of them, the English phrase stays whole instead of turning into a mix of both languages.
  const patterns: Array<[RegExp, (...groups: string[]) => string | null]> = [
    [/^(\d+) issues?$/, count => `${count} 个事项`],
    // Typed date filter durations ("10 days", "2 weeks ago", "3 months from now").
    [/^(\d+) (day|week|month|year)s?( ago| from now)?$/, (count, unit, suffix) => { const amount = `${count} ${({ day: '天', week: '周', month: '个月', year: '年' } as Record<string, string>)[unit]}`; return suffix === ' ago' ? `${amount}前` : suffix ? `从现在起 ${amount}` : amount }],
    [/^New issue in triage from (.+)$/, actor => `${actor} 提交了新的分流事项`],
    [/^(\d+) upcoming cycles?$/, count => `${count} 个后续周期`],
    [/^(\d+) identity providers? configured$/, count => `已配置 ${count} 个身份提供商`],
    [/^(\d+) active$/, count => `${count} 项已启用`],
    [/^(\d+) projects?$/, count => `${count} 个项目`],
    [/^(\d+) documents?$/, count => `${count} 篇文档`],
    [/^Page (\d+) of (\d+)$/, (page, total) => `第 ${page} 页，共 ${total} 页`],
    [/^Page (\d+)$/, page => `第 ${page} 页`],
    [/^(\d+) dependencies?$/, count => `${count} 个依赖项`],
    [/^(\d+) initiatives?$/, count => `${count} 个目标`],
    [/^(\d+) labeled (issues?|projects?|initiatives?)$/, (count, resource) => `${count} 个带此标签的${countNoun(resource)}`],
    [/^(\d+) selected (issues?|projects?|labels?|members?|teams?|files?)$/, (count, resource) => `已选择 ${count} ${countResource(resource)}`],
    [/^(\d+) members?$/, count => `${count} 位成员`],
    [/^(\d+) teams?$/, count => `${count} 个团队`],
    [/^(\d+) requests?$/, count => `${count} 条需求`],
    [/^(\d+) comments?$/, count => `${count} 条评论`],
    [/^(\d+) updates?$/, count => `${count} 条更新`],
    [/^(\d+) notifications?$/, count => `${count} 条通知`],
    [/^(\d+) status types?$/, count => `${count} 种状态类型`],
    [/^(\d+) priorities?$/, count => `${count} 种优先级`],
    [/^(\d+) review statuses?$/, count => `${count} 种评审状态`],
    [/^(\d+) notification types?$/, count => `${count} 种通知类型`],
    [/^(\d+) (?:person|people)$/, count => `${count} 人`],
    [/^(\d+) active connections across workspace members$/, count => `工作区成员共有 ${count} 个活跃连接`],
    [/^(\d+) pull requests?$/, count => `${count} 个合并请求`],
    [/^(\d+) reactions?$/, count => `${count} 个反应`],
    [/^(\d+) release pipelines?$/, count => `${count} 个发布管线`],
    [/^(\d+) reviews?$/, count => `${count} 个评审`],
    [/^(\d+) filters?$/, count => `${count} 个筛选条件`],
    [/^(\d+) (statuses|labels|assignees|creators|subscribers|agents|milestones|customers|templates|relations|values)$/, (count, noun) => `${count} 个${({ statuses: '状态', labels: '标签', assignees: '负责人', creators: '创建者', subscribers: '订阅者', agents: ' Agent', milestones: '里程碑', customers: '客户', templates: '模板', relations: '关系', values: '值' } as Record<string, string>)[noun]}`],
    [/^(\d+) files? selected$/, count => `已选择 ${count} 个文件`],
    [/^(\d+) invitations? sent$/, count => `已发送 ${count} 份邀请`],
    [/^(\d+) other (?:person|people) viewing$/, count => `其他 ${count} 人正在查看`],
    [/^(\d+) drafts?$/, count => `${count} 份草稿`],
    [/^(\d+) releases?$/, count => `${count} 个发布版本`],
    [/^(\d+) views?$/, count => `${count} 个视图`],
    [/^(Issues|Projects) matching (\d+) filters?$/, (resource, count) => `${resource === 'Issues' ? '事项' : '项目'}符合 ${count} 个筛选条件`],
    [/^All (issues|projects)$/, resource => resource === 'issues' ? '所有事项' : '所有项目'],
    [/^(\d+) cycles?$/, count => `${count} 个周期`],
    [/^(\d+) loops?$/, count => `${count} 个 Loops`],
    [/^(\d+) characters?$/, count => `${count} 个字符`],
    [/^(\d+) rows?$/, count => `${count} 行`],
    [/^(\d+) warnings?$/, count => `${count} 条警告`],
    [/^(\d+) imported$/, count => `已导入 ${count} 条`],
    [/^(\d+) imported · (\d+) warnings?$/, (imported, warnings) => `已导入 ${imported} 条 · ${warnings} 条警告`],
    [/^(\d+) projects? · (\d+) issues?$/, (projects, issues) => `${projects} 个项目 · ${issues} 个事项`],
    [/^(\d+) (seconds?|minutes?|hours?|days?|weeks?|months?|years?)$/, (count, unit) => `${count}${durationUnit(unit)}`],
    [/^(\d+) weekdays? left$/, count => `剩余 ${count} 个工作日`],
    [/^(\d+)% success$/, count => `${count}% 完成率`],
    [/^(\d+)% progress$/, count => `进度 ${count}%`],
    [/^(\d+) issues? total$/, count => `共 ${count} 个事项`],
    [/^(\d+) issues? started$/, count => `${count} 个事项已开始`],
    [/^(\d+) issues? completed$/, count => `${count} 个事项已完成`],
    [/^(\d+) scope$/, count => `${count} 个范围项`],
    [/^(\d+) completed$/, count => `已完成 ${count} 项`],
    [/^Assigned, (\d+) issues?$/, count => `分配给我，${count} 个事项`],
    [/^(\d+) of (\d+) sub-issues completed$/, (completed, total) => `已完成 ${completed}/${total} 个子事项`],
    [/^(\d+) of (\d+) projects? completed\. Click to view projects\.$/, (completed, total) => `已完成 ${completed}/${total} 个项目。点击查看项目。`],
    [/^(\d+) projects? need an update\. Click to open updates\.$/, count => `${count} 个项目需要更新。点击打开更新。`],
    [/^(\d+)% project progress$/, count => `项目进度 ${count}%`],
    // Sits in a 51px column in front of the count button, so it stays as short as "100% of".
    [/^(\d+)% of$/, count => `${count}% 共`],
    [/^(.+) (\d+)% of (\d+)$/, (label, percent, count) => `${label} 已完成 ${percent}%，共 ${count}`],
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
    [/^(No|No Priority|Urgent|High|Medium|Low) Priority$/, priorityPhrase],
    [/^Assign to\. Current assignee is (.+)$/, name => `指派负责人。当前负责人是 ${name}`],
    [/^Change priority\. No priority is selected$/, () => '更改优先级。当前未设置优先级'],
    [/^Change assignee\. (.+) is assigned$/, name => `更改负责人。当前负责人是 ${name}`],
    [/^Change assignee\. (.+) is delegated$/, name => `更改负责人。已委托给 ${name}`],
    [/^Change project target date$/, () => '更改项目目标日期'],
    // Property pickers name their field and current value: "Change Status. Current value is Backlog".
    [/^Change (.+)\. Current value is (.+)$/, (field, value) => withNoun(field, noun => `更改${noun}。当前值为${value === 'none' ? '无' : translateNoun(value)}`)],
    [/^Change (.+)\. (.+) is selected$/, (field, value) => withNoun(field, noun => `更改${noun}。已选择${translateNoun(value)}`)],
    [/^Change or add labels\. (.+) selected$/, labels => `更改或添加标签。已选择 ${labels}`],
    [/^Change (.+)\. (.+) selected$/, (field, value) => withNoun(field, noun => `更改${noun}。已选择 ${value}`)],
    [/^Open sub-issues of (.+)$/, identifier => `打开 ${identifier} 的子事项`],
    [/^Create (issue|project) view in (.+)$/, (resource, name) => `在 ${name} 中创建${resource === 'issue' ? '事项' : '项目'}视图`],
    [/^Save to (.+)$/, name => `保存到 ${name}`],
    [/^Add to (.+)$/, resource => withNoun(resource, noun => `添加到${noun}`)],
    [/^Open (\d+) comments?$/, count => `打开 ${count} 条评论`],
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
    [/^(.+) retired$/, name => `${name} 已停用`],
    // Retire team form summaries and results.
    [/^(\d+) issues? will be canceled$/, count => `${count} 个事项将被取消`],
    [/^(\d+) issues? will be moved to selected team$/, count => `${count} 个事项将被移至所选团队`],
    [/^(\d+) issues? will be moved to (.+)$/, (count, team) => `${count} 个事项将被移至 ${team}`],
    [/^(\d+) sub-teams? will be retired$/, count => `${count} 个子团队将被停用`],
    [/^(\d+) direct sub-teams? will be removed from parent$/, count => `${count} 个直属子团队将从上级团队中移除`],
    [/^(Canceled|Moved) (\d+) issues?, (\d+) failed$/, (verb, success, failed) => `${verb === 'Canceled' ? '已取消' : '已移动'} ${success} 个事项，${failed} 个失败`],
    // Row and picker aria-labels built from data.
    [/^Project actions for (.+)$/, name => `${name} 的项目操作`],
    [/^(Group ordering|Ordering direction): (ascending|descending)$/, (field, direction) => `${field === 'Group ordering' ? '分组排序' : '排序方向'}：${direction === 'ascending' ? '升序' : '降序'}`],
    [/^(Created|Updated|My activity|Joined) (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) (\d{1,2}), (\d{1,2}):(\d{2}):(\d{2}) (AM|PM)$/, (kind, month, day, hour, minute, second, meridiem) => `${({ Created: '创建于', Updated: '更新于', 'My activity': '我的活动：', Joined: '加入于' } as Record<string, string>)[kind]} ${shortMonth(month)}月${day}日 ${meridiem === 'AM' ? '上午' : '下午'}${hour}:${minute}:${second}`],
    [/^Open team (.+)$/, name => `打开团队 ${name}`],
    [/^Sub-issue of (.+)$/, identifier => `子事项隶属于 ${identifier}`],
    [/^Order by (.+), sorted (ascending|descending)$/, (field, direction) => withNoun(field, noun => `按${noun}排序，${direction === 'ascending' ? '升序' : '降序'}`)],
    [/^Restore (.+)\?$/, name => `恢复 ${name}？`],
    [/^Connected as (.+)$/, name => `连接身份：${name}`],
    [/^Next (.+)$/, value => `下次：${value}`],
    [/^(.+) cadence$/, () => '更改重复频率'],
    [/^This permanently deletes (.+) and all of its data\.$/, name => `这将永久删除 ${name} 及其所有数据。`],
    // A keyboard hint after a phrase ("Open details (⌘I)") keeps the hint and translates the phrase.
    [/^(.+?) (\([⌘⇧⌥⌃][^)]*\)|\((?:Ctrl|Alt|Shift|Cmd|Esc)[^)]*\))$/, (phrase, hint) => { const rendered = translateToChinese(phrase); return rendered === phrase ? null : `${rendered} ${hint}` }],
    // Catch-all verb phrases: only a noun with its own entry is translated, anything else stays whole English.
    [/^Open (.+)$/, name => withNoun(name, noun => `打开 ${noun}`)],
    [/^Search (.+)$/, resource => withNoun(resource, noun => `搜索${noun}`)],
    [/^New (.+)$/, resource => withNoun(resource, noun => `新建${noun}`)],
    [/^Create (.+)$/, resource => withNoun(resource, noun => `创建${noun}`)],
    [/^Add (.+)$/, resource => withNoun(resource, noun => `添加${noun}`)],
    [/^Remove (.+)$/, resource => withNoun(resource, noun => `移除${noun}`)],
    [/^Change (.+)$/, resource => withNoun(resource, noun => `更改${noun}`)],
    [/^No (.+)$/, resource => withNoun(resource, noun => `没有${noun}`)],
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
    if (!match) continue
    const rendered = render(...match.slice(1))
    if (rendered != null) return `${leading}${rendered}${trailing}`
  }
  return source
}

/** Exact dictionary lookup that treats `...` and `…` as the same ending. */
function lookup(text: string) {
  if (Object.hasOwn(zhCN, text)) return zhCN[text]
  const swapped = text.endsWith('...') ? `${text.slice(0, -3)}…` : text.endsWith('…') ? `${text.slice(0, -1)}...` : ''
  return swapped && Object.hasOwn(zhCN, swapped) ? zhCN[swapped] : undefined
}

/** A noun phrase ("status", "Sub-issues", "labels…") with a zh-CN entry, or null. Issue identifiers stay as they are. */
function knownNoun(value: string): string | null {
  const ellipsis = /\s*(?:\.\.\.|…)$/.exec(value)
  const base = ellipsis ? value.slice(0, ellipsis.index) : value
  const normalized = base ? base[0].toUpperCase() + base.slice(1) : base
  const found = [base, normalized, normalized.replace(/s$/, '')].map(lookup).find(Boolean) ?? (/^[A-Z][A-Z0-9]*-\d+$/.test(base) ? base : undefined)
  return found == null ? null : `${found}${ellipsis ? '…' : ''}`
}

/** Renders a catch-all phrase around a known noun. An unknown noun gives null so the phrase is never half translated. */
function withNoun(value: string, render: (noun: string) => string): string | null {
  const noun = knownNoun(value)
  return noun == null ? null : render(noun)
}

function translateNoun(value: string) {
  const normalized = value ? value[0].toUpperCase() + value.slice(1) : value
  return lookup(value) ?? lookup(normalized) ?? lookup(normalized.replace(/s$/, '')) ?? value
}

function translateEmbedded(value: string) {
  // "Urgent Priority RVI-23 Title": only the leading priority label is translated, as one phrase; the title is data.
  return value.replace(/^(No Priority|No|Urgent|High|Medium|Low) Priority\b/, (_, priority: string) => priorityPhrase(priority))
}

/** The label rows build as `${name} Priority`: "No Priority" arrives as "No" + "Priority". */
function priorityPhrase(priority: string) {
  return priority === 'No' || priority === 'No Priority' ? '无优先级' : `${translateNoun(priority)}优先级`
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
