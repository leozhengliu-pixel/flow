import type { SearchResourceType } from '@/types/flow'

export type SearchTab = 'all' | SearchResourceType
export type SearchOrder = 'relevance' | 'createdAt' | 'updatedAt' | 'title'
export type SearchFilterField = 'statusType' | 'assigneeId' | 'creatorId' | 'createdAt' | 'updatedAt'
export interface SearchCondition { id: string; field: SearchFilterField; operator: 'is' | 'isNot' | 'before' | 'after'; value: string }
export interface SearchPageState { query: string; tab: SearchTab; order: SearchOrder; includeArchived: boolean; showId: boolean; match: 'and' | 'or'; filters: SearchCondition[] }
export const searchFields: Array<{id: SearchFilterField; label: string}> = [{id:'statusType',label:'Status type'},{id:'assigneeId',label:'Assignee / Lead'},{id:'creatorId',label:'Creator'},{id:'updatedAt',label:'Updated date'},{id:'createdAt',label:'Created date'}]
const resourceTypes = ['all', 'issue', 'project', 'initiative', 'document', 'customer', 'release', 'view', 'member']

export function readSearchState(params: URLSearchParams): SearchPageState {
  let filters: SearchCondition[] = []
  try {
    const parsed: unknown = JSON.parse(params.get('filters') ?? '[]')
    if (Array.isArray(parsed)) filters = parsed.filter((item): item is SearchCondition => item && typeof item.id === 'string' && searchFields.some(field => field.id === item.field) && ['is','isNot','before','after'].includes(item.operator) && typeof item.value === 'string').slice(0, 20)
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

export function searchFilterAST(state: Pick<SearchPageState,'filters'|'match'>): Record<string,unknown> | undefined {
  const conditions = state.filters.filter(filter=>filter.value).map(filter=>({field:filter.field,operator:filter.operator,values:[filter.value]}))
  return conditions.length ? {[state.match]:conditions} : undefined
}
