/** FilterBlock platform (Wave 5) — public surface. */

export type {
  ActiveFilterBlock,
  FilterBlockDefinition,
  FilterBlockInputType,
  FilterBlockOption,
  FilterBlockSelection,
  FilterBlockValueType,
  FilterCombineOperator,
  FilterCompareOption,
  FilterCompareOptionChoice,
  FilterEntityType,
  FilterGroup,
  FilterModelNode,
  FilterValidationContext,
} from './filter-block-types'

export {
  CombinedFilterMap,
  FILTER_NULL_SENTINEL,
  blockSelectionToModelFilter,
  combineModelFilters,
  compareOptionsForValueType,
  compareToUiOperator,
  createBlockSelection,
  findBlocks,
  fiscalYearBounds,
  getItemsFingerprint,
  isEmptyModelFilter,
  normalizeFiscalYearStartMonth,
  resolveDefaultCompareOption,
  rewriteWithinToIn,
  scoreFreeFormOptions,
  shouldSkipFilterScoring,
  uiOperatorToCompare,
} from './filter-block-helper'

export {
  FILTER_ENTITY_TYPES,
  appendFilterValues,
  clearFilterValueRegistry,
  getRegisteredFilterBlock,
  getRegisteredFilterBlocks,
  hasRegisteredFilterValues,
  listRegisteredFilterEntityTypes,
  registerFilterValues,
  subscribeFilterValueRegistry,
} from './register-filter-values'

export {
  RegisterFilterValuesShouldBeLazyLoaded,
  ensureFilterValuesRegistered,
  preloadFilterValuePacks,
} from './register-filter-values-lazy'

export {
  useFilterValidation,
  validateFilterModel,
  type FilterValidationApi,
  type UseFilterValidationArgs,
} from './use-filter-validation'

export {
  EmptyAddFilterButton,
  UniversalCustomFilterPanel,
  type UniversalCustomFilterPanelProps,
} from './universal-custom-filter-panel'

export {
  CustomerUniversalCustomFilterPanelShouldBeLazyLoaded,
  DocumentUniversalCustomFilterPanelShouldBeLazyLoaded,
  EmptyAddCustomerFilterButtonShouldBeLazyLoaded,
  EmptyAddDocumentFilterButtonShouldBeLazyLoaded,
  EmptyAddFeedItemFilterButtonShouldBeLazyLoaded,
  EmptyAddInitiativeFilterButtonShouldBeLazyLoaded,
  EmptyAddIssueFilterButtonShouldBeLazyLoaded,
  EmptyAddMemberFilterButtonShouldBeLazyLoaded,
  EmptyAddNotificationFilterButtonShouldBeLazyLoaded,
  EmptyAddProjectFilterButtonShouldBeLazyLoaded,
  EmptyAddPullRequestFilterButtonShouldBeLazyLoaded,
  EmptyAddTeamFilterButtonShouldBeLazyLoaded,
  FeedItemUniversalCustomFilterPanelShouldBeLazyLoaded,
  InitiativeUniversalCustomFilterPanelShouldBeLazyLoaded,
  IssueUniversalCustomFilterPanelShouldBeLazyLoaded,
  MemberUniversalCustomFilterPanelShouldBeLazyLoaded,
  NotificationUniversalCustomFilterPanelShouldBeLazyLoaded,
  ProjectUniversalCustomFilterPanelShouldBeLazyLoaded,
  PullRequestUniversalCustomFilterPanelShouldBeLazyLoaded,
  SearchResultUniversalCustomFilterPanelShouldBeLazyLoaded,
  TeamUniversalCustomFilterPanelShouldBeLazyLoaded,
  UniversalCustomFilterPanelShouldBeLazyLoaded,
  WorkflowDefinitionUniversalCustomFilterPanelShouldBeLazyLoaded,
  type UniversalCustomFilterPanelShouldBeLazyLoadedProps,
} from './universal-custom-filter-panel-lazy'


export * from './block-utils'

export {
  readFilterFromSearch,
  encodeFilterForSearch,
  prioritySlaQuickFilterTransformer,
  getViewFilters,
  getStaticViewFilters,
  type ViewFilterKind,
  type ViewFilterTransformer,
  type ViewFiltersOptions,
  type ViewFilterStore,
  type ViewFilters,
} from './get-view-filters'

export {
  useViewFilters,
  type UseViewFiltersArgs,
  type UseViewFiltersResult,
} from './use-view-filters'

export {
  getGroupedIssueFilterBlocks,
  flattenGroupedIssueFilterBlocks,
  groupedIssueFilterBlocks,
  type IssueFilterBlockGroup,
  type GroupedIssueFilterBlocksOptions,
} from './filter-blocks/grouped-issue-filter-blocks'

export {
  getGroupedTeamFilterBlocks,
  flattenGroupedTeamFilterBlocks,
  groupedTeamFilterBlocks,
  blocksDivider,
  type TeamFilterBlockDivider,
} from './filter-blocks/grouped-team-filter-blocks'

export {
  TeamFilterBlocks,
  allTeamFilterBlocks,
  teamFilterBlocks,
  createdAtBlock,
  membersBlock,
  ownersBlock,
  privateBlock,
} from './filter-blocks/team-filter-blocks'
