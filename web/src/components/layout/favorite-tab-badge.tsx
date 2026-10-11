import type { ReactNode } from 'react'

import { ReleasesIcon } from '@/components/releases/release-icons'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'

import './favorite-tab-badge.css'

/** Page tabs that can be favorited on their own, by resource type, with their English labels. */
const FAVORITE_TAB_LABELS: Record<string, Record<string, string>> = {
  release_pipeline: { releases: 'Releases', changelog: 'Changelog' },
}

/** The English label of a favorited tab ("Releases", "Changelog"), or undefined for an unknown tab. */
function favoriteTabLabel(resourceType: string, tab: string | undefined) {
  return tab ? FAVORITE_TAB_LABELS[resourceType]?.[tab] : undefined
}

/** The Changelog tab glyph: a page with text lines. */
function ChangelogGlyph({ size = 10 }: { size?: number }) {
  return <svg aria-hidden="true" data-icon="changelog" fill="currentColor" height={size} viewBox="0 0 16 16" width={size}>
    <path clipRule="evenodd" fillRule="evenodd" d="M11 1a3 3 0 0 1 3 3v8a3 3 0 0 1-3 3H5a3 3 0 0 1-3-3V4a3 3 0 0 1 3-3h6Zm0 1.5H5A1.5 1.5 0 0 0 3.5 4v8A1.5 1.5 0 0 0 5 13.5h6a1.5 1.5 0 0 0 1.5-1.5V4A1.5 1.5 0 0 0 11 2.5Z"/>
    <path d="M5.75 5h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1 0-1.5Zm0 2.75h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1 0-1.5Zm0 2.75h2.5a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1 0-1.5Z"/>
  </svg>
}

function FavoriteTabGlyph({ resourceType, tab }: { resourceType: string; tab: string }) {
  if (resourceType === 'release_pipeline' && tab === 'changelog') return <ChangelogGlyph/>
  return <ReleasesIcon size={10}/>
}

/**
 * A tab favorite's sidebar icon: the resource icon with a small round badge on
 * its bottom-right corner showing the favorited tab (the reference app's sidebar tab
 * favorite). The badge's tooltip names the tab.
 */
export function FavoriteTabIcon({ icon, resourceType, tab }: { icon: ReactNode; resourceType: string; tab: string }) {
  const { t } = useI18n()
  const english = favoriteTabLabel(resourceType, tab)
  const label = english ? t(english) : tab
  return <span className="favorite-tab-icon" data-favorite-tab={tab}>
    {icon}
    <ScopedFlowTooltip label={label}>
      <span aria-label={label} className="favorite-tab-badge" role="img"><FavoriteTabGlyph resourceType={resourceType} tab={tab}/></span>
    </ScopedFlowTooltip>
  </span>
}
