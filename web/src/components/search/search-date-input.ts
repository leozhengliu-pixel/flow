import type { DateMode } from '@/components/projects-page/project-target-date-picker'
import { parseNaturalDateInput } from '@/lib/natural-date'
import { fiscalMonth, fiscalYear } from '@/lib/runtime-preferences'

export function parseSearchDateInput(input: string, now = new Date()): {date:Date;mode:DateMode}|undefined {
  const value=input.trim()
  const period=value.match(/^(Q[1-4]|H[12])(\s+\d{4})?$/i)
  if(period){const mode=period[1][0].toUpperCase()==='Q'?'quarter':'half-year';return {mode,date:new Date(Number(period[2]?.trim()??fiscalYear(now)),fiscalMonth()+(Number(period[1][1])-1)*(mode==='quarter'?3:6),1)}}
  if(/^\d{4}$/.test(value))return {mode:'year',date:new Date(Number(value),fiscalMonth(),1)}
  const date=parseNaturalDateInput(value,now)
  if(!date)return
  const mode=/^(?:[a-z]+\s+\d{4}|\d{4}年\d{1,2}月)$/i.test(value)?'month':'day'
  return {date,mode}
}
