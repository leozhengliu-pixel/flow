/**
 * Shared FilterBlock platform types (Wave 5 / LS-0273+).
 * Shapes are REST-friendly: `{ and|or, field, operator, values }` — no GraphQL.
 */

export type FilterCombineOperator = 'and' | 'or'

/** Compare vocabulary aligned with Linear FilterBlocks, mapped onto Flow REST operators. */
export type FilterCompareOption =
  | 'is'
  | 'not'
  | 'neither'
  | 'all'
  | 'notAll'
  | 'contains'
  | 'notContains'
  | 'before'
  | 'after'
  | 'on'
  | 'within'
  | 'in'
  | 'eq'
  | 'gt'
  | 'lt'
  | 'gte'
  | 'lte'
  | 'empty'

export type FilterBlockValueType =
  | 'equalValue'
  | 'date'
  | 'dateOrInterval'
  | 'dateRange'
  | 'number'
  | 'bool'
  | 'str'
  | 'string'

export type FilterBlockInputType = 'options' | 'freeForm' | 'fuzzyDate'

/** Entity packs that RegisterFilterValues / lazy loader can wire. */
export type FilterEntityType =
  | 'base'
  | 'issue'
  | 'project'
  | 'initiative'
  | 'team'
  | 'pullRequest'
  | 'notification'
  | 'customer'
  | 'searchResult'
  | 'document'
  | 'member'
  | 'feedItem'
  | 'workflowDefinition'

/** JSON-compatible model filter node sent to Flow REST list endpoints. */
export interface FilterModelNode {
  and?: FilterModelNode[]
  or?: FilterModelNode[]
  field?: string
  operator?: string
  values?: string[]
  [key: string]: unknown
}

export interface FilterBlockOption {
  id: string
  label: string
  color?: string
  keywords?: string
  disabled?: boolean
  /** Free-form text prefix (content / links style). */
  textConditionPrefix?: string
}

export interface FilterBlockSelection {
  compareOption: FilterCompareOption
  values: string[]
}

export interface FilterValidationContext {
  /** Known option ids for the active block (when available). */
  knownValueIds?: ReadonlySet<string>
  /** Extra caller context (workspace settings, fiscal month, etc.). */
  [key: string]: unknown
}

export interface FilterBlockDefinition {
  id: string
  /** REST query field key (e.g. assigneeId, status). */
  key: string
  name: string
  valueType: FilterBlockValueType
  inputType?: FilterBlockInputType
  entityType: FilterEntityType
  hideInMenuRoot?: boolean
  hideWhenSearching?: boolean
  onlyVisibleThroughSearch?: boolean
  sortPriority?: number
  defaultCompareOption?: FilterCompareOption | ((values: string[]) => FilterCompareOption)
  options?: FilterBlockOption[] | ((context?: FilterValidationContext) => FilterBlockOption[])
  getCompareOption?: (selection: FilterBlockSelection) => FilterCompareOption
  selectedValues?: (selection: FilterBlockSelection) => string[]
  isValueValid?: (value: string, context?: FilterValidationContext) => boolean
  /** Build a REST model node for this block + selection. */
  toModelFilter?: (selection: FilterBlockSelection) => FilterModelNode
}

export interface FilterCompareOptionChoice {
  label: string
  value: FilterCompareOption
}

export interface ActiveFilterBlock {
  block: FilterBlockDefinition
  selection: FilterBlockSelection
  /** Stable instance id for UI keys / CombinedFilterMap. */
  instanceId: string
}

export interface FilterGroup {
  id: string
  operator: FilterCombineOperator
  blocks: ActiveFilterBlock[]
}
