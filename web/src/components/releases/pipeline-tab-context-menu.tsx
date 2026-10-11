import { forwardRef, type ComponentPropsWithoutRef, type ReactElement } from 'react'

import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { FlowContextMenuPortal, FlowContextMenuRoot, FlowContextMenuTrigger, FlowMenuContent, FlowMenuItem } from '@/components/ui/row-context-menu'
import { useI18n } from '@/i18n/i18n'
import { releasePipelinePath } from '@/lib/app-routes'
import { isFavoriteFor, toggleTabFavoriteFor } from '@/lib/favorites'
import type { BootstrapData, ReleasePipeline } from '@/types/flow'

import { absoluteUrl, copyToClipboard } from './release-actions-model'

/** The favorited state of the menu's "Unfavorite" row: the reference app's filled star. */
function FilledStarGlyph() {
  return <svg aria-hidden="true" data-flow-glyph="favoriteFilled" fill="currentColor" focusable="false" height={16} viewBox="0 0 16 16" width={16}>
    <path d="M10.5193 4.98997L9.46118 2.01693C9.34483 1.70806 9.1452 1.45362 8.88451 1.27433C8.62466 1.09562 8.31641 1 8.00081 1C7.68521 1 7.37696 1.09562 7.11712 1.27433C6.85642 1.45362 6.65679 1.70806 6.54528 2.00374L5.48248 4.98997L2.55536 4.98997C2.23765 4.98973 1.92683 5.08675 1.66556 5.26809C1.40342 5.45004 1.20379 5.70812 1.09414 6.00737C0.984248 6.30728 0.970192 6.63372 1.05394 6.94194C1.13753 7.2496 1.31442 7.52386 1.56019 7.7275L4.08545 9.80411L3.02371 12.9604C2.91854 13.2733 2.91647 13.6112 3.01776 13.9252C3.11884 14.2385 3.3175 14.5113 3.58464 14.7044C3.85102 14.8969 4.17178 15.0003 4.50071 14.9996C4.82872 14.9993 5.14907 14.8951 5.41483 14.702L8.00053 12.8223L10.5851 14.7014C10.8496 14.8944 11.17 14.9991 11.4991 15C11.8281 15.0009 12.1491 14.8978 12.4157 14.7054C12.6831 14.5124 12.882 14.2394 12.9833 13.926C13.0848 13.6113 13.0827 13.2731 12.9773 12.9602L11.9156 9.80207L14.444 7.72408C14.695 7.51166 14.8686 7.23684 14.9493 6.92968C15.0168 6.67352 15.0167 6.40505 14.9504 6.15011L14.9022 5.99753C14.791 5.70157 14.5918 5.44667 14.3314 5.26673C14.0718 5.08736 13.7637 4.9909 13.4479 4.98998L10.5193 4.98997Z"/>
  </svg>
}

type Props = Omit<ComponentPropsWithoutRef<'a'>, 'children'> & {
  data: BootstrapData
  pipeline: ReleasePipeline
  tab: 'releases' | 'changelog'
  children: ReactElement
}

/**
 * Right-click menu on a release pipeline tab (the reference app: "Copy link" and
 * "Favorite" / "Unfavorite"). The favorite targets that tab only; the header
 * star stays the pipeline's root favorite. Forwards trigger props and ref so a
 * tooltip can wrap it.
 */
export const PipelineTabContextMenu = forwardRef<HTMLAnchorElement, Props>(function PipelineTabContextMenu({ data, pipeline, tab, children, ...props }, ref) {
  const { t } = useI18n()
  const favorite = isFavoriteFor(data, 'release_pipeline', pipeline.id, tab)
  const href = releasePipelinePath(data.workspace.urlKey, pipeline.slugId, tab)
  return <FlowContextMenuRoot modal={false}>
    <FlowContextMenuTrigger asChild {...props} ref={ref}>{children}</FlowContextMenuTrigger>
    <FlowContextMenuPortal>
      <FlowMenuContent label={t('Tab options')} className="flow-pipeline-tab-menu">
        <FlowMenuItem icon={<IssueActionGlyph label="Copy link" fallback={null}/>} label="Copy link" onSelect={() => void copyToClipboard(absoluteUrl(href), t('Link copied to clipboard'), t('Could not copy link'))}/>
        <FlowMenuItem icon={favorite ? <FilledStarGlyph/> : <FlowGlyph name="favorite"/>} label={favorite ? 'Unfavorite' : 'Favorite'} onSelect={() => void toggleTabFavoriteFor(data, 'release_pipeline', pipeline.id, tab).catch(() => undefined)}/>
      </FlowMenuContent>
    </FlowContextMenuPortal>
  </FlowContextMenuRoot>
})
