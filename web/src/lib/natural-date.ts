import { parse, zh } from 'chrono-node'
import { addMonths, addYears } from 'date-fns'

/** Shared typed dates for reminders, snooze, calendars and command pages. */
export function parseNaturalDateInput(input: string, now = new Date(), options: { defaultHour?: number; forwardDate?: boolean } = {}): Date | undefined {
  const text = input.trim()
  if (!text) return
  const normalized = normalizeChineseDate(text)
  const calendarOffset = normalized.match(/^(?:in\s+)?(\d+)\s*(months?|years?)(?:\s+from now)?$/i)
  if (calendarOffset) return calendarOffset[2].toLowerCase().startsWith('month') ? addMonths(now, Number(calendarOffset[1])) : addYears(now, Number(calendarOffset[1]))
  const relative = /^(?:in\s+)?(?:\d+|an?|one)\s*(?:minutes?|mins?|hours?|hrs?|h|days?|d|weeks?|w|months?|years?)(?:\s+from now)?$/i.test(normalized)
  const results = /[\u3400-\u9fff]/.test(normalized) ? zh.hans.parse(normalized, now, { forwardDate: options.forwardDate ?? true }) : parse(normalized, now, { forwardDate: options.forwardDate ?? true })
  const result = results[0]
  if (!result || result.index !== 0 || result.text.length !== normalized.length) return
  const date = result.start.date()
  if (options.defaultHour !== undefined && !relative && !result.start.isCertain('hour')) date.setHours(options.defaultHour, 0, 0, 0)
  return Number.isFinite(date.getTime()) ? date : undefined
}

function normalizeChineseDate(value: string) {
  const aliases: Record<string, string> = { 今天: 'today', 明天: 'tomorrow', 后天: 'in 2 days', 下周: 'next Monday', 下星期: 'next Monday', 下个月: 'next month', 下月: 'next month', 明年: 'next year' }
  if (aliases[value]) return aliases[value]
  const relative = value.match(/^([零一二两三四五六七八九十百\d]+)\s*(分钟|小时|天|日|周|星期|个月|月|年)\s*(后|前)$/)
  if (!relative) return value
  const amount = chineseNumber(relative[1])
  if (amount === undefined) return value
  const unit = ({ 分钟: 'minutes', 小时: 'hours', 天: 'days', 日: 'days', 周: 'weeks', 星期: 'weeks', 个月: 'months', 月: 'months', 年: 'years' } as Record<string, string>)[relative[2]]
  return relative[3] === '后' ? `in ${amount} ${unit}` : `${amount} ${unit} ago`
}

function chineseNumber(value: string) {
  if (/^\d+$/.test(value)) return Number(value)
  const digits = '零一二三四五六七八九'
  let total = 0, digit = 0
  for (const char of value.replaceAll('两', '二')) {
    const index = digits.indexOf(char)
    if (index >= 0) digit = index
    else if (char === '十' || char === '百') { total += (digit || 1) * (char === '十' ? 10 : 100); digit = 0 }
    else return
  }
  return total + digit
}
