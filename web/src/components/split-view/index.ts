export {
  SPLIT_VIEW_LIST_WIDTH_KEYS,
  SPLIT_VIEW_DEFAULT_LIST_WIDTH,
  SPLIT_VIEW_MIN_LIST_WIDTH,
  SPLIT_VIEW_MIN_DETAIL_WIDTH,
  LEGACY_INBOX_LIST_WIDTH_KEY,
  splitViewListWidthKey,
  readSplitViewListWidth,
  persistSplitViewListWidth,
} from './split-view-widths'
export type { SplitViewSurface } from './split-view-widths'

export { SplitView } from './split-view'
export type { SplitViewProps } from './split-view'

export { SplitViewList, SplitViewListTitle } from './split-view-list'
export type { SplitViewListProps } from './split-view-list'

export { useIsSplitView } from './use-is-split-view'
export { useSplitViewListActivation } from './use-split-view-list-activation'
export type { SplitViewListActivationOptions } from './use-split-view-list-activation'

export {
  resolveSplitViewOrigin,
  isTeamIssuesViewKind,
  teamIssuesViewLabel,
  teamIssuesViewPath,
} from './split-view-origin-helper'
export type {
  SplitViewOrigin,
  SplitViewOriginKind,
  SplitViewOriginLink,
} from './split-view-origin-helper'
