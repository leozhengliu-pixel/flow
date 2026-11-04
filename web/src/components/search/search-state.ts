import type { SearchResourceType } from '@/types/flow'
import { addDays, addMonths, parseISO, startOfDay, startOfMonth } from 'date-fns'
import type { DateMode } from '@/components/projects-page/project-target-date-picker'
import { fiscalMonth, fiscalMonthOffset, fiscalYear } from '@/lib/runtime-preferences'

export type SearchTab = 'all' | SearchResourceType
export type SearchOrder = 'relevance' | 'createdAt' | 'updatedAt' | 'title'
export type SearchFilterField = 'statusType' | 'assigneeId' | 'creatorId' | 'createdAt' | 'updatedAt'
export interface SearchCondition { id: string; field: SearchFilterField; operator: 'is' | 'isNot' | 'on' | 'before' | 'after'; value: string; granularity?: DateMode }
export interface SearchPageState { query: string; tab: SearchTab; order: SearchOrder; includeArchived: boolean; showId: boolean; match: 'and' | 'or'; filters: SearchCondition[] }
export const searchFields: Array<{id: SearchFilterField; label: string}> = [{id:'statusType',label:'Status type'},{id:'assigneeId',label:'Assignee / Lead'},{id:'creatorId',label:'Creator'},{id:'updatedAt',label:'Updated date'},{id:'createdAt',label:'Created date'}]
const resourceTypes = ['all', 'issue', 'project', 'initiative', 'document', 'customer', 'release', 'view', 'member']

export function readSearchState(params: URLSearchParams): SearchPageState {
  let filters: SearchCondition[] = []
  try {
    const parsed: unknown = JSON.parse(params.get('filters') ?? '[]')
    if (Array.isArray(parsed)) filters = parsed.filter((item): item is SearchCondition => item && typeof item.id === 'string' && searchFields.some(field => field.id === item.field) && ['is','isNot','on','before','after'].includes(item.operator) && typeof item.value === 'string' && (item.granularity===undefined||['day','month','quarter','half-year','year'].includes(item.granularity))).filter(isSearchConditionValid).slice(0, 20)
  } catch { /* Invalid shared filters do not prevent opening search. */ }
  const tab = params.get('type') ?? params.get('tab') ?? 'all'
  const order = params.get('sort') ?? 'relevance'
  return {query:(params.get('q') ?? '').trim(),tab:(resourceTypes.includes(tab)?tab:'all') as SearchTab,order:(['relevance','createdAt','updatedAt','title'].includes(order)?order:'relevance') as SearchOrder,includeArchived:params.get('includeArchived') !== 'false',showId:params.get('showId') !== 'false',match:params.get('match')==='or'?'or':'and',filters}
}

export function writeSearchState(state: SearchPageState) {
  const params = new URLSearchParams()
  if (state.query) params.set('q',state.query)
  if (state.tab !== 'all') params.set('type',state.tab)
  params.set('includeArchived',String(state.includeArchived))
  if (state.order !== 'relevance') params.set('sort',state.order)
  if (!state.showId) params.set('showId','false')
  if (state.filters.length) { params.set('filters',JSON.stringify(state.filters));params.set('match',state.match) }
  return params
}

export function searchFilterAST(state: Pick<SearchPageState,'filters'|'match'>, firstFiscalMonth = fiscalMonth()): Record<string,unknown> | undefined {
  const conditions = state.filters.filter(isSearchConditionValid).map(filter=>{
    if(!filter.field.endsWith('At')||!filter.granularity&&filter.operator!=='on')return {field:filter.field,operator:filter.operator,values:[filter.value]}
    const [start,end]=searchDateRange(filter.value,filter.granularity??'day',firstFiscalMonth)
    if(filter.operator==='before')return {field:filter.field,operator:'before',values:[start.toISOString()]}
    if(filter.operator==='after')return {field:filter.field,operator:'gte',values:[end.toISOString()]}
    return {and:[{field:filter.field,operator:'gte',values:[start.toISOString()]},{field:filter.field,operator:'before',values:[end.toISOString()]}]}
  })
  return conditions.length ? {[state.match]:conditions} : undefined
}

export function isSearchConditionValid(filter: SearchCondition) {
  return Boolean(filter.value && (!filter.field.endsWith('At') || /^\d{4}-\d{2}-\d{2}$/.test(filter.value) && !Number.isNaN(parseISO(filter.value).getTime())))
}

export function searchDateRange(value:string,mode:DateMode,firstFiscalMonth = fiscalMonth()):[Date,Date] {
  const date=startOfDay(parseISO(value))
  if(mode==='day')return [date,addDays(date,1)]
  const months=mode==='month'?1:mode==='quarter'?3:mode==='half-year'?6:12
  const offset=mode==='month'?0:firstFiscalMonth
  const month=date.getMonth()-((date.getMonth()-offset+12)%months)
  const start=startOfMonth(new Date(date.getFullYear(),month,1))
  return [start,addMonths(start,months)]
}

export function searchDateLabel(value:string,mode:DateMode,locale:string) {
  const date=parseISO(value)
  if(mode==='day'||Number.isNaN(date.getTime()))return value
  if(mode==='month')return new Intl.DateTimeFormat(locale,{month:'long',year:'numeric'}).format(date)
  const year=fiscalYear(date)
  if(mode==='year')return String(year)
  return `${mode==='quarter'?'Q':'H'}${Math.floor(fiscalMonthOffset(date)/(mode==='quarter'?3:6))+1} ${year}`
}
