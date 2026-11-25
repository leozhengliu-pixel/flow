import { datePeriod, type DatePeriod } from './issue-date-filter'

/** A custom day (with its on / before / after comparison) or a calendar period. */
export type DateFilterTarget = { date: string; period?: undefined } | { period: DatePeriod; date?: undefined }

export const MONTH_NAMES = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December']
export const MONTH_SHORT = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

export function pad(value: number) { return String(value).padStart(2, '0') }
export function dayString(year: number, month: number, day: number) { return `${year}-${pad(month)}-${pad(day)}` }

/**
 * What the dialog's search field understands, like the reference app's "Try: May 2027, Q4, 05/20/2027":
 * 05/20/2027, 5/20, 2027-05-20, 2027/05/20, May 2027, May, 2027-05, Q4, Q4 2027, 2027 Q4, H2, H2 2027, 2027, and the Chinese
 * 2027年5月20日, 2027年5月, 2027 年第 4 季度, 2027年下半年, 2027年.
 */
export function parseDateFilterInput(text: string, now = Date.now()): DateFilterTarget | undefined {
  const input = text.trim().toLowerCase().replace(/\s+/g, ' ')
  if (!input) return
  const thisYear = new Date(now).getFullYear()
  const valid = (year: number, month: number, day: number) => {
    if (month < 1 || month > 12 || day < 1) return
    const date = new Date(Date.UTC(year, month - 1, day))
    return date.getUTCMonth() === month - 1 ? { date: dayString(year, month, day) } : undefined
  }
  let match = input.match(/^(\d{1,2})\/(\d{1,2})(?:\/(\d{4}))?$/)
  if (match) return valid(match[3] ? Number(match[3]) : thisYear, Number(match[1]), Number(match[2]))
  if ((match = input.match(/^(\d{4})[-/](\d{1,2})[-/](\d{1,2})$/))) return valid(Number(match[1]), Number(match[2]), Number(match[3]))
  if ((match = input.match(/^(\d{4})年(\d{1,2})月(\d{1,2})日?$/))) return valid(Number(match[1]), Number(match[2]), Number(match[3]))
  if ((match = input.match(/^(\d{4})年(\d{1,2})月$/)) || (match = input.match(/^(\d{4})-(\d{1,2})$/))) {
    const month = Number(match[2])
    return month >= 1 && month <= 12 ? { period: datePeriod('month', Number(match[1]), month) } : undefined
  }
  if ((match = input.match(/^(\d{4})年?$/))) return { period: datePeriod('year', Number(match[1])) }
  if ((match = input.match(/^(\d{4}) ?年 ?第 ?([1-4]) ?季度$/))) return { period: datePeriod('quarter', Number(match[1]), Number(match[2])) }
  if ((match = input.match(/^(\d{4}) ?年 ?([上下])半年$/))) return { period: datePeriod('half', Number(match[1]), match[2] === '上' ? 1 : 2) }
  if ((match = input.match(/^q([1-4])(?: (\d{4}))?$/))) return { period: datePeriod('quarter', match[2] ? Number(match[2]) : thisYear, Number(match[1])) }
  if ((match = input.match(/^(\d{4}) q([1-4])$/))) return { period: datePeriod('quarter', Number(match[1]), Number(match[2])) }
  if ((match = input.match(/^h([12])(?: (\d{4}))?$/))) return { period: datePeriod('half', match[2] ? Number(match[2]) : thisYear, Number(match[1])) }
  if ((match = input.match(/^(\d{4}) h([12])$/))) return { period: datePeriod('half', Number(match[1]), Number(match[2])) }
  if ((match = input.match(/^([a-z]{3,9})\.?(?: (\d{4}))?$/))) {
    const month = MONTH_NAMES.findIndex(name => name.toLowerCase().startsWith(match![1]) && match![1].length >= 3)
    if (month >= 0) return { period: datePeriod('month', match[2] ? Number(match[2]) : thisYear, month + 1) }
  }
}

/** The field's text for a target: 05/20/2027 for a day, the period's own name otherwise ("Feb 2022", "Q4 2026"). */
export function targetText(target: DateFilterTarget) {
  if (target.date) return `${target.date.slice(5, 7)}/${target.date.slice(8, 10)}/${target.date.slice(0, 4)}`
  const period = target.period!
  if (period.unit === 'month') return `${MONTH_SHORT[period.index - 1]} ${period.year}`
  if (period.unit === 'quarter') return `Q${period.index} ${period.year}`
  if (period.unit === 'half') return `H${period.index} ${period.year}`
  return String(period.year)
}

