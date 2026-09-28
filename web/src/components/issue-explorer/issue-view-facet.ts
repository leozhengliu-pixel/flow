/**
 * LS-0155 / LS-0583 / LS-0585 / LS-0586 / LS-0587 — CustomIssueView / TeamIssues facet packing.
 * enableSplitLayout, subscriptions, triage option, edit/new facet chrome flags.
 */
import type { SavedView } from '@/types/flow'

export type IssueViewFacetMode = 'browse' | 'create' | 'edit'

export interface IssueViewFacetPack {
  mode: IssueViewFacetMode
  /** Enable list/detail split chrome (TeamIssuesPageContent parity). */
  enableSplitLayout: boolean
  /** Show view subscription / notification toggle. */
  enableSubscriptions: boolean
  /** Offer triage empty-state / triage responsibility option for team facets. */
  enableTriageOption: boolean
  /** Show insights side panel host. */
  enableInsights: boolean
  /** Header chrome for `/view/new` and `/view/edit`. */
  showFacetHeader: boolean
  facetTitle: string
  savedView?: SavedView
}

export interface PackIssueViewFacetArgs {
  mode?: IssueViewFacetMode
  savedView?: SavedView
  /** Team-scoped facets unlock triage option. */
  teamScoped?: boolean
  /** Workspace prefers split for issue lists. */
  preferSplitLayout?: boolean
  creatingView?: boolean
  editingView?: boolean
}

/** Pack facet chrome flags for CustomIssueViewPage / TeamIssues*FacetPage. */
export function packIssueViewFacet(args: PackIssueViewFacetArgs = {}): IssueViewFacetPack {
  const mode: IssueViewFacetMode =
    args.mode ??
    (args.creatingView ? 'create' : args.editingView ? 'edit' : 'browse')

  const isFacetEditor = mode === 'create' || mode === 'edit'
  const teamScoped = Boolean(args.teamScoped || args.savedView?.scope === 'team' || args.savedView?.teamId)

  return {
    mode,
    enableSplitLayout: args.preferSplitLayout !== false && !isFacetEditor,
    enableSubscriptions: Boolean(args.savedView) && mode === 'browse',
    enableTriageOption: teamScoped,
    enableInsights: mode === 'browse',
    showFacetHeader: isFacetEditor,
    facetTitle:
      mode === 'create'
        ? 'New view'
        : mode === 'edit'
          ? `Edit ${args.savedView?.name ?? 'view'}`
          : args.savedView?.name ?? 'Issues',
    savedView: args.savedView,
  }
}

/** Derive create/edit facet routes from explorer page props. */
export function resolveIssueViewFacetMode(args: {
  creatingView?: boolean
  editingView?: boolean
}): IssueViewFacetMode {
  if (args.creatingView) return 'create'
  if (args.editingView) return 'edit'
  return 'browse'
}
